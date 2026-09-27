package bundle

import (
	"sort"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// objectRefs is the outgoing references of one object, mirroring
// pkg/hgit/check's unexported refs (same rule set: commit -> tree + parents +
// relation target + attrs; tree -> children). A bundle never contains an
// object only reachable through a conflict record (BUNDLE.md), so conflict
// objects are never walked into and Type 5 refs are not followed here.
func objectRefs(t archive.Type, content []byte) (out []archive.Hash, ok bool) {
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
	}
	return out, true
}

// closure returns the reachable set of object hashes from heads (each
// object's own hash, plus everything it references, recursively), reading
// through get. A hash get cannot resolve is simply not expanded further,
// since Verify (not closure) is responsible for reporting an unresolved
// reference.
func closure(get func(archive.Hash) (archive.Record, bool), heads []archive.Hash) map[archive.Hash]bool {
	seen := map[archive.Hash]bool{}
	var stack []archive.Hash
	stack = append(stack, heads...)
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[h] {
			continue
		}
		rec, ok := get(h)
		if !ok {
			continue
		}
		seen[h] = true
		if out, ok := objectRefs(rec.Type(), rec.Content()); ok {
			stack = append(stack, out...)
		}
	}
	return seen
}

// orderedClosure is closure's deterministic-order counterpart used to build
// a bundle's object records: depth-first post-order from heads in name
// order, children before parents, each object exactly once (BUNDLE.md
// "Objects"). exclude is the prerequisite closure; excluded hashes are
// skipped entirely (not visited, not emitted).
func orderedClosure(r *repo.Repo, heads []HeadEntry, exclude map[archive.Hash]bool) []archive.Hash {
	sorted := append([]HeadEntry(nil), heads...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	visited := map[archive.Hash]bool{}
	var order []archive.Hash
	var visit func(h archive.Hash)
	visit = func(h archive.Hash) {
		if visited[h] || exclude[h] {
			return
		}
		rec, ok := r.Get(h)
		if !ok {
			return
		}
		visited[h] = true
		if out, ok := objectRefs(rec.Type(), rec.Content()); ok {
			for _, c := range out {
				visit(c)
			}
		}
		order = append(order, h) // post-order: children already appended above
	}
	for _, he := range sorted {
		visit(he.Head)
	}
	return order
}

// ancestorsOf returns start's complete ancestor set (including start),
// walking every parent edge - mirrors mergebase.collectAllAncestors (that
// helper is unexported and package-private to mergebase, so this is an
// independent copy for bundle's own fast-forward/divergence decision).
// commit reads through get, so it can see objects a bundle just added to a
// repo's in-memory index as well as ones already on disk.
func ancestorsOf(get func(archive.Hash) (*object.Commit, error), start archive.Hash) (map[archive.Hash]bool, error) {
	visited := map[archive.Hash]bool{start: true}
	queue := []archive.Hash{start}
	for i := 0; i < len(queue); i++ {
		c, err := get(queue[i])
		if err != nil {
			return nil, err
		}
		for _, p := range c.Parents {
			if !visited[p] {
				visited[p] = true
				queue = append(queue, p)
			}
		}
	}
	return visited, nil
}
