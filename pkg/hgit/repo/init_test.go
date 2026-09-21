package repo_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

func TestInitWritesEmptyHeader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new.hgs")
	if err := repo.Init(p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != archive.HeaderLen {
		t.Fatalf("size = %d, want %d", len(b), archive.HeaderLen)
	}
	h, err := archive.ParseHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 4 || h.Count != 0 {
		t.Fatalf("header = %+v, want version 4 count 0", h)
	}
	// The metadata file is created lazily, by the first Save.
	if _, err := os.Stat(p + ".m"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".m stat = %v, want not-exist", err)
	}
	r, err := repo.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Arc.Records) != 0 {
		t.Fatalf("records = %d, want 0", len(r.Arc.Records))
	}
}

func TestInitRefusesExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "taken.hgs")
	if err := os.WriteFile(p, []byte("not a repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Init(p); !errors.Is(err, repo.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "not a repo" {
		t.Fatalf("file was overwritten: %q", b)
	}
}
