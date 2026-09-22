package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/cli"
	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
)

// TestScenarioStatusReplaysRegression replays the regression exactly up to
// its own TFULL_STATUS segment (contract/tests/full-regression.hc lines
// 41-70): init, the first offer, the working-directory edits that precede
// `status`, then `status` itself - before the second offer ever runs, so
// this is status seeing genuinely uncommitted changes, not a replay of
// already-committed history.
func TestScenarioStatusReplaysRegression(t *testing.T) {
	oldClock := clock.Now
	t.Cleanup(func() { clock.Now = oldClock })
	clock.Now = func() uint64 { return 345402 }

	dir := t.TempDir()
	path := filepath.Join(dir, "TFullRepo.hgs")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	write("TFStays.txt", "never touched")
	write("TFToModify.txt", "version one")
	write("TFToDelete.txt", "going away")
	write("TFOrig.txt", origContent)

	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := offer.Offer(r, dir, "TF*.txt", offer.Options{Message: "first_offer"}); err != nil {
		t.Fatal(err)
	}

	write("TFToModify.txt", "version two")
	if err := os.Remove(filepath.Join(dir, "TFToDelete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "TFOrig.txt")); err != nil {
		t.Fatal(err)
	}
	write("TFRenamed.txt", renamedContent)
	write("TFGenuinelyNew.txt", "brand new content, unrelated")

	r2, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := status.Status(r2, dir, "TF*.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := cli.SerialStatus(changes, err)
	want := segment(t, testfix.ExpectedLog(t), "TFULL_STATUS_BEGIN", "TFULL_STATUS_END_MARKER")
	if normalize(got) != normalize(want+"\n") {
		t.Fatalf("status output:\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestScenarioStatusTreeReplaysRegression replays the regression's
// statustree segment (lines 161-173): offertree the tree fixture's first
// commit, edit SubA/inner.txt, then `statustree` before the second offertree
// commits anything.
func TestScenarioStatusTreeReplaysRegression(t *testing.T) {
	oldClock, oldID := clock.Now, offer.NewEntityID
	t.Cleanup(func() { clock.Now, offer.NewEntityID = oldClock, oldID })
	clock.Now = func() uint64 { return treeTS1 }
	ids := []uint64{fixInnerID, fixSubAID, fixTopID}
	n := 0
	offer.NewEntityID = func() uint64 {
		n++
		if n <= len(ids) {
			return ids[n-1]
		}
		return uint64(9000 + n)
	}

	base := t.TempDir()
	path := filepath.Join(base, "TFullTreeRepo.hgs")
	root := filepath.Join(base, "TFTreeRoot")
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "SubA"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("top.txt", "tree top v1")
	write("SubA/inner.txt", "tree inner v1")

	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := offer.OfferTree(r, root, offer.Options{Message: "tree_first_offer"}); err != nil {
		t.Fatal(err)
	}

	write("SubA/inner.txt", "tree inner v2 CHANGED")

	r2, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := status.StatusTree(r2, root)
	if err != nil {
		t.Fatal(err)
	}
	got := cli.SerialStatusTree(changes, err)
	// The segment also carries a trailing "DISPATCH_OK statustree" - that is
	// the CLI dispatcher's own line (a later task), not part of what
	// HgitStatusTree itself printed, so it is stripped before comparing.
	want := segment(t, testfix.ExpectedLog(t), "TFULL_STATUSTREE_BEGIN", "TFULL_STATUSTREE_END_MARKER")
	want = strings.TrimSuffix(want, "\nDISPATCH_OK statustree")
	if normalize(got) != normalize(want+"\n") {
		t.Fatalf("statustree output:\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestScenarioAttrsStatusReplaysRegression replays the regression's
// TFULL_ATTRS_STATUS segment (lines 246-256): a flat offer, then a
// mode-only change via .hgitattributes, with no content edit at all -
// STATUS_UNCHANGED and STATUS_MODE_CHANGED both fire for the same file.
func TestScenarioAttrsStatusReplaysRegression(t *testing.T) {
	oldClock := clock.Now
	t.Cleanup(func() { clock.Now = oldClock })
	clock.Now = func() uint64 { return 600000 }

	dir := t.TempDir()
	path := filepath.Join(dir, "TFAttrsRepo.hgs")
	if err := os.WriteFile(filepath.Join(dir, "TFAttrsScript.txt"), []byte("echo hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := offer.Offer(r, dir, "TFAttrsScript.txt", offer.Options{Message: "attrs_root_commit"}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".hgitattributes"), []byte("TFAttrsScript.txt executable\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r2, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := status.Status(r2, dir, "TFAttrsScript.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := cli.SerialStatus(changes, err)
	want := segment(t, testfix.ExpectedLog(t), "TFULL_ATTRS_STATUS_BEGIN", "TFULL_ATTRS_STATUS_END_MARKER")
	if normalize(got) != normalize(want+"\n") {
		t.Fatalf("status output:\n got:\n%s\nwant:\n%s", got, want)
	}
}
