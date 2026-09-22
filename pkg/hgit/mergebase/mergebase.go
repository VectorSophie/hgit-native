// Package mergebase finds the lowest common ancestor of two commits. Ports
// MergeBase.HC's fixed algorithm (probe 109): a real ancestor-set walk over
// every parent of a commit, not just parent[0] - a parent[0]-only walk is
// not merely ambiguous in a criss-cross history, it is flatly wrong the
// moment either side's history passes through a real merge commit, since a
// merge's second (and later) parents are invisible to it.
package mergebase

import (
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// collectAllAncestors returns start's complete ancestor set (including
// start itself), via a real BFS over every parent edge - not just
// parent[0]. Mirrors MergeBase.HC's CollectAllAncestors.
func collectAllAncestors(r *repo.Repo, start archive.Hash) (map[archive.Hash]bool, error) {
	visited := map[archive.Hash]bool{start: true}
	queue := []archive.Hash{start}
	for i := 0; i < len(queue); i++ {
		c, err := r.Commit(queue[i])
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

// FindMergeBase finds the lowest common ancestor of a and b. It mirrors
// MergeBase.HC's fixed FindMergeBase: it builds a's complete ancestor set
// (every parent edge), then does a breadth-first walk from b, level by
// level, over every one of b's parents too, returning the first node found
// in a's ancestor set - the closest (to b) common ancestor. That is a real,
// practical proxy for "most recent common ancestor" and correctly reaches a
// merge's non-"ours" parent, but it is not Git's exact "recursive
// merge-base" algorithm: a genuine criss-cross history (multiple common
// ancestors, none descending from another) has no single correct answer
// here either - FindMergeBase returns whichever one the breadth-first walk
// from b reaches first, deterministically, but without attempting to
// disambiguate further. That remains a documented, out-of-scope limitation
// carried over from the HolyC, not something this port fixes.
//
// found is false with a nil error when a and b share no common ancestor
// (disconnected histories) - not an error case, since the archive itself
// is well-formed and the answer is simply "no merge base exists". A
// non-nil error means the commit graph itself is malformed (a parent hash
// that does not resolve to a commit in the archive) - unlike the HolyC,
// which silently treats an unresolvable parent as a dead end and keeps
// going (see docs/porting-notes.md), this port surfaces it as an error
// rather than risking a silently-wrong answer.
func FindMergeBase(r *repo.Repo, a, b archive.Hash) (archive.Hash, bool, error) {
	ancestorsA, err := collectAllAncestors(r, a)
	if err != nil {
		var zero archive.Hash
		return zero, false, err
	}

	visited := map[archive.Hash]bool{b: true}
	queue := []archive.Hash{b}
	for i := 0; i < len(queue); i++ {
		cur := queue[i]
		if ancestorsA[cur] {
			return cur, true, nil
		}
		c, err := r.Commit(cur)
		if err != nil {
			var zero archive.Hash
			return zero, false, err
		}
		for _, p := range c.Parents {
			if !visited[p] {
				visited[p] = true
				queue = append(queue, p)
			}
		}
	}

	var zero archive.Hash
	return zero, false, nil
}
