// Package offer records a working directory as a new commit: the port of
// Offer.HC's flat path (HgitOffer / HgitOfferWithRelation). It never prints;
// the HolyC's per-file OFFER_IGNORED notice is surfaced through
// Options.OnIgnored instead.
package offer

import (
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"os"
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

// MaxFuzzyRenameBytes bounds which files take part in fuzzy (edited-during-
// rename) detection. The HolyC buffers fuzzy-rename candidates in fixed
// 512-byte slots (Status.HC), so a larger file never scores; mirrored here as
// a parity choice, and raisable on its own. Exact-name and exact-content
// identity are unaffected, at any size.
const MaxFuzzyRenameBytes = 512

// MaxMessageLen is the longest commit message this port accepts. The HolyC
// builds its commit in a 512-byte stack buffer; rather than overflow or
// truncate, an over-long message is refused.
const MaxMessageLen = 255

// maxNameLen is what a tree entry's U8 name_len can encode.
const maxNameLen = 255

var (
	ErrMessageTooLong = errors.New("offer: message longer than 255 bytes")
	ErrNameTooLong    = errors.New("offer: file name longer than 255 bytes")
	ErrEntityID       = errors.New("offer: could not generate a nonzero entity id")
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
// is what plain `offer` does; correct/revert/reconcile set them.
type Options struct {
	Message        string
	Relation       object.Relation
	RelationTarget archive.Hash
	RelationEntity uint64

	// OnIgnored, when set, is called with the name of each untracked file an
	// ignore rule hid - the HolyC prints OFFER_IGNORED there.
	OnIgnored func(name string)
}

// Offer records every file in work matching mask as a new commit on the
// current path, and saves the repository. It returns the new commit's hash.
//
// Nothing is written unless the whole offer succeeds: every input is
// validated before the first object is stored, and the archive and metadata
// are saved together at the end.
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

	// .hgitignore and .hgitattributes live in the working directory itself
	// (ADR 0014/0015), read fresh on every offer, never cached.
	ign := ignore.ParseIgnore(readRules(work, ".hgitignore"))
	att := attrs.ParseAttrs(readRules(work, ".hgitattributes"))

	path := r.CurrentPath()
	parent, hasParent := r.Head(path)
	old := parentTree(r, parent, hasParent)

	tree := &object.Tree{}
	list := &object.Attrs{}
	for _, f := range files {
		// ADR 0014: an ignore rule can only ever hide a name the parent
		// commit does not already track. Checked before any ignore lookup.
		_, tracked := find(old, f.Name)
		if !tracked && ign.Ignored(f.Name, false) {
			if opts.OnIgnored != nil {
				opts.OnIgnored(f.Name)
			}
			continue
		}
		blob := r.Put(archive.Blob, f.Content)

		id, ok := CarryEntityID(r, old, f.Name, blob, f.Content)
		if !ok {
			if id = NewEntityID(); id == 0 {
				return zero, ErrEntityID
			}
		}

		// ADR 0015: mode is a property of the file's current state, never
		// carried forward. Only a non-default mode costs an attrs entry.
		mode, explicit := att.Mode(f.Name)
		if !explicit && attrs.DetectBinary(f.Content) {
			mode |= object.ModeBinary
		}
		if mode != 0 {
			list.Entries = append(list.Entries, object.AttrEntry{EntityID: id, Mode: mode})
		}

		tree.Entries = append(tree.Entries, object.Entry{
			Name:      f.Name,
			ChildType: archive.Blob,
			ChildHash: blob,
			EntityID:  id,
		})
	}

	c := &object.Commit{
		Tree:           r.Put(archive.Tree, tree.Encode()),
		Timestamp:      clock.Now(),
		Message:        []byte(opts.Message),
		Relation:       opts.Relation,
		RelationTarget: opts.RelationTarget,
		RelationEntity: opts.RelationEntity,
	}
	if hasParent {
		c.Parents = []archive.Hash{parent}
	}
	if len(list.Entries) > 0 {
		h := r.Put(archive.Attrs, list.Encode())
		c.Attrs = &h
	}
	commit := r.Put(archive.Commit, c.Encode())

	// OpLogAppend: log the HEAD transition (all-zero prev for a root
	// offering) and clear the redo log, since new work invalidates whatever
	// was undone.
	var prev archive.Hash
	if hasParent {
		prev = parent
	}
	r.Meta.Append(path, meta.TagOpLog, meta.OpLogEntry{Timestamp: c.Timestamp, Prev: prev, New: commit}.Encode())
	for {
		if _, ok := r.Meta.PopLast(path, meta.TagRedoLog); !ok {
			break
		}
	}
	if err := r.SetHead(path, commit); err != nil {
		return zero, err
	}
	if err := r.Save(); err != nil {
		return zero, err
	}
	return commit, nil
}

// CarryEntityID resolves the identity a file keeps from the parent commit's
// tree, in the HolyC's order: exact name, then exact content hash (ADR 0009),
// then fuzzy similarity (ADR 0009 addendum). ok is false when the name is
// genuinely new and the caller must generate a fresh id. Shared with the
// recursive offertree port, which resolves identity per directory level.
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
// entry's identity, if it reaches the rename threshold. Ties go to the entry
// seen first, and no cross-file disambiguation is attempted (ADR 0009).
func FindFuzzyRename(r *repo.Repo, old *object.Tree, target []byte) (uint64, bool) {
	if old == nil || len(target) > MaxFuzzyRenameBytes {
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
		content := rec.Content()
		if len(content) > MaxFuzzyRenameBytes {
			continue
		}
		if sim := fossil.SimilarityPercent(content, target); sim > best {
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
