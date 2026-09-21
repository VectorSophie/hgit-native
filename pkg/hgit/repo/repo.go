// Package repo opens a .hgs archive together with its <path>.m metadata file
// and answers object, path and history queries. Ports Index.HC, Paths.HC (read
// side), History.HC and See.HC. It never prints.
package repo

import (
	"errors"
	"fmt"
	"os"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
)

var (
	ErrNoHead      = errors.New("repo: current path has no head")
	ErrBrokenChain = errors.New("repo: broken chain")
	ErrNotFound    = errors.New("repo: object not found")
	ErrNameTooLong = errors.New("repo: path name longer than 255 bytes")
)

// NotTypeError reports an object found under a hash but of the wrong type.
type NotTypeError struct{ Got archive.Type }

func (e *NotTypeError) Error() string { return fmt.Sprintf("repo: unexpected object type %d", e.Got) }

type Repo struct {
	Path string // path of the .hgs file
	Arc  *archive.Archive
	Meta *meta.File
	idx  map[archive.Hash]int
}

// Open reads path and path+".m"; a missing .m is an empty File.
func Open(path string) (*Repo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	a, err := archive.Parse(raw)
	if err != nil {
		return nil, err
	}
	m := &meta.File{}
	if mb, err := os.ReadFile(path + ".m"); err == nil {
		if m, err = meta.Parse(mb); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	r := &Repo{Path: path, Arc: a, Meta: m, idx: make(map[archive.Hash]int, len(a.Records))}
	for i, rec := range a.Records {
		if _, dup := r.idx[rec.Hash]; !dup { // first occurrence wins, as the linear scan did
			r.idx[rec.Hash] = i
		}
	}
	return r, nil
}

// writeAtomic writes b to a temp file beside path, then renames it over path.
// ponytail: on Windows os.Rename replaces an existing file (MoveFileEx with
// REPLACE_EXISTING) but can fail if another process holds it open; not retried.
func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Save writes .hgs first, then .m. Header.Count is synced to the record count.
func (r *Repo) Save() error {
	r.Arc.Header.Count = uint64(len(r.Arc.Records))
	if err := writeAtomic(r.Path, r.Arc.Marshal()); err != nil {
		return err
	}
	return writeAtomic(r.Path+".m", r.Meta.Marshal())
}

// Get returns a copy of the record for h.
func (r *Repo) Get(h archive.Hash) (archive.Record, bool) {
	i, ok := r.idx[h]
	if !ok {
		return archive.Record{}, false
	}
	rec := r.Arc.Records[i]
	rec.Data = append([]byte(nil), rec.Data...)
	return rec, true
}

// Put appends the object if absent and returns its hash.
func (r *Repo) Put(t archive.Type, content []byte) archive.Hash {
	rec := archive.NewObject(t, content)
	if _, ok := r.idx[rec.Hash]; !ok {
		r.idx[rec.Hash] = len(r.Arc.Records)
		r.Arc.Records = append(r.Arc.Records, rec)
		r.Arc.Header.Count = uint64(len(r.Arc.Records))
	}
	return rec.Hash
}

// CurrentPath is the current path's name, "main" if unset.
func (r *Repo) CurrentPath() string {
	if rec, ok := r.Meta.Find("", meta.TagCurrent); ok {
		return string(rec.Payload)
	}
	return "main"
}

func (r *Repo) Head(path string) (archive.Hash, bool) {
	var h archive.Hash
	rec, ok := r.Meta.Find(path, meta.TagHead)
	if !ok || len(rec.Payload) < archive.HashLen {
		return h, false
	}
	copy(h[:], rec.Payload)
	return h, true
}

// SetHead records h as path's head. Names over 255 bytes cannot be encoded
// and return ErrNameTooLong, leaving the metadata unchanged.
func (r *Repo) SetHead(path string, h archive.Hash) error {
	if len(path) > 255 {
		return ErrNameTooLong
	}
	r.Meta.Set(path, meta.TagHead, h[:])
	return nil
}

func (r *Repo) typed(h archive.Hash, t archive.Type) ([]byte, error) {
	rec, ok := r.Get(h)
	if !ok {
		return nil, ErrNotFound
	}
	if rec.Type() != t {
		return nil, &NotTypeError{rec.Type()}
	}
	return rec.Content(), nil
}

func (r *Repo) Commit(h archive.Hash) (*object.Commit, error) {
	b, err := r.typed(h, archive.Commit)
	if err != nil {
		return nil, err
	}
	return object.DecodeCommit(b)
}

func (r *Repo) Tree(h archive.Hash) (*object.Tree, error) {
	b, err := r.typed(h, archive.Tree)
	if err != nil {
		return nil, err
	}
	return object.DecodeTree(b)
}

type HistoryLine struct {
	Timestamp uint64
	Message   string
	Hash      archive.Hash
}

// History walks the current path's first-parent chain, newest first (only the
// first parent, as History.HC). On a broken link it returns the lines so far
// plus ErrBrokenChain or a *NotTypeError; with no head, ErrNoHead.
func (r *Repo) History() ([]HistoryLine, error) {
	cur, ok := r.Head(r.CurrentPath())
	if !ok {
		return nil, ErrNoHead
	}
	var out []HistoryLine
	seen := map[archive.Hash]bool{}
	for {
		if seen[cur] {
			return out, ErrBrokenChain
		}
		seen[cur] = true
		c, err := r.Commit(cur)
		if errors.Is(err, ErrNotFound) {
			return out, ErrBrokenChain
		}
		if err != nil {
			return out, err
		}
		out = append(out, HistoryLine{c.Timestamp, string(c.Message), cur})
		if len(c.Parents) == 0 {
			return out, nil
		}
		cur = c.Parents[0]
	}
}

// SeeEntry is one tree entry at a nesting depth; Missing marks a nested tree
// object absent from the archive.
type SeeEntry struct {
	Entry   object.Entry
	Depth   int
	Missing bool
}

type SeeResult struct {
	Commit  *object.Commit
	Entries []SeeEntry // depth-first, in tree order
	Count   int        // top-level entry count
}

// ErrTreeNotFound is returned by See (with a partial result) when the
// commit's root tree is absent.
var ErrTreeNotFound = errors.New("repo: tree not found")

// See ports HgitSee: the commit, then its tree flattened depth-first.
// The commit lookup fails with ErrNotFound or *NotTypeError.
func (r *Repo) See(h archive.Hash) (*SeeResult, error) {
	c, err := r.Commit(h)
	if err != nil {
		return nil, err
	}
	t, err := r.Tree(c.Tree)
	if errors.Is(err, ErrNotFound) {
		return &SeeResult{Commit: c}, ErrTreeNotFound
	}
	if err != nil {
		return nil, err
	}
	res := &SeeResult{Commit: c, Count: len(t.Entries)}
	r.flatten(t, 0, map[archive.Hash]bool{}, res)
	return res, nil
}

func (r *Repo) flatten(t *object.Tree, depth int, active map[archive.Hash]bool, res *SeeResult) {
	for _, e := range t.Entries {
		se := SeeEntry{Entry: e, Depth: depth}
		if e.ChildType != archive.Tree {
			res.Entries = append(res.Entries, se)
			continue
		}
		child, err := r.Tree(e.ChildHash)
		if err != nil || active[e.ChildHash] { // missing, malformed or cyclic
			se.Missing = true
			res.Entries = append(res.Entries, se)
			continue
		}
		res.Entries = append(res.Entries, se)
		active[e.ChildHash] = true
		r.flatten(child, depth+1, active, res)
		delete(active, e.ChildHash)
	}
}
