package merge

import (
	"errors"
	"fmt"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/mergebase"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// The conflict lifecycle's own errors (ADR 0016): `conflicts`, `resolve`,
// `merge continue` and `merge abort` all refuse the same way when the current
// path has no in-progress merge.
var (
	// ErrNoMergeInProgress is MERGE_ERR/RESOLVE_ERR no_merge_in_progress -
	// and, for `conflicts`, the plain CONFLICTS_NONE notice.
	ErrNoMergeInProgress = errors.New("merge: no merge in progress")
	// ErrConflictNotFound is RESOLVE_ERR conflict_not_found: no conflict
	// record at that index.
	ErrConflictNotFound = errors.New("merge: conflict not found")
	// ErrConflictObjectMissing is RESOLVE_ERR conflict_object_missing: the
	// record points at an OBJ_CONFLICT the archive does not hold.
	ErrConflictObjectMissing = errors.New("merge: conflict object missing")
	// ErrConflictMalformed is RESOLVE_ERR conflict_object_malformed.
	ErrConflictMalformed = errors.New("merge: conflict object malformed")
	// ErrUnknownSelector is RESOLVE_ERR unknown_selector: anything but
	// take-ours or take-theirs.
	ErrUnknownSelector = errors.New("merge: unknown resolution selector")
	// ErrStillConflicted is MERGE_ERR internal_still_conflicted_after_
	// resolution - the HolyC's own defensive report for a case that cannot
	// happen, since every conflict was confirmed resolved and OBJ_CONFLICT
	// hashes are deterministic.
	ErrStillConflicted = errors.New("merge: still conflicted after resolution")
)

// UnresolvedError is `merge continue`'s refusal: the indices of the conflicts
// that are still unresolved, in record order.
type UnresolvedError struct{ Indices []int }

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("merge: %d conflict(s) still unresolved", len(e.Indices))
}

// TakeOurs and TakeTheirs are the two resolution selectors this first slice
// accepts (ADR 0016: an explicit replacement content is a later, separate
// thing). An absent chosen side is a valid thing to resolve to - it means
// "resolve to deletion".
const (
	TakeOurs   = "take-ours"
	TakeTheirs = "take-theirs"
)

// ConflictInfo is one META_TAG_CONFLICT record of the current path's
// in-progress merge. Object is nil when the OBJ_CONFLICT evidence is missing
// from the archive or does not decode - a real case `check` flags and this
// listing reports rather than crashes on.
type ConflictInfo struct {
	Index      int
	Hash       archive.Hash
	Object     *object.Conflict
	Missing    bool
	Malformed  bool
	Resolved   bool
	Resolution archive.Hash
}

