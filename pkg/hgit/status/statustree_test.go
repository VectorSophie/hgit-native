package status_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
	"github.com/VectorSophie/hgit-native/pkg/hgit/workdir"
)

func TestStatusTreeModifiedNestedNeverReportsUnchanged(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1, 0x2, 0x3)
	f.mkdir("SubA")
	f.write("top.txt", "top v1")
	f.write("SubA/inner.txt", "inner v1")
	f.offerTree("first")

	f.write("SubA/inner.txt", "inner v2")

	r := f.reopen()
	got, err := status.StatusTree(r, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"MODIFIED SubA/inner.txt"} // top.txt unchanged is never reported by statustree
	if got2 := kindsAndPaths(t, got); strings.Join(got2, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %v, want %v", got2, want)
	}
}

func TestStatusTreeNewAndDeletedNested(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1, 0x2)
	f.mkdir("SubA")
	f.write("SubA/keep.txt", "keep")
	f.write("SubA/gone.txt", "gone")
	f.offerTree("first")

	f.remove("SubA/gone.txt")
	f.write("SubA/fresh.txt", "fresh, unrelated")

	r := f.reopen()
	got, err := status.StatusTree(r, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]status.ChangeKind{"SubA/fresh.txt": status.New, "SubA/gone.txt": status.Deleted}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	for _, c := range got {
		if k, ok := want[c.Path]; !ok || k != c.Kind {
			t.Fatalf("got %+v, want %v", got, want)
		}
	}
}

func TestStatusTreeDeletedDirectoryRecursesEveryFile(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1, 0x2, 0x3)
	f.mkdir("SubA/Deep")
	f.write("SubA/a.txt", "a")
	f.write("SubA/Deep/b.txt", "b")
	f.offerTree("first")

	if err := os.RemoveAll(filepath.Join(f.dir, "SubA")); err != nil {
		t.Fatal(err)
	}

	r := f.reopen()
	got, err := status.StatusTree(r, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"SubA/a.txt": true, "SubA/Deep/b.txt": true}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	for _, c := range got {
		if c.Kind != status.Deleted || !want[c.Path] {
			t.Fatalf("got %+v, want DELETED %v", got, want)
		}
	}
}

// A tracked file replaced by a same-named directory: the forward pass
// reports TYPE_CHANGED (it is now a real directory, and TreeFindEntry found
// a non-tree old entry for that name); Status.HC's own deletion pass then
// separately tries to HgitFileRead the same name as a plain file (it only
// special-cases an old TREE-typed entry, not a old-BLOB one that is now a
// directory) - that read fails, so the same name is ALSO reported DELETED.
// Both lines are genuine HolyC output for this case, not a porting bug.
func TestStatusTreeTypeChanged(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1)
	f.write("thing.txt", "was a file")
	f.offerTree("first")

	f.remove("thing.txt")
	f.mkdir("thing.txt")
	f.write("thing.txt/inner.txt", "now a directory")

	r := f.reopen()
	got, err := status.StatusTree(r, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[status.ChangeKind]bool{status.TypeChanged: false, status.Deleted: false}
	if len(got) != 2 {
		t.Fatalf("got %+v, want a TYPE_CHANGED and a DELETED line for thing.txt", got)
	}
	for _, c := range got {
		if c.Path != "thing.txt" {
			t.Fatalf("got %+v", got)
		}
		if _, ok := want[c.Kind]; !ok {
			t.Fatalf("got %+v", got)
		}
		want[c.Kind] = true
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("got %+v, missing kind %v", got, k)
		}
	}
}

