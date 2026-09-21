// Package offer records a working directory as a new commit: the port of
// Offer.HC's flat path (HgitOffer / HgitOfferWithRelation) and its recursive
// one (HgitOfferTree / HgitOfferTreeWithRelation, ADR 0010). It never prints;
// the HolyC's per-file OFFER_IGNORED notice is surfaced through
// Options.OnIgnored instead.
//
// The relation commands are not separate entry points: `correct`, `revert`
// and `reconcile` are Offer with Options.Relation set to object.RelCorrects,
// RelReverts or RelReconciles and Options.RelationTarget naming the commit
// they relate to, and `correcttree`/`reverttree`/`reconciletree` are the same
// options passed to OfferTree - exactly the parameterization the HolyC's own
// HgitOfferRelatedCmd/HgitOfferTreeRelatedCmd perform. The command line's
// <target_hex> and <entity_hex> tokens are parsed by archive.ParseHex and
// archive.ParseEntityID, whose errors are the CLI's bad_hash and
// bad_entity_id.
package offer

import (
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"path"
	"path/filepath"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/attrs"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/fossil"
	"github.com/VectorSophie/hgit-native/pkg/hgit/ignore"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/workdir"
)

// MaxMessageLen is the longest commit message this port accepts. The format
// itself stores the length in a U32, so it is not the constraint; the HolyC's
// is its fixed 512-byte commit_content buffer, whose fixed fields cost 79
// bytes for a root offering and 279 in the worst case (one parent, a relation
// and an attrs object), leaving room for 433 and 233 message bytes. 255 is
// this port's single flat limit across that whole range - the same U8 ceiling
// a tree entry's name has - and an over-long message is refused rather than
// truncated. See docs/porting-notes.md.
const MaxMessageLen = 255

// maxNameLen is what a tree entry's U8 name_len can encode.
const maxNameLen = 255

// MaxTreeDepth bounds OfferTree's recursion. The HolyC has no depth limit of
// its own (TREE_LEVEL_MAX is a per-level byte cap, not a depth), so this is
// this port's own guard against unbounded recursion: it sits well above any
// path a filesystem can actually hand back - Linux's own PATH_MAX of 4096
// bytes cannot express more than about 2048 single-character levels - and far
// below where Go's growable stack would be in trouble.
const MaxTreeDepth = 1024

var (
	ErrMessageTooLong = errors.New("offer: message longer than 255 bytes")
	ErrNameTooLong    = errors.New("offer: file name longer than 255 bytes")
	ErrEntityID       = errors.New("offer: could not generate a nonzero entity id")
	ErrTooDeep        = errors.New("offer: directory tree deeper than MaxTreeDepth")
)

// NewEntityID returns a fresh, stable identity for a newly tracked name
// (Tree.HC's GenerateEntityId). It never returns a usable 0; a 0 result means
// the system source of randomness failed and the offer is refused. Tests
// replace it.
var NewEntityID = func() uint64 {
	var b [8]byte
	for try := 0; try < 8; try++ {
		if _, err := crand.Read(b[:]); err != nil {
			return 0
		}
		if id := binary.LittleEndian.Uint64(b[:]); id != 0 {
			return id
		}
	}
	return 0
}

// Options are HgitOfferWithRelation's relation parameters plus the message.
// A zero Relation (RelNone) leaves the target and entity fields unused, which
// is what plain `offer` and `offertree` do; correct/revert/reconcile and
// their tree variants set them.
type Options struct {
	Message        string
	Relation       object.Relation
	RelationTarget archive.Hash
	RelationEntity uint64

	// OnIgnored, when set, is called with the path (relative to the offered
	// root) of each untracked name an ignore rule hid - the HolyC prints
	// OFFER_IGNORED there. An ignored directory is reported once and never
	// descended into.
	OnIgnored func(name string)
}