// Conflicts lists the current path's in-progress merge conflicts, resolved
// and unresolved both, 0-based in append order - the same numbering Resolve
// takes (HgitConflicts). ErrNoMergeInProgress if there is no merge.
func Conflicts(r *repo.Repo) ([]ConflictInfo, error) {
	cur := r.CurrentPath()
	if _, ok := r.Meta.Find(cur, meta.TagMergeState); !ok {
		return nil, ErrNoMergeInProgress
	}
	var out []ConflictInfo
	for i, rec := range r.Meta.All(cur, meta.TagConflict) {
		cr, err := meta.DecodeConflictRecord(rec.Payload)
		if err != nil {
			return nil, err
		}
		info := ConflictInfo{Index: i, Hash: cr.Conflict, Resolved: cr.Resolved, Resolution: cr.Resolution}
		switch obj, ok := r.Get(cr.Conflict); {
		case !ok:
			info.Missing = true
		case obj.Type() != archive.Conflict:
			info.Malformed = true
		default:
			c, err := object.DecodeConflict(obj.Content())
			if err != nil {
				info.Malformed = true
			} else {
				info.Object = c
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// Resolve marks one conflict resolved, recording the chosen side's content
// hash - or an all-zero hash when that side is absent, which Continue reads
// as "omit this entry" (HgitResolve). It returns the conflict's path, for the
// RESOLVE_OK notice. Re-resolving is allowed, as it is in the HolyC: the
// record is rewritten in place, so indices stay stable.
func Resolve(r *repo.Repo, index int, choice string) (string, error) {
	cs, err := Conflicts(r)
	if err != nil {
		return "", err
	}
	if index < 0 || index >= len(cs) {
		return "", ErrConflictNotFound
	}
	c := cs[index]
	switch {
	case c.Missing:
		return "", ErrConflictObjectMissing
	case c.Malformed:
		return "", ErrConflictMalformed
	}
	var chosen object.Side
	switch choice {
	case TakeOurs:
		chosen = c.Object.Ours
	case TakeTheirs:
		chosen = c.Object.Theirs
	default:
		return "", ErrUnknownSelector
	}
	var resolution archive.Hash
	if chosen.Present {
		resolution = chosen.Hash
	}
	if !setResolved(r, r.CurrentPath(), c.Hash, resolution) {
		return "", ErrConflictNotFound
	}
	return c.Object.Path, r.Save()
}

// setResolved rewrites the conflict record for conflictHash in place, keyed
// by that hash rather than by position (MetaConflictSetResolved). The last
// matching record wins, as it does there.
func setResolved(r *repo.Repo, cur string, conflictHash, resolution archive.Hash) bool {
	payload := meta.ConflictRecord{Conflict: conflictHash, Resolved: true, Resolution: resolution}.Encode()
	for i := len(r.Meta.Records) - 1; i >= 0; i-- {
		rec := r.Meta.Records[i]
		if rec.Name != cur || rec.Tag != meta.TagConflict || len(rec.Payload) < archive.HashLen {
			continue
		}
		if archive.Hash(*(*[archive.HashLen]byte)(rec.Payload[:archive.HashLen])) != conflictHash {
			continue
		}
		r.Meta.Records[i].Payload = payload
		return true
	}
	return false
}

// Continue completes an in-progress merge once every conflict is resolved: it
// re-runs the identical three-way merge with the accumulated resolutions, so
// every previously conflicting site takes its resolution instead of
// conflicting again, then writes the real two-parent merge commit and clears
// the in-progress state (HgitMergeContinue). The OBJ_CONFLICT objects stay in
// the archive - unreferenced now, and reported dangling by `check`, but never
// deleted.
func Continue(r *repo.Repo) (Result, error) {
	var res Result
	cur := r.CurrentPath()
	rec, ok := r.Meta.Find(cur, meta.TagMergeState)
	if !ok {
		return res, ErrNoMergeInProgress
	}
	ms, err := meta.DecodeMergeState(rec.Payload)
	if err != nil {
		return res, err
	}
	cs, err := Conflicts(r)
	if err != nil {
		return res, err
	}
	var unresolved []int
	resolutions := make(map[archive.Hash]archive.Hash, len(cs))
	for _, c := range cs {
		if !c.Resolved {
			unresolved = append(unresolved, c.Index)
			continue
		}
		resolutions[c.Hash] = c.Resolution
	}
	if len(unresolved) > 0 {
		return res, &UnresolvedError{Indices: unresolved}
	}

	// The base is re-derived rather than stored: the object graph only ever
	// grows between merge-start and now, so this is deterministic.
	base, found, err := mergebase.FindMergeBase(r, ms.Ours, ms.Theirs)
	if err != nil {
		return res, err
	}
	if !found {
		return res, ErrNoCommonAncestor
	}
	oursTree, oursAttrs, err := sideOf(r, ms.Ours)
	if err != nil {
		return res, err
	}
	theirsTree, theirsAttrs, err := sideOf(r, ms.Theirs)
	if err != nil {
		return res, err
	}
	baseTree, baseAttrs, err := sideOf(r, base)
	if err != nil {
		baseTree, baseAttrs = nil, nil
	}

	// w.refused is not consulted: HgitMergeContinue never checks
	// merge_rename_refused, and Merge refuses any rename/rename before a
	// merge can be left in progress.
	w := walk(r, sides{oursTree, theirsTree, baseTree, oursAttrs, theirsAttrs, baseAttrs}, resolutions)
	if len(w.conflicts) > 0 {
		return res, ErrStillConflicted
	}
	res.Autos = w.autos
	res.Commit, err = finish(r, cur, ms.Ours, ms.Theirs, ms.OtherPath, provenance(cs), w)
	if err != nil {
		return res, err
	}
	clearMergeState(r, cur)
	return res, r.Save()
}

// Abort discards the in-progress merge state and its conflict records
// (HgitMergeAbort). Nothing else is touched: a conflicting merge never moved
// HEAD, never wrote a commit and never touched the working directory, so
// dropping this metadata IS the exact pre-merge state. The OBJ_CONFLICT
// objects are left in the archive, unreferenced.
func Abort(r *repo.Repo) error {
	cur := r.CurrentPath()
	if _, ok := r.Meta.Find(cur, meta.TagMergeState); !ok {
		return ErrNoMergeInProgress
	}
	clearMergeState(r, cur)
	return r.Save()
}

// clearMergeState is MetaMergeStateClear: the merge-state record AND every
// conflict record for this path, together - an in-progress merge always ends
// completely, never partially.
func clearMergeState(r *repo.Repo, cur string) {
	for _, tag := range []byte{meta.TagMergeState, meta.TagConflict} {
		for {
			if _, ok := r.Meta.PopLast(cur, tag); !ok {
				break
			}
		}
	}
}

// provenance is the merge commit's " [resolved <path>=ours|theirs|deleted]"
// message suffix, including the HolyC's own two length caps. A record whose
// OBJ_CONFLICT object is missing contributes nothing, as it does there.
func provenance(cs []ConflictInfo) string {
	b := []byte(" [resolved")
	for _, c := range cs {
		if c.Object == nil {
			continue
		}
		word := "theirs"
		switch {
		case c.Resolution == archive.Hash{}:
			word = "deleted"
		case c.Object.Ours.Present && c.Object.Ours.Hash == c.Resolution:
			word = "ours"
		}
		if len(b) >= 150 {
			continue
		}
		b = append(b, ' ')
		for i := 0; i < len(c.Object.Path) && len(b) < 170; i++ {
			b = append(b, c.Object.Path[i])
		}
		b = append(b, '=')
		b = append(b, word...)
	}
	return string(append(b, ']'))
}
