package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/cli"
	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// TestScenarioOpsReplaysRegression replays contract/tests/full-regression.hc
// from `init` through `undo`/`redo`/`operation history`, the named-path
// segment, the `correct` offering and finally `export`/`import`, comparing
// every segment the regression prints along the way.
func TestScenarioOpsReplaysRegression(t *testing.T) {
	oldClock := clock.Now
	t.Cleanup(func() { clock.Now = oldClock })
	stamps := []uint64{345402, 394453, 451100, 512602}
	clock.Now = func() uint64 {
		ts := stamps[0]
		if len(stamps) > 1 {
			stamps = stamps[1:]
		}
		return ts
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "TFullRepo.hgs")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	open := func() *repo.Repo {
		t.Helper()
		r, err := repo.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	want := func(begin, end string) string {
		t.Helper()
		return normalize(segment(t, testfix.ExpectedLog(t), begin, end)) + "\n"
	}

	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	write("TFStays.txt", "never touched")
	write("TFToModify.txt", "version one")
	write("TFToDelete.txt", "going away")
	write("TFOrig.txt", origContent)
	if _, err := offer.Offer(open(), dir, "TF*.txt", offer.Options{Message: "first_offer"}); err != nil {
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
	head2, err := offer.Offer(open(), dir, "TF*.txt", offer.Options{Message: "second_offer"})
	if err != nil {
		t.Fatal(err)
	}

	// --- undo: the second commit's objects become dangling ---
	r := open()
	if err := r.Undo(); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if got := normalize(cli.SerialCheck(check.Run(open()), nil)); got != want("TFULL_CHECK_AFTER_UNDO_BEGIN", "TFULL_CHECK_AFTER_UNDO_END_MARKER") {
		t.Fatalf("check after undo:\n got:\n%s\nwant:\n%s", got, want("TFULL_CHECK_AFTER_UNDO_BEGIN", "TFULL_CHECK_AFTER_UNDO_END_MARKER"))
	}

	// --- redo, then operation history ---
	r = open()
	if err := r.Redo(); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r = open()
	if h, ok := r.Head("main"); !ok || h != head2 {
		t.Fatalf("head after redo = %x, want %x", h, head2)
	}
	if got := normalize(cli.SerialOperationHistory(r.OperationHistory())); got != want("TFULL_OPHISTORY_BEGIN", "TFULL_OPHISTORY_END_MARKER") {
		t.Fatalf("operation history:\n got:\n%s\nwant:\n%s", got, want("TFULL_OPHISTORY_BEGIN", "TFULL_OPHISTORY_END_MARKER"))
	}

	// --- named paths ---
	r = open()
	if err := r.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.PathGo("feature"); err != nil {
		t.Fatal(err)
	}
	write("TFFeatureFile.txt", "feature branch content\x00")
	if _, err := offer.Offer(open(), dir, "TF*.txt", offer.Options{Message: "feature_offer"}); err != nil {
		t.Fatal(err)
	}
	if got := cli.SerialPathList(open().PathList()); got != want("TFULL_PATHLIST_BEGIN", "TFULL_PATHLIST_END_MARKER") {
		t.Fatalf("path list:\n got:\n%s\nwant:\n%s", got, want("TFULL_PATHLIST_BEGIN", "TFULL_PATHLIST_END_MARKER"))
	}
	r = open()
	if err := r.PathGo("main"); err != nil {
		t.Fatal(err)
	}

	// --- correct (a typed relation on main), then export / import ---
	if _, err := offer.Offer(open(), dir, "TFToModify.txt", offer.Options{
		Message:        "correcting_offer",
		Relation:       object.RelCorrects,
		RelationTarget: head2,
	}); err != nil {
		t.Fatal(err)
	}

	exported := filepath.Join(dir, "TFullExported.hgs")
	if err := repo.Export(path, exported); err != nil {
		t.Fatal(err)
	}
	er, err := repo.Open(exported)
	if err != nil {
		t.Fatal(err)
	}
	if got := normalize(cli.SerialCheck(check.Run(er), nil)); got != want("TFULL_CHECK_EXPORTED_BEGIN", "TFULL_CHECK_EXPORTED_END_MARKER") {
		t.Fatalf("check exported:\n got:\n%s\nwant:\n%s", got, want("TFULL_CHECK_EXPORTED_BEGIN", "TFULL_CHECK_EXPORTED_END_MARKER"))
	}

	imported := filepath.Join(dir, "TFullImported.hgs")
	if err := repo.Import(exported, imported); err != nil {
		t.Fatal(err)
	}
	ir, err := repo.Open(imported)
	if err != nil {
		t.Fatal(err)
	}
	if got := normalize(cli.SerialCheck(check.Run(ir), nil)); got != want("TFULL_CHECK_IMPORTED_BEGIN", "TFULL_CHECK_IMPORTED_END_MARKER") {
		t.Fatalf("check imported:\n got:\n%s\nwant:\n%s", got, want("TFULL_CHECK_IMPORTED_BEGIN", "TFULL_CHECK_IMPORTED_END_MARKER"))
	}
	if got := cli.SerialPathList(ir.PathList()); got != "PATH main\nPATH feature\nDISPATCH_OK path_list\n" {
		t.Fatalf("imported repo lost its paths:\n%s", got)
	}
}
