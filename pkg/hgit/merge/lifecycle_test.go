package merge_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
)

// oneConflict builds the standard one-conflict shape: c.txt edited differently
// on both sides, plus an untouched file so the merged tree has real company.
func oneConflict(t *testing.T) *fx {
	t.Helper()
	f := setup(t)
	f.write("c.txt", "base_c")
	f.write("k.txt", "keep")
	f.offer("root")
	f.diverge(func() { f.write("c.txt", "feat_c") }, func() { f.write("c.txt", "main_c") })
	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %d", len(res.Conflicts))
	}
	return f
}

func TestConflictsWithoutMergeInProgress(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "a")
	f.offer("root")
	if _, err := merge.Conflicts(f.open()); !errors.Is(err, merge.ErrNoMergeInProgress) {
		t.Fatalf("got %v, want ErrNoMergeInProgress", err)
	}
}

func TestConflictsListsEveryRecordInOrder(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base_a")
	f.write("b.txt", "base_b")
	f.offer("root")
	f.diverge(
		func() { f.write("a.txt", "feat_a"); f.write("b.txt", "feat_b") },
		func() { f.write("a.txt", "main_a"); f.write("b.txt", "main_b") },
	)
	if _, err := f.merge("feat"); err != nil {
		t.Fatal(err)
	}
	cs, err := merge.Conflicts(f.open())
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("want 2 conflicts, got %d", len(cs))
	}
	for i, c := range cs {
		if c.Index != i || c.Resolved || c.Object == nil {
			t.Fatalf("conflict %d: %+v", i, c)
		}
	}
	if cs[0].Object.Path != "a.txt" || cs[1].Object.Path != "b.txt" {
		t.Fatalf("paths %q %q", cs[0].Object.Path, cs[1].Object.Path)
	}
}

func TestResolveTakeOursRecordsOursHashAndSurvivesReopen(t *testing.T) {
	f := oneConflict(t)
	r := f.open()
	cs, _ := merge.Conflicts(r)
	want := cs[0].Object.Ours.Hash
	path, err := merge.Resolve(r, 0, "take-ours")
	if err != nil || path != "c.txt" {
		t.Fatalf("Resolve: %q %v", path, err)
	}
	cs, err = merge.Conflicts(f.open()) // reopened: it survived a restart
	if err != nil {
		t.Fatal(err)
	}
	if !cs[0].Resolved || cs[0].Resolution != want {
		t.Fatalf("got resolved=%v %s, want ours %s", cs[0].Resolved, cs[0].Resolution.Hex(), want.Hex())
	}
}

func TestResolveTakeTheirsRecordsTheirsHash(t *testing.T) {
	f := oneConflict(t)
	r := f.open()
	cs, _ := merge.Conflicts(r)
	want := cs[0].Object.Theirs.Hash
	if _, err := merge.Resolve(r, 0, "take-theirs"); err != nil {
		t.Fatal(err)
	}
	cs, _ = merge.Conflicts(f.open())
	if cs[0].Resolution != want {
		t.Fatalf("got %s, want theirs %s", cs[0].Resolution.Hex(), want.Hex())
	}
}

// An absent side is a real thing to resolve TO: the record keeps an all-zero
// resolution hash, and `merge continue` omits the entry.
func TestResolveToAbsentSideIsRecordedAsZeroAndDeletes(t *testing.T) {
	f := setup(t)
	f.write("c.txt", "base_c")
	f.write("k.txt", "keep")
	f.offer("root")
	f.diverge(func() { f.rm("c.txt") }, func() { f.write("c.txt", "main_c") })
	if _, err := f.merge("feat"); err != nil {
		t.Fatal(err)
	}
	r := f.open()
	if _, err := merge.Resolve(r, 0, "take-theirs"); err != nil { // theirs deleted it
		t.Fatal(err)
	}
	cs, _ := merge.Conflicts(f.open())
	if !cs[0].Resolved || cs[0].Resolution != (archive.Hash{}) {
		t.Fatalf("want a zero resolution hash, got %+v", cs[0])
	}
	res, err := merge.Continue(f.open())
	if err != nil {
		t.Fatal(err)
	}
	tr := treeOf(t, f.open(), res.Commit)
	if _, ok := tr["c.txt"]; ok {
		t.Fatal("c.txt should have been deleted by the resolution")
	}
	if _, ok := tr["k.txt"]; !ok {
		t.Fatal("k.txt should have survived")
	}
}

