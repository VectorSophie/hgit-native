package tests

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/cli"
	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// TestFullRegressionReplay is pillar B: contract/tests/full-regression.hc,
// every step in order, each `Hgit("...")` call run as a real command line
// through cli.Run with --serial, and the WHOLE accumulated output compared
// against the WHOLE of contract/fixtures/expected.log.
//
// The scenario's own directory, C:/Home/, is a fresh temporary directory
// here, which is also the working directory (the regression's bare masks such
// as "TF*.txt" are relative to it, as they were on TempleOS). The only
// rewrite applied to the output is that directory prefix back to "C:/Home/";
// everything else goes through normalize() alone - timestamps, 128-hex hashes
// and 16-hex entity ids, which legitimately differ between runs. The
// TFULL_* marker lines are the regression's own CommPrint calls, so the
// replay prints them itself; so are its FileWrite/Del/DirMk steps and the
// three raw metadata writes of TFULL_HARDEN, which are not commands.
func TestFullRegressionReplay(t *testing.T) {
	base := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	home := base + string(filepath.Separator) // stands for C:/Home/
	p := func(rel string) string { return home + filepath.FromSlash(rel) }

	var out bytes.Buffer
	mark := func(s string) { out.WriteString(s + "\n") }
	hgit := func(args ...string) {
		t.Helper()
		var stderr bytes.Buffer
		code := cli.Run(append([]string{"--serial"}, args...), &out, &stderr)
		if code == 2 || stderr.Len() != 0 {
			t.Errorf("hgit %v: exit %d, stderr %q", args, code, stderr.String())
		}
	}
	// write is FileWrite: content must already hold exactly the byte count
	// the regression passed (see offer_scenario_test.go for the literal+NUL
	// cases).
	write := func(rel, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p(rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	del := func(rel string) {
		t.Helper()
		if err := os.Remove(p(rel)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	mkdir := func(rel string) {
		t.Helper()
		if err := os.MkdirAll(p(rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// headHex is CurrentHeadRead + HashToHex.
	headHex := func(rel string) string {
		t.Helper()
		r, err := repo.Open(p(rel))
		if err != nil {
			t.Fatal(err)
		}
		h, ok := r.Head(r.CurrentPath())
		if !ok {
			t.Fatalf("%s has no head", rel)
		}
		return h.Hex()
	}

	const repoF = "TFullRepo.hgs"
	mark("TFULL_BEGIN")

	// --- init, offer (root commit)
	hgit("init", p(repoF))
	write("TFStays.txt", "never touched")
	write("TFToModify.txt", "version one")
	write("TFToDelete.txt", "going away")
	write("TFOrig.txt", origContent)
	hgit("offer", p(repoF), "TF*.txt", "first_offer")

	// --- second commit's working-directory changes, status before offering
	write("TFToModify.txt", "version two")
	del("TFToDelete.txt")
	del("TFOrig.txt")
	write("TFRenamed.txt", renamedContent)
	write("TFGenuinelyNew.txt", "brand new content, unrelated")
	mark("TFULL_STATUS_BEGIN")
	hgit("status", p(repoF), p("TF*.txt"), p(""))
	mark("TFULL_STATUS_END_MARKER")
	hgit("offer", p(repoF), "TF*.txt", "second_offer")
	head2 := headHex(repoF)

	mark("TFULL_HISTORY_BEGIN")
	hgit("history", p(repoF))
	mark("TFULL_HISTORY_END_MARKER")

	mark("TFULL_SEE_BEGIN")
	hgit("see", p(repoF), head2)
	mark("TFULL_SEE_END_MARKER")

	mark("TFULL_DIFF_BEGIN")
	hgit("diff", p(repoF), head2)
	mark("TFULL_DIFF_END_MARKER")

	mark("TFULL_CHECK_BEGIN")
	hgit("check", p(repoF))
	mark("TFULL_CHECK_END_MARKER")

	// --- undo / redo
	hgit("undo", p(repoF))
	mark("TFULL_CHECK_AFTER_UNDO_BEGIN")
	hgit("check", p(repoF))
	mark("TFULL_CHECK_AFTER_UNDO_END_MARKER")
	hgit("redo", p(repoF))

	mark("TFULL_OPHISTORY_BEGIN")
	hgit("operation", "history", p(repoF))
	mark("TFULL_OPHISTORY_END_MARKER")

	// --- named paths
	hgit("path", "new", p(repoF), "feature")
	hgit("path", "go", p(repoF), "feature")
	write("TFFeatureFile.txt", "feature branch content\x00")
	hgit("offer", p(repoF), "TF*.txt", "feature_offer")
	mark("TFULL_PATHLIST_BEGIN")
	hgit("path", "list", p(repoF))
	mark("TFULL_PATHLIST_END_MARKER")
	hgit("path", "go", p(repoF), "main")

	// --- typed relations
	hgit("correct", p(repoF), head2, "0000000000000000", "TFToModify.txt", "correcting_offer")
	correctHex := headHex(repoF)
	mark("TFULL_SEE_CORRECT_BEGIN")
	hgit("see", p(repoF), correctHex)
	mark("TFULL_SEE_CORRECT_END_MARKER")

	// --- views
	hgit("historydoc", p(repoF), p("TFullHistory.DD"))
	hgit("reconciledoc", p(repoF), correctHex, p("TFullReconcile.DD"))
	hgit("reconcileoverview", p(repoF), p("TFullOverview.DD"))
	hgit("graph", p(repoF), p("TFullGraph.DD"))

	// --- export / import
	del("TFullExported.hgs")
	del("TFullExported.hgs.m")
	hgit("export", p(repoF), p("TFullExported.hgs"))
	mark("TFULL_CHECK_EXPORTED_BEGIN")
	hgit("check", p("TFullExported.hgs"))
	mark("TFULL_CHECK_EXPORTED_END_MARKER")
	hgit("import", p("TFullExported.hgs"), p("TFullImported.hgs"))
	mark("TFULL_CHECK_IMPORTED_BEGIN")
	hgit("check", p("TFullImported.hgs"))
	mark("TFULL_CHECK_IMPORTED_END_MARKER")

	// --- offertree / statustree / correcttree
	const treeF = "TFullTreeRepo.hgs"
	hgit("init", p(treeF))
	mkdir("TFTreeRoot")
	mkdir("TFTreeRoot/SubA")
	write("TFTreeRoot/top.txt", "tree top v1")
	write("TFTreeRoot/SubA/inner.txt", "tree inner v1")
	hgit("offertree", p(treeF), p("TFTreeRoot/"), "tree_first_offer")
	mark("TFULL_STATUSTREE_BEGIN")
	write("TFTreeRoot/SubA/inner.txt", "tree inner v2 CHANGED")
	hgit("statustree", p(treeF), p("TFTreeRoot/"))
	mark("TFULL_STATUSTREE_END_MARKER")
	hgit("offertree", p(treeF), p("TFTreeRoot/"), "tree_second_offer")
	treeHead2 := headHex(treeF)
	hgit("correcttree", p(treeF), treeHead2, "0000000000000000", p("TFTreeRoot/"), "tree_correcting_offer")
	mark("TFULL_CHECK_TREE_BEGIN")
	hgit("check", p(treeF))
	mark("TFULL_CHECK_TREE_END_MARKER")

	// --- merge: non-conflicting, then a fast-forward. Every FileWrite here
	// passes one byte past its literal, so each file ends in a NUL.
	const mergeF = "TFullMergeRepo.hgs"
	hgit("init", p(mergeF))
	write("TFMergeFileA.txt", "merge fileA root\x00")
	write("TFMergeFileB.txt", "merge fileB root\x00")
	hgit("offer", p(mergeF), "TFMergeFile*.txt", "merge_root_offer")
	hgit("path", "new", p(mergeF), "merge_feature")
	hgit("path", "go", p(mergeF), "merge_feature")
	write("TFMergeFileB.txt", "merge fileB EDITED by feature\x00")
	hgit("offer", p(mergeF), "TFMergeFile*.txt", "merge_feature_edit_b")
	hgit("path", "go", p(mergeF), "main")
	write("TFMergeFileB.txt", "merge fileB root\x00")
	write("TFMergeFileA.txt", "merge fileA EDITED by main\x00")
	hgit("offer", p(mergeF), "TFMergeFile*.txt", "merge_main_edit_a")
	mark("TFULL_MERGE_BEGIN")
	hgit("merge", p(mergeF), "merge_feature")
	mark("TFULL_MERGE_END_MARKER")
	mark("TFULL_CHECK_MERGED_BEGIN")
	hgit("check", p(mergeF))
	mark("TFULL_CHECK_MERGED_END_MARKER")
	hgit("path", "new", p(mergeF), "merge_ff_target")
	write("TFMergeFileA.txt", "merge fileA further advanced\x00")
	hgit("offer", p(mergeF), "TFMergeFile*.txt", "merge_advance_main_only")
	hgit("path", "go", p(mergeF), "merge_ff_target")
	mark("TFULL_MERGE_FF_BEGIN")
	hgit("merge", p(mergeF), "main")
	mark("TFULL_MERGE_FF_END_MARKER")

	// --- ignore rules
	const ignF = "TFIgnoreRepo.hgs"
	hgit("init", p(ignF))
	mkdir("TFIgnoreRoot")
	write("TFIgnoreRoot/.hgitignore", "*.tmp\n")
	write("TFIgnoreRoot/keep.txt", "kept")
	write("TFIgnoreRoot/x.tmp", "ignored")
	mark("TFULL_IGNORE_BEGIN")
	hgit("offertree", p(ignF), p("TFIgnoreRoot/"), "ignore_test_offer")
	mark("TFULL_IGNORE_END_MARKER")
	mark("TFULL_IGNORE_CHECK_BEGIN")
	hgit("check", p(ignF))
	mark("TFULL_IGNORE_CHECK_END_MARKER")

	// --- attributes/modes
	const attrsF = "TFAttrsRepo.hgs"
	del(".hgitattributes")
	hgit("init", p(attrsF))
	write("TFAttrsScript.txt", "echo hi")
	hgit("offer", p(attrsF), p("TFAttrsScript.txt"), "attrs_root_commit")
	write(".hgitattributes", "TFAttrsScript.txt executable\n")
	mark("TFULL_ATTRS_STATUS_BEGIN")
	hgit("status", p(attrsF), p("TFAttrsScript.txt"), p(""))
	mark("TFULL_ATTRS_STATUS_END_MARKER")
	hgit("offer", p(attrsF), p("TFAttrsScript.txt"), "attrs_mode_offer")
	attrsHead := headHex(attrsF)
	mark("TFULL_ATTRS_DIFF_BEGIN")
	hgit("diff", p(attrsF), attrsHead)
	mark("TFULL_ATTRS_DIFF_END_MARKER")
	mark("TFULL_ATTRS_CHECK_BEGIN")
	hgit("check", p(attrsF))
	mark("TFULL_ATTRS_CHECK_END_MARKER")
	del(".hgitattributes")

	// --- merge's three-way mode merge
	const mmF = "TFMergeModeRepo.hgs"
	hgit("init", p(mmF))
	write("TFMM_a.txt", "shared")
	write("TFMM_b.txt", "other root")
	hgit("offer", p(mmF), "TFMM_*.txt", "mm_root")
	hgit("path", "new", p(mmF), "mm_feature")
	hgit("path", "go", p(mmF), "mm_feature")
	write(".hgitattributes", "TFMM_a.txt executable\n\x00") // 23 bytes from a 22-byte literal
	hgit("offer", p(mmF), "TFMM_*.txt", "mm_mode_change")
	hgit("path", "go", p(mmF), "main")
	del(".hgitattributes")
	write("TFMM_b.txt", "other EDITED")
	hgit("offer", p(mmF), "TFMM_*.txt", "mm_other_edit")
	mark("TFULL_MERGEMODE_MERGE_BEGIN")
	hgit("merge", p(mmF), "mm_feature")
	mark("TFULL_MERGEMODE_MERGE_END_MARKER")
	mmHead := headHex(mmF)
	mark("TFULL_MERGEMODE_DIFF_BEGIN")
	hgit("diff", p(mmF), mmHead)
	mark("TFULL_MERGEMODE_DIFF_END_MARKER")
	mark("TFULL_MERGEMODE_CHECK_BEGIN")
	hgit("check", p(mmF))
	mark("TFULL_MERGEMODE_CHECK_END_MARKER")

	// --- persistent conflicts, rename-aware merge
	const confF = "TFConfRepo.hgs"
	confMask := p("TFConfW/*.txt")
	write("TFConfW/c.txt", "base_c\n")
	write("TFConfW/r.txt", "rename_me_content_long\n")
	hgit("init", p(confF))
	hgit("offer", p(confF), confMask, "base")
	hgit("path", "new", p(confF), "cf")
	hgit("path", "go", p(confF), "cf")
	write("TFConfW/c.txt", "feat_c\n")
	write("TFConfW/r.txt", "EDITED_me_content_long\n")
	hgit("offer", p(confF), confMask, "cf_edits")
	hgit("path", "go", p(confF), "main")
	write("TFConfW/c.txt", "main_c\n")
	del("TFConfW/r.txt")
	write("TFConfW/r2.txt", "rename_me_content_long\n")
	hgit("offer", p(confF), confMask, "main_edits_and_renames")
	mark("TFULL_CONFLICT_MERGE_BEGIN")
	hgit("merge", p(confF), "cf")
	hgit("conflicts", p(confF))
	hgit("merge", "continue", p(confF))
	hgit("resolve", p(confF), "0", "take-theirs")
	hgit("merge", "continue", p(confF))
	hgit("check", p(confF))
	hgit("merge", "abort", p(confF))
	mark("TFULL_CONFLICT_MERGE_END_MARKER")

	mark("TFULL_CONFLICT_ABORT_BEGIN")
	const confB = "TFConfRepoB.hgs"
	write("TFConfW/c.txt", "base_c\n")
	del("TFConfW/r2.txt")
	hgit("init", p(confB))
	hgit("offer", p(confB), confMask, "base")
	hgit("path", "new", p(confB), "cf")
	hgit("path", "go", p(confB), "cf")
	write("TFConfW/c.txt", "feat_c\n")
	hgit("offer", p(confB), confMask, "cf_edit")
	hgit("path", "go", p(confB), "main")
	write("TFConfW/c.txt", "main_c\n")
	hgit("offer", p(confB), confMask, "main_edit")
	hgit("merge", p(confB), "cf")
	hgit("status", p(confB), confMask, p("TFConfW/"))
	hgit("conflictdoc", p(confB), p("TFConfDoc.DD"))
	hgit("merge", "abort", p(confB))
	hgit("conflicts", p(confB))
	mark("TFULL_CONFLICT_ABORT_END_MARKER")

	// --- hardening: MetaMergeStateWrite + MetaConflictAppend by hand (no
	// command does this), then a header from a newer format version.
	mark("TFULL_HARDEN_BEGIN")
	{
		r, err := repo.Open(p(confB))
		if err != nil {
			t.Fatal(err)
		}
		head, _ := r.Head(r.CurrentPath())
		var fake archive.Hash
		for i := range fake {
			fake[i] = 7
		}
		r.Meta.Set("main", meta.TagMergeState, meta.MergeState{Ours: head, Theirs: head, OtherPath: "cf"}.Encode())
		r.Meta.Append("main", meta.TagConflict, meta.ConflictRecord{Conflict: fake}.Encode())
		if err := r.Save(); err != nil {
			t.Fatal(err)
		}
	}
	hgit("check", p(confB))
	hgit("merge", "abort", p(confB))
	del("TFConfNewer.hgs")
	write("TFConfNewer.hgs", string(archive.Header{Version: 9}.Marshal()))
	hgit("check", p("TFConfNewer.hgs"))
	mark("TFULL_HARDEN_END_MARKER")

	// --- discoverability
	hgit("version")
	hgit("logo")
	mark("TFULL_HELP_BEGIN")
	hgit("help")
	mark("TFULL_HELP_END_MARKER")
	mark("TFULL_END")

	got := strings.ReplaceAll(out.String(), home, "C:/Home/")
	want := testfix.ExpectedLog(t)
	if normalize(got) != normalize(want) {
		t.Fatal(firstDivergence(normalize(got), normalize(want)))
	}

	// normalize() masks every 16-hex token, which hides the conflict
	// evidence hashes; those are content-only blob hashes, so compare them
	// unmasked against TempleOS's own.
	if g, w := evidenceHashes(got), evidenceHashes(want); strings.Join(g, " ") != strings.Join(w, " ") {
		t.Fatalf("conflict evidence hashes differ:\ngot:  %v\nwant: %v", g, w)
	}
}

// firstDivergence names the first line where got and want differ, with a few
// lines of context from each.
func firstDivergence(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	i := 0
	for i < len(g) && i < len(w) && g[i] == w[i] {
		i++
	}
	ctx := func(ls []string) string {
		lo, hi := max(0, i-3), min(len(ls), i+4)
		var b strings.Builder
		for k := lo; k < hi; k++ {
			mark := "  "
			if k == i {
				mark = "> "
			}
			b.WriteString(mark + ls[k] + "\n")
		}
		return b.String()
	}
	return fmt.Sprintf("output diverges from expected.log at line %d\n--- got:\n%s--- want:\n%s", i+1, ctx(g), ctx(w))
}
