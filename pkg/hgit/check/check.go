// Package check is the repository integrity check (port of Check.HC): hash
// verification, referential integrity and dangling-object detection. It never
// prints; internal/cli formats the report.
package check

import (
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

type DanglingObj struct {
	Type archive.Type
	Hash archive.Hash
}

// BrokenRef is one CHECK_BROKEN_REF line: Kind is the HolyC token
// (commit_tree_missing, tree_child_missing, ...).
type BrokenRef struct {
	Kind string
	Hash archive.Hash
}

type Report struct {
	Objects       int
	FormatVersion uint16
	HeaderCount   uint64
	HashBad       []archive.Hash
	RefsBroken    int
	Broken        []BrokenRef
	Dangling      []DanglingObj
}

type checker struct {
	r   *repo.Repo
	rep *Report
}

func (c *checker) has(h archive.Hash) bool { _, ok := c.r.Get(h); return ok }

func (c *checker) ref(kind string, h archive.Hash) {
	if !c.has(h) {
		c.broken(kind, h)
	}
}

func (c *checker) broken(kind string, h archive.Hash) {
	c.rep.RefsBroken++
	c.rep.Broken = append(c.rep.Broken, BrokenRef{kind, h})
}

// refs returns the outgoing references of one object; ok is false if the
// content does not decode.
func refs(t archive.Type, content []byte) (out []archive.Hash, ok bool) {
	switch t {
	case archive.Commit:
		cm, err := object.DecodeCommit(content)
		if err != nil {
			return nil, false
		}
		out = append(out, cm.Tree)
		out = append(out, cm.Parents...)
		if cm.Relation != object.RelNone {
			out = append(out, cm.RelationTarget)
		}
		if cm.Attrs != nil {
			out = append(out, *cm.Attrs)
		}
	case archive.Tree:
		tr, err := object.DecodeTree(content)
		if err != nil {
			return nil, false
		}
		for _, e := range tr.Entries {
			out = append(out, e.ChildHash)
		}
	case archive.Conflict:
		cf, err := object.DecodeConflict(content)
		if err != nil {
			return nil, false
		}
		for _, s := range []object.Side{cf.Base, cf.Ours, cf.Theirs} {
			if s.Present {
				out = append(out, s.Hash)
			}
		}
	}
	return out, true
}

// Run checks r. The order of Broken and Dangling matches Check.HC.
func Run(r *repo.Repo) Report {
	rep := Report{Objects: len(r.Arc.Records), FormatVersion: r.Arc.Header.Version, HeaderCount: r.Arc.Header.Count}
	c := &checker{r, &rep}
	for _, rec := range r.Arc.Records {
		if !rec.HashOK() {
			rep.HashBad = append(rep.HashBad, rec.Hash)
		}
	}

	// Referential pass, in archive order.
	for _, rec := range r.Arc.Records {
		t := rec.Type()
		out, ok := refs(t, rec.Content())
		if !ok {
			switch t {
			case archive.Conflict:
				c.broken("conflict_object_malformed", rec.Hash)
			case archive.Commit:
				c.broken("commit_malformed", rec.Hash) // native-only token
			case archive.Tree:
				c.broken("tree_malformed", rec.Hash) // native-only token
			}
			continue
		}
		switch t {
		case archive.Commit:
			cm, _ := object.DecodeCommit(rec.Content())
			c.ref("commit_tree_missing", cm.Tree)
			for _, p := range cm.Parents {
				c.ref("commit_parent_missing", p)
			}
			if cm.Relation != object.RelNone {
				c.ref("relation_target_missing", cm.RelationTarget)
			}
			if cm.Attrs != nil {
				c.ref("commit_attrs_missing", *cm.Attrs)
			}
		case archive.Tree:
			for _, h := range out {
				c.ref("tree_child_missing", h)
			}
		}
	}

	paths := []string{"main"}
	for _, m := range r.Meta.Records {
		if m.Tag == meta.TagPathDeclared {
			paths = append(paths, m.Name)
		}
	}
	// In-progress merge conflicts: a root and a referential check.
	var conflictRoots []archive.Hash
	for _, name := range paths {
		if _, ok := r.Meta.Find(name, meta.TagMergeState); !ok {
			continue
		}
		for _, m := range r.Meta.All(name, meta.TagConflict) {
			if len(m.Payload) < archive.HashLen {
				continue
			}
			var ch archive.Hash
			copy(ch[:], m.Payload)
			conflictRoots = append(conflictRoots, ch)
			rec, ok := r.Get(ch)
			if !ok {
				c.broken("conflict_object_missing", ch)
				continue
			}
			cf, err := object.DecodeConflict(rec.Content())
			if err != nil {
				c.broken("conflict_malformed", ch)
				continue
			}
			for _, s := range []object.Side{cf.Base, cf.Ours, cf.Theirs} {
				if s.Present {
					c.ref("conflict_side_missing", s.Hash)
				}
			}
		}
	}

	// Reachability. Marking is by hash, which also covers duplicate records
	// of one hash (HolyC's coalescing pass). The stack is explicit so a
	// forged cyclic graph terminates and deep history cannot overflow.
	reach := map[archive.Hash]bool{}
	var stack []archive.Hash
	for _, name := range paths {
		if h, ok := r.Head(name); ok {
			stack = append(stack, h)
		}
	}
	stack = append(stack, conflictRoots...)
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if reach[h] {
			continue
		}
		rec, ok := r.Get(h)
		if !ok {
			continue
		}
		reach[h] = true
		if out, ok := refs(rec.Type(), rec.Content()); ok {
			stack = append(stack, out...)
		}
	}
	for _, rec := range r.Arc.Records {
		if !reach[rec.Hash] {
			rep.Dangling = append(rep.Dangling, DanglingObj{rec.Type(), rec.Hash})
		}
	}
	return rep
}