func TestResolveRejectsBadIndexSelectorAndNoMerge(t *testing.T) {
	f := oneConflict(t)
	if _, err := merge.Resolve(f.open(), 7, "take-ours"); !errors.Is(err, merge.ErrConflictNotFound) {
		t.Fatalf("out of range: %v", err)
	}
	if _, err := merge.Resolve(f.open(), -1, "take-ours"); !errors.Is(err, merge.ErrConflictNotFound) {
		t.Fatalf("negative: %v", err)
	}
	if _, err := merge.Resolve(f.open(), 0, "take-mine"); !errors.Is(err, merge.ErrUnknownSelector) {
		t.Fatalf("bad selector: %v", err)
	}
	if err := merge.Abort(f.open()); err != nil {
		t.Fatal(err)
	}
	if _, err := merge.Resolve(f.open(), 0, "take-ours"); !errors.Is(err, merge.ErrNoMergeInProgress) {
		t.Fatalf("after abort: %v", err)
	}
}

// The HolyC never refuses a second `resolve` on the same conflict: the record
// is rewritten in place, keeping its index.
func TestResolveAgainOverwritesInPlace(t *testing.T) {
	f := oneConflict(t)
	r := f.open()
	cs, _ := merge.Conflicts(r)
	ours, theirs := cs[0].Object.Ours.Hash, cs[0].Object.Theirs.Hash
	if _, err := merge.Resolve(r, 0, "take-ours"); err != nil {
		t.Fatal(err)
	}
	if _, err := merge.Resolve(f.open(), 0, "take-theirs"); err != nil {
		t.Fatal(err)
	}
	cs, _ = merge.Conflicts(f.open())
	if len(cs) != 1 || cs[0].Index != 0 || cs[0].Resolution != theirs || cs[0].Resolution == ours {
		t.Fatalf("re-resolve: %+v", cs)
	}
}

func TestContinueRefusesWhileAnythingIsUnresolved(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base_a")
	f.write("b.txt", "base_b")
	f.offer("root")
	f.diverge(
		func() { f.write("a.txt", "feat_a"); f.write("b.txt", "feat_b") },
		func() { f.write("a.txt", "main_a"); f.write("b.txt", "main_b") },
	)
	if _, err := f.merge("feat"); err != nil {
		t.Fatal(err)
	}
	before := f.head("main")
	if _, err := merge.Resolve(f.open(), 0, "take-ours"); err != nil {
		t.Fatal(err)
	}
	_, err := merge.Continue(f.open())
	var un *merge.UnresolvedError
	if !errors.As(err, &un) {
		t.Fatalf("got %v, want *UnresolvedError", err)
	}
	if len(un.Indices) != 1 || un.Indices[0] != 1 {
		t.Fatalf("indices %v, want [1]", un.Indices)
	}
	if f.head("main") != before {
		t.Fatal("HEAD moved on a refused continue")
	}
}

func TestContinueWithoutMergeInProgress(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "a")
	f.offer("root")
	if _, err := merge.Continue(f.open()); !errors.Is(err, merge.ErrNoMergeInProgress) {
		t.Fatalf("got %v, want ErrNoMergeInProgress", err)
	}
}

