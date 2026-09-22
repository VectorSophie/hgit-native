package status_test

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
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
)

// offerH is f.offer, keeping the commit hash Diff needs.
func (f *fixture) offerH(mask, msg string) archive.Hash {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	h, err := offer.Offer(r, f.dir, mask, offer.Options{Message: msg})
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (f *fixture) offerTreeH(msg string) archive.Hash {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	h, err := offer.OfferTree(r, f.dir, offer.Options{Message: msg})
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (f *fixture) diff(h archive.Hash) []status.Change {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	changes, err := status.Diff(r, h)
	if err != nil {
		f.t.Fatal(err)
	}
	return changes
}

// lines renders changes the way SerialDiff does, so a test can assert on the
// whole, ordered result in one string.
func lines(changes []status.Change) string {
	var b strings.Builder
	for _, c := range changes {
		switch c.Kind {
		case status.New:
			b.WriteString("NEW " + c.Path + "\n")
		case status.Modified:
			b.WriteString("MODIFIED " + c.Path + "\n")
		case status.Deleted:
			b.WriteString("DELETED " + c.Path + "\n")
		case status.Renamed:
			b.WriteString("RENAMED " + c.OldPath + " -> " + c.Path + "\n")
		case status.TypeChanged:
			b.WriteString("TYPE_CHANGED " + c.Path + "\n")
		case status.ModeChanged:
			b.WriteString("MODE_CHANGED " + c.Path + "\n")
		default:
			b.WriteString("?\n")
		}
	}
	return b.String()
}

func want(t *testing.T, changes []status.Change, expect string) {
	t.Helper()
	if got := lines(changes); got != expect {
		t.Fatalf("diff:\n got:\n%s\nwant:\n%s", got, expect)
	}
}

func TestDiffRootCommitIsAllNew(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("a.txt", "aaa")
	f.write("b.txt", "bbb")
	h := f.offerH("*.txt", "root")
	want(t, f.diff(h), "NEW a.txt\nNEW b.txt\n")
}

func TestDiffModifiedAndDeletedAndNew(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("stays.txt", "same")
	f.write("mod.txt", "version one")
	f.write("gone.txt", "going away")
	f.offerH("*.txt", "root")

	f.write("mod.txt", "version two")
	f.remove("gone.txt")
	f.write("fresh.txt", "brand new content, unrelated")
	h := f.offerH("*.txt", "second")
	want(t, f.diff(h), "MODIFIED mod.txt\nNEW fresh.txt\nDELETED gone.txt\n")
}

func TestDiffModeOnlyChange(t *testing.T) {
	f := setup(t)
	f.writeIgnore(".hgitattributes\n")
	f.write("script.txt", "echo hi")
	f.offerH("*.txt", "root")

	f.write(".hgitattributes", "script.txt executable\n")
	h := f.offerH("*.txt", "mode")
	changes := f.diff(h)
	want(t, changes, "MODE_CHANGED script.txt\n")
	if changes[0].OldMode != 0 || changes[0].NewMode != object.ModeExecutable {
		t.Fatalf("modes %d -> %d", changes[0].OldMode, changes[0].NewMode)
	}
}

func TestDiffExactRename(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("orig.txt", "identical bytes either way")
	f.offerH("*.txt", "root")

	f.remove("orig.txt")
	f.write("moved.txt", "identical bytes either way")
	h := f.offerH("*.txt", "renamed")
	want(t, f.diff(h), "RENAMED orig.txt -> moved.txt\n")
}

func TestDiffFuzzyRename(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("orig.txt", "The quick brown fox jumps over the lazy dog. "+
		"The quick brown fox jumps over the lazy dog again.")
	f.offerH("*.txt", "root")

	f.remove("orig.txt")
	f.write("moved.txt", "The quick brown fox LEAPS over the lazy dog. "+
		"The quick brown fox jumps over the lazy dog again.")
	h := f.offerH("*.txt", "renamed")
	want(t, f.diff(h), "RENAMED orig.txt -> moved.txt\n")
}

// Diff.HC has no equivalent of Status.HC's 512-byte fuzzy-rename buffer:
// both sides are already-stored blobs, resolved through the index on demand
// (Diff.HC's own file header, and its rename pass 2). A pair far over that
// ceiling still matches here, where status would not have buffered it.
func TestDiffFuzzyRenameHasNoSizeCeiling(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	big := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 40) // 1760 bytes
	f.write("orig.txt", big)
	f.offerH("*.txt", "root")

	f.remove("orig.txt")
	f.write("moved.txt", big+"one extra trailing line\n")
	h := f.offerH("*.txt", "renamed")
	want(t, f.diff(h), "RENAMED orig.txt -> moved.txt\n")
}

func TestDiffNestedSubdirectoryModified(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write("top.txt", "tree top v1")
	f.write("SubA/inner.txt", "tree inner v1")
	f.offerTreeH("tree_first")

	f.write("SubA/inner.txt", "tree inner v2 CHANGED")
	h := f.offerTreeH("tree_second")
	want(t, f.diff(h), "MODIFIED SubA/inner.txt\n")
}

func TestDiffWhollyNewAndDeletedSubdirectoryRecurse(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("top.txt", "tree top v1")
	f.offerTreeH("tree_first")

	f.mkdir("SubA/SubB")
	f.write("SubA/inner.txt", "inner")
	f.write("SubA/SubB/deep.txt", "deep")
	h2 := f.offerTreeH("tree_second")
	want(t, f.diff(h2), "NEW SubA/SubB/deep.txt\nNEW SubA/inner.txt\n")

	if err := os.RemoveAll(filepath.Join(f.dir, "SubA")); err != nil {
		t.Fatal(err)
	}
	h3 := f.offerTreeH("tree_third")
	want(t, f.diff(h3), "DELETED SubA/SubB/deep.txt\nDELETED SubA/inner.txt\n")
}

func TestDiffTypeChanged(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("Thing", "was a file")
	f.offerTreeH("tree_first")

	f.remove("Thing")
	f.mkdir("Thing")
	f.write("Thing/inner.txt", "now a directory")
	h := f.offerTreeH("tree_second")
	want(t, f.diff(h), "TYPE_CHANGED Thing\n")
}

// A merge commit is diffed against its FIRST parent only, exactly as
// git show/git log -p do - probe 111.
func TestDiffMergeCommitUsesFirstParentOnly(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("a.txt", "shared")
	first := f.offerH("*.txt", "root")
	f.write("b.txt", "only via the second parent")
	second := f.offerH("*.txt", "second")

	r, err := repo.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := r.Commit(second)
	if err != nil {
		t.Fatal(err)
	}
	merge := (&object.Commit{
		Tree:      sc.Tree,
		Parents:   []archive.Hash{first, second},
		Timestamp: sc.Timestamp + 1,
		Message:   []byte("merge"),
	}).Encode()
	h := r.Append(archive.Commit, merge)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	want(t, f.diff(h), "NEW b.txt\n")
}

func TestDiffCommitNotFoundAndNotACommit(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("a.txt", "aaa")
	f.offerH("*.txt", "root")
	r, err := repo.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	var missing archive.Hash
	missing[0] = 0xff
	if _, err := status.Diff(r, missing); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("missing commit: %v", err)
	}
	blob := archive.NewObject(archive.Blob, []byte("aaa")).Hash
	var nt *repo.NotTypeError
	if _, err := status.Diff(r, blob); !errors.As(err, &nt) {
		t.Fatalf("blob hash: %v", err)
	}
}

