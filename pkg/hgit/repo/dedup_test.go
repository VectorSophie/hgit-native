package repo

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

// ADR 0019 section 1: Store skips an object whose hash is already present,
// counting records appended earlier by the same command.
func TestStoreSkipsAnObjectAlreadyStored(t *testing.T) {
	r := emptyRepo(t)
	r.Dedup = true
	a := r.Store(archive.Blob, []byte("a"))
	if r.Store(archive.Blob, []byte("a")) != a {
		t.Fatal("hash changed")
	}
	b := r.Store(archive.Blob, []byte("b"))
	r.Store(archive.Blob, []byte("a"))
	if len(r.Arc.Records) != 2 || r.Arc.Records[0].Hash != a || r.Arc.Records[1].Hash != b {
		t.Fatalf("%d records, want [a b]", len(r.Arc.Records))
	}
	if r.Arc.Header.Count != 2 {
		t.Fatalf("header count %d, want 2", r.Arc.Header.Count)
	}
}

// With Dedup off, Store is exactly Append: the 1.8.9 ObjectPut.
func TestStoreLegacyAppendsEveryTime(t *testing.T) {
	r := emptyRepo(t)
	r.Dedup = false
	h := r.Store(archive.Blob, []byte("a"))
	if r.Store(archive.Blob, []byte("a")) != h || len(r.Arc.Records) != 2 || r.Arc.Header.Count != 2 {
		t.Fatalf("%d records, want 2", len(r.Arc.Records))
	}
	if i := r.idx[h]; i != 0 {
		t.Fatalf("index points at %d, want the first occurrence", i)
	}
}

// Open copies DefaultDedup, so a test can pin legacy mode for every repo it
// opens and restore the default afterwards.
func TestOpenCopiesDefaultDedup(t *testing.T) {
	if !DefaultDedup {
		t.Fatal("DefaultDedup must be on by default (ADR 0019)")
	}
	p := filepath.Join(t.TempDir(), "r.hgs")
	if err := Init(p); err != nil {
		t.Fatal(err)
	}
	for _, want := range []bool{true, false} {
		restore := SetDefaultDedup(want)
		r, err := Open(p)
		restore()
		if err != nil {
			t.Fatal(err)
		}
		if r.Dedup != want {
			t.Fatalf("Dedup %v, want %v", r.Dedup, want)
		}
	}
	if !DefaultDedup {
		t.Fatal("restore did not put the default back")
	}
}

// A legacy archive keeps its duplicates and its count; new writes dedupe
// against them, and Save's header count is the record count.
func TestLegacyDuplicatesKeptAndNewWritesDedupeAgainstThem(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.hgs")
	if err := Init(p); err != nil {
		t.Fatal(err)
	}
	restore := SetDefaultDedup(false)
	r, _ := Open(p)
	restore()
	x := r.Store(archive.Blob, []byte("x"))
	r.Store(archive.Blob, []byte("x"))
	r.Store(archive.Blob, []byte("y"))
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Dedup || len(r.Arc.Records) != 3 {
		t.Fatalf("dedup %v, %d records", r.Dedup, len(r.Arc.Records))
	}
	if r.Store(archive.Blob, []byte("x")) != x {
		t.Fatal("hash changed")
	}
	r.Store(archive.Blob, []byte("y"))
	r.Store(archive.Blob, []byte("z"))
	r.Store(archive.Blob, []byte("z"))
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r, _ = Open(p)
	if len(r.Arc.Records) != 4 || r.Arc.Header.Count != 4 {
		t.Fatalf("%d records, header %d; want 4 and 4", len(r.Arc.Records), r.Arc.Header.Count)
	}
	raw, _ := os.ReadFile(p)
	a, _ := archive.Parse(raw)
	if a.Header.Count != uint64(len(a.Records)) {
		t.Fatalf("on disk: header %d, records %d", a.Header.Count, len(a.Records))
	}
	if r.Arc.Records[0].Hash != r.Arc.Records[1].Hash {
		t.Fatal("the legacy duplicate was removed")
	}
}

// Get shares the stored bytes (no copy); growing a result - the only kind of
// change a reader could plausibly make - reallocates and leaves every record,
// and what Save writes, unchanged.
func TestGrowingAGetResultCannotCorruptTheRepo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.hgs")
	if err := Init(p); err != nil {
		t.Fatal(err)
	}
	r, _ := Open(p)
	hs := []archive.Hash{r.Store(archive.Blob, []byte("one")), r.Store(archive.Blob, []byte("two")), r.Store(archive.Tree, nil)}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(p)
	for _, fresh := range []bool{false, true} { // appended in memory, then parsed from disk
		if fresh {
			r, _ = Open(p)
		}
		for _, h := range hs {
			rec, _ := r.Get(h)
			_ = append(rec.Data, 0xee, 0xee)
			_ = append(rec.Content(), 0xee, 0xee)
		}
		for _, h := range hs {
			if rec, ok := r.Get(h); !ok || !rec.HashOK() {
				t.Fatalf("record %x changed", h[:4])
			}
		}
		if !bytes.Equal(r.Arc.Marshal(), orig) {
			t.Fatal("the archive bytes changed")
		}
	}
}