func TestContinueCommitsWithBothParentsAndClearsState(t *testing.T) {
	f := oneConflict(t)
	ours, theirs := f.head("main"), f.head("feat")
	cs, _ := merge.Conflicts(f.open())
	conflictHash := cs[0].Hash
	if _, err := merge.Resolve(f.open(), 0, "take-theirs"); err != nil {
		t.Fatal(err)
	}
	res, err := merge.Continue(f.open())
	if err != nil {
		t.Fatal(err)
	}
	r := f.open()
	c, err := r.Commit(res.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Parents) != 2 || c.Parents[0] != ours || c.Parents[1] != theirs {
		t.Fatalf("parents %v", c.Parents)
	}
	if h, _ := r.Head("main"); h != res.Commit {
		t.Fatal("HEAD did not move to the merge commit")
	}
	if got := string(c.Message); !strings.HasPrefix(got, "merge feat [resolved c.txt=theirs]") {
		t.Fatalf("message %q", got)
	}
	if _, ok := r.Meta.Find("main", meta.TagMergeState); ok {
		t.Fatal("merge state not cleared")
	}
	if n := len(r.Meta.All("main", meta.TagConflict)); n != 0 {
		t.Fatalf("%d conflict records left", n)
	}
	if _, ok := r.Get(conflictHash); !ok {
		t.Fatal("the OBJ_CONFLICT evidence was deleted")
	}
	if tr := treeOf(t, r, res.Commit); tr["c.txt"].ChildHash != cs[0].Object.Theirs.Hash {
		t.Fatal("c.txt is not theirs")
	}
	// The oplog records the transition, and the conflict object is now
	// unreferenced - dangling, not gone.
	ops := r.Meta.All("main", meta.TagOpLog)
	last, err := meta.DecodeOpLog(ops[len(ops)-1].Payload)
	if err != nil || last.Prev != ours || last.New != res.Commit {
		t.Fatalf("oplog tail %+v %v", last, err)
	}
	rep := check.Run(r)
	if rep.RefsBroken != 0 {
		t.Fatalf("refs broken: %+v", rep.Broken)
	}
	if !dangling(rep, conflictHash) {
		t.Fatal("want the conflict object reported dangling")
	}
}

func TestContinueMixesResolutionsAndKeepsCleanEntries(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base_a")
	f.write("b.txt", "base_b")
	f.write("k.txt", "keep")
	f.write("t.txt", "base_t")
	f.offer("root")
	f.diverge(
		func() { f.write("a.txt", "feat_a"); f.write("b.txt", "feat_b"); f.write("t.txt", "feat_t") },
		// t.txt is put back the way the base had it, so main really is
		// unchanged there and theirs' edit wins cleanly.
		func() { f.write("a.txt", "main_a"); f.write("b.txt", "main_b"); f.write("t.txt", "base_t") },
	)
	if _, err := f.merge("feat"); err != nil {
		t.Fatal(err)
	}
	cs, _ := merge.Conflicts(f.open())
	if len(cs) != 2 {
		t.Fatalf("want 2 conflicts, got %d", len(cs))
	}
	wantA, wantB := cs[0].Object.Ours.Hash, cs[1].Object.Theirs.Hash
	if _, err := merge.Resolve(f.open(), 0, "take-ours"); err != nil {
		t.Fatal(err)
	}
	if _, err := merge.Resolve(f.open(), 1, "take-theirs"); err != nil {
		t.Fatal(err)
	}
	res, err := merge.Continue(f.open())
	if err != nil {
		t.Fatal(err)
	}
	tr := treeOf(t, f.open(), res.Commit)
	if tr["a.txt"].ChildHash != wantA || tr["b.txt"].ChildHash != wantB {
		t.Fatal("wrong resolution landed in the merged tree")
	}
	if _, ok := tr["k.txt"]; !ok {
		t.Fatal("the untouched entry is missing")
	}
	// t.txt changed on theirs only: the ordinary non-conflicting decision
	// still runs on a continue.
	if len(res.Autos) != 1 || res.Autos[0].Path != "t.txt" || res.Autos[0].Kind != merge.AutoTookTheirs {
		t.Fatalf("autos %+v", res.Autos)
	}
}

func TestAbortWithoutMergeInProgress(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "a")
	f.offer("root")
	if err := merge.Abort(f.open()); !errors.Is(err, merge.ErrNoMergeInProgress) {
		t.Fatalf("got %v, want ErrNoMergeInProgress", err)
	}
}

