package workdir_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/workdir"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		mask, name string
		want       bool
	}{
		{"TF*.txt", "TFStays.txt", true},
		{"TF*.txt", "TFullRepo.hgs", false},
		{"TF*.txt", "OtherTF.txt", false},
		{"*", "anything", true},
		{"*", "", true},
		{"", "", true},
		{"", "a", false},
		{"a.txt", "a.txt", true},
		{"a.txt", "A.txt", false}, // case sensitive
		{"?.txt", "a.txt", true},
		{"?.txt", "ab.txt", false},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
		{"*.txt", ".txt", true},
		{"**", "ab", true},
	}
	for _, c := range cases {
		if got := workdir.Match(c.mask, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.mask, c.name, got, c.want)
		}
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("TFb.txt", "bee")
	write("TFa.txt", "ay")
	write("skip.hgs", "no")
	if err := os.Mkdir(filepath.Join(dir, "TFdir.txt"), 0o755); err != nil {
		t.Fatal(err)
	}

	files, err := workdir.List(dir, "TF*.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v, want 2", files)
	}
	if files[0].Name != "TFa.txt" || files[1].Name != "TFb.txt" {
		t.Fatalf("order = %q %q, want sorted", files[0].Name, files[1].Name)
	}
	b, err := workdir.Read(dir, files[0].Name)
	if err != nil || string(b) != "ay" {
		t.Fatalf("Read = %q, %v", b, err)
	}
}

func TestListDirReportsDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodes, err := workdir.ListDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []workdir.Node{{Name: "a.txt"}, {Name: "sub", IsDir: true}}
	if len(nodes) != 2 || nodes[0] != want[0] || nodes[1] != want[1] {
		t.Fatalf("ListDir = %+v, want %+v", nodes, want)
	}
}

// A symlink to a directory is never followed: it is reported as a plain file,
// so the walk cannot loop, and reading it fails by name instead of descending.
func TestListDirDoesNotFollowASymlinkToADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	nodes, err := workdir.ListDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Name != "link" || nodes[0].IsDir {
		t.Fatalf("ListDir = %+v, want link reported as a plain file", nodes)
	}
	var re *workdir.ReadError
	if _, err := workdir.Read(dir, "link"); !errors.As(err, &re) || re.Name != "link" {
		t.Fatalf("Read(link) = %v, want a *workdir.ReadError naming it", err)
	}
}

func TestReadNamesTheFileItCouldNotRead(t *testing.T) {
	var re *workdir.ReadError
	_, err := workdir.Read(t.TempDir(), "sub/missing.txt")
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a *workdir.ReadError", err)
	}
	if re.Name != "sub/missing.txt" {
		t.Fatalf("ReadError names %q", re.Name)
	}
}

func TestListEmptyMatchIsNotAnError(t *testing.T) {
	files, err := workdir.List(t.TempDir(), "TF*.txt")
	if err != nil || len(files) != 0 {
		t.Fatalf("List = %v, %v; want no files, no error", files, err)
	}
}

func TestListMissingDirectory(t *testing.T) {
	if _, err := workdir.List(filepath.Join(t.TempDir(), "nope"), "*"); err == nil {
		t.Fatal("want an error for a missing directory")
	}
}
