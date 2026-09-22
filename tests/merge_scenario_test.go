package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/cli"
	"github.com/VectorSophie/hgit-native/internal/testfix"

	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// mergeFx replays a regression merge scenario natively: a working directory,
// a repository beside it, and the handful of commands those segments use.
type mergeFx struct {
	t    *testing.T
	dir  string
	path string
	mask string
}

func newMergeFx(t *testing.T, name, mask string) *mergeFx {
	t.Helper()
	oldClock, oldID := clock.Now, offer.NewEntityID
	t.Cleanup(func() { clock.Now, offer.NewEntityID = oldClock, oldID })
	ts := uint64(600000)
	clock.Now = func() uint64 { ts++; return ts }
	next := uint64(0)
	offer.NewEntityID = func() uint64 { next++; return next }

	f := &mergeFx{t: t, dir: t.TempDir(), mask: mask}
	f.path = filepath.Join(f.dir, name)
	if err := repo.Init(f.path); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *mergeFx) open() *repo.Repo {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *mergeFx) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *mergeFx) remove(name string) {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.dir, name)); err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
}

func (f *mergeFx) offer(msg string) {
	f.t.Helper()
	if _, err := offer.Offer(f.open(), f.dir, f.mask, offer.Options{Message: msg}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *mergeFx) pathNew(name string) {
	f.t.Helper()
	if err := f.open().PathNew(name); err != nil {
		f.t.Fatal(err)
	}
}

func (f *mergeFx) pathGo(name string) {
	f.t.Helper()
	if err := f.open().PathGo(name); err != nil {
		f.t.Fatal(err)
	}
}

func (f *mergeFx) merge(other string) string {
	f.t.Helper()
	res, err := merge.Merge(f.open(), other)
	return cli.SerialMerge(other, res, err)
}

func (f *mergeFx) check() string {
	f.t.Helper()
	return cli.SerialCheck(check.Run(f.open()), nil)
}

// wantSegment compares got against one segment of the golden log.
func wantSegment(t *testing.T, got, begin, end string) {
	t.Helper()
	want := segment(t, testfix.ExpectedLog(t), begin, end)
	if normalize(trim(got)) != normalize(want) {
		t.Fatalf("%s mismatch:\ngot:\n%s\nwant:\n%s", begin, trim(got), want)
	}
}

func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// TestScenarioMergeReplaysRegression replays the regression's own merge
// repository: a real non-conflicting three-way merge, then a fast-forward.
func TestScenarioMergeReplaysRegression(t *testing.T) {
	f := newMergeFx(t, "TFullMergeRepo.hgs", "TFMergeFile*.txt")
	// The regression's own FileWrite lengths are one past each literal, so
	// every one of these files ends in the terminating NUL - which makes them
	// binary by detection, and is why each offer writes an OBJ_ATTRS object.
	f.write("TFMergeFileA.txt", "merge fileA root\x00")
	f.write("TFMergeFileB.txt", "merge fileB root\x00")
	f.offer("merge_root_offer")

	f.pathNew("merge_feature")
	f.pathGo("merge_feature")
	f.write("TFMergeFileB.txt", "merge fileB EDITED by feature\x00")
	f.offer("merge_feature_edit_b")

	f.pathGo("main")
	f.write("TFMergeFileB.txt", "merge fileB root\x00")
	f.write("TFMergeFileA.txt", "merge fileA EDITED by main\x00")
	f.offer("merge_main_edit_a")

	wantSegment(t, f.merge("merge_feature"), "TFULL_MERGE_BEGIN", "TFULL_MERGE_END_MARKER")
	wantSegment(t, f.check(), "TFULL_CHECK_MERGED_BEGIN", "TFULL_CHECK_MERGED_END_MARKER")

	f.pathNew("merge_ff_target")
	f.write("TFMergeFileA.txt", "merge fileA further advanced\x00")
	f.offer("merge_advance_main_only")
	f.pathGo("merge_ff_target")
	wantSegment(t, f.merge("main"), "TFULL_MERGE_FF_BEGIN", "TFULL_MERGE_FF_END_MARKER")
}

// TestScenarioMergeModeReplaysRegression replays the mode-merge repository: a
// clean mode-only change on one side and a content edit on the other merge
// without a conflict, and the merged mode lands in the merge commit's attrs.
func TestScenarioMergeModeReplaysRegression(t *testing.T) {
	f := newMergeFx(t, "TFMergeModeRepo.hgs", "TFMM_*.txt")
	f.write("TFMM_a.txt", "shared")
	f.write("TFMM_b.txt", "other root")
	f.offer("mm_root")

	f.pathNew("mm_feature")
	f.pathGo("mm_feature")
	f.write(".hgitattributes", "TFMM_a.txt executable\n")
	f.offer("mm_mode_change")

	f.pathGo("main")
	f.remove(".hgitattributes")
	f.write("TFMM_b.txt", "other EDITED")
	f.offer("mm_other_edit")

	wantSegment(t, f.merge("mm_feature"), "TFULL_MERGEMODE_MERGE_BEGIN", "TFULL_MERGEMODE_MERGE_END_MARKER")
	wantSegment(t, f.check(), "TFULL_MERGEMODE_CHECK_BEGIN", "TFULL_MERGEMODE_CHECK_END_MARKER")
}
