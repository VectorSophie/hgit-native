package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/cli"
	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
)

func (f *mergeFx) conflicts() string {
	f.t.Helper()
	return cli.SerialConflicts(merge.Conflicts(f.open()))
}

func (f *mergeFx) resolve(index int, which string) string {
	f.t.Helper()
	path, err := merge.Resolve(f.open(), index, which)
	return cli.SerialResolve(index, which, path, err)
}

func (f *mergeFx) mergeContinue() string {
	f.t.Helper()
	return cli.SerialMergeContinue(merge.Continue(f.open()))
}

func (f *mergeFx) mergeAbort() string {
	f.t.Helper()
	return cli.SerialMergeAbort(merge.Abort(f.open()))
}

// status is `hgit status`: the ADR 0016 merge banner first, then the
// ordinary working-directory report.
func (f *mergeFx) status() string {
	f.t.Helper()
	banner := cli.SerialMergeBanner(merge.Conflicts(f.open()))
	changes, err := status.Status(f.open(), f.dir, f.mask)
	return banner + cli.SerialStatus(changes, err)
}

// TestScenarioConflictAbortReplaysRegression replays the regression's own
// TFULL_CONFLICT_ABORT repository (contract/tests/full-regression.hc lines
// 335-354): one conflicting file, the in-progress-merge banner `status`
// prints, then `merge abort` and an empty `conflicts`.
//
// The segment's leading DISPATCH_OK lines belong to `init`/`offer`/`path`,
// whose serial output earlier tasks already cover, and its CONFLICTDOC_OK
// line belongs to `conflictdoc` (v1.8.6), which is not ported - both are
// dropped from the expectation here, and nothing else is.
func TestScenarioConflictAbortReplaysRegression(t *testing.T) {
	f := newConflictRepoB(t)

	var got strings.Builder
	got.WriteString(f.merge("cf"))
	got.WriteString(f.status())
	got.WriteString(f.mergeAbort())
	got.WriteString(f.conflicts())

	want := segment(t, testfix.ExpectedLog(t), "TFULL_CONFLICT_ABORT_BEGIN", "TFULL_CONFLICT_ABORT_END_MARKER")
	want = dropLines(want, "DISPATCH_OK ", "CONFLICTDOC_OK ")
	if normalize(trim(got.String())) != normalize(want) {
		t.Fatalf("TFULL_CONFLICT_ABORT mismatch:\ngot:\n%s\nwant:\n%s", trim(got.String()), want)
	}
}

// TestScenarioConflictMergeReplaysRegression replays TFULL_CONFLICT_MERGE
// (contract/tests/full-regression.hc lines 309-333): a conflicting c.txt plus
// r.txt renamed to r2.txt on main and edited on cf (ADR 0017's rename-aware
// merge), the conflict listing with its base/ours/theirs evidence, a refused
// `merge continue`, `resolve`, a second `merge continue` that succeeds,
// `check`, and a `merge abort` with nothing left to abort. The whole segment
// is compared, object count included.
func TestScenarioConflictMergeReplaysRegression(t *testing.T) {
	f := newConflictRepo(t)

	var got strings.Builder
	got.WriteString(f.merge("cf"))
	got.WriteString(f.conflicts())
	got.WriteString(f.mergeContinue())
	got.WriteString(f.resolve(0, "take-theirs"))
	got.WriteString(f.mergeContinue())
	got.WriteString(f.check())
	got.WriteString(f.mergeAbort())

	want := segment(t, testfix.ExpectedLog(t), "TFULL_CONFLICT_MERGE_BEGIN", "TFULL_CONFLICT_MERGE_END_MARKER")
	gotText := trim(got.String())
	if normalize(gotText) != normalize(want) {
		t.Fatalf("TFULL_CONFLICT_MERGE mismatch:\ngot:\n%s\nwant:\n%s", gotText, want)
	}

	// normalize() masks every 16-hex token, so the comparison above says
	// nothing about the conflict evidence hashes themselves. Compare those
	// UNMASKED, against the golden log's own bytes: these are the blob hashes
	// TempleOS wrote for base_c/main_c/feat_c, so a wrong hash here means the
	// blob hashing or the OBJ_CONFLICT side encoding disagrees with it.
	gotSides, wantSides := evidenceHashes(gotText), evidenceHashes(want)
	if len(wantSides) != 3 {
		t.Fatalf("expected 3 evidence lines in the fixture, got %v", wantSides)
	}
	if strings.Join(gotSides, " ") != strings.Join(wantSides, " ") {
		t.Fatalf("conflict evidence hashes differ:\ngot:  %v\nwant: %v", gotSides, wantSides)
	}
}

