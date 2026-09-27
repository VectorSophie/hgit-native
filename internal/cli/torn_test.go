package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// buildTornCLIRepo writes a small repo with duplicate (legacy) records and
// then tears its last record off, for exercising `check`/`compact` end to
// end through the CLI.
func buildTornCLIRepo(t *testing.T, path string) {
	t.Helper()
	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	restore := repo.SetDefaultDedup(false)
	defer restore()
	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b := r.Store(archive.Blob, []byte("x"))
	r.Store(archive.Blob, []byte("x")) // legacy duplicate
	trh := r.Store(archive.Tree, (&object.Tree{Entries: []object.Entry{
		{Name: "a", ChildType: archive.Blob, ChildHash: b, EntityID: 1},
	}}).Encode())
	c := r.Store(archive.Commit, (&object.Commit{Tree: trh, Message: []byte("m")}).Encode())
	r.SetHead("main", c)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	// A second commit whose own record we tear off below.
	c2 := r.Store(archive.Commit, (&object.Commit{Tree: trh, Parents: []archive.Hash{c}, Message: []byte("m2")}).Encode())
	r.SetHead("main", c2)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw[:len(raw)-10], 0o644); err != nil {
		t.Fatal(err)
	}
}

// CHECK_WARN torn_tail must print before object_count_mismatch (Check.HC's
// own order, contract/src/hgit-cli/Check.HC lines ~151-158), and the
// existing non-torn tokens/order must be untouched.
func TestCheckReportsTornTailBeforeObjectCountMismatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "torn.hgs")
	buildTornCLIRepo(t, p)

	out := serial(t, ExitOK, "check", p)
	tornIdx := strings.Index(out, "CHECK_WARN torn_tail offset=")
	mismatchIdx := strings.Index(out, "CHECK_WARN object_count_mismatch")
	if tornIdx < 0 {
		t.Fatalf("no torn_tail warning in:\n%s", out)
	}
	if mismatchIdx < 0 {
		t.Fatalf("no object_count_mismatch warning in:\n%s", out)
	}
	if tornIdx > mismatchIdx {
		t.Fatalf("torn_tail must print before object_count_mismatch, got:\n%s", out)
	}
	if !strings.Contains(out, "CHECK_OK objects=") {
		t.Fatalf("torn repo must still report what it could read: %s", out)
	}

	hout, _, code := run("check", p)
	if code != ExitOK || !strings.Contains(hout, "torn tail at offset") {
		t.Fatalf("human check: exit %d, %q", code, hout)
	}
}

// The `compact` command rewrites the archive, clears the torn state and
// lets a subsequent write through the CLI succeed again.
func TestCompactCommandRepairsATornRepoThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "torn.hgs")
	buildTornCLIRepo(t, p)

	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	out := serial(t, ExitOK, "compact", p)
	if !strings.HasPrefix(out, "COMPACT_OK objects=") {
		t.Fatalf("compact: %q", out)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) >= len(before) {
		t.Fatalf("compact did not shrink the archive: before=%d after=%d", len(before), len(after))
	}

	checkOut := serial(t, ExitOK, "check", p)
	if strings.Contains(checkOut, "torn_tail") || strings.Contains(checkOut, "object_count_mismatch") {
		t.Fatalf("compacted repo should have neither warning: %s", checkOut)
	}

	// A write now succeeds (Save no longer refuses).
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o644)
	offerOut := serial(t, ExitOK, "offer", p, filepath.Join(dir, "*.txt"), "after compact")
	if offerOut != "DISPATCH_OK offer\n" {
		t.Fatalf("offer after compact: %q", offerOut)
	}
}

// Save refuses on a torn repo when driven through the CLI too (offer's own
// dispatch line still prints, exit code carries the failure).
func TestOfferOnATornRepoRefusesThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "torn.hgs")
	buildTornCLIRepo(t, p)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o644)

	out, _, code := run("--serial", "offer", p, filepath.Join(dir, "*.txt"), "should not land")
	if code != ExitFail {
		t.Fatalf("offer on a torn repo: exit %d, %q", code, out)
	}
	if !strings.Contains(out, "OFFER_ERR") {
		t.Fatalf("want an OFFER_ERR token, got %q", out)
	}
}

// `compact` on an already-minimal archive refuses and touches nothing.
func TestCompactRefusesOnAnAlreadyMinimalArchive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.hgs")
	serial(t, ExitOK, "init", p)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o644)
	serial(t, ExitOK, "offer", p, filepath.Join(dir, "*.txt"), "one")

	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	out := serial(t, ExitOK, "compact", p)
	if !strings.Contains(out, "COMPACT_NOTHING_TO_COMPACT") {
		t.Fatalf("compact: %q", out)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a refused compact must not touch the file")
	}
}
