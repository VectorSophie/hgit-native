// Package merge is the three-way merge of another declared path's history
// into the current one: the port of Merge.HC's HgitMerge (ADR 0011), its
// separate 3-way merge of file mode (ADR 0015 follow-up) and its persistence
// of conflicts as real repository data (ADR 0016). It never prints - the
// MERGE_AUTO/MERGE_CONFLICT notices the HolyC emits inline are returned in
// Result instead.
//
// A name that is a real subtree on every side that has it is merged one level
// deeper by the same three-way decision (MergeTreesRecursive, probe 100), and
// each tree level is first normalized for renames by entity id (ADR 0017), so
// a rename on one side and an edit on the other merge cleanly.
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
	// ErrAmbiguousRename is MERGE_REFUSED ambiguous_rename_rename: the two
	// sides renamed one entity to different names. Nothing is written.
	ErrAmbiguousRename = errors.New("merge: ambiguous rename/rename")
)

// AutoKind distinguishes the notices the HolyC prints inline while it merges:
// the MERGE_AUTO lines, and MERGE_RENAME_RENAME. Taking ours is silent there,
// so it has no kind here.
type AutoKind int

const (
	AutoTookTheirs    AutoKind = iota
	AutoDeleted                // Path is the deleted entry's full path
	AutoRenamed                // Path is prefix+base name; NewName the new local name
	AutoRenameRefused          // Path is the base name (no prefix) renamed two ways
)

// Auto is one notice, in the order the HolyC prints it.
type Auto struct {
	Kind    AutoKind
	Path    string
	NewName string
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
	// RenameRefused goes with ErrAmbiguousRename: the whole merge was
	// refused, and Autos holds the notices printed before the refusal.
	RenameRefused bool
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

	w := walk(r, sides{oursTree, theirsTree, baseTree, oursAttrs, theirsAttrs, baseAttrs}, nil)
	res.Autos = w.autos
	// Checked after the whole walk and before anything is saved, as the HolyC
	// checks merge_rename_refused before its conflict FileWrite: the subtrees
	// and conflict evidence computed on the way are dropped with the scratch
	// archive. Here they are still only w.pending, so dropping it is enough:
	// neither the disk nor the in-memory repository has them.
	if w.refused {
		res.RenameRefused = true
		return res, ErrAmbiguousRename
	}
	res.Conflicts = w.conflicts
	if len(res.Conflicts) > 0 {
		return res, persist(r, cur, ours, theirs, otherPath, w, &res)
	}
	res.Commit, err = finish(r, cur, ours, theirs, otherPath, "", w)
	if err != nil {
		return res, err
	}
	return res, r.Save()
}

// sides is the six inputs of one three-way walk. A nil tree or attrs list is
// a side that has nothing, which is a value like any other here.
type sides struct {
	ours, theirs, base                *object.Tree
	oursAttrs, theirsAttrs, baseAttrs *object.Attrs
}

// walked is one walk's outcome: the merged tree and attrs list (meaningful
// only when no conflict was found and nothing was refused), plus what it
// decided along the way.
type walked struct {
	tree      *object.Tree
	attrs     *object.Attrs
	autos     []Auto
	conflicts []Conflict
	refused   bool
	// pending is every object the walk would ObjectPut - merged subtrees and
	// unresolved conflict evidence - in the order it found them. It reaches
	// the repository only through append, once the merge is known to finish
	// or to persist its conflicts; a refusal simply drops it.
	pending []archive.Record
}

// append writes the walk's pending objects to the repository, in walk order.
func (w walked) append(r *repo.Repo) {
	for _, rec := range w.pending {
		r.Append(rec.Type(), rec.Content())
	}
}

// walker carries what MergeTreesRecursive shares across every depth: the
// repository (read only - subtrees are resolved from it; what ObjectPut
// writes to the scratch archive goes to w.pending instead), the resolution
// table,
// each side's commit-level attrs and the one merged attrs list (mode is keyed
// by entity id, the same at every depth), and the notices and conflicts in
// the order they are found. w.refused is Merge.HC's merge_rename_refused.
type walker struct {
	r                                 *repo.Repo
	resolutions                       map[archive.Hash]archive.Hash
	oursAttrs, theirsAttrs, baseAttrs *object.Attrs
	w                                 walked
}