// builder is the state one offer threads through every directory level: the
// HolyC's own "one archive, one alen, one attrs accumulator" convention.
type builder struct {
	r    *repo.Repo
	root string
	ign  *ignore.Rules
	att  *attrs.AttrRules
	list *object.Attrs
	opts *Options
}

// Offer records every file in work matching mask as a new commit on the
// current path, and saves the repository. It returns the new commit's hash.
//
// Every input is validated before the first object is stored. A failure after
// that point can still leave objects behind: Save writes the .hgs before the
// .m, so an interrupted save leaves objects that `check` reports as dangling
// and a HEAD that never moved.
func Offer(r *repo.Repo, work string, mask string, opts Options) (archive.Hash, error) {
	var zero archive.Hash
	if len(opts.Message) > MaxMessageLen {
		return zero, ErrMessageTooLong
	}
	files, err := workdir.List(work, mask)
	if err != nil {
		return zero, err
	}
	for _, f := range files {
		if len(f.Name) > maxNameLen {
			return zero, ErrNameTooLong
		}
	}

	b := newBuilder(r, work, &opts)
	p := r.CurrentPath()
	parent, hasParent := r.Head(p)
	old := parentTree(r, parent, hasParent)

	tree := &object.Tree{}
	for _, f := range files {
		e, kept, err := b.addFile(old, "", f.Name)
		if err != nil {
			return zero, err
		}
		if kept {
			tree.Entries = append(tree.Entries, e)
		}
	}
	return b.finish(p, tree, parent, hasParent)
}

// OfferTree records work and every subdirectory under it recursively: the
// port of HgitOfferTreeWithRelation, whose one substitution for the flat path
// is TreeBuildRecursive in place of the single FilesFind pass. Everything
// else - the parent lookup, the commit, the operation log, HEAD and the save
// - is identical.
func OfferTree(r *repo.Repo, work string, opts Options) (archive.Hash, error) {
	var zero archive.Hash
	if len(opts.Message) > MaxMessageLen {
		return zero, ErrMessageTooLong
	}

	b := newBuilder(r, work, &opts)
	p := r.CurrentPath()
	parent, hasParent := r.Head(p)
	old := parentTree(r, parent, hasParent)

	tree, err := b.buildTree("", old, 0)
	if err != nil {
		return zero, err
	}
	return b.finish(p, tree, parent, hasParent)
}

// newBuilder loads the one .hgitignore and .hgitattributes that govern the
// whole offer. Both live at the offered root (ADR 0014/0015) and are read
// fresh on every offer, never cached.
func newBuilder(r *repo.Repo, work string, opts *Options) *builder {
	return &builder{
		r:    r,
		root: work,
		ign:  ignore.ParseIgnore(readRules(work, ".hgitignore")),
		att:  attrs.ParseAttrs(readRules(work, ".hgitattributes")),
		list: &object.Attrs{},
		opts: opts,
	}
}