func TestAbortRestoresTheExactPreMergeState(t *testing.T) {
	f := oneConflict(t)
	r := f.open()
	before, beforeOps := f.head("main"), len(r.Meta.All("main", meta.TagOpLog))
	objects := len(r.Arc.Records)
	cs, _ := merge.Conflicts(r)
	conflictHash := cs[0].Hash

	if err := merge.Abort(f.open()); err != nil {
		t.Fatal(err)
	}
	r = f.open()
	if f.head("main") != before {
		t.Fatal("HEAD moved")
	}
	if n := len(r.Meta.All("main", meta.TagOpLog)); n != beforeOps {
		t.Fatalf("%d oplog entries, want %d - abort must not commit", n, beforeOps)
	}
	if _, ok := r.Meta.Find("main", meta.TagMergeState); ok {
		t.Fatal("merge state not cleared")
	}
	if n := len(r.Meta.All("main", meta.TagConflict)); n != 0 {
		t.Fatalf("%d conflict records left", n)
	}
	if len(r.Arc.Records) != objects {
		t.Fatalf("%d objects, want %d - abort must not touch the archive", len(r.Arc.Records), objects)
	}
	if _, ok := r.Get(conflictHash); !ok {
		t.Fatal("the OBJ_CONFLICT evidence was deleted")
	}
	rep := check.Run(r)
	if rep.RefsBroken != 0 || !dangling(rep, conflictHash) {
		t.Fatalf("want a clean check with one dangling conflict, got %+v", rep)
	}
	if _, err := merge.Conflicts(r); !errors.Is(err, merge.ErrNoMergeInProgress) {
		t.Fatalf("conflicts after abort: %v", err)
	}
	// And the merge can simply be attempted again.
	if _, err := merge.Merge(f.open(), "feat"); err != nil {
		t.Fatal(err)
	}
}

func dangling(rep check.Report, h archive.Hash) bool {
	for _, d := range rep.Dangling {
		if d.Hash == h {
			return true
		}
	}
	return false
}

// A conflict record whose OBJ_CONFLICT object is not in the archive is a real
// case (TFULL_HARDEN hand-writes one): it is listed, not crashed on, and
// refused by resolve.
func TestConflictRecordWithMissingObject(t *testing.T) {
	f := oneConflict(t)
	r := f.open()
	if err := merge.Abort(r); err != nil {
		t.Fatal(err)
	}
	r = f.open()
	head, _ := r.Head("main")
	r.Meta.Set("main", meta.TagMergeState, meta.MergeState{Ours: head, Theirs: head, OtherPath: "feat"}.Encode())
	var fake archive.Hash
	for i := range fake {
		fake[i] = 7
	}
	r.Meta.Append("main", meta.TagConflict, meta.ConflictRecord{Conflict: fake}.Encode())
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	cs, err := merge.Conflicts(f.open())
	if err != nil || len(cs) != 1 || cs[0].Object != nil || cs[0].Hash != fake {
		t.Fatalf("got %+v, %v", cs, err)
	}
	if _, err := merge.Resolve(f.open(), 0, "take-ours"); !errors.Is(err, merge.ErrConflictObjectMissing) {
		t.Fatalf("resolve: %v", err)
	}
	if err := merge.Abort(f.open()); err != nil {
		t.Fatal(err)
	}
}

// The other half of that case: a record pointing at an object that IS in the
// archive but does not decode as a conflict - a different refusal, and one
// `merge abort` still recovers from.
func TestConflictRecordWithMalformedObject(t *testing.T) {
	f := oneConflict(t)
	if err := merge.Abort(f.open()); err != nil {
		t.Fatal(err)
	}
	r := f.open()
	head, _ := r.Head("main")
	bad := r.Append(archive.Conflict, []byte{0xff, 0x00})
	r.Meta.Set("main", meta.TagMergeState, meta.MergeState{Ours: head, Theirs: head, OtherPath: "feat"}.Encode())
	r.Meta.Append("main", meta.TagConflict, meta.ConflictRecord{Conflict: bad}.Encode())
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	cs, err := merge.Conflicts(f.open())
	if err != nil || len(cs) != 1 || !cs[0].Malformed || cs[0].Object != nil {
		t.Fatalf("got %+v, %v", cs, err)
	}
	if _, err := merge.Resolve(f.open(), 0, "take-ours"); !errors.Is(err, merge.ErrConflictMalformed) {
		t.Fatalf("resolve: %v", err)
	}
	if err := merge.Abort(f.open()); err != nil {
		t.Fatal(err)
	}
}