// walk is MergeTreesRecursive from the root. resolutions maps an OBJ_CONFLICT
// object's hash to the content it was resolved to (all-zero meaning "resolve
// to absent"): a site whose conflict object is in that table takes the
// matching side whole instead of conflicting again, which is exactly how
// `merge continue` re-runs the same merge without re-deciding (ADR 0016).
func walk(r *repo.Repo, s sides, resolutions map[archive.Hash]archive.Hash) walked {
	wk := &walker{r: r, resolutions: resolutions,
		oursAttrs: s.oursAttrs, theirsAttrs: s.theirsAttrs, baseAttrs: s.baseAttrs}
	wk.w.attrs = &object.Attrs{}
	wk.w.tree, _ = wk.level(s.ours, s.theirs, s.base, "")
	return wk.w
}

// level three-way merges one tree level. It reports false when this level or
// any subtree below it conflicted; every conflict is appended, with its full
// slash path, to the walker in the order it was found.
func (wk *walker) level(oursTree, theirsTree, baseTree *object.Tree, prefix string) (*object.Tree, bool) {
	// Undo unambiguous renames on each side first, so the name-keyed logic
	// below sees base names. Both sides are normalized against the other
	// side's ORIGINAL tree, as the HolyC does before reassigning either.
	renames := map[string]string{}
	oursNorm := wk.normalize(baseTree, oursTree, theirsTree, renames)
	theirsNorm := wk.normalize(baseTree, theirsTree, oursTree, renames)
	if oursNorm != nil {
		oursTree = oursNorm
	}
	if theirsNorm != nil {
		theirsTree = theirsNorm
	}

	merged := &object.Tree{}
	ok := true
	// take appends a side's entry under the merged (possibly renamed) name.
	take := func(e object.Entry, outName string) {
		e.Name = outName
		merged.Entries = append(merged.Entries, e)
	}
	for _, name := range union(oursTree, theirsTree, baseTree) {
		outName := name
		if to, found := renames[name]; found {
			outName = to
			wk.w.autos = append(wk.w.autos, Auto{Kind: AutoRenamed, Path: prefix + name, NewName: outName})
		}
		full := fullName(prefix, outName)

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
			// Every side that has this name agrees it is a directory: merge it
			// one level deeper with this same function. A subtree that
			// conflicted writes nothing; a clean one is queued right away and
			// written even if a conflict elsewhere later means no merge commit
			// - the stray subtree Merge.HC's header documents.
			sub, subOK := wk.level(wk.subtree(oe, oFound), wk.subtree(te, tFound), wk.subtree(be, bFound), full+"/")
			if !subOK {
				ok = false
				continue
			}
			id := te.EntityID // zero when theirs is absent too
			if oFound {
				id = oe.EntityID
			}
			rec := archive.NewObject(archive.Tree, sub.Encode())
			wk.w.pending = append(wk.w.pending, rec)
			take(object.Entry{ChildType: archive.Tree, ChildHash: rec.Hash, EntityID: id}, outName)
			continue
		}
		if anyTree { // a real kind mismatch: a file on one side, a directory
			// on another. Mode is left 0 on every side - it is not
			// meaningfully comparable across two different kinds, and no
			// attrs entry is written for a resolution of one either.
			c := Conflict{Object: object.Conflict{
				Kind:     object.KindType,
				EntityID: entityID(oe, oFound, te, tFound, be),
				Path:     full,
				Base:     side(be, bFound, be.ChildType, 0),
				Ours:     side(oe, oFound, oe.ChildType, 0),
				Theirs:   side(te, tFound, te.ChildType, 0),
			}}
			c.Hash = archive.NewObject(archive.Conflict, c.Object.Encode()).Hash
			if resolution, found := wk.resolutions[c.Hash]; found {
				if e, _, keep := resolved(resolution, oe, oFound, te); keep {
					take(e, outName)
				}
				continue
			}
			wk.w.conflicts = append(wk.w.conflicts, c)
			wk.w.pending = append(wk.w.pending, archive.NewObject(archive.Conflict, c.Object.Encode()))
			ok = false
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
			baseMode = wk.baseAttrs.FindMode(be.EntityID)
		}
		if oFound {
			oursMode = wk.oursAttrs.FindMode(oe.EntityID)
		}
		if tFound {
			theirsMode = wk.theirsAttrs.FindMode(te.EntityID)
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

		list := wk.w.attrs
		switch {
		case isConflict || modeConflict:
			var kind byte
			if isConflict {
				kind |= object.KindContent
			}
			if modeConflict {
				kind |= object.KindMode
			}
			c := Conflict{Object: object.Conflict{
				Kind:     kind,
				EntityID: entityID(oe, oFound, te, tFound, be),
				Path:     full,
				Base:     side(be, bFound, archive.Blob, baseMode),
				Ours:     side(oe, oFound, archive.Blob, oursMode),
				Theirs:   side(te, tFound, archive.Blob, theirsMode),
			}}
			c.Hash = archive.NewObject(archive.Conflict, c.Object.Encode()).Hash
			if resolution, found := wk.resolutions[c.Hash]; found {
				// The resolved side is taken whole - its entry AND its own
				// mode, not the three-way winning mode, which the conflict
				// made meaningless.
				e, useOurs, keep := resolved(resolution, oe, oFound, te)
				if !keep {
					continue // resolved to absent: a real deletion
				}
				take(e, outName)
				mode := theirsMode
				if useOurs {
					mode = oursMode
				}
				if mode != 0 {
					list.Entries = append(list.Entries, object.AttrEntry{EntityID: e.EntityID, Mode: mode})
				}
				continue
			}
			wk.w.conflicts = append(wk.w.conflicts, c)
			wk.w.pending = append(wk.w.pending, archive.NewObject(archive.Conflict, c.Object.Encode()))
			ok = false
		case takeOurs && oFound:
			take(oe, outName)
			if winningMode != 0 {
				list.Entries = append(list.Entries, object.AttrEntry{EntityID: oe.EntityID, Mode: winningMode})
			}
		case takeTheirs && tFound:
			wk.w.autos = append(wk.w.autos, Auto{Kind: AutoTookTheirs, Path: full})
			take(te, outName)
			if winningMode != 0 {
				list.Entries = append(list.Entries, object.AttrEntry{EntityID: te.EntityID, Mode: winningMode})
			}
		default:
			// A real deletion survived cleanly: removed on the winning side,
			// unchanged on the other. Note this is also where a mode-only
			// change on the deleted side lands, unflagged - the edit-vs-delete
			// decision above consults content alone (see docs/porting-notes.md).
			wk.w.autos = append(wk.w.autos, Auto{Kind: AutoDeleted, Path: full})
		}
	}
	return merged, ok
}

