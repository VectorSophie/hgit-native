package merge_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

type fx struct {
	t    *testing.T
	dir  string
	path string
}

func setup(t *testing.T) *fx {
	t.Helper()
	oldClock, oldID := clock.Now, offer.NewEntityID
	t.Cleanup(func() { clock.Now, offer.NewEntityID = oldClock, oldID })
	ts := uint64(1000)
	clock.Now = func() uint64 { ts++; return ts }
	next := uint64(0)
	offer.NewEntityID = func() uint64 { next++; return next }

	f := &fx{t: t, dir: t.TempDir()}
	f.path = filepath.Join(f.dir, "R.hgs")
	if err := repo.Init(f.path); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fx) open() *repo.Repo {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fx) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) rm(name string) {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.dir, name)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) offer(msg string) archive.Hash {
	f.t.Helper()
	h, err := offer.Offer(f.open(), f.dir, "*.txt", offer.Options{Message: msg})
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (f *fx) pathNew(name string) {
	f.t.Helper()
	if err := f.open().PathNew(name); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) pathGo(name string) {
	f.t.Helper()
	if err := f.open().PathGo(name); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) merge(other string) (merge.Result, error) {
	f.t.Helper()
	return merge.Merge(f.open(), other)
}

func (f *fx) head(path string) archive.Hash {
	f.t.Helper()
	h, ok := f.open().Head(path)
	if !ok {
		f.t.Fatalf("no head on %s", path)
	}
	return h
}

// diverge builds the standard shape: a root commit on main, then one commit
// on "feat" and one on main, so neither side is the merge base.
func (f *fx) diverge(featEdit, mainEdit func()) {
	f.t.Helper()
	f.pathNew("feat")
	f.pathGo("feat")
	featEdit()
	f.offer("feat")
	f.pathGo("main")
	mainEdit()
	f.offer("main")
}

func treeOf(t *testing.T, r *repo.Repo, h archive.Hash) map[string]object.Entry {
	t.Helper()
	c, err := r.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := r.Tree(c.Tree)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]object.Entry{}
	for _, e := range tr.Entries {
		m[e.Name] = e
	}
	return m
}

func TestAlreadyUpToDate(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base")
	f.offer("root")
	f.pathNew("feat")
	f.write("a.txt", "main edit")
	f.offer("main")

	before := f.head("main")
	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if !res.UpToDate || res.FastForward {
		t.Fatalf("want up-to-date, got %+v", res)
	}
	if f.head("main") != before {
		t.Fatal("HEAD moved on an up-to-date merge")
	}
}

func TestFastForwardMovesHeadWithoutAMergeCommit(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base")
	f.offer("root")
	f.pathNew("feat")
	f.pathGo("feat")
	f.write("a.txt", "feat edit")
	featHead := f.offer("feat")
	f.pathGo("main")

	objectsBefore := len(f.open().Arc.Records)
	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if !res.FastForward || res.UpToDate {
		t.Fatalf("want fast-forward, got %+v", res)
	}
	if res.Commit != featHead || f.head("main") != featHead {
		t.Fatal("fast-forward did not move HEAD to theirs")
	}
	if got := len(f.open().Arc.Records); got != objectsBefore {
		t.Fatalf("fast-forward wrote %d new objects, want 0", got-objectsBefore)
	}
	ops, err := f.open().OperationHistory()
	if err != nil {
		t.Fatal(err)
	}
	last := ops[len(ops)-1]
	if last.New != featHead {
		t.Fatal("fast-forward did not log the HEAD transition")
	}
}

func TestCleanThreeWayMerge(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "a root")
	f.write("b.txt", "b root")
	f.offer("root")
	base := f.head("main")

	f.diverge(func() { f.write("b.txt", "b edited by feat") },
		func() { f.write("b.txt", "b root"); f.write("a.txt", "a edited by main") })
	ours, theirs := f.head("main"), f.head("feat")

	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.UpToDate || res.FastForward {
		t.Fatalf("want a real merge, got %+v", res)
	}
	r := f.open()
	if f.head("main") != res.Commit {
		t.Fatal("HEAD is not the merge commit")
	}
	c, err := r.Commit(res.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Parents) != 2 || c.Parents[0] != ours || c.Parents[1] != theirs {
		t.Fatalf("parents %v, want [ours theirs] = [%s %s]", c.Parents, ours.Hex(), theirs.Hex())
	}
	if string(c.Message) != "merge feat" {
		t.Fatalf("message %q", c.Message)
	}
	merged := treeOf(t, r, res.Commit)
	if merged["a.txt"].ChildHash != treeOf(t, r, ours)["a.txt"].ChildHash {
		t.Fatal("a.txt should be ours")
	}
	if merged["b.txt"].ChildHash != treeOf(t, r, theirs)["b.txt"].ChildHash {
		t.Fatal("b.txt should be theirs")
	}
	if treeOf(t, r, base)["a.txt"].ChildHash == merged["a.txt"].ChildHash {
		t.Fatal("a.txt should not be the base version")
	}
	if len(res.Autos) != 1 || res.Autos[0].Kind != merge.AutoTookTheirs || res.Autos[0].Path != "b.txt" {
		t.Fatalf("autos %+v", res.Autos)
	}
}

