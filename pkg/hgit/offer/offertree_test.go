package offer_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/workdir"
)

// mkdir creates a subdirectory of the working directory.
func (f *fixture) mkdir(rel string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir, rel), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) offerTree(msg string) archive.Hash {
	f.t.Helper()
	h, err := f.offerTreeOpts(offer.Options{Message: msg})
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (f *fixture) offerTreeOpts(opts offer.Options) (archive.Hash, error) {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.r = r
	return offer.OfferTree(r, f.dir, opts)
}

// sub resolves a tree-typed entry's own tree.
func (f *fixture) sub(tr *object.Tree, name string) *object.Tree {
	f.t.Helper()
	e, ok := tr.Find(name)
	if !ok {
		f.t.Fatalf("%s missing from tree %v", name, names(tr))
	}
	if e.ChildType != archive.Tree {
		f.t.Fatalf("%s is type %d, want a tree", name, e.ChildType)
	}
	child, err := f.r.Tree(e.ChildHash)
	if err != nil {
		f.t.Fatal(err)
	}
	return child
}

// The repo file lives in the working directory in these fixtures, so it and
// its .m are ignored to keep the trees to the files under test.
const repoIgnore = "r.hgs\nr.hgs.m\nr.hgs.tmp\n"

func (f *fixture) writeIgnore(extra string) { f.write(".hgitignore", repoIgnore+extra) }

func TestOfferTreeNestsSubdirectoriesInNameOrder(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x11, 0x22, 0x33, 0x44)
	f.mkdir("SubA")
	f.write("top.txt", "tree top v1")
	f.write("SubA/inner.txt", "tree inner v1")

	tr := f.tree(f.offerTree("first"))
	if got := names(tr); len(got) != 3 || got[0] != ".hgitignore" || got[1] != "SubA" || got[2] != "top.txt" {
		t.Fatalf("root entries = %v", got)
	}
	e, _ := tr.Find("SubA")
	if e.ChildType != archive.Tree {
		t.Fatalf("SubA child type = %d, want a tree", e.ChildType)
	}
	if got := names(f.sub(tr, "SubA")); len(got) != 1 || got[0] != "inner.txt" {
		t.Fatalf("SubA entries = %v", got)
	}
}

func TestOfferTreeNestedEditCarriesEntityIDs(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write("top.txt", "tree top v1")
	f.write("SubA/inner.txt", "tree inner v1")
	first := f.tree(f.offerTree("first"))
	firstInner := idOf(t, f.sub(first, "SubA"), "inner.txt")

	f.write("SubA/inner.txt", "tree inner v2 CHANGED")
	second := f.tree(f.offerTree("second"))
	if idOf(t, second, "SubA") != idOf(t, first, "SubA") {
		t.Fatal("the subdirectory must keep its own entity id")
	}
	if idOf(t, second, "top.txt") != idOf(t, first, "top.txt") {
		t.Fatal("top.txt must keep its entity id")
	}
	if idOf(t, f.sub(second, "SubA"), "inner.txt") != firstInner {
		t.Fatal("a nested edit must carry the nested file's entity id")
	}
}

func TestOfferTreeNestedRenameCarriesEntityID(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write("SubA/inner.txt", "tree inner v1")
	first := f.tree(f.offerTree("first"))
	want := idOf(t, f.sub(first, "SubA"), "inner.txt")

	f.remove("SubA/inner.txt")
	f.write("SubA/renamed.txt", "tree inner v1")
	second := f.tree(f.offerTree("second"))
	if got := idOf(t, f.sub(second, "SubA"), "renamed.txt"); got != want {
		t.Fatalf("renamed entity id = %#x, want %#x", got, want)
	}
}

func TestOfferTreeNewSubdirectoryGetsAFreshID(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write("SubA/inner.txt", "a")
	first := f.tree(f.offerTree("first"))

	f.mkdir("SubB")
	f.write("SubB/other.txt", "b")
	second := f.tree(f.offerTree("second"))
	if idOf(t, second, "SubB") == idOf(t, first, "SubA") {
		t.Fatal("a new subdirectory must get its own identity")
	}
	if idOf(t, second, "SubA") != idOf(t, first, "SubA") {
		t.Fatal("the existing subdirectory keeps its identity")
	}
}

func TestOfferTreeDeletedSubdirectoryLeavesTheTree(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write("SubA/inner.txt", "a")
	f.write("top.txt", "t")
	f.offerTree("first")

	f.remove("SubA/inner.txt")
	if err := os.Remove(filepath.Join(f.dir, "SubA")); err != nil {
		t.Fatal(err)
	}
	second := f.tree(f.offerTree("second"))
	if _, ok := second.Find("SubA"); ok {
		t.Fatalf("SubA should be gone: %v", names(second))
	}
}

