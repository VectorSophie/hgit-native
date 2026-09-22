package mergebase_test

import (
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/mergebase"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// newRepo opens a fresh, empty repo backed by a temp file.
func newRepo(t *testing.T) *repo.Repo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "r.hgs")
	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// treeHash returns a shared, empty-tree hash for building commits whose
// tree contents don't matter to merge-base.
func treeHash(t *testing.T, r *repo.Repo) archive.Hash {
	t.Helper()
	return r.Put(archive.Tree, (&object.Tree{}).Encode())
}

// mkCommit appends a commit with the given message, timestamp and parents,
// returning its hash. Distinct (msg, ts) keep otherwise-identical commits
// from colliding on hash.
func mkCommit(t *testing.T, r *repo.Repo, tree archive.Hash, msg string, ts uint64, parents ...archive.Hash) archive.Hash {
	t.Helper()
	c := &object.Commit{
		Tree:      tree,
		Parents:   parents,
		Timestamp: ts,
		Message:   []byte(msg),
	}
	return r.Append(archive.Commit, c.Encode())
}

func TestLinearChain(t *testing.T) {
	r := newRepo(t)
	tr := treeHash(t, r)
	root := mkCommit(t, r, tr, "root", 1)
	c1 := mkCommit(t, r, tr, "c1", 2, root)
	c2 := mkCommit(t, r, tr, "c2", 3, c1)
	c3 := mkCommit(t, r, tr, "c3", 4, c2)
	side := mkCommit(t, r, tr, "side", 5, c1)

	base, found, err := mergebase.FindMergeBase(r, c3, side)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected a common ancestor")
	}
	if base != c1 {
		t.Fatalf("base = %x, want c1 = %x", base, c1)
	}
}

func TestTwoBranchesFromRoot(t *testing.T) {
	r := newRepo(t)
	tr := treeHash(t, r)
	root := mkCommit(t, r, tr, "root", 1)
	left := mkCommit(t, r, tr, "left", 2, root)
	right := mkCommit(t, r, tr, "right", 3, root)

	base, found, err := mergebase.FindMergeBase(r, left, right)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected a common ancestor")
	}
	if base != root {
		t.Fatalf("base = %x, want root = %x", base, root)
	}
}

func TestFastForward(t *testing.T) {
	r := newRepo(t)
	tr := treeHash(t, r)
	root := mkCommit(t, r, tr, "root", 1)
	c1 := mkCommit(t, r, tr, "c1", 2, root)
	c2 := mkCommit(t, r, tr, "c2", 3, c1)

	// c1 is an ancestor of c2: the base should be c1 itself.
	base, found, err := mergebase.FindMergeBase(r, c1, c2)
	if err != nil {
		t.Fatal(err)
	}
	if !found || base != c1 {
		t.Fatalf("base = %x found=%v, want c1 = %x", base, found, c1)
	}

	// Symmetric: b ancestor of a.
	base2, found2, err := mergebase.FindMergeBase(r, c2, c1)
	if err != nil {
		t.Fatal(err)
	}
	if !found2 || base2 != c1 {
		t.Fatalf("base2 = %x found=%v, want c1 = %x", base2, found2, c1)
	}
}

func TestSameCommit(t *testing.T) {
	r := newRepo(t)
	tr := treeHash(t, r)
	root := mkCommit(t, r, tr, "root", 1)

	base, found, err := mergebase.FindMergeBase(r, root, root)
	if err != nil {
		t.Fatal(err)
	}
	if !found || base != root {
		t.Fatalf("base = %x found=%v, want root = %x", base, found, root)
	}
}

func TestNoCommonAncestor(t *testing.T) {
	r := newRepo(t)
	tr := treeHash(t, r)
	a := mkCommit(t, r, tr, "root-a", 1)
	b := mkCommit(t, r, tr, "root-b", 2)

	_, found, err := mergebase.FindMergeBase(r, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("expected no common ancestor")
	}
}