// buildTree ports TreeBuildRecursive: one directory level, depth first.
// relDir is the directory's path relative to the offered root ("" at the
// root) and old is the tree that occupied the same relative position in the
// parent commit, or nil. Objects are appended as the walk goes, children
// before the tree that names them, because a tree's hash needs its full
// content first; that order is what the archive's record order records.
func (b *builder) buildTree(relDir string, old *object.Tree, depth int) (*object.Tree, error) {
	if depth > MaxTreeDepth {
		return nil, ErrTooDeep
	}
	nodes, err := workdir.ListDir(filepath.Join(b.root, filepath.FromSlash(relDir)))
	if err != nil {
		return nil, err
	}
	tree := &object.Tree{}
	for _, n := range nodes {
		if len(n.Name) > maxNameLen {
			return nil, ErrNameTooLong
		}
		if !n.IsDir {
			e, kept, err := b.addFile(old, relDir, n.Name)
			if err != nil {
				return nil, err
			}
			if kept {
				tree.Entries = append(tree.Entries, e)
			}
			continue
		}

		rel := path.Join(relDir, n.Name)
		// The old entry of the same NAME decides two separate things: that
		// this name was tracked at all (ADR 0014's safety rule, which holds
		// even when the old entry was a file - a real kind change), and, only
		// when it was a tree, the old subtree to diff against and the
		// identity to carry forward.
		oldEntry, tracked := find(old, n.Name)
		if !tracked && b.ign.IgnoredLast(rel) {
			b.ignored(rel)
			continue
		}
		foundOldSub := tracked && oldEntry.ChildType == archive.Tree
		var oldSub *object.Tree
		if foundOldSub {
			// An unresolvable old subtree still carries its identity
			// forward, as the HolyC's own NULL old_sub_tree_content does.
			if t, err := b.r.Tree(oldEntry.ChildHash); err == nil {
				oldSub = t
			}
		}

		child, err := b.buildTree(rel, oldSub, depth+1)
		if err != nil {
			return nil, err
		}
		// A directory with nothing trackable in it - empty on disk, or
		// everything under it ignored - is never tracked: no tree object is
		// stored and no entry is encoded.
		if len(child.Entries) == 0 {
			continue
		}
		h := b.r.Append(archive.Tree, child.Encode())
		id := oldEntry.EntityID
		if !foundOldSub {
			if id = NewEntityID(); id == 0 {
				return nil, ErrEntityID
			}
		}
		tree.Entries = append(tree.Entries, object.Entry{
			Name:      n.Name,
			ChildType: archive.Tree,
			ChildHash: h,
			EntityID:  id,
		})
	}
	return tree, nil
}

// addFile stores one working-directory file as a blob and returns its tree
// entry, resolving identity against old - this directory level's own parent
// tree - and accumulating its mode into the one commit-level attrs list.
// kept is false when an ignore rule hid the name, in which case the file is
// never read at all.
func (b *builder) addFile(old *object.Tree, relDir, name string) (object.Entry, bool, error) {
	var e object.Entry
	rel := path.Join(relDir, name)

	// ADR 0014: an ignore rule can only ever hide a name the parent commit
	// does not already track. Checked before any ignore lookup.
	_, tracked := find(old, name)
	if !tracked && b.ign.IgnoredLast(rel) {
		b.ignored(rel)
		return e, false, nil
	}
	content, err := workdir.Read(b.root, rel)
	if err != nil {
		return e, false, err
	}
	blob := b.r.Append(archive.Blob, content)

	id, ok := CarryEntityID(b.r, old, name, blob, content)
	if !ok {
		if id = NewEntityID(); id == 0 {
			return e, false, ErrEntityID
		}
	}

	// ADR 0015: mode is a property of the file's current state, never carried
	// forward. Only a non-default mode costs an attrs entry. A dir-contents
	// pattern is anchored on this level's own directory, which rel carries.
	mode, explicit := b.att.Mode(rel)
	if !explicit && attrs.DetectBinary(content) {
		mode |= object.ModeBinary
	}
	if mode != 0 {
		b.list.Entries = append(b.list.Entries, object.AttrEntry{EntityID: id, Mode: mode})
	}
	return object.Entry{Name: name, ChildType: archive.Blob, ChildHash: blob, EntityID: id}, true, nil
}

func (b *builder) ignored(rel string) {
	if b.opts.OnIgnored != nil {
		b.opts.OnIgnored(rel)
	}
}