func TestOfferTreeDoesNotTrackAnEmptyDirectory(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("Empty")
	f.mkdir("OnlyEmptyChildren/Deeper")
	f.write("top.txt", "t")

	tr := f.tree(f.offerTree("first"))
	for _, n := range []string{"Empty", "OnlyEmptyChildren"} {
		if _, ok := tr.Find(n); ok {
			t.Fatalf("%s has nothing trackable and must produce no entry: %v", n, names(tr))
		}
	}
	// ...and no tree object was stored for it either. The root tree here is
	// non-empty, so any empty tree record would have to be a directory's.
	for _, rec := range f.r.Arc.Records {
		if rec.Type() != archive.Tree {
			continue
		}
		sub, err := object.DecodeTree(rec.Content())
		if err != nil {
			t.Fatal(err)
		}
		if len(sub.Entries) == 0 {
			t.Fatal("an empty tree object was stored")
		}
	}
}

func TestOfferTreeNeverDescendsIntoAnIgnoredDirectory(t *testing.T) {
	f := setup(t)
	f.writeIgnore("Skip/\n")
	f.mkdir("Skip")
	f.write("Skip/secret.txt", "must never be read")
	f.write("top.txt", "t")

	// An unreadable file inside the ignored directory proves it is never
	// even opened: reading it would fail the offer.
	if os.Geteuid() != 0 {
		if err := os.Chmod(filepath.Join(f.dir, "Skip", "secret.txt"), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(filepath.Join(f.dir, "Skip", "secret.txt"), 0o644) })
	}

	var ignored []string
	h, err := f.offerTreeOpts(offer.Options{
		Message:   "first",
		OnIgnored: func(name string) { ignored = append(ignored, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.tree(h).Find("Skip"); ok {
		t.Fatal("Skip must not be tracked")
	}
	// The repo's own files are ignored here too; what matters is that Skip
	// was reported once, by its own name, and nothing inside it ever was.
	var sawSkip int
	for _, n := range ignored {
		switch {
		case n == "Skip":
			sawSkip++
		case strings.HasPrefix(n, "Skip/"):
			t.Fatalf("the walk descended into Skip: %v", ignored)
		}
	}
	if sawSkip != 1 {
		t.Fatalf("OnIgnored = %v, want Skip reported once", ignored)
	}
	for _, rec := range f.r.Arc.Records {
		if rec.Type() == archive.Blob && string(rec.Content()) == "must never be read" {
			t.Fatal("the ignored directory's content was stored")
		}
	}
}

func TestOfferTreeIgnoredFileIsNotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	f := setup(t)
	f.writeIgnore("*.tmp\n")
	f.write("keep.txt", "kept")
	f.write("x.tmp", "ignored")
	if err := os.Chmod(filepath.Join(f.dir, "x.tmp"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(f.dir, "x.tmp"), 0o644) })

	if _, err := f.offerTreeOpts(offer.Options{Message: "first"}); err != nil {
		t.Fatalf("an ignored file must never be opened: %v", err)
	}
}

func TestOfferTreeUnreadableFileIsATypedError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write("SubA/inner.txt", "a")
	if err := os.Chmod(filepath.Join(f.dir, "SubA", "inner.txt"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(f.dir, "SubA", "inner.txt"), 0o644) })

	_, err := f.offerTreeOpts(offer.Options{Message: "first"})
	var re *workdir.ReadError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a *workdir.ReadError", err)
	}
	if re.Name != "SubA/inner.txt" {
		t.Fatalf("ReadError names %q, want the offered-root-relative path", re.Name)
	}
}

// ADR 0014's safety rule at directory level: a name already tracked in the
// parent tree is never hidden by an ignore rule added later.
func TestOfferTreeTrackedNameInsideALaterIgnoredDirectoryStaysTracked(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write("SubA/inner.txt", "a")
	first := f.tree(f.offerTree("first"))
	wantSub := idOf(t, first, "SubA")

	f.writeIgnore("SubA/\n")
	f.write("SubA/added.txt", "b")
	second := f.tree(f.offerTree("second"))
	if idOf(t, second, "SubA") != wantSub {
		t.Fatal("an already-tracked directory must not be hidden by a new rule")
	}
	sub := f.sub(second, "SubA")
	if got := names(sub); len(got) != 2 || got[0] != "added.txt" || got[1] != "inner.txt" {
		// The HolyC matches a "SubA/" rule against the candidate's own NAME
		// only, so a new file inside a tracked SubA is not hidden by it.
		t.Fatalf("SubA entries = %v, want added.txt and inner.txt", got)
	}
}