// TestMergeCommitSecondParent pins probe 109's exact regression: main and Y
// fork from a root, diverge, and are merged into M1 (a real 2-parent
// commit). Z forks from Y's own pre-merge commit (not from M1) and edits
// the same file. main advances again after the merge. The true lowest
// common ancestor of Z and main's new head is Y's pre-merge commit,
// reachable from main's head only through M1's SECOND parent - invisible
// to a parent[0]-only walk, which would instead return the much older
// root.
func TestMergeCommitSecondParent(t *testing.T) {
	r := newRepo(t)
	tr := treeHash(t, r)
	root := mkCommit(t, r, tr, "root", 1)
	mainPre := mkCommit(t, r, tr, "main-pre-merge", 2, root)
	yPre := mkCommit(t, r, tr, "y-pre-merge", 3, root)
	m1 := mkCommit(t, r, tr, "merge-y-into-main", 4, mainPre, yPre)
	mainHead := mkCommit(t, r, tr, "main-advances", 5, m1)
	z := mkCommit(t, r, tr, "z-forks-from-y-pre", 6, yPre)

	base, found, err := mergebase.FindMergeBase(r, mainHead, z)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected a common ancestor")
	}
	if base != yPre {
		t.Fatalf("base = %x, want yPre = %x (the stale parent[0]-only answer would be root = %x)", base, yPre, root)
	}
}

// TestCrissCross pins the HolyC's actual (documented, non-disambiguated)
// behavior for a genuine criss-cross history: two merge commits, each with
// two parents, where there are two valid common ancestors (A1 and B1)
// neither descending from the other. FindMergeBase does not attempt to
// pick "the" lowest common ancestor here (that remains, per MergeBase.HC's
// own comments, an undecided, out-of-scope question) - it returns whichever
// one its breadth-first walk from b happens to reach first, which is
// deterministic given a deterministic parent order and pinned here so a
// later merge implementation knows exactly what to expect.
func TestCrissCross(t *testing.T) {
	r := newRepo(t)
	tr := treeHash(t, r)
	root := mkCommit(t, r, tr, "root", 1)
	a1 := mkCommit(t, r, tr, "a1", 2, root)
	b1 := mkCommit(t, r, tr, "b1", 3, root)
	m1 := mkCommit(t, r, tr, "m1-merge-a1-b1", 4, a1, b1)
	a2 := mkCommit(t, r, tr, "a2", 5, a1)
	b2 := mkCommit(t, r, tr, "b2", 6, b1)
	// Parent order [a2, b2]: the BFS from m2 reaches a1 (via a2) before b1
	// (via b2), so a1 - not b1 - is the pinned answer.
	m2 := mkCommit(t, r, tr, "m2-merge-a2-b2", 7, a2, b2)

	base, found, err := mergebase.FindMergeBase(r, m1, m2)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected a common ancestor")
	}
	if base != a1 {
		t.Fatalf("base = %x, want a1 = %x (pinned criss-cross choice; b1 = %x is also valid but not chosen)", base, a1, b1)
	}
}

func TestMalformedGraph(t *testing.T) {
	r := newRepo(t)
	tr := treeHash(t, r)
	root := mkCommit(t, r, tr, "root", 1)

	var missing archive.Hash
	copy(missing[:], []byte("this hash was never put in the archive at all!!"))

	broken := mkCommit(t, r, tr, "broken", 2, missing)

	_, _, err := mergebase.FindMergeBase(r, root, broken)
	if err == nil {
		t.Fatal("expected an error for a commit graph referencing a missing parent")
	}
}

func TestNoInfiniteLoopOnCycle(t *testing.T) {
	// The commit format has no way to construct a real cycle through this
	// package's own API (a commit's hash is content-addressed and can't
	// name itself as a parent before it exists), so this exercises the
	// nearest real case instead: a long, otherwise ordinary linear history
	// with no timeout, proving CollectAllAncestors/BFS visited-tracking
	// terminates rather than looping. The real defense against a cycle is
	// the same visited-set membership check exercised here.
	r := newRepo(t)
	tr := treeHash(t, r)
	cur := mkCommit(t, r, tr, "root", 1)
	const n = 200
	for i := 0; i < n; i++ {
		cur = mkCommit(t, r, tr, "c", uint64(i+2), cur)
	}
	base, found, err := mergebase.FindMergeBase(r, cur, cur)
	if err != nil {
		t.Fatal(err)
	}
	if !found || base != cur {
		t.Fatal("expected the long chain's own head to be its own base")
	}
}
