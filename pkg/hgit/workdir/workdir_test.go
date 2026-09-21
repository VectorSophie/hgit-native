package workdir_test

import (
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
	if string(files[0].Content) != "ay" {
		t.Fatalf("content = %q", files[0].Content)
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
