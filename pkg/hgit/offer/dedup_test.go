package offer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

func records(t *testing.T, path string) *archive.Archive {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := archive.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Header.Count != uint64(len(a.Records)) {
		t.Fatalf("header count %d, %d records", a.Header.Count, len(a.Records))
	}
	return a
}

// ADR 0019: offering an unchanged working directory stores only the new
// commit - every blob and the root tree are already there.
func TestOfferUnchangedContentAddsOnlyTheCommit(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "alpha")
	f.write("b.txt", "beta")
	f.offerMsg("*.txt", "one")
	before := len(records(t, f.path).Records)
	f.offerMsg("*.txt", "two")
	a := records(t, f.path)
	if got := len(a.Records) - before; got != 1 || a.Records[len(a.Records)-1].Type() != archive.Commit {
		t.Fatalf("second offer added %d records, want the commit only", got)
	}
}

// Two files with the same content in one offer share one blob record.
func TestIdenticalFilesInOneOfferAreStoredOnce(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "same")
	f.write("b.txt", "same")
	f.offerMsg("*.txt", "one")
	a := records(t, f.path)
	var types []archive.Type
	for _, r := range a.Records {
		types = append(types, r.Type())
	}
	if len(types) != 3 || types[0] != archive.Blob || types[1] != archive.Tree || types[2] != archive.Commit {
		t.Fatalf("records %v, want [blob tree commit]", types)
	}
}

// history drives offer and offertree through edits, a rename, an unchanged
// re-offer and a subdirectory; the returned archive bytes are the result.
func history(t *testing.T, legacy bool) []byte {
	t.Helper()
	if legacy {
		defer repo.SetDefaultDedup(false)()
	}
	f := setup(t)
	seedIDs(t)
	f.write("a.txt", "alpha")
	f.write("b.txt", "alpha")
	f.offerMsg("*.txt", "one")
	f.write("b.txt", "beta")
	f.offerMsg("*.txt", "two")
	f.offerMsg("*.txt", "three")
	// offertree's root must not contain the archive itself
	os.MkdirAll(filepath.Join(f.dir, "sub", "deep"), 0o755)
	f.write("sub/a.txt", "alpha")
	f.write("sub/deep/c.txt", "alpha")
	for _, msg := range []string{"four", "five"} {
		r, err := repo.Open(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := offer.OfferTree(r, filepath.Join(f.dir, "sub"), offer.Options{Message: msg}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(f.path)
	return b
}

// Same inputs, same bytes; and the dedup archive is exactly the legacy one
// with every repeated record dropped - skipping never reorders.
func TestDedupOutputIsLegacyMinusRepeatsAndDeterministic(t *testing.T) {
	d1, d2 := history(t, false), history(t, false)
	if !bytes.Equal(d1, d2) {
		t.Fatal("two runs over the same inputs wrote different archives")
	}
	legacy, err := archive.Parse(history(t, true))
	if err != nil {
		t.Fatal(err)
	}
	want := &archive.Archive{Header: legacy.Header}
	seen := map[archive.Hash]bool{}
	for _, r := range legacy.Records {
		if !seen[r.Hash] {
			seen[r.Hash] = true
			want.Records = append(want.Records, r)
		}
	}
	if len(want.Records) == len(legacy.Records) {
		t.Fatal("scenario has no duplicates to skip")
	}
	want.Header.Count = uint64(len(want.Records))
	if !bytes.Equal(d1, want.Marshal()) {
		t.Fatal("dedup archive is not the legacy archive minus its repeats")
	}
}

// A failed offer never touches the disk. In memory it may hold the objects
// stored so far (Offer's documented contract), but never a repeat.
func TestFailedOfferLeavesDiskAloneAndNoRepeatsInMemory(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "alpha")
	f.offerMsg("*.txt", "one")
	onDisk, _ := os.ReadFile(f.path)
	f.write("b.txt", "beta")
	f.write("c.txt", "alpha")
	seedIDs(t, 0) // the first fresh id fails
	r, _ := repo.Open(f.path)
	before := len(r.Arc.Records)
	if _, err := offer.Offer(r, f.dir, "*.txt", offer.Options{Message: "x"}); err == nil {
		t.Fatal("want ErrEntityID")
	}
	if after, _ := os.ReadFile(f.path); !bytes.Equal(after, onDisk) {
		t.Fatal("a failed offer changed the archive on disk")
	}
	// a.txt's blob exists; only b.txt's new blob may have been added.
	if got := len(r.Arc.Records) - before; got != 1 || r.Arc.Records[before].Type() != archive.Blob {
		t.Fatalf("%d stray records in memory, want 1 (b.txt's blob)", got)
	}
}