// finish appends the root tree, the attrs object if any file earned one, and
// the commit; logs the HEAD transition, clears the redo log, moves HEAD and
// saves. Identical for both paths, as it is in the HolyC.
func (b *builder) finish(p string, tree *object.Tree, parent archive.Hash, hasParent bool) (archive.Hash, error) {
	var zero archive.Hash
	c := &object.Commit{
		Tree:           b.r.Append(archive.Tree, tree.Encode()),
		Timestamp:      clock.Now(),
		Message:        []byte(b.opts.Message),
		Relation:       b.opts.Relation,
		RelationTarget: b.opts.RelationTarget,
		RelationEntity: b.opts.RelationEntity,
	}
	if hasParent {
		c.Parents = []archive.Hash{parent}
	}
	if len(b.list.Entries) > 0 {
		h := b.r.Append(archive.Attrs, b.list.Encode())
		c.Attrs = &h
	}
	commit := b.r.Append(archive.Commit, c.Encode())

	// OpLogAppend: log the HEAD transition (all-zero prev for a root
	// offering) and clear the redo log, since new work invalidates whatever
	// was undone.
	var prev archive.Hash
	if hasParent {
		prev = parent
	}
	b.r.Meta.Append(p, meta.TagOpLog, meta.OpLogEntry{Timestamp: c.Timestamp, Prev: prev, New: commit}.Encode())
	for {
		if _, ok := b.r.Meta.PopLast(p, meta.TagRedoLog); !ok {
			break
		}
	}
	if err := b.r.SetHead(p, commit); err != nil {
		return zero, err
	}
	if err := b.r.Save(); err != nil {
		return zero, err
	}
	return commit, nil
}

// CarryEntityID resolves the identity a file keeps from the parent commit's
// tree, in the HolyC's order: exact name, then exact content hash (ADR 0009),
// then fuzzy similarity (ADR 0009 addendum). ok is false when the name is
// genuinely new and the caller must generate a fresh id. The recursive path
// calls it per directory level, against that level's own old tree - a file
// moved between directories is not tracked across the move (ADR 0010).
func CarryEntityID(r *repo.Repo, old *object.Tree, name string, blob archive.Hash, content []byte) (uint64, bool) {
	if old == nil {
		return 0, false
	}
	if e, ok := old.Find(name); ok {
		return e.EntityID, true
	}
	for _, e := range old.Entries { // TreeFindEntryByHash: any entry, not only blobs
		if e.ChildHash == blob {
			return e.EntityID, true
		}
	}
	return FindFuzzyRename(r, old, content)
}

// FindFuzzyRename ports OfferFindFuzzyRename: score target against every blob
// in old whose content the archive still holds and return the best-scoring
// entry's identity, if it reaches fossil.RenameSimilarityThreshold. Ties go
// to the entry seen first, and no cross-file disambiguation is attempted
// (ADR 0009). Size is not a criterion - the offer path has no per-file cap -
// so this is the unbounded scan the HolyC runs.
func FindFuzzyRename(r *repo.Repo, old *object.Tree, target []byte) (uint64, bool) {
	if old == nil {
		return 0, false
	}
	best, bestID := -1, uint64(0)
	for _, e := range old.Entries {
		if e.ChildType != archive.Blob {
			continue
		}
		rec, ok := r.Get(e.ChildHash)
		if !ok || rec.Type() != archive.Blob {
			continue
		}
		if sim := fossil.SimilarityPercent(rec.Content(), target); sim > best {
			best, bestID = sim, e.EntityID
		}
	}
	if best >= fossil.RenameSimilarityThreshold {
		return bestID, true
	}
	return 0, false
}

// parentTree resolves the parent commit's tree, or nil when there is no
// parent or it cannot be read - the same "no old identity to carry forward"
// state the HolyC reaches by leaving old_tree_content NULL.
func parentTree(r *repo.Repo, h archive.Hash, hasParent bool) *object.Tree {
	if !hasParent {
		return nil
	}
	c, err := r.Commit(h)
	if err != nil {
		return nil
	}
	t, err := r.Tree(c.Tree)
	if err != nil {
		return nil
	}
	return t
}

func find(t *object.Tree, name string) (object.Entry, bool) {
	if t == nil {
		return object.Entry{}, false
	}
	return t.Find(name)
}

// readRules returns a rules file's text, or "" when it is absent or
// unreadable (IgnoreLoad/AttrsRulesLoad treat a missing file as no rules).
func readRules(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(b)
}