// DiffResolveTreeByHash returns FALSE for a hash that isn't in the index and
// HgitDiff carries on: the unresolved side is simply an empty tree at that
// level (a broken reference is Check.HC's job to report). Here the NEW side
// of a changed subdirectory is unresolvable, so every entry the OLD side
// still holds is reported deleted - no error, no crash.
func TestDiffBrokenNestedTreeReferenceIsAnEmptyTree(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.mkdir("SubA")
	f.write("top.txt", "top")
	f.write("SubA/inner.txt", "inner")
	first := f.offerTreeH("tree_first")

	r, err := repo.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	fc, err := r.Commit(first)
	if err != nil {
		t.Fatal(err)
	}
	root, err := r.Tree(fc.Tree)
	if err != nil {
		t.Fatal(err)
	}
	for i := range root.Entries {
		if root.Entries[i].ChildType == archive.Tree {
			root.Entries[i].ChildHash[0] ^= 0xff // a hash nothing in the archive has
		}
	}
	newRoot := r.Append(archive.Tree, root.Encode())
	h := r.Append(archive.Commit, (&object.Commit{
		Tree:      newRoot,
		Parents:   []archive.Hash{first},
		Timestamp: fc.Timestamp + 1,
		Message:   []byte("broken"),
	}).Encode())
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	want(t, f.diff(h), "DELETED SubA/inner.txt\n")
}

// The root tree itself missing is HgitDiff's own DIFF_ERR tree_not_found.
func TestDiffRootTreeNotFound(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("a.txt", "aaa")
	f.offerH("*.txt", "root")
	r, err := repo.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	var bogus archive.Hash
	bogus[0] = 0x5a
	h := r.Append(archive.Commit, (&object.Commit{
		Tree:      bogus,
		Timestamp: 1,
		Message:   []byte("no tree"),
	}).Encode())
	if _, err := status.Diff(r, h); !errors.Is(err, repo.ErrTreeNotFound) {
		t.Fatalf("tree_not_found: %v", err)
	}
}
