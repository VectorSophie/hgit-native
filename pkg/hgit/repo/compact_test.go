package repo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
)

// legacyRepoWithDuplicates builds an archive the way a pre-1.9 (non-dedup)
// writer would: the same blob offered/appended twice produces two identical
// records, exactly what compact must collapse to one.
func legacyRepoWithDuplicates(t *testing.T) (path string, dup archive.Hash) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "legacy.hgs")
	if err := Init(p); err != nil {
		t.Fatal(err)
	}
	restore := SetDefaultDedup(false)
	defer restore()
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	dup = r.Store(archive.Blob, []byte("dup"))
	r.Store(archive.Blob, []byte("dup")) // legacy Append: a second, identical record
	unique := r.Store(archive.Blob, []byte("unique"))
	trh := r.Store(archive.Tree, (&object.Tree{Entries: []object.Entry{
		{Name: "a", ChildType: archive.Blob, ChildHash: dup, EntityID: 1},
		{Name: "b", ChildType: archive.Blob, ChildHash: unique, EntityID: 2},
	}}).Encode())
	c1 := r.Store(archive.Commit, (&object.Commit{Tree: trh, Message: []byte("c1")}).Encode())
	r.SetHead("main", c1)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	// An undone commit: dangling, but must survive compact (not a GC).
	c2 := r.Store(archive.Commit, (&object.Commit{Tree: trh, Parents: []archive.Hash{c1}, Message: []byte("c2 to be undone")}).Encode())
	r.SetHead("main", c2)
	r.AppendOp("main", meta.OpLogEntry{Prev: c1, New: c2})
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if err := r.Undo(); err != nil {
		t.Fatal(err)
	}
	return p, dup
}

func TestCompactDedupesAndKeepsDanglingObjects(t *testing.T) {
	p, dup := legacyRepoWithDuplicates(t)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	beforeRecords := len(r.Arc.Records)
	// The undone commit's own hash, still present as a dangling record.
	danglingHashes := make(map[archive.Hash]bool, len(r.Arc.Records))
	for _, rec := range r.Arc.Records {
		danglingHashes[rec.Hash] = true
	}

	if err := r.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if r.Torn() {
		t.Fatal("Compact must clear torn state")
	}
	after := len(r.Arc.Records)
	if after >= beforeRecords {
		t.Fatalf("compact did not remove the duplicate: before=%d after=%d", beforeRecords, after)
	}
	seen := map[archive.Hash]bool{}
	for _, rec := range r.Arc.Records {
		if seen[rec.Hash] {
			t.Fatalf("compact left a duplicate record %x", rec.Hash[:4])
		}
		seen[rec.Hash] = true
	}
	if r.Arc.Header.Count != uint64(after) {
		t.Fatalf("header count %d != record count %d", r.Arc.Header.Count, after)
	}
	if !seen[dup] {
		t.Fatal("the deduplicated object itself must still be present once")
	}
	for h := range danglingHashes {
		if !seen[h] {
			t.Fatalf("compact dropped object %x - it is not a GC", h[:4])
		}
	}

	// The original file was replaced only with a verified, working archive:
	// re-open it from disk and confirm it works and is different from the
	// pre-compact bytes (something was actually removed on disk, not just in
	// memory).
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= len(before) {
		t.Fatalf("on-disk archive did not shrink: before=%d after=%d", len(before), len(raw))
	}
	r2, err := Open(p)
	if err != nil {
		t.Fatalf("compacted archive did not reopen: %v", err)
	}
	if len(r2.Arc.Records) != after {
		t.Fatalf("reopened record count %d != %d", len(r2.Arc.Records), after)
	}
	if total, ok := r2.Arc.Verify(); ok != total {
		t.Fatalf("reopened archive has %d corrupt record(s)", total-ok)
	}

	// Save works again after compact.
	if err := r2.Save(); err != nil {
		t.Fatalf("Save after compact: %v", err)
	}
}

func TestCompactOnATornRepoClearsTornState(t *testing.T) {
	p, _, _ := buildTornSourceRepo(t)
	tearFile(t, p, 10)

	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Torn() {
		t.Fatal("setup: want a torn repo")
	}
	beforeRecords := len(r.Arc.Records)
	if err := r.Compact(); err != nil {
		t.Fatalf("Compact on a torn repo: %v", err)
	}
	if r.Torn() {
		t.Fatal("Compact must clear torn state")
	}
	if len(r.Arc.Records) != beforeRecords {
		t.Fatalf("compact changed the readable record count: before=%d after=%d", beforeRecords, len(r.Arc.Records))
	}
	if err := r.Save(); err != nil {
		t.Fatalf("Save after compacting a torn repo: %v", err)
	}
	r2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Torn() {
		t.Fatal("reopened compacted archive must not be torn")
	}
}

// Compact refuses when there is nothing to do: no torn tail, no duplicates.
func TestCompactRefusesWhenAlreadyMinimal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.hgs")
	if err := Init(p); err != nil {
		t.Fatal(err)
	}
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	r.Store(archive.Blob, []byte("one"))
	r.Store(archive.Blob, []byte("two"))
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Compact(); err != ErrNothingToCompact {
		t.Fatalf("Compact() = %v, want ErrNothingToCompact", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a refused compact must not touch the file")
	}
}