// subtree resolves a present tree entry's subtree. One that does not resolve
// is treated as absent, as MergeResolveTreeByHash's failure leaves it NULL.
func (wk *walker) subtree(e object.Entry, found bool) *object.Tree {
	if !found {
		return nil
	}
	t, err := wk.r.Tree(e.ChildHash)
	if err != nil {
		return nil
	}
	return t
}

// normalize is MergeNormalizeSide: a copy of sideTree with unambiguous
// renames relative to baseTree undone, or nil when none applied. A blob entry
// is renamed back to its base name only when all of these hold:
//   - its own name is not in the base at all;
//   - its entity id is carried by exactly one blob in the base, and by
//     exactly one blob on this side;
//   - this side has nothing else at the base name;
//   - the other side still has the base name, or renamed that same entity to
//     this very name.
//
// When the other side instead carries the entity (exactly once) under a
// DIFFERENT name, that is the rename/rename case: it is recorded as refused
// and left un-normalized. renames collects base name -> new name, first
// mapping wins.
func (wk *walker) normalize(baseTree, sideTree, otherTree *object.Tree, renames map[string]string) *object.Tree {
	if baseTree == nil || sideTree == nil {
		return nil
	}
	out := &object.Tree{Entries: make([]object.Entry, 0, len(sideTree.Entries))}
	applied := false
	for _, e := range sideTree.Entries {
		out.Entries = append(out.Entries, e)
		if e.ChildType != archive.Blob {
			continue
		}
		if _, inBase := baseTree.Find(e.Name); inBase {
			continue
		}
		bname, bmatch := findEntity(baseTree, e.EntityID)
		_, smatch := findEntity(sideTree, e.EntityID)
		if bmatch != 1 || smatch != 1 {
			continue
		}
		if _, sideHasB := sideTree.Find(bname); sideHasB {
			continue
		}
		_, normOK := find(otherTree, bname)
		if !normOK {
			if oname, omatch := findEntity(otherTree, e.EntityID); omatch == 1 {
				if oname == e.Name {
					normOK = true // the same rename on both sides
				} else {
					wk.w.autos = append(wk.w.autos, Auto{Kind: AutoRenameRefused, Path: bname})
					wk.w.refused = true
				}
			}
		}
		if !normOK {
			continue
		}
		out.Entries[len(out.Entries)-1].Name = bname
		applied = true
		if _, seen := renames[bname]; !seen {
			renames[bname] = e.Name
		}
	}
	if !applied {
		return nil
	}
	return out
}

