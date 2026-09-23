package merge_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// work is the offertree root: a directory beside the repository, so the
// repository's own files are never offered.
func (f *fx) work() string { return filepath.Join(f.dir, "w") }

func (f *fx) put(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.work(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) del(rel string) {
	f.t.Helper()
	if err := os.RemoveAll(filepath.Join(f.work(), filepath.FromSlash(rel))); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) offerTree(msg string) {
	f.t.Helper()
	if _, err := offer.OfferTree(f.open(), f.work(), offer.Options{Message: msg}); err != nil {
		f.t.Fatal(err)
	}
}

// divergeTree is diverge for offertree-built commits.
func (f *fx) divergeTree(featEdit, mainEdit func()) {
	f.t.Helper()
	f.pathNew("feat")
	f.pathGo("feat")
	featEdit()
	f.offerTree("feat")
	f.pathGo("main")
	mainEdit()
	f.offerTree("main")
}

// at resolves a slash path inside a commit's tree.
func at(t *testing.T, r *repo.Repo, commit archive.Hash, path string) (object.Entry, bool) {
	t.Helper()
	c, err := r.Commit(commit)
	if err != nil {
		t.Fatal(err)
	}
	h := c.Tree
	parts := strings.Split(path, "/")
	for i, p := range parts {
		tr, err := r.Tree(h)
		if err != nil {
			t.Fatal(err)
		}
		e, ok := tr.Find(p)
		if !ok {
			return object.Entry{}, false
		}
		if i == len(parts)-1 {
			return e, true
		}
		h = e.ChildHash
	}
	return object.Entry{}, false
}

func must(t *testing.T, r *repo.Repo, commit archive.Hash, path string) object.Entry {
	t.Helper()
	e, ok := at(t, r, commit, path)
	if !ok {
		t.Fatalf("%s missing", path)
	}
	return e
}

func TestNestedCleanMergeRecursesIntoTheSubtree(t *testing.T) {
	f := setup(t)
	f.put("top.txt", "top")
	f.put("SubA/x.txt", "x root")
	f.put("SubA/y.txt", "y root")
	f.offerTree("root")
	f.divergeTree(func() { f.put("SubA/y.txt", "y feat") }, func() { f.put("SubA/y.txt", "y root"); f.put("SubA/x.txt", "x main") })
	ours, theirs := f.head("main"), f.head("feat")

	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	r := f.open()
	if must(t, r, res.Commit, "SubA/x.txt").ChildHash != must(t, r, ours, "SubA/x.txt").ChildHash {
		t.Fatal("SubA/x.txt should be ours")
	}
	if must(t, r, res.Commit, "SubA/y.txt").ChildHash != must(t, r, theirs, "SubA/y.txt").ChildHash {
		t.Fatal("SubA/y.txt should be theirs")
	}
	sub := must(t, r, res.Commit, "SubA")
	if sub.ChildType != archive.Tree || sub.EntityID != must(t, r, ours, "SubA").EntityID {
		t.Fatalf("SubA entry %+v", sub)
	}
	if len(res.Autos) != 1 || res.Autos[0] != (merge.Auto{Kind: merge.AutoTookTheirs, Path: "SubA/y.txt"}) {
		t.Fatalf("autos %+v", res.Autos)
	}
	if rep := check.Run(r); rep.RefsBroken != 0 || len(rep.Dangling) != 0 {
		t.Fatalf("check %+v", rep)
	}
}

func TestNestedConflictHasThePrefixedPath(t *testing.T) {
	f := setup(t)
	f.put("top.txt", "top")
	f.put("SubA/inner.txt", "base")
	f.offerTree("root")
	f.divergeTree(func() { f.put("SubA/inner.txt", "feat") }, func() { f.put("SubA/inner.txt", "main") })

	res, _ := conflicted(t, f)
	if len(res.Conflicts) != 1 || res.Conflicts[0].Object.Path != "SubA/inner.txt" ||
		res.Conflicts[0].Object.Kind != object.KindContent {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
}

func TestNestedKindMismatchIsATypeConflict(t *testing.T) {
	f := setup(t)
	f.put("SubA/k", "a file")
	f.put("SubA/other.txt", "o")
	f.offerTree("root")
	f.divergeTree(func() { f.del("SubA/k"); f.put("SubA/k/in.txt", "now a dir") },
		func() { f.del("SubA/k"); f.put("SubA/k", "a file"); f.put("SubA/other.txt", "o main") })

	res, _ := conflicted(t, f)
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	c := res.Conflicts[0].Object
	if c.Path != "SubA/k" || c.Kind != object.KindType || c.Theirs.Type != archive.Tree || c.Ours.Type != archive.Blob {
		t.Fatalf("conflict %+v", c)
	}
}

// A clean sibling subtree is written into the archive even when another
// subtree conflicts: MergeTreesRecursive puts each merged subtree as the
// recursion unwinds, and on a conflict the whole archive - that subtree
// included - is saved with the evidence. It is left unreferenced (dangling),
// the tradeoff Merge.HC's header documents. Deliberate, not a bug.
func TestCleanSiblingSubtreeIsWrittenEvenWhenAnotherConflicts(t *testing.T) {
	f := setup(t)
	f.put("SubA/x.txt", "x root")
	f.put("SubA/y.txt", "y root")
	f.put("SubB/z.txt", "z root")
	f.offerTree("root")
	f.divergeTree(func() { f.put("SubA/y.txt", "y feat"); f.put("SubB/z.txt", "z feat") },
		func() { f.put("SubA/y.txt", "y root"); f.put("SubA/x.txt", "x main"); f.put("SubB/z.txt", "z main") })
	before := len(f.open().Arc.Records)

	res, err := f.merge("feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Object.Path != "SubB/z.txt" {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	r := f.open()
	if got := len(r.Arc.Records) - before; got != 2 {
		t.Fatalf("archive grew by %d, want 2 (the merged SubA tree and the conflict)", got)
	}
	subA := r.Arc.Records[before]
	if subA.Type() != archive.Tree {
		t.Fatalf("first new record is %v, want the merged SubA tree", subA.Type())
	}
	tr, err := object.DecodeTree(subA.Content())
	if err != nil || len(tr.Entries) != 2 {
		t.Fatalf("merged SubA %+v %v", tr, err)
	}
	if !dangling(check.Run(r), subA.Hash) {
		t.Fatal("want the stray SubA tree reported dangling")
	}
}

func TestNewSubdirectoryOnOneSideIsTakenWhole(t *testing.T) {
	f := setup(t)
	f.put("top.txt", "top")
	f.offerTree("root")
	f.divergeTree(func() { f.put("SubN/n.txt", "new") }, func() { f.del("SubN"); f.put("top.txt", "top main") })
	theirs := f.head("feat")

	res, err := f.merge("feat")
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	r := f.open()
	if must(t, r, res.Commit, "SubN/n.txt").ChildHash != must(t, r, theirs, "SubN/n.txt").ChildHash {
		t.Fatal("SubN/n.txt should be theirs")
	}
	if must(t, r, res.Commit, "SubN").EntityID != must(t, r, theirs, "SubN").EntityID {
		t.Fatal("a subtree only theirs has keeps theirs' entity id")
	}
	if len(res.Autos) != 1 || res.Autos[0] != (merge.Auto{Kind: merge.AutoTookTheirs, Path: "SubN/n.txt"}) {
		t.Fatalf("autos %+v", res.Autos)
	}
}

// A subdirectory deleted on one side recurses against an absent tree: every
// entry inside is a clean deletion, but the merged subtree itself is still
// written, empty - exactly as MergeTreesRecursive writes any subtree whose
// recursion succeeded.
func TestDeletedSubdirectoryLeavesAnEmptySubtree(t *testing.T) {
	f := setup(t)
	f.put("top.txt", "top")
	f.put("SubD/d.txt", "d")
	f.offerTree("root")
	f.divergeTree(func() { f.del("SubD") }, func() { f.put("SubD/d.txt", "d"); f.put("top.txt", "top main") })

	res, err := f.merge("feat")
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	r := f.open()
	if _, ok := at(t, r, res.Commit, "SubD/d.txt"); ok {
		t.Fatal("SubD/d.txt survived its deletion")
	}
	sub := must(t, r, res.Commit, "SubD")
	tr, err := r.Tree(sub.ChildHash)
	if err != nil || len(tr.Entries) != 0 {
		t.Fatalf("SubD %+v %v", tr, err)
	}
	if len(res.Autos) != 1 || res.Autos[0] != (merge.Auto{Kind: merge.AutoDeleted, Path: "SubD/d.txt"}) {
		t.Fatalf("autos %+v", res.Autos)
	}
}

func TestContinueRebuildsAResolvedNestedConflict(t *testing.T) {
	f := setup(t)
	f.put("SubA/inner.txt", "base")
	f.put("SubA/keep.txt", "keep")
	f.offerTree("root")
	f.divergeTree(func() { f.put("SubA/inner.txt", "feat") }, func() { f.put("SubA/inner.txt", "main") })
	conflicted(t, f)
	if _, err := merge.Resolve(f.open(), 0, merge.TakeTheirs); err != nil {
		t.Fatal(err)
	}
	res, err := merge.Continue(f.open())
	if err != nil {
		t.Fatal(err)
	}
	r := f.open()
	c, _ := r.Commit(res.Commit)
	if got := string(c.Message); got != "merge feat [resolved SubA/inner.txt=theirs]" {
		t.Fatalf("message %q", got)
	}
	if must(t, r, res.Commit, "SubA/inner.txt").ChildHash != must(t, r, f.head("feat"), "SubA/inner.txt").ChildHash {
		t.Fatal("SubA/inner.txt is not theirs")
	}
	if _, ok := at(t, r, res.Commit, "SubA/keep.txt"); !ok {
		t.Fatal("SubA/keep.txt lost")
	}
	if _, ok := r.Meta.Find("main", meta.TagMergeState); ok {
		t.Fatal("merge state not cleared")
	}
}

// --- rename-aware merge (ADR 0017) ---

func TestRenameOnOneSideEditOnTheOther(t *testing.T) {
	f := setup(t)
	f.write("r.txt", "rename_me_content_long\n")
	f.write("k.txt", "k")
	f.offer("root")
	f.diverge(func() { f.write("r.txt", "EDITED_me_content_long\n") },
		func() {
			f.write("r.txt", "rename_me_content_long\n")
			f.rm("r.txt")
			f.write("r2.txt", "rename_me_content_long\n")
		})
	theirs := f.head("feat")

	res, err := f.merge("feat")
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	r := f.open()
	m := treeOf(t, r, res.Commit)
	if _, ok := m["r.txt"]; ok {
		t.Fatal("r.txt came back")
	}
	if m["r2.txt"].ChildHash != treeOf(t, r, theirs)["r.txt"].ChildHash {
		t.Fatal("r2.txt should carry theirs' edit")
	}
	want := []merge.Auto{
		{Kind: merge.AutoRenamed, Path: "r.txt", NewName: "r2.txt"},
		{Kind: merge.AutoTookTheirs, Path: "r2.txt"},
	}
	if len(res.Autos) != 2 || res.Autos[0] != want[0] || res.Autos[1] != want[1] {
		t.Fatalf("autos %+v", res.Autos)
	}
}

func TestSameRenameOnBothSidesMergesCleanly(t *testing.T) {
	f := setup(t)
	f.write("r.txt", "rename_me_content_long\n")
	f.write("k.txt", "k")
	f.offer("root")
	rename := func() { f.rm("r.txt"); f.write("r2.txt", "rename_me_content_long\n") }
	// main's working directory already holds feat's rename.
	f.diverge(rename, func() { f.write("k.txt", "k main") })

	res, err := f.merge("feat")
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	m := treeOf(t, f.open(), res.Commit)
	if _, ok := m["r.txt"]; ok || len(m) != 2 {
		t.Fatalf("merged tree %+v", m)
	}
	if _, ok := m["r2.txt"]; !ok {
		t.Fatal("r2.txt missing")
	}
	if len(res.Autos) != 1 || res.Autos[0] != (merge.Auto{Kind: merge.AutoRenamed, Path: "r.txt", NewName: "r2.txt"}) {
		t.Fatalf("autos %+v", res.Autos)
	}
}

// Rename-vs-delete is out of scope in the HolyC: the other side has neither
// the base name nor the entity, so nothing is normalized and the renamed file
// simply survives as an addition.
func TestRenameVersusDeleteKeepsTheRenamedFile(t *testing.T) {
	f := setup(t)
	f.write("r.txt", "rename_me_content_long\n")
	f.write("k.txt", "k")
	f.offer("root")
	f.diverge(func() { f.rm("r.txt") }, func() { f.write("r2.txt", "rename_me_content_long\n") })

	res, err := f.merge("feat")
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	m := treeOf(t, f.open(), res.Commit)
	if _, ok := m["r2.txt"]; !ok {
		t.Fatal("r2.txt lost")
	}
	for _, a := range res.Autos {
		if a.Kind == merge.AutoRenamed {
			t.Fatalf("unexpected rename notice %+v", a)
		}
	}
}

// snapshot is the repository's on-disk bytes, archive and metadata both.
func snapshot(t *testing.T, f *fx) []byte {
	t.Helper()
	a, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := os.ReadFile(f.path + ".m")
	if err != nil {
		t.Fatal(err)
	}
	return append(a, m...)
}

func TestAmbiguousRenameRenameRefusesTheWholeMerge(t *testing.T) {
	f := setup(t)
	f.write("r.txt", "rename_me_content_long\n")
	f.write("c.txt", "base_c")
	f.offer("root")
	// c.txt conflicts too: the refusal must win over persisting that
	// conflict's evidence.
	f.diverge(func() { f.rm("r.txt"); f.write("r3.txt", "rename_me_content_long\n"); f.write("c.txt", "feat_c") },
		func() { f.rm("r3.txt"); f.write("r2.txt", "rename_me_content_long\n"); f.write("c.txt", "main_c") })
	before := snapshot(t, f)

	res, err := f.merge("feat")
	if !errors.Is(err, merge.ErrAmbiguousRename) || !res.RenameRefused {
		t.Fatalf("err = %v, res %+v; want ErrAmbiguousRename", err, res)
	}
	if len(res.Autos) == 0 || res.Autos[0] != (merge.Auto{Kind: merge.AutoRenameRefused, Path: "r.txt"}) {
		t.Fatalf("autos %+v", res.Autos)
	}
	if len(res.Conflicts) != 0 || res.Commit != (archive.Hash{}) {
		t.Fatalf("refusal reported work %+v", res)
	}
	if !bytes.Equal(snapshot(t, f), before) {
		t.Fatal("a refused merge changed the repository on disk")
	}
	if _, err := merge.Conflicts(f.open()); !errors.Is(err, merge.ErrNoMergeInProgress) {
		t.Fatalf("merge state left behind: %v", err)
	}
}

// The refusal propagates up from any depth, and no clean subtree or conflict
// evidence computed along the way survives it.
func TestNestedAmbiguousRenameRefusesTheWholeMerge(t *testing.T) {
	f := setup(t)
	f.put("SubA/r.txt", "rename_me_content_long\n")
	f.put("SubB/x.txt", "x root")
	f.put("SubB/y.txt", "y root")
	f.put("c.txt", "base_c")
	f.offerTree("root")
	f.divergeTree(func() {
		f.del("SubA/r.txt")
		f.put("SubA/r3.txt", "rename_me_content_long\n")
		f.put("SubB/y.txt", "y feat")
		f.put("c.txt", "feat_c")
	}, func() {
		f.del("SubA/r3.txt")
		f.put("SubA/r2.txt", "rename_me_content_long\n")
		f.put("SubB/y.txt", "y root")
		f.put("SubB/x.txt", "x main")
		f.put("c.txt", "main_c")
	})
	before := snapshot(t, f)

	// The same in-memory repository must be untouched too: nothing computed
	// by the refused walk may be left where a later Save would persist it.
	r := f.open()
	records, count := len(r.Arc.Records), r.Arc.Header.Count
	res, err := merge.Merge(r, "feat")
	if len(r.Arc.Records) != records || r.Arc.Header.Count != count {
		t.Fatalf("in-memory archive went %d/%d -> %d/%d records on a refusal",
			records, count, len(r.Arc.Records), r.Arc.Header.Count)
	}
	if !errors.Is(err, merge.ErrAmbiguousRename) {
		t.Fatalf("err = %v, want ErrAmbiguousRename", err)
	}
	found := false
	for _, a := range res.Autos {
		if a == (merge.Auto{Kind: merge.AutoRenameRefused, Path: "r.txt"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("autos %+v", res.Autos)
	}
	if !bytes.Equal(snapshot(t, f), before) {
		t.Fatal("a refused merge changed the repository on disk")
	}
}

// Records land in the archive in walk order, conflicts and merged subtrees
// interleaved, as ObjectPut writes each the moment it is found: a.txt
// conflicts before zsub merges cleanly, so the conflict comes first.
func TestConflictAndCleanSubtreeAreWrittenInWalkOrder(t *testing.T) {
	f := setup(t)
	f.put("a.txt", "base")
	f.put("zsub/x.txt", "x root")
	f.put("zsub/y.txt", "y root")
	f.offerTree("root")
	f.divergeTree(func() { f.put("a.txt", "feat"); f.put("zsub/y.txt", "y feat") },
		func() { f.put("a.txt", "main"); f.put("zsub/y.txt", "y root"); f.put("zsub/x.txt", "x main") })
	if tr := treeOf(t, f.open(), f.head("main")); len(tr) != 2 {
		t.Fatalf("root tree %+v", tr)
	}
	before := len(f.open().Arc.Records)

	res, err := f.merge("feat")
	if err != nil || len(res.Conflicts) != 1 || res.Conflicts[0].Object.Path != "a.txt" {
		t.Fatalf("%+v %v", res, err)
	}
	r := f.open()
	var got []archive.Type
	for _, rec := range r.Arc.Records[before:] {
		got = append(got, rec.Type())
	}
	if len(got) != 2 || got[0] != archive.Conflict || got[1] != archive.Tree {
		t.Fatalf("new records %v, want [conflict tree]", got)
	}
	if r.Arc.Records[before].Hash != res.Conflicts[0].Hash {
		t.Fatal("the conflict record is not the reported conflict")
	}
}

// A HolyC hazard ported as is: ours renames r.txt to r2.txt while theirs keeps
// r.txt and separately adds an unrelated r2.txt. Ours normalizes (theirs still
// has the base name), the entry is written under r2.txt, and theirs' own
// r2.txt is taken too - two entries named r2.txt in one merged tree.
func TestRenameOntoTheOtherSidesNewNameGivesTwoEntries(t *testing.T) {
	f := setup(t)
	f.write("r.txt", "rename_me_content_long\n")
	f.write("k.txt", "k")
	f.offer("root")
	f.diverge(func() { f.write("r2.txt", "an unrelated file") },
		func() { f.rm("r2.txt"); f.rm("r.txt"); f.write("r2.txt", "rename_me_content_long\n") })

	res, err := f.merge("feat")
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	r := f.open()
	c, _ := r.Commit(res.Commit)
	tr, _ := r.Tree(c.Tree)
	n := 0
	for _, e := range tr.Entries {
		if e.Name == "r2.txt" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d entries named r2.txt, want the HolyC's 2: %+v", n, tr.Entries)
	}
}