// TestStatusTreeTrackedDirectoryReplacedByFile: Status.HC's deletion pass
// decides whether an old TREE-typed entry's directory is "still real" with
// FilesFind("<dir_path><tname>*", 0) != NULL - a PREFIX glob against the
// parent directory's own entries, not an exact-name existence check. A
// same-named plain file satisfies that glob trivially (zero extra
// characters), so the HolyC treats the directory as still present and
// skips the deletion recursion entirely - only STATUS_TYPE_CHANGED (from
// the forward pass) is printed, nothing from inside the former directory,
// and (this is the crash this test guards against) no error: a literal
// "is this still a directory" check would instead try to list a plain file
// as a directory and fail with ENOTDIR.
func TestStatusTreeTrackedDirectoryReplacedByFile(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1, 0x2)
	f.mkdir("Sub")
	f.write("Sub/inner.txt", "was a directory")
	f.offerTree("first")

	if err := os.RemoveAll(filepath.Join(f.dir, "Sub")); err != nil {
		t.Fatal(err)
	}
	f.write("Sub", "now a plain file")

	r := f.reopen()
	got, err := status.StatusTree(r, f.dir)
	if err != nil {
		t.Fatalf("StatusTree returned an error instead of STATUS_TYPE_CHANGED: %v", err)
	}
	if len(got) != 1 || got[0].Kind != status.TypeChanged || got[0].Path != "Sub" {
		t.Fatalf("got %+v, want exactly one TYPE_CHANGED Sub and nothing else", got)
	}
}

// TestStatusTreeDeletedDirectorySuppressedByPrefixSibling: the same glob,
// Status.HC's own behaviour when the tracked directory is genuinely gone
// but a SIBLING name happens to start with the same prefix (e.g. "Sub" the
// directory vs. "SubNotes.txt" the file) - FilesFind("Sub*", 0) still finds
// SubNotes.txt, so still_dir is (wrongly, but faithfully) true and the
// deletion recursion into Sub/ is suppressed: none of Sub/'s former
// children are reported DELETED.
func TestStatusTreeDeletedDirectorySuppressedByPrefixSibling(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1, 0x2)
	f.mkdir("Sub")
	f.write("Sub/inner.txt", "content")
	f.offerTree("first")

	if err := os.RemoveAll(filepath.Join(f.dir, "Sub")); err != nil {
		t.Fatal(err)
	}
	f.write("SubNotes.txt", "an unrelated sibling whose name happens to start with Sub")

	r := f.reopen()
	got, err := status.StatusTree(r, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.Kind == status.Deleted {
			t.Fatalf("got %+v, want no DELETED lines - SubNotes.txt suppresses the Sub/ recursion", got)
		}
	}
	sawNew := false
	for _, c := range got {
		if c.Kind == status.New && c.Path == "SubNotes.txt" {
			sawNew = true
		}
	}
	if !sawNew {
		t.Fatalf("got %+v, want SubNotes.txt reported NEW", got)
	}
}

func TestStatusTreeIgnoredDirNotDescendedNorRead(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	f := setup(t)
	f.writeIgnore("build/\n")
	seedIDs(t, 0x1)
	f.write("keep.txt", "kept")
	f.offerTree("first")

	f.mkdir("build")
	f.write("build/secret.txt", "should never be read")
	// chmod-0 proof: if statustree ever descended into build/, reading this
	// unreadable file inside it would surface as an error.
	if err := os.Chmod(filepath.Join(f.dir, "build", "secret.txt"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(f.dir, "build", "secret.txt"), 0o644)

	r := f.reopen()
	got, err := status.StatusTree(r, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want nothing (build/ never descended into)", got)
	}
}

func TestStatusTreeEmptyRepoNoOfferingsYet(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "hi")
	r := f.reopen()
	_, err := status.StatusTree(r, f.dir)
	var noOff *status.NoOfferingsYetError
	if !errors.As(err, &noOff) {
		t.Fatalf("err = %v, want *NoOfferingsYetError", err)
	}
}

func TestStatusTreeUnreadableFileTypedError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	f := setup(t)
	f.writeIgnore("")
	f.write("a.txt", "hi")
	f.offerTree("first")
	if err := os.Chmod(filepath.Join(f.dir, "a.txt"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(f.dir, "a.txt"), 0o644)

	r := f.reopen()
	_, err := status.StatusTree(r, f.dir)
	var re *workdir.ReadError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want *workdir.ReadError", err)
	}
}

func TestStatusTreeTooDeep(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	f.write("root.txt", "x")
	f.offerTree("first")

	dir := f.dir
	for i := 0; i < status.MaxTreeDepth+2; i++ {
		dir = filepath.Join(dir, "d")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "leaf.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := f.reopen()
	_, err := status.StatusTree(r, f.dir)
	if !errors.Is(err, status.ErrTooDeep) {
		t.Fatalf("err = %v, want ErrTooDeep", err)
	}
}