// findEntity is MergeFindEntityInTree: how many BLOB entries of t carry id,
// and the first one's name.
func findEntity(t *object.Tree, id uint64) (string, int) {
	if t == nil {
		return "", 0
	}
	name, n := "", 0
	for _, e := range t.Entries {
		if e.ChildType == archive.Blob && e.EntityID == id {
			if n == 0 {
				name = e.Name
			}
			n++
		}
	}
	return name, n
}

// fullName is the HolyC's full_name: prefix plus the local name, the name cut
// so the whole stays within 254 bytes. Only with a prefix of 255+ bytes does
// this cut one byte more than the HolyC, whose fixed 256-byte buffer already
// overflows at that depth.
func fullName(prefix, name string) string {
	n := 254 - len(prefix)
	if n < 0 {
		n = 0
	}
	if n < len(name) {
		name = name[:n]
	}
	s := prefix + name
	if len(s) > 254 {
		s = s[:254]
	}
	return s
}

// resolved picks what one resolved conflict site contributes to the merged
// tree: nothing at all for an all-zero resolution (resolved to absent, a real
// deletion), ours' entry when ours is present and its content is what was
// resolved to, and theirs' entry otherwise - the side is taken whole, entity
// id and all (MergeTreesRecursive's own resolution branch).
func resolved(resolution archive.Hash, oe object.Entry, oFound bool, te object.Entry) (object.Entry, bool, bool) {
	switch {
	case resolution == archive.Hash{}:
		return object.Entry{}, false, false
	case oFound && oe.ChildHash == resolution:
		return oe, true, true
	default:
		return te, false, true
	}
}

// finish is MergeFinishSuccess: the merged tree, its attrs list and the
// two-parent merge commit, then the oplog entry and HEAD move. msgSuffix is
// `merge continue`'s resolution provenance, capped exactly as the HolyC's own
// 254-byte message loop caps it. The walk's merged subtrees go first, as
// they were put during the walk there. It does not save; the caller does.
func finish(r *repo.Repo, cur string, ours, theirs archive.Hash, otherPath, msgSuffix string, w walked) (archive.Hash, error) {
	w.append(r)
	msg := []byte("merge " + otherPath)
	for i := 0; i < len(msgSuffix) && len(msg) < 254; i++ {
		msg = append(msg, msgSuffix[i])
	}
	c := &object.Commit{
		Tree:      r.Append(archive.Tree, w.tree.Encode()),
		Parents:   []archive.Hash{ours, theirs}, // ours first, then theirs
		Timestamp: clock.Now(),
		Message:   msg,
	}
	if len(w.attrs.Entries) > 0 {
		h := r.Append(archive.Attrs, w.attrs.Encode())
		c.Attrs = &h
	}
	h := r.Append(archive.Commit, c.Encode())
	r.AppendOp(cur, meta.OpLogEntry{Timestamp: c.Timestamp, Prev: ours, New: h})
	return h, r.SetHead(cur, h)
}

// persist writes the conflict evidence: every OBJ_CONFLICT object, together
// with any clean subtree the walk merged, in walk order; then the in-progress
// merge state and one unresolved record per conflict. No merge commit is
// created and HEAD does not move, but all of this is saved, so the conflict
// survives a restart (ADR 0016).
func persist(r *repo.Repo, cur string, ours, theirs archive.Hash, otherPath string, w walked, res *Result) error {
	w.append(r)
	if len(cur) >= repo.MaxPathName {
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
