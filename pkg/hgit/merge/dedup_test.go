package merge_test

import (
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
)

// ADR 0019: the same conflicting merge attempted twice stores its conflict
// evidence once, and the retry's conflict records point at that one object.
func TestMergeRetriedAfterAbortDoesNotDuplicateConflictEvidence(t *testing.T) {
	f := oneConflict(t)
	r := f.open()
	objects := len(r.Arc.Records)
	first, _ := merge.Conflicts(r)
	if err := merge.Abort(f.open()); err != nil {
		t.Fatal(err)
	}
	res, err := f.merge("feat")
	if err != nil || len(res.Conflicts) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	r = f.open()
	if len(r.Arc.Records) != objects || r.Arc.Header.Count != uint64(objects) {
		t.Fatalf("%d records (header %d), want %d", len(r.Arc.Records), r.Arc.Header.Count, objects)
	}
	if res.Conflicts[0].Hash != first[0].Hash || len(r.Meta.All("main", meta.TagConflict)) != 1 {
		t.Fatal("retry does not reference the stored evidence exactly once")
	}
}

// A clean merge whose merged tree equals a tree already stored adds only the
// commit (here: theirs deleted what ours never had, so merged == ours).
func TestCleanMergeReusesExistingTrees(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "a")
	f.write("b.txt", "b")
	f.offer("root")
	f.diverge(func() { f.rm("b.txt") }, func() { f.write("a.txt", "a main") }) // the disk is shared: b.txt is already gone for main too
	before := len(f.open().Arc.Records)
	res, err := f.merge("feat")
	if err != nil || len(res.Conflicts) != 0 || res.FastForward || res.UpToDate {
		t.Fatalf("%+v %v", res, err)
	}
	if got := len(f.open().Arc.Records) - before; got != 1 {
		t.Fatalf("merge added %d records, want the commit only", got)
	}
}
