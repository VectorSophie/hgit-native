package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/cli"
	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
)

// TestScenarioDiffReplaysRegression replays the regression up to its own
// TFULL_DIFF segment (contract/tests/full-regression.hc lines 41-96): the
// first offer, the modify/delete/exact-rename/fuzzy-rename/new edits, the
// second offer, then `diff` against the second commit - i.e. the second
// commit's tree against the first commit's own tree.
func TestScenarioDiffReplaysRegression(t *testing.T) {
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
	for _, name := range []string{"TFToDelete.txt", "TFOrig.txt"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	write("TFRenamed.txt", renamedContent)
	write("TFGenuinelyNew.txt", "brand new content, unrelated")

	r2, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	head2, err := offer.Offer(r2, dir, "TF*.txt", offer.Options{Message: "second_offer"})
	if err != nil {
		t.Fatal(err)
	}

	r3, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := status.Diff(r3, head2)
	got := cli.SerialDiff(changes, err)
	want := segment(t, testfix.ExpectedLog(t), "TFULL_DIFF_BEGIN", "TFULL_DIFF_END_MARKER")
	if normalize(got) != normalize(want+"\n") {
		t.Fatalf("diff output:\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestScenarioAttrsDiffReplaysRegression replays the regression's
// TFULL_ATTRS_DIFF segment (lines 248-268): a root commit, then a mode-only
// second commit via .hgitattributes, diffed against its parent - no content
// changed, so DIFF_MODE_CHANGED is the only line.
func TestScenarioAttrsDiffReplaysRegression(t *testing.T) {
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
	head, err := offer.Offer(r2, dir, "TFAttrsScript.txt", offer.Options{Message: "attrs_mode_offer"})
	if err != nil {
		t.Fatal(err)
	}

	r3, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := status.Diff(r3, head)
	got := cli.SerialDiff(changes, err)
	want := segment(t, testfix.ExpectedLog(t), "TFULL_ATTRS_DIFF_BEGIN", "TFULL_ATTRS_DIFF_END_MARKER")
	if normalize(got) != normalize(want+"\n") {
		t.Fatalf("attrs diff output:\n got:\n%s\nwant:\n%s", got, want)
	}
}