func TestOfferTreeAttrsCoverEveryLevelInTraversalOrder(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write(".hgitattributes", "*.sh executable\n")
	f.write("SubA/inner.sh", "echo inner")
	f.write("top.sh", "echo top")
	f.write("plain.txt", "nothing special")

	h := f.offerTree("first")
	c, err := f.r.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	if c.Attrs == nil {
		t.Fatal("want a commit-level attrs object")
	}
	rec, ok := f.r.Get(*c.Attrs)
	if !ok {
		t.Fatal("attrs object missing")
	}
	a, err := object.DecodeAttrs(rec.Content())
	if err != nil {
		t.Fatal(err)
	}
	tr := f.tree(h)
	want := []uint64{idOf(t, f.sub(tr, "SubA"), "inner.sh"), idOf(t, tr, "top.sh")}
	if len(a.Entries) != 2 {
		t.Fatalf("attrs = %+v, want two entries", a.Entries)
	}
	for i, e := range a.Entries {
		if e.EntityID != want[i] || e.Mode != object.ModeExecutable {
			t.Fatalf("attrs entry %d = %#x/%#x, want %#x/executable", i, e.EntityID, e.Mode, want[i])
		}
	}
}

func TestOfferTreeRelationCommit(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write("SubA/inner.txt", "a")
	first := f.offerTree("first")

	f.write("SubA/inner.txt", "a fixed")
	h, err := f.offerTreeOpts(offer.Options{
		Message:        "correcting",
		Relation:       object.RelCorrects,
		RelationTarget: first,
		RelationEntity: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.r.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	if c.Relation != object.RelCorrects || c.RelationTarget != first || c.RelationEntity != 0 {
		t.Fatalf("relation fields = %v %v %#x", c.Relation, c.RelationTarget, c.RelationEntity)
	}
}

func TestOfferTreeDepthLimit(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	deep := ""
	for i := 0; i <= offer.MaxTreeDepth; i++ {
		deep = filepath.Join(deep, "d")
	}
	f.mkdir(deep)
	f.write(filepath.Join(deep, "x.txt"), "deep")

	if _, err := f.offerTreeOpts(offer.Options{Message: "deep"}); !errors.Is(err, offer.ErrTooDeep) {
		t.Fatalf("err = %v, want ErrTooDeep", err)
	}
}

// Carry-over from the flat path: a failing entity-id source refuses the offer
// rather than recording a 0 identity.
func TestOfferRefusesWhenEntityIDGenerationFails(t *testing.T) {
	f := setup(t)
	old := offer.NewEntityID
	offer.NewEntityID = func() uint64 { return 0 }
	t.Cleanup(func() { offer.NewEntityID = old })
	f.write("a.txt", "alpha")

	if _, err := f.offerOpts("*.txt", offer.Options{Message: "x"}); !errors.Is(err, offer.ErrEntityID) {
		t.Fatalf("err = %v, want ErrEntityID", err)
	}
}

func TestOfferWithNoOnIgnoredHookStillIgnores(t *testing.T) {
	f := setup(t)
	f.write(".hgitignore", "*.tmp\n")
	f.write("keep.txt", "kept")
	f.write("x.tmp", "ignored")

	h, err := f.offerOpts("*", offer.Options{Message: "x"}) // OnIgnored nil
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.tree(h).Find("x.tmp"); ok {
		t.Fatal("x.tmp must still be ignored without a hook")
	}
}

// TestOfferTreeFileBecomesDirectoryGetsFreshIdentity: buildTree only carries
// a directory node's old identity forward when the OLD entry of that name
// was itself a tree (foundOldSub) - a name that used to be a tracked FILE
// and is now a directory does not satisfy that, so it gets a brand-new
// identity instead. This is the opposite rule from the plain-file path
// (CarryEntityID, used for FILE nodes only), which matches by name alone
// regardless of the old entry's type - see the reverse-direction test below.
func TestOfferTreeFileBecomesDirectoryGetsFreshIdentity(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x11, 0x22)
	f.write("thing", "was a plain file")
	first := f.offerTree("first")
	fileID := idOf(t, f.tree(first), "thing")

	if err := os.Remove(filepath.Join(f.dir, "thing")); err != nil {
		t.Fatal(err)
	}
	f.mkdir("thing")
	f.write("thing/inner.txt", "now a directory")
	second := f.offerTree("second")

	tr := f.tree(second)
	e, ok := tr.Find("thing")
	if !ok || e.ChildType != archive.Tree {
		t.Fatalf("thing = %+v, want a tree entry", e)
	}
	if e.EntityID == fileID {
		t.Fatal("a file-to-directory type change must get a fresh identity, not the file's old one")
	}
}

// TestOfferTreeDirectoryBecomesFileCarriesIdentityByName is the reverse
// direction of the same rule.
func TestOfferTreeDirectoryBecomesFileCarriesIdentityByName(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x11, 0x22)
	f.mkdir("thing")
	f.write("thing/inner.txt", "a directory")
	first := f.offerTree("first")
	dirID := idOf(t, f.tree(first), "thing")

	if err := os.RemoveAll(filepath.Join(f.dir, "thing")); err != nil {
		t.Fatal(err)
	}
	f.write("thing", "now a plain file")
	second := f.offerTree("second")

	e, ok := f.tree(second).Find("thing")
	if !ok || e.ChildType != archive.Blob {
		t.Fatalf("thing = %+v, want a blob entry", e)
	}
	if e.EntityID != dirID {
		t.Fatal("the name-matched entity id must carry forward across the type change")
	}
}

