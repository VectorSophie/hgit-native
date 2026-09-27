package repo

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
)

// buildRepo writes a small repository with two commits (so History has
// something to walk) and returns its bytes plus the head commit hash.
func buildTornSourceRepo(t *testing.T) (path string, headBefore, headAfter archive.Hash) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.hgs")
	if err := Init(p); err != nil {
		t.Fatal(err)
	}
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	blob := r.Put(archive.Blob, []byte("hello"))
	tr := (&object.Tree{Entries: []object.Entry{{Name: "a", ChildType: archive.Blob, ChildHash: blob, EntityID: 1}}}).Encode()
	trh := r.Put(archive.Tree, tr)
	c1 := (&object.Commit{Tree: trh, Message: []byte("first")}).Encode()
	h1 := r.Put(archive.Commit, c1)
	r.SetHead("main", h1)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	// A second commit whose own last record we will tear off below.
	blob2 := r.Put(archive.Blob, []byte("world"))
	tr2 := (&object.Tree{Entries: []object.Entry{
		{Name: "a", ChildType: archive.Blob, ChildHash: blob, EntityID: 1},
		{Name: "b", ChildType: archive.Blob, ChildHash: blob2, EntityID: 2},
	}}).Encode()
	trh2 := r.Put(archive.Tree, tr2)
	c2 := (&object.Commit{Tree: trh2, Parents: []archive.Hash{h1}, Message: []byte("second")}).Encode()
	h2 := r.Put(archive.Commit, c2)
	r.SetHead("main", h2)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	return p, h1, h2
}

// tearFile truncates the last n bytes off path, simulating an interrupted
// write or a portable-media copy cut short (ADR 0019/0020).
func tearFile(t *testing.T, path string, n int) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	torn := raw[:len(raw)-n]
	if err := os.WriteFile(path, torn, 0o644); err != nil {
		t.Fatal(err)
	}
	return torn
}

func TestTornOpenReadsEverythingBeforeTheTear(t *testing.T) {
	p, h1, h2 := buildTornSourceRepo(t)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	full, err := archive.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	// The last record (h2's commit) is where the tear lands: cut 10 bytes
	// off the end, into that record's hash field, so it no longer fits.
	const tearAmount = 10
	lastRecSize := 8 + len(full.Records[len(full.Records)-1].Data) + archive.HashLen
	wantOffset := len(raw) - lastRecSize
	torn := tearFile(t, p, tearAmount)

	r, err := Open(p)
	if err != nil {
		t.Fatalf("Open on a torn archive must succeed, got %v", err)
	}
	if !r.Torn() {
		t.Fatal("want Torn() true")
	}
	if r.TornOffset != wantOffset {
		t.Fatalf("TornOffset=%d, want %d (the last record's start)", r.TornOffset, wantOffset)
	}
	if r.TornOffset+r.TornBytes != len(torn) {
		t.Fatalf("offset=%d bytes=%d don't add up to the truncated file length %d", r.TornOffset, r.TornBytes, len(torn))
	}
	// The last commit (h2)'s own record was torn off; only h1 survives as a
	// readable object, but the head metadata (already saved pointing at h2)
	// still points past the tear - a broken chain check's job, not Open's.
	// Confirm at least what IS still there reads fine.
	if _, ok := r.Get(h1); !ok {
		t.Fatal("h1 should still be readable")
	}
	if c, err := r.Commit(h1); err != nil || string(c.Message) != "first" {
		t.Fatalf("Commit(h1): %v, %+v", err, c)
	}
	if _, ok := r.Get(h2); ok {
		t.Fatal("h2's record was torn off - it must not be readable")
	}
	lines, err := r.History()
	if len(lines) != 0 { // History follows main's head, h2, which is now unreadable
		t.Fatalf("History() = %+v, want none reachable through the torn head", lines)
	}
	_ = err // ErrBrokenChain or similar is expected and is not this test's concern
}

// A record whose length field is corrupted to overrun the file is also
// torn (it does not fit), exercising the other bounds-check branch.
func TestTornOpenViaOverrunLength(t *testing.T) {
	p, h1, _ := buildTornSourceRepo(t)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := archive.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Overwrite the last record's length field with a huge value so it can
	// never fit in the remaining bytes.
	lastRecStart := len(raw) - (8 + len(a.Records[len(a.Records)-1].Data) + archive.HashLen)
	corrupt := append([]byte(nil), raw...)
	for i := 0; i < 8; i++ {
		corrupt[lastRecStart+i] = 0xff
	}
	if err := os.WriteFile(p, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Open(p)
	if err != nil {
		t.Fatalf("Open must tolerate an overrun length, got %v", err)
	}
	if !r.Torn() {
		t.Fatal("want Torn() true")
	}
	if r.TornOffset != lastRecStart {
		t.Fatalf("TornOffset=%d, want %d", r.TornOffset, lastRecStart)
	}
	if _, ok := r.Get(h1); !ok {
		t.Fatal("h1 (before the corrupted record) should still be readable")
	}
}

// Save on a torn repo refuses with ErrTorn and touches nothing on disk.
func TestSaveRefusesOnATornRepo(t *testing.T) {
	p, _, _ := buildTornSourceRepo(t)
	tearFile(t, p, 40)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	beforeM, _ := os.ReadFile(p + ".m")

	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != ErrTorn {
		t.Fatalf("Save() = %v, want ErrTorn", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("Save touched the .hgs file despite refusing")
	}
	afterM, _ := os.ReadFile(p + ".m")
	if !bytes.Equal(beforeM, afterM) {
		t.Fatal("Save touched the .m file despite refusing")
	}
}

// A record that fits but has a bad hash is corrupt, not torn: it does not
// stop the walk and is unrelated to torn-tail reporting.
func TestCorruptRecordIsNotTorn(t *testing.T) {
	p, h1, h2 := buildTornSourceRepo(t)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := archive.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a bit in the first record's content: it still fits structurally.
	firstDataStart := archive.HeaderLen + 8
	corrupt := append([]byte(nil), raw...)
	corrupt[firstDataStart] ^= 0xff
	if err := os.WriteFile(p, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Torn() {
		t.Fatal("a corrupt-but-complete record must not be reported as torn")
	}
	if len(r.Arc.Records) != len(a.Records) {
		t.Fatalf("corrupt record must not stop the walk: got %d records, want %d", len(r.Arc.Records), len(a.Records))
	}
	// Both commits remain reachable through Get; only the hash check (done
	// by check.Run, exercised separately) flags the corruption.
	if _, ok := r.Get(h1); !ok {
		t.Fatal("h1 missing")
	}
	if _, ok := r.Get(h2); !ok {
		t.Fatal("h2 missing")
	}
}