// reEvidence matches one `  base|ours|theirs type=N mode=N hash=XXXXXXXXXXXXXXXX`
// line of a conflict listing.
var reEvidence = regexp.MustCompile(`(?m)^  (base|ours|theirs) type=\d+ mode=\d+ hash=([0-9a-f]{16})$`)

// evidenceHashes returns "<side>=<hash>" for every evidence line in s, in
// order, with nothing masked.
func evidenceHashes(s string) []string {
	var out []string
	for _, m := range reEvidence.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1]+"="+m[2])
	}
	return out
}

// TestScenarioHardenReplaysRegression replays TFULL_HARDEN (lines 356-368) on
// the very repository the abort scenario leaves behind: a hand-written
// conflict record pointing at an object that does not exist, `check` on it,
// `merge abort`, and `check` on an archive from a newer format version.
func TestScenarioHardenReplaysRegression(t *testing.T) {
	f := newConflictRepoB(t)
	f.merge("cf")
	f.mergeAbort()

	r := f.open()
	head, _ := r.Head("main")
	var fake archive.Hash
	for i := range fake {
		fake[i] = 7
	}
	r.Meta.Set("main", meta.TagMergeState, meta.MergeState{Ours: head, Theirs: head, OtherPath: "cf"}.Encode())
	r.Meta.Append("main", meta.TagConflict, meta.ConflictRecord{Conflict: fake}.Encode())
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	var got strings.Builder
	got.WriteString(f.check())
	got.WriteString(f.mergeAbort())

	// An archive whose header says format version 9: this build reads up to 4.
	newer := filepath.Join(f.dir, "TFConfNewer.hgs")
	if err := os.WriteFile(newer, archive.Header{Version: 9}.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := repo.Open(newer)
	got.WriteString(cli.SerialCheck(check.Report{}, err))

	wantSegment(t, got.String(), "TFULL_HARDEN_BEGIN", "TFULL_HARDEN_END_MARKER")
}

// newConflictRepo builds the regression's TFConfRepo.hgs: c.txt edited
// differently on main and on cf, and r.txt edited on cf but renamed to r2.txt
// on main.
func newConflictRepo(t *testing.T) *mergeFx {
	t.Helper()
	f := newMergeFx(t, "TFConfRepo.hgs", "*.txt")
	f.write("c.txt", "base_c\n")
	f.write("r.txt", "rename_me_content_long\n")
	f.offer("base")
	f.pathNew("cf")
	f.pathGo("cf")
	f.write("c.txt", "feat_c\n")
	f.write("r.txt", "EDITED_me_content_long\n")
	f.offer("cf_edits")
	f.pathGo("main")
	f.write("c.txt", "main_c\n")
	f.remove("r.txt")
	f.write("r2.txt", "rename_me_content_long\n")
	f.offer("main_edits_and_renames")
	return f
}

// newConflictRepoB builds the regression's TFConfRepoB.hgs: one file, edited
// differently on main and on cf.
func newConflictRepoB(t *testing.T) *mergeFx {
	t.Helper()
	f := newMergeFx(t, "TFConfRepoB.hgs", "*.txt")
	f.write("c.txt", "base_c\n")
	f.offer("base")
	f.pathNew("cf")
	f.pathGo("cf")
	f.write("c.txt", "feat_c\n")
	f.offer("cf_edit")
	f.pathGo("main")
	f.write("c.txt", "main_c\n")
	f.offer("main_edit")
	return f
}

// dropLines removes every line starting with one of the given prefixes.
func dropLines(s string, prefixes ...string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		drop := false
		for _, p := range prefixes {
			if strings.HasPrefix(line, p) {
				drop = true
			}
		}
		if !drop {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
