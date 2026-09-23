package repo

import (
	"errors"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"os"
	"path/filepath"
	"strings"
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
	if len(h) != 3 || h[len(h)-1].Message != "first_offer" || h[len(h)-2].Message != "second_offer" {
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
	if err := r.SetHead("side", h); err != nil {
		t.Fatal(err)
	}
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
	_ = r.SetHead("main", archive.Sum([]byte("nope")))
	if _, err := r.History(); err != ErrBrokenChain {
		t.Fatalf("got %v", err)
	}
}

func TestSetHeadNameTooLong(t *testing.T) {
	r, _ := Open(copyFixture(t, "TFullRepo.hgs"))
	before, _ := r.Head("main")
	err := r.SetHead(strings.Repeat("x", MaxPathName), archive.Sum([]byte("z")))
	if !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("got %v", err)
	}
	if after, _ := r.Head("main"); after != before || len(r.Meta.All(strings.Repeat("x", MaxPathName), meta.TagHead)) != 0 {
		t.Fatal("state changed")
	}
	if err := r.SetHead(strings.Repeat("x", MaxPathName-1), archive.Sum([]byte("z"))); err != nil {
		t.Fatal(err)
	}
}

// forge appends a record whose stored hash is chosen by the caller, so a
// graph with loops can exist (a real hash cannot reference itself).
func forge(r *Repo, h archive.Hash, ty archive.Type, content []byte) {
	rec := archive.NewObject(ty, content)
	rec.Hash = h
	r.idx[h] = len(r.Arc.Records)
	r.Arc.Records = append(r.Arc.Records, rec)
}

func h(s string) archive.Hash { return archive.Sum([]byte(s)) }

func emptyRepo(t *testing.T) *Repo {
	return &Repo{Path: filepath.Join(t.TempDir(), "r.hgs"), Arc: &archive.Archive{Header: archive.Header{Version: 4}},
		Meta: &meta.File{}, idx: map[archive.Hash]int{}}
}

func TestHistoryCycleTerminates(t *testing.T) {
	r := emptyRepo(t)
	a, b := h("a"), h("b")
	forge(r, a, archive.Commit, (&object.Commit{Parents: []archive.Hash{b}, Message: []byte("a")}).Encode())
	forge(r, b, archive.Commit, (&object.Commit{Parents: []archive.Hash{a}, Message: []byte("b")}).Encode())
	_ = r.SetHead("main", a)
	lines, err := r.History()
	if err != ErrBrokenChain || len(lines) != 2 {
		t.Fatalf("lines=%d err=%v", len(lines), err)
	}
}

func TestSeeCycleTerminates(t *testing.T) {
	r := emptyRepo(t)
	ta, tb := h("ta"), h("tb")
	ent := func(c archive.Hash) []byte {
		return (&object.Tree{Entries: []object.Entry{{Name: "d", ChildType: archive.Tree, ChildHash: c}}}).Encode()
	}
	forge(r, ta, archive.Tree, ent(tb))
	forge(r, tb, archive.Tree, ent(ta))
	c := r.Put(archive.Commit, (&object.Commit{Tree: ta, Message: []byte("m")}).Encode())
	res, err := r.See(c)
	if err != nil {
		t.Fatal(err)
	}
	last := res.Entries[len(res.Entries)-1]
	if len(res.Entries) != 3 || !last.Missing {
		t.Fatalf("entries=%+v", res.Entries)
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, b, 0o644)
		return p
	}
	good := testfix.Read(t, "TFullRepo.hgs")
	if _, err := Open(write("magic.hgs", append([]byte("XXXX"), good[4:]...))); !errors.Is(err, archive.ErrBadMagic) {
		t.Fatalf("magic: %v", err)
	}
	if _, err := Open(write("trunc.hgs", good[:len(good)-10])); !errors.Is(err, archive.ErrTruncated) {
		t.Fatalf("trunc: %v", err)
	}
	p := write("badm.hgs", good)
	os.WriteFile(p+".m", []byte{5, 'a'}, 0o644)
	if _, err := Open(p); !errors.Is(err, meta.ErrMalformed) {
		t.Fatalf("meta: %v", err)
	}
	_, err := Open(write("newer.hgs", testfix.Read(t, "TFConfNewer.hgs")))
	var uv *archive.UnsupportedVersionError
	if !errors.As(err, &uv) {
		t.Fatalf("newer: %v", err)
	}
	if _, err := Open(filepath.Join(dir, "absent.hgs")); err == nil {
		t.Fatal("missing file opened")
	}
}

func TestSaveFailureLeavesNoTmp(t *testing.T) {
	p := copyFixture(t, "TFullRepo.hgs")
	os.Remove(p + ".m")
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	os.Mkdir(p+".m", 0o755) // rename of a file onto a directory fails
	os.WriteFile(filepath.Join(p+".m", "keep"), nil, 0o644)
	if err := r.Save(); err == nil {
		t.Fatal("expected failure")
	}
	tmps, _ := filepath.Glob(p + "*.tmp")
	if len(tmps) != 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
}

// A metadata record that cannot be encoded makes Save fail and leaves the
// files on disk as they were, still readable.
func TestSaveRefusesUnencodableMeta(t *testing.T) {
	p := copyFixture(t, "TFullRepo.hgs")
	r, _ := Open(p)
	before, _ := os.ReadFile(p + ".m")
	r.Meta.Set("main", meta.TagMergeState, meta.MergeState{OtherPath: strings.Repeat("x", 200)}.Encode())
	if err := r.Save(); !errors.Is(err, meta.ErrFieldTooLong) {
		t.Fatalf("got %v", err)
	}
	if after, _ := os.ReadFile(p + ".m"); string(after) != string(before) {
		t.Fatal(".m changed")
	}
	if _, err := Open(p); err != nil {
		t.Fatal(err)
	}
}

func TestSetHeadSaveOpenRoundTrip(t *testing.T) {
	p := copyFixture(t, "TFullRepo.hgs")
	r, _ := Open(p)
	name := strings.Repeat("n", MaxPathName-1)
	h := archive.Sum([]byte("z"))
	if err := r.SetHead(name, h); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r2.Head(name); !ok || got != h {
		t.Fatal("head lost")
	}
}
