// Package merge is the three-way merge of another declared path's history
// into the current one: the port of Merge.HC's HgitMerge (ADR 0011), its
// separate 3-way merge of file mode (ADR 0015 follow-up) and its persistence
// of conflicts as real repository data (ADR 0016). It never prints - the
// MERGE_AUTO/MERGE_CONFLICT notices the HolyC emits inline are returned in
// Result instead.
//
// Flat trees only, for now: a name that is a real subtree on every side that
// has it needs the recursion Merge.HC's MergeTreesRecursive does, and is
// refused with ErrNestedTree rather than guessed at. Rename-awareness (ADR
// 0017) is likewise not here yet, so entities are matched by name alone.
package merge

import (
	"errors"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/mergebase"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

var (
	// ErrInProgress refuses a second overlapping merge on one path, whose
	// in-progress state would clobber or orphan the first's.
	ErrInProgress = errors.New("merge: conflicts already in progress")
	// ErrNoCommonAncestor is the disconnected-histories case: the HolyC
	// refuses the whole merge rather than treating "no base" as an empty one.
	ErrNoCommonAncestor = errors.New("merge: no common ancestor")
	// ErrTreeNotFound mirrors MERGE_ERR tree_not_found: either side's commit
	// or its root tree does not resolve.
	ErrTreeNotFound = errors.New("merge: tree not found")
	// ErrNestedTree is native-only: a subtree that would need the recursive
	// merge this package does not implement yet.
	ErrNestedTree = errors.New("merge: nested trees are not merged yet")
)

// AutoKind distinguishes the two automatic decisions the HolyC announces as
// MERGE_AUTO lines. Taking ours is silent there, so it has no kind here.
type AutoKind int

const (
	AutoTookTheirs AutoKind = iota
	AutoDeleted
)

// Auto is one automatically resolved entry.
type Auto struct {
	Kind AutoKind
	Path string
}

// Conflict is one real conflict: its OBJ_CONFLICT evidence object, and that
// object's hash, which the metadata records reference.
type Conflict struct {
	Hash   archive.Hash
	Object object.Conflict
}

// Result is what one merge attempt did. UpToDate and FastForward are the two
// trivial cases; otherwise Conflicts being empty means Commit is the new
// two-parent merge commit, and Conflicts being non-empty means nothing was
// committed and HEAD did not move.
type Result struct {
	UpToDate    bool
	FastForward bool
	Commit      archive.Hash // the merge commit, or theirs on a fast-forward
	Autos       []Auto
	Conflicts   []Conflict
}

// Merge brings otherPath's head into the current path. The current path is
// "ours", as in the HolyC, which reads it from the repository rather than
// taking it as an argument - so there is nothing to pass here either.
func Merge(r *repo.Repo, otherPath string) (Result, error) {
	var res Result
	cur := r.CurrentPath()
	if _, ok := r.Meta.Find(cur, meta.TagMergeState); ok {
		return res, ErrInProgress
	}
	ours, ok := r.Head(cur)
	if !ok {
		return res, repo.ErrNoHead
	}
	theirs, ok := r.Head(otherPath)
	if !ok {
		return res, repo.ErrNoSuchPath
	}

	base, found, err := mergebase.FindMergeBase(r, ours, theirs)
	if err != nil {
		return res, err
	}
	if !found {
		return res, ErrNoCommonAncestor
	}
	if theirs == base {
		res.UpToDate = true
		return res, nil
	}
	if ours == base { // a real fast-forward: HEAD just moves, no merge commit
		r.AppendOp(cur, meta.OpLogEntry{Timestamp: clock.Now(), Prev: ours, New: theirs})
		if err := r.SetHead(cur, theirs); err != nil {
			return res, err
		}
		if err := r.Save(); err != nil {
			return res, err
		}
		res.FastForward, res.Commit = true, theirs
		return res, nil
	}

	oursTree, oursAttrs, err := sideOf(r, ours)
	if err != nil {
		return res, err
	}
	theirsTree, theirsAttrs, err := sideOf(r, theirs)
	if err != nil {
		return res, err
	}
	// A base whose tree does not resolve is degenerate but real: it is
	// treated as an empty side, not as an error.
	baseTree, baseAttrs, err := sideOf(r, base)
	if err != nil {
		baseTree, baseAttrs = nil, nil
	}

	merged := &object.Tree{}
	list := &object.Attrs{}
	for _, name := range union(oursTree, theirsTree, baseTree) {
		oe, oFound := find(oursTree, name)
		te, tFound := find(theirsTree, name)
		be, bFound := find(baseTree, name)

		anyTree := (oFound && oe.ChildType == archive.Tree) ||
			(tFound && te.ChildType == archive.Tree) ||
			(bFound && be.ChildType == archive.Tree)
		allTree := (!oFound || oe.ChildType == archive.Tree) &&
			(!tFound || te.ChildType == archive.Tree) &&
			(!bFound || be.ChildType == archive.Tree)

		if anyTree && allTree {
			return Result{}, ErrNestedTree
		}
		if anyTree { // a real kind mismatch: a file on one side, a directory
			// on another. Mode is left 0 on every side - it is not
			// meaningfully comparable across two different kinds.
			res.Conflicts = append(res.Conflicts, Conflict{Object: object.Conflict{
				Kind:     object.KindType,
				EntityID: entityID(oe, oFound, te, tFound, be),
				Path:     name,
				Base:     side(be, bFound, be.ChildType, 0),
				Ours:     side(oe, oFound, oe.ChildType, 0),
				Theirs:   side(te, tFound, te.ChildType, 0),
			}})
			continue
		}

		// Content: unchanged-from-base on one side takes the other side's
		// value; changed differently on both sides is a real conflict. An
		// absent entry is a value like any other, which is what makes
		// edit-vs-delete a conflict and delete-vs-unchanged a clean deletion.
		oursEqTheirs := oFound == tFound && (!oFound || oe.ChildHash == te.ChildHash)
		oursEqBase := oFound == bFound && (!oFound || oe.ChildHash == be.ChildHash)
		theirsEqBase := tFound == bFound && (!tFound || te.ChildHash == be.ChildHash)

		takeOurs, takeTheirs, isConflict := false, false, false
		switch {
		case oursEqTheirs:
			takeOurs = true // identical, or both absent - either works
		case theirsEqBase:
			takeOurs = true // only ours changed
		case oursEqBase:
			takeTheirs = true // only theirs changed
		default:
			isConflict = true // both changed, differently
		}

		// Mode is a wholly separate three-way decision from content, made the
		// same way and regardless of which content branch fired. A side that
		// does not have the entry at all contributes mode 0, which is the
		// documented limitation ported verbatim below.
		var baseMode, oursMode, theirsMode byte
		if bFound {
			baseMode = baseAttrs.FindMode(be.EntityID)
		}
		if oFound {
			oursMode = oursAttrs.FindMode(oe.EntityID)
		}
		if tFound {
			theirsMode = theirsAttrs.FindMode(te.EntityID)
		}
		var winningMode byte
		modeConflict := false
		switch {
		case oursMode == theirsMode:
			winningMode = oursMode
		case theirsMode == baseMode:
			winningMode = oursMode // only ours changed mode
		case oursMode == baseMode:
			winningMode = theirsMode // only theirs changed mode
		default:
			modeConflict = true // both changed mode, differently
		}

		switch {
		case isConflict || modeConflict:
			var kind byte
			if isConflict {
				kind |= object.KindContent
			}
			if modeConflict {
				kind |= object.KindMode
			}
			res.Conflicts = append(res.Conflicts, Conflict{Object: object.Conflict{
				Kind:     kind,
				EntityID: entityID(oe, oFound, te, tFound, be),
				Path:     name,
				Base:     side(be, bFound, archive.Blob, baseMode),
				Ours:     side(oe, oFound, archive.Blob, oursMode),
				Theirs:   side(te, tFound, archive.Blob, theirsMode),
			}})
		case takeOurs && oFound:
			merged.Entries = append(merged.Entries, oe)
			if winningMode != 0 {
				list.Entries = append(list.Entries, object.AttrEntry{EntityID: oe.EntityID, Mode: winningMode})
			}
		case takeTheirs && tFound:
			res.Autos = append(res.Autos, Auto{AutoTookTheirs, name})
			merged.Entries = append(merged.Entries, te)
			if winningMode != 0 {
				list.Entries = append(list.Entries, object.AttrEntry{EntityID: te.EntityID, Mode: winningMode})
			}
		default:
			// A real deletion survived cleanly: removed on the winning side,
			// unchanged on the other. Note this is also where a mode-only
			// change on the deleted side lands, unflagged - the edit-vs-delete
			// decision above consults content alone (see docs/porting-notes.md).
			res.Autos = append(res.Autos, Auto{AutoDeleted, name})
		}
	}

	if len(res.Conflicts) > 0 {
		return res, persist(r, cur, ours, theirs, otherPath, &res)
	}

	c := &object.Commit{
		Tree:      r.Append(archive.Tree, merged.Encode()),
		Parents:   []archive.Hash{ours, theirs}, // ours first, then theirs
		Timestamp: clock.Now(),
		Message:   []byte("merge " + otherPath),
	}
	if len(list.Entries) > 0 {
		h := r.Append(archive.Attrs, list.Encode())
		c.Attrs = &h
	}
	res.Commit = r.Append(archive.Commit, c.Encode())
	r.AppendOp(cur, meta.OpLogEntry{Timestamp: c.Timestamp, Prev: ours, New: res.Commit})
	if err := r.SetHead(cur, res.Commit); err != nil {
		return res, err
	}
	return res, r.Save()
}

// persist writes the conflict evidence: every OBJ_CONFLICT object, the
// in-progress merge state and one unresolved record per conflict. No merge
// commit is created and HEAD does not move, but all of this is saved, so the
// conflict survives a restart (ADR 0016).
func persist(r *repo.Repo, cur string, ours, theirs archive.Hash, otherPath string, res *Result) error {
	for i := range res.Conflicts {
		res.Conflicts[i].Hash = r.Append(archive.Conflict, res.Conflicts[i].Object.Encode())
	}
	if len(cur) > 255 {
		return repo.ErrNameTooLong
	}
	r.Meta.Set(cur, meta.TagMergeState, meta.MergeState{Ours: ours, Theirs: theirs, OtherPath: otherPath}.Encode())
	for _, c := range res.Conflicts {
		r.Meta.Append(cur, meta.TagConflict, meta.ConflictRecord{Conflict: c.Hash}.Encode())
	}
	return r.Save()
}

// sideOf resolves one commit's root tree and its attrs list. A commit with no
// attrs object, or one that does not resolve, yields a nil list, whose
// FindMode is 0 for every entity - the same backward compatibility every
// other reader of OBJ_ATTRS has.
func sideOf(r *repo.Repo, h archive.Hash) (*object.Tree, *object.Attrs, error) {
	c, err := r.Commit(h)
	if err != nil {
		return nil, nil, ErrTreeNotFound
	}
	t, err := r.Tree(c.Tree)
	if err != nil {
		return nil, nil, ErrTreeNotFound
	}
	if c.Attrs == nil {
		return t, nil, nil
	}
	a, err := r.Attrs(*c.Attrs)
	if err != nil {
		return t, nil, nil
	}
	return t, a, nil
}

// union is MergeCollectNames: every distinct name across the three trees, in
// first-seen order, ours then theirs then base.
func union(trees ...*object.Tree) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range trees {
		if t == nil {
			continue
		}
		for _, e := range t.Entries {
			if !seen[e.Name] {
				seen[e.Name] = true
				out = append(out, e.Name)
			}
		}
	}
	return out
}

func find(t *object.Tree, name string) (object.Entry, bool) {
	if t == nil {
		return object.Entry{}, false
	}
	return t.Find(name)
}

func side(e object.Entry, present bool, typ archive.Type, mode byte) object.Side {
	if !present {
		return object.Side{}
	}
	return object.Side{Present: true, Type: typ, Mode: mode, Hash: e.ChildHash}
}

// entityID is the HolyC's own order: ours, else theirs, else base.
func entityID(oe object.Entry, oFound bool, te object.Entry, tFound bool, be object.Entry) uint64 {
	switch {
	case oFound:
		return oe.EntityID
	case tFound:
		return te.EntityID
	default:
		return be.EntityID
	}
}