// TestOfferTreeDeletedThenRecreatedDirectoryGetsFreshIdentity: once a
// directory is fully removed from a tree (nothing left inside it to track),
// its name no longer appears in that tree at all - so re-creating it later
// finds no name match and gets a brand-new identity, never the old one.
func TestOfferTreeDeletedThenRecreatedDirectoryGetsFreshIdentity(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x11, 0x22, 0x33)
	f.mkdir("SubA")
	f.write("SubA/inner.txt", "v1")
	first := f.offerTree("first")
	oldID := idOf(t, f.tree(first), "SubA")

	if err := os.RemoveAll(filepath.Join(f.dir, "SubA")); err != nil {
		t.Fatal(err)
	}
	second := f.offerTree("second") // SubA tracked nowhere now (empty dirs aren't tracked)
	if _, ok := f.tree(second).Find("SubA"); ok {
		t.Fatal("an empty/gone directory must not appear in the tree")
	}

	f.mkdir("SubA")
	f.write("SubA/inner.txt", "v2, a different file entirely")
	third := f.offerTree("third")
	e, ok := f.tree(third).Find("SubA")
	if !ok {
		t.Fatal("SubA should be tracked again")
	}
	if e.EntityID == oldID {
		t.Fatal("a re-created directory must get a fresh identity, not the old one")
	}
}

// TestOfferTreeUnresolvableOldSubtreeStillCarriesItsID: when the old tree's
// entry for a directory names a hash the archive cannot resolve, buildTree's
// oldSub falls back to nil (recursed as if empty) but the entity id itself -
// read directly off the old entry, never off the resolved subtree - still
// carries forward, exactly as the HolyC's own NULL old_sub_tree_content
// leaves old_entity_id untouched.
func TestOfferTreeUnresolvableOldSubtreeStillCarriesItsID(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x11, 0x22)
	f.mkdir("SubA")
	f.write("SubA/inner.txt", "v1")
	first := f.offerTree("first")
	oldID := idOf(t, f.tree(first), "SubA")

	// Corrupt the archive so SubA's own tree object can no longer be
	// resolved by hash, without touching the root tree's entry for SubA.
	r, err := repo.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	rootTr, err := r.Tree(mustCommitTree(t, r, first))
	if err != nil {
		t.Fatal(err)
	}
	subEntry, ok := rootTr.Find("SubA")
	if !ok {
		t.Fatal("SubA missing from the root tree")
	}
	for i := range r.Arc.Records {
		if r.Arc.Records[i].Hash == subEntry.ChildHash {
			r.Arc.Records[i].Data[len(r.Arc.Records[i].Data)-1] ^= 1          // corrupt, keep the hash key unresolved on purpose
			r.Arc.Records = append(r.Arc.Records[:i], r.Arc.Records[i+1:]...) // drop it: unresolvable
			break
		}
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	f.write("SubA/inner.txt", "v2")
	second := f.offerTree("second")
	e, ok := f.tree(second).Find("SubA")
	if !ok {
		t.Fatal("SubA missing from the second tree")
	}
	if e.EntityID != oldID {
		t.Fatal("an unresolvable old subtree must still carry its own entity id forward")
	}
}

func mustCommitTree(t *testing.T, r *repo.Repo, h archive.Hash) archive.Hash {
	t.Helper()
	c, err := r.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	return c.Tree
}