// conflicted returns the persisted state after a conflicting merge, read back
// through a fresh repo.Open - the ADR 0016 requirement.
func conflicted(t *testing.T, f *fx) (merge.Result, *repo.Repo) {
	t.Helper()
	oursBefore := f.head("main")
	objectsBefore := len(f.open().Arc.Records)
	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) == 0 {
		t.Fatalf("want a conflict, got %+v", res)
	}
	r := f.open()
	if h, _ := r.Head("main"); h != oursBefore {
		t.Fatal("HEAD moved on a conflicting merge")
	}
	if len(r.Arc.Records) != objectsBefore+len(res.Conflicts) {
		t.Fatalf("archive grew by %d, want %d conflict objects only",
			len(r.Arc.Records)-objectsBefore, len(res.Conflicts))
	}
	st, ok := r.Meta.Find("main", meta.TagMergeState)
	if !ok {
		t.Fatal("no merge state persisted")
	}
	ms, err := meta.DecodeMergeState(st.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if ms.Ours != oursBefore || ms.Theirs != f.head("feat") || ms.OtherPath != "feat" {
		t.Fatalf("merge state %+v", ms)
	}
	recs := r.Meta.All("main", meta.TagConflict)
	if len(recs) != len(res.Conflicts) {
		t.Fatalf("%d conflict records, want %d", len(recs), len(res.Conflicts))
	}
	for i, rec := range recs {
		cr, err := meta.DecodeConflictRecord(rec.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if cr.Resolved || cr.Conflict != res.Conflicts[i].Hash {
			t.Fatalf("conflict record %d = %+v", i, cr)
		}
		rec, ok := r.Get(cr.Conflict)
		if !ok || rec.Type() != archive.Conflict {
			t.Fatalf("conflict object %s missing", cr.Conflict.Hex())
		}
		got, err := object.DecodeConflict(rec.Content())
		if err != nil {
			t.Fatal(err)
		}
		if *got != res.Conflicts[i].Object {
			t.Fatalf("conflict object %d = %+v, want %+v", i, *got, res.Conflicts[i].Object)
		}
	}
	return res, r
}

func TestContentConflictPersists(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base")
	f.offer("root")
	f.diverge(func() { f.write("a.txt", "feat") }, func() { f.write("a.txt", "main") })

	res, _ := conflicted(t, f)
	if len(res.Conflicts) != 1 {
		t.Fatalf("%d conflicts", len(res.Conflicts))
	}
	c := res.Conflicts[0].Object
	if c.Kind != object.KindContent || c.Path != "a.txt" {
		t.Fatalf("conflict %+v", c)
	}
	if !c.Base.Present || !c.Ours.Present || !c.Theirs.Present {
		t.Fatalf("all three sides should be present: %+v", c)
	}
	if c.Ours.Mode != 0 || c.Theirs.Mode != 0 {
		t.Fatalf("modes should be plain: %+v", c)
	}
}

func TestModeOnlyConflict(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "shared")
	f.offer("root")
	f.diverge(
		func() { f.write(".hgitattributes", "a.txt executable\n") },
		func() { f.write(".hgitattributes", "a.txt binary\n") })

	res, _ := conflicted(t, f)
	if len(res.Conflicts) != 1 {
		t.Fatalf("%d conflicts", len(res.Conflicts))
	}
	c := res.Conflicts[0].Object
	if c.Kind != object.KindMode {
		t.Fatalf("kind %d, want mode only", c.Kind)
	}
	if c.Ours.Mode != object.ModeBinary || c.Theirs.Mode != object.ModeExecutable || c.Base.Mode != 0 {
		t.Fatalf("modes %+v", c)
	}
	if c.Ours.Hash != c.Theirs.Hash {
		t.Fatal("content was not supposed to change")
	}
}

func TestContentAndModeConflictSetsBothKindBits(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "shared")
	f.offer("root")
	f.diverge(
		func() { f.write("a.txt", "feat"); f.write(".hgitattributes", "a.txt executable\n") },
		func() { f.write("a.txt", "main"); f.write(".hgitattributes", "a.txt binary\n") })

	res, _ := conflicted(t, f)
	c := res.Conflicts[0].Object
	if c.Kind != object.KindContent|object.KindMode {
		t.Fatalf("kind %d, want content|mode", c.Kind)
	}
}

