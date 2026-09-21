package repo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	os.WriteFile(p, testfix.Read(t, name), 0o644)
	if b, err := os.ReadFile(testfix.Path(name + ".m")); err == nil {
		os.WriteFile(p+".m", b, 0o644)
	}
	return p
}

func TestOpenHistoryFixture(t *testing.T) {
	r, err := Open(copyFixture(t, "TFullRepo.hgs"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := r.History()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d commits: %+v", len(h), h)
	if len(h) < 2 || h[len(h)-1].Message != "first_offer" || h[len(h)-2].Message != "second_offer" {
		t.Fatalf("history tail wrong: %+v", h)
	}
}

func TestPutSaveOpen(t *testing.T) {
	p := copyFixture(t, "TFullRepo.hgs")
	r, _ := Open(p)
	n := len(r.Arc.Records)
	h := r.Put(archive.Blob, []byte("hello"))
	if r.Put(archive.Blob, []byte("hello")) != h || len(r.Arc.Records) != n+1 {
		t.Fatal("Put not idempotent")
	}
	r.SetHead("side", h)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Arc.Header.Count != uint64(n+1) {
		t.Fatalf("count %d", r2.Arc.Header.Count)
	}
	if rec, ok := r2.Get(h); !ok || string(rec.Content()) != "hello" {
		t.Fatal("blob lost")
	}
	if g, ok := r2.Head("side"); !ok || g != h {
		t.Fatal("head lost")
	}
	if tot, ok := r2.Arc.Verify(); tot != ok {
		t.Fatal("verify")
	}
	tmps, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.tmp"))
	if len(tmps) != 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
}

func TestMissingMetaAndEmptyHistory(t *testing.T) {
	p := copyFixture(t, "TFullRepo.hgs")
	os.Remove(p + ".m")
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.CurrentPath() != "main" {
		t.Fatal("default path")
	}
	if _, err := r.History(); err != ErrNoHead {
		t.Fatalf("got %v", err)
	}
}

func TestBrokenChain(t *testing.T) {
	r, _ := Open(copyFixture(t, "TFullRepo.hgs"))
	r.SetHead("main", archive.Sum([]byte("nope")))
	if _, err := r.History(); err != ErrBrokenChain {
		t.Fatalf("got %v", err)
	}
}