func TestEditVersusDeleteIsAContentConflict(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base")
	f.write("keep.txt", "keep")
	f.offer("root")
	f.diverge(func() { f.write("a.txt", "feat") }, func() { f.rm("a.txt") })

	res, _ := conflicted(t, f)
	if len(res.Conflicts) != 1 {
		t.Fatalf("%d conflicts", len(res.Conflicts))
	}
	c := res.Conflicts[0].Object
	if c.Kind != object.KindContent || c.Path != "a.txt" {
		t.Fatalf("conflict %+v", c)
	}
	if c.Ours.Present || !c.Base.Present || !c.Theirs.Present {
		t.Fatalf("ours (the deleting side) should be absent: %+v", c)
	}
}

// The documented HolyC limitation (ported, not fixed): the edit-vs-delete
// decision only ever consults content, so a mode-only change on the side a
// file is deleted from is not flagged - the file is simply gone.
func TestModeChangeOnTheDeletedSideIsNotAConflict(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "shared")
	f.write("keep.txt", "keep")
	f.offer("root")
	f.diverge(
		func() { f.write(".hgitattributes", "a.txt executable\n") },
		func() { f.rm("a.txt") })

	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("want no conflict, got %+v", res.Conflicts)
	}
	if _, ok := treeOf(t, f.open(), res.Commit)["a.txt"]; ok {
		t.Fatal("a.txt should be gone from the merge result")
	}
	if len(res.Autos) != 1 || res.Autos[0].Kind != merge.AutoDeleted || res.Autos[0].Path != "a.txt" {
		t.Fatalf("autos %+v", res.Autos)
	}
}

func TestDeletedOnBothSidesIsNotAConflict(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "shared")
	f.write("b.txt", "b")
	f.write("c.txt", "c")
	f.offer("root")
	// feat deletes a.txt; main never restores it, so both sides deleted it.
	f.diverge(func() { f.rm("a.txt"); f.write("b.txt", "b feat") },
		func() { f.write("b.txt", "b"); f.write("c.txt", "c main") })

	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	if _, ok := treeOf(t, f.open(), res.Commit)["a.txt"]; ok {
		t.Fatal("a.txt should be gone")
	}
}

func TestAddedOnBothSidesDifferentlyConflicts(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base")
	f.offer("root")
	f.diverge(func() { f.write("n.txt", "feat new") }, func() { f.write("n.txt", "main new") })

	res, _ := conflicted(t, f)
	c := res.Conflicts[0].Object
	if c.Path != "n.txt" || c.Base.Present || c.Kind != object.KindContent {
		t.Fatalf("conflict %+v", c)
	}
}

func TestRefusesASecondMergeWhileOneIsInProgress(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base")
	f.offer("root")
	f.diverge(func() { f.write("a.txt", "feat") }, func() { f.write("a.txt", "main") })
	conflicted(t, f)

	if _, err := f.merge("feat"); !errors.Is(err, merge.ErrInProgress) {
		t.Fatalf("err = %v, want ErrInProgress", err)
	}
}

func TestNoCommonAncestor(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base")
	f.offer("root")

	// A second, unrelated root commit, reachable only from path "other".
	r := f.open()
	tree := r.Append(archive.Tree, (&object.Tree{}).Encode())
	h := r.Append(archive.Commit, (&object.Commit{Tree: tree, Timestamp: 1, Message: []byte("other root")}).Encode())
	if err := r.SetHead("other", h); err != nil {
		t.Fatal(err)
	}
	r.Meta.Append("other", meta.TagPathDeclared, nil)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	if _, err := f.merge("other"); !errors.Is(err, merge.ErrNoCommonAncestor) {
		t.Fatalf("err = %v, want ErrNoCommonAncestor", err)
	}
}

func TestUnknownOtherPath(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "base")
	f.offer("root")
	if _, err := f.merge("nope"); !errors.Is(err, repo.ErrNoSuchPath) {
		t.Fatalf("err = %v, want ErrNoSuchPath", err)
	}
}

func TestNoHeadOnCurrentPath(t *testing.T) {
	f := setup(t)
	if _, err := f.merge("main"); !errors.Is(err, repo.ErrNoHead) {
		t.Fatalf("err = %v, want ErrNoHead", err)
	}
}

func TestMergedModeSurvivesIntoTheMergeCommitAttrs(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "shared")
	f.write("b.txt", "b root")
	f.offer("root")
	f.diverge(
		func() { f.write(".hgitattributes", "a.txt executable\n") },
		func() { f.rm(".hgitattributes"); f.write("b.txt", "b edited") })

	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	r := f.open()
	c, err := r.Commit(res.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if c.Attrs == nil {
		t.Fatal("merge commit has no attrs object")
	}
	a, err := r.Attrs(*c.Attrs)
	if err != nil {
		t.Fatal(err)
	}
	id := treeOf(t, r, res.Commit)["a.txt"].EntityID
	if a.FindMode(id) != object.ModeExecutable {
		t.Fatalf("mode %d, want executable", a.FindMode(id))
	}
	if len(a.Entries) != 1 {
		t.Fatalf("attrs %+v", a.Entries)
	}
}
