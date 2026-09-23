package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// run is one command line: stdout, stderr and the exit code.
func run(args ...string) (string, string, int) {
	var out, errw bytes.Buffer
	code := Run(args, &out, &errw)
	return out.String(), errw.String(), code
}

// serial runs a --serial command line and fails the test on an unexpected
// exit code or any stderr output.
func serial(t *testing.T, wantCode int, args ...string) string {
	t.Helper()
	out, errw, code := run(append([]string{"--serial"}, args...)...)
	if code != wantCode || errw != "" {
		t.Fatalf("hgit --serial %v: exit %d (want %d), stderr %q, stdout %q", args, code, wantCode, errw, out)
	}
	return out
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	out := serial(t, ExitUsage, "frobnicate", "x")
	if out != "DISPATCH_ERR unknown_command frobnicate\nDISPATCH_HINT try 'help' for a full command list\n" {
		t.Fatalf("serial: %q", out)
	}
	out, errw, code := run("frobnicate")
	if code != ExitUsage || out != "" || !strings.Contains(errw, `unknown command "frobnicate"`) {
		t.Fatalf("human: exit %d, stdout %q, stderr %q", code, out, errw)
	}
	// Hgit.HC keeps at most 31 bytes of the command name.
	long := strings.Repeat("x", 40)
	if out := serial(t, ExitUsage, long); !strings.HasPrefix(out, "DISPATCH_ERR unknown_command "+long[:31]+"\n") {
		t.Fatalf("long name: %q", out)
	}
	// `interactive` is listed by the verbatim HolyC help but has no native
	// counterpart.
	if out := serial(t, ExitUsage, "interactive"); !strings.HasPrefix(out, "DISPATCH_ERR unknown_command interactive\n") {
		t.Fatalf("interactive: %q", out)
	}
}

func TestSerialFlagIsHidden(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {}, {"offer", "-h"}, {"merge", "continue", "--help"}, {"path", "new", "-h"}} {
		out, errw, code := run(args...)
		if code != ExitOK {
			t.Errorf("%v: exit %d", args, code)
		}
		if strings.Contains(out+errw, "serial") {
			t.Errorf("%v mentions serial:\n%s%s", args, out, errw)
		}
		if !strings.Contains(out, "usage: hgit") {
			t.Errorf("%v prints no usage:\n%s", args, out)
		}
	}
	// A bad flag: the flag package's own error, then usage - still no serial.
	out, errw, code := run("--bogus")
	if code != ExitUsage || out != "" || !strings.Contains(errw, "flag provided but not defined: -bogus") || strings.Contains(errw, "serial") {
		t.Fatalf("--bogus: exit %d, stdout %q, stderr %q", code, out, errw)
	}
	// help with --serial is HgitHelp verbatim; the human help leaves out
	// interactive.
	if out := serial(t, ExitOK, "help"); out != Help() {
		t.Fatalf("serial help: %q", out)
	}
	if out := serial(t, ExitOK); out != Help() {
		t.Fatalf("serial, no command: %q", out)
	}
	if out, _, _ := run("help"); strings.Contains(out, "interactive") || !strings.Contains(out, "hgit merge continue <repo_path>") {
		t.Fatalf("human help:\n%s", out)
	}
}

func TestWrongArgumentCountIsAUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"init"}, {"init", "a", "b"}, {"offer", "r"}, {"see", "r"}, {"merge", "r"}, {"merge", "continue"},
		{"path", "new", "r"}, {"operation", "restore", "r"}, {"resolve", "r", "0"}, {"correct", "r", "h", "e"},
		{"graph"}, {"graph", "r", "d", "extra"}, {"status", "r"},
	} {
		out, errw, code := run(append([]string{"--serial"}, args...)...)
		if code != ExitUsage || out != "" || !strings.Contains(errw, "usage: hgit ") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errw)
		}
	}
}

func TestMalformedArgvNeverPanics(t *testing.T) {
	dir := t.TempDir()
	r := filepath.Join(dir, "r.hgs")
	for _, args := range [][]string{
		{""}, {"", ""}, {"--"}, {"-"}, {"--serial=maybe"}, {"--serial", "--serial"}, {"merge"}, {"merge", ""},
		{"path"}, {"path", ""}, {"operation"}, {"operation", "restore", r, "-1"}, {"operation", "restore", r, "99999999999999999999999"},
		{"resolve", r, "x", "take-ours"}, {"see", r, ""}, {"see", r, strings.Repeat("g", 128)}, {"diff", "", ""},
		{"status", "", "", ""}, {"offer", "", "", ""}, {"offertree", "", ""}, {"correcttree", "", "", "", ""},
		{"export", "", ""}, {"reconciledoc", "", "", ""}, {"conflictdoc", ""}, {"init", ""},
	} {
		for _, mode := range [][]string{nil, {"--serial"}} {
			_, _, code := run(append(append([]string{}, mode...), args...)...)
			if code != ExitOK && code != ExitFail && code != ExitUsage {
				t.Errorf("%v %v: exit %d", mode, args, code)
			}
		}
	}
}

func TestSerialAndHumanModes(t *testing.T) {
	dir := t.TempDir()
	r := filepath.Join(dir, "r.hgs")
	if out := serial(t, ExitOK, "init", r); out != "DISPATCH_OK init "+r+"\n" {
		t.Fatalf("serial init: %q", out)
	}
	r2 := filepath.Join(dir, "r2.hgs")
	out, errw, code := run("init", r2)
	if code != ExitOK || errw != "" || !strings.Contains(out, "Initialized empty hgit repository") || strings.Contains(out, "DISPATCH") {
		t.Fatalf("human init: exit %d, %q, %q", code, out, errw)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o644)
	if out := serial(t, ExitOK, "offer", r, filepath.Join(dir, "*.txt"), "first", "words"); out != "DISPATCH_OK offer\n" {
		t.Fatalf("serial offer: %q", out)
	}
	out = serial(t, ExitOK, "history", r)
	if !strings.HasPrefix(out, "commit ts=") || !strings.HasSuffix(out, " msg=first words\nHISTORY_END shown=1\n") {
		t.Fatalf("serial history: %q", out)
	}
	out, errw, code = run("history", r)
	if code != ExitOK || errw != "" || !strings.Contains(out, "first words") || strings.Contains(out, "HISTORY_END") {
		t.Fatalf("human history: exit %d, %q, %q", code, out, errw)
	}
	// Errors: a token on stdout with --serial, a message on stderr without.
	if out := serial(t, ExitFail, "init", r); out != "DISPATCH_ERR init_failed "+r+"\n" {
		t.Fatalf("serial init again: %q", out)
	}
	out, errw, code = run("init", r)
	if code != ExitFail || out != "" || !strings.HasPrefix(errw, "hgit: ") {
		t.Fatalf("human init again: exit %d, %q, %q", code, out, errw)
	}
}

// TestEveryCommandRoutes drives each subcommand once through Run with
// --serial and checks the token it prints and the state it leaves.
func TestEveryCommandRoutes(t *testing.T) {
	dir := t.TempDir()
	r := filepath.Join(dir, "r.hgs")
	work := filepath.Join(dir, "w")
	os.MkdirAll(filepath.Join(work, "sub"), 0o755)
	write := func(name, content string) { os.WriteFile(filepath.Join(work, name), []byte(content), 0o644) }
	head := func() string {
		rp, err := repo.Open(r)
		if err != nil {
			t.Fatal(err)
		}
		h, _ := rp.Head(rp.CurrentPath())
		return h.Hex()
	}
	mask := filepath.Join(work, "*.txt")
	expect := func(got, want string) {
		t.Helper()
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	contains := func(got, want string) {
		t.Helper()
		if !strings.Contains(got, want) {
			t.Fatalf("%q lacks %q", got, want)
		}
	}

	serial(t, ExitOK, "init", r)
	write("a.txt", "one")
	write("b.txt", "bee")
	expect(serial(t, ExitOK, "offer", r, mask, "root"), "DISPATCH_OK offer\n")
	root := head()
	write("a.txt", "two")
	expect(serial(t, ExitOK, "status", r, mask, work+"/"), "STATUS_MODIFIED a.txt\nSTATUS_UNCHANGED b.txt\nSTATUS_END\n")
	// dir_prefix may be left out: the mask's own directory is used.
	expect(serial(t, ExitOK, "status", r, mask), "STATUS_MODIFIED a.txt\nSTATUS_UNCHANGED b.txt\nSTATUS_END\n")
	expect(serial(t, ExitOK, "offer", r, mask, "second"), "DISPATCH_OK offer\n")
	second := head()
	contains(serial(t, ExitOK, "see", r, second), "SEE_COMMIT ts=")
	expect(serial(t, ExitOK, "diff", r, second), "DIFF_MODIFIED a.txt\nDIFF_END\n")
	contains(serial(t, ExitOK, "check", r), "CHECK_REFS_OK\nCHECK_DANGLING_NONE\n")

	// relations, flat and tree, with the tag each one stores
	for _, c := range []struct {
		cmd string
		tag string
	}{{"correct", "2"}, {"revert", "3"}, {"reconcile", "4"}, {"correcttree", "2"}, {"reverttree", "3"}, {"reconciletree", "4"}} {
		where := mask
		if strings.HasSuffix(c.cmd, "tree") {
			where = work
		}
		expect(serial(t, ExitOK, c.cmd, r, root, "00000000000000a1", where, "rel", c.cmd), "DISPATCH_OK "+c.cmd+"\n")
		contains(serial(t, ExitOK, "see", r, head()), "SEE_RELATION tag="+c.tag+" target="+root+" entity=00000000000000a1\n")
	}
	write("sub/in.txt", "nested")
	expect(serial(t, ExitOK, "offertree", r, work, "tree"), "DISPATCH_OK offertree\n")
	write("sub/in.txt", "nested, changed")
	expect(serial(t, ExitOK, "statustree", r, work), "STATUS_MODIFIED sub/in.txt\nSTATUS_END\nDISPATCH_OK statustree\n")

	// undo / redo / operation log
	expect(serial(t, ExitOK, "undo", r), "DISPATCH_OK undo\n")
	expect(serial(t, ExitOK, "redo", r), "DISPATCH_OK redo\n")
	contains(serial(t, ExitOK, "operation", "history", r), "OP 0 ts=")
	expect(serial(t, ExitOK, "operation", "restore", r, "1"), "DISPATCH_OK operation_restore 1\n")
	if head() != second {
		t.Fatal("operation restore 1 should put HEAD back on the second offer")
	}

	// paths and a merge with a conflict, resolved and continued
	expect(serial(t, ExitOK, "path", "new", r, "side"), "DISPATCH_OK path_new side\n")
	expect(serial(t, ExitOK, "path", "go", r, "side"), "DISPATCH_OK path_go side\n")
	write("a.txt", "side edit")
	serial(t, ExitOK, "offer", r, mask, "on side")
	serial(t, ExitOK, "path", "go", r, "main")
	write("a.txt", "main edit")
	serial(t, ExitOK, "offer", r, mask, "on main")
	expect(serial(t, ExitOK, "path", "list", r), "PATH main\nPATH side\nDISPATCH_OK path_list\n")
	contains(serial(t, ExitFail, "merge", r, "side"), "MERGE_CONFLICT a.txt\nMERGE_ABORTED conflicts=1\n")
	contains(serial(t, ExitOK, "conflicts", r), "CONFLICTS_COUNT 1\nCONFLICT 0 UNRESOLVED kind=1 a.txt\n")
	expect(serial(t, ExitOK, "conflictdoc", r, filepath.Join(dir, "c.DD")), "CONFLICTDOC_OK conflicts=1\n")
	if b, err := os.ReadFile(filepath.Join(dir, "c.DD")); err != nil || !strings.Contains(string(b), "a.txt") {
		t.Fatalf("conflictdoc file: %q %v", b, err)
	}
	expect(serial(t, ExitFail, "merge", "continue", r), "MERGE_ERR conflict_unresolved 0\nMERGE_ERR conflicts_still_unresolved count=1\n")
	expect(serial(t, ExitFail, "resolve", r, "0", "take-mine"), "RESOLVE_ERR unknown_selector take-mine (use take-ours or take-theirs)\n")
	expect(serial(t, ExitOK, "resolve", r, "0", "take-ours"), "RESOLVE_OK 0 a.txt\n")
	expect(serial(t, ExitOK, "merge", "continue", r), "MERGE_OK\n")
	expect(serial(t, ExitFail, "merge", "abort", r), "MERGE_ERR no_merge_in_progress\n")
	expect(serial(t, ExitOK, "conflicts", r), "CONFLICTS_NONE\n")
	expect(serial(t, ExitOK, "merge", r, "side"), "MERGE_ALREADY_UP_TO_DATE\n")
	expect(serial(t, ExitOK, "path", "close", r, "side"), "DISPATCH_OK path_close side\n")
	expect(serial(t, ExitOK, "path", "list", r), "PATH main\nDISPATCH_OK path_list\n")

	// views: to a file, or to stdout when the destination is left out
	for _, v := range [][]string{{"historydoc", r}, {"reconciledoc", r, root}, {"reconcileoverview", r}, {"graph", r}} {
		dest := filepath.Join(dir, v[0]+".DD")
		expect(serial(t, ExitOK, append(v, dest)...), "DISPATCH_OK "+v[0]+"\n")
		b, err := os.ReadFile(dest)
		if err != nil || len(b) == 0 {
			t.Fatalf("%s wrote %q, %v", v[0], b, err)
		}
		if out := serial(t, ExitOK, v...); out != string(b)+"DISPATCH_OK "+v[0]+"\n" {
			t.Fatalf("%s to stdout: %q", v[0], out)
		}
	}

	// export / import
	ex := filepath.Join(dir, "ex.hgs")
	expect(serial(t, ExitOK, "export", r, ex), "DISPATCH_OK export\n")
	expect(serial(t, ExitOK, "import", ex, filepath.Join(dir, "im.hgs")), "DISPATCH_OK import\n")
	contains(serial(t, ExitOK, "check", filepath.Join(dir, "im.hgs")), "CHECK_OK")

	expect(serial(t, ExitOK, "version"), Version())
	expect(serial(t, ExitOK, "logo"), Logo())
}

// TestErrorTokensAreReachable drives the error paths through real command
// lines: argument errors, a missing repository, a newer format, and command
// failures.
func TestErrorTokensAreReachable(t *testing.T) {
	dir := t.TempDir()
	r := filepath.Join(dir, "r.hgs")
	missing := filepath.Join(dir, "nope.hgs")
	newer := filepath.Join(dir, "newer.hgs")
	os.WriteFile(newer, archive.Header{Version: 9}.Marshal(), 0o644)
	serial(t, ExitOK, "init", r)
	hex := strings.Repeat("ab", 64)

	for _, c := range []struct {
		code int
		args []string
		want string
	}{
		{ExitUsage, []string{"see", r, "zz"}, "DISPATCH_ERR bad_hash zz\n"},
		{ExitUsage, []string{"diff", r, "zz"}, "DISPATCH_ERR bad_hash zz\n"},
		{ExitUsage, []string{"correct", r, "zz", "0000000000000000", "*", "m"}, "DISPATCH_ERR bad_hash zz\n"},
		{ExitUsage, []string{"reverttree", r, hex, "12", dir, "m"}, "DISPATCH_ERR bad_entity_id 12\n"},
		{ExitUsage, []string{"reconciledoc", r, "zz", "d"}, "DISPATCH_ERR reconciledoc_bad_hex\n"},
		{ExitUsage, []string{"path", "rename", r}, "DISPATCH_ERR unknown_path_subcommand rename\n"},
		{ExitUsage, []string{"operation", "redo", r}, "DISPATCH_ERR unknown_operation_subcommand redo\n"},
		{ExitFail, []string{"history", missing}, "HISTORY_ERR not_a_repository\n"},
		{ExitFail, []string{"see", missing, hex}, "SEE_ERR not_a_repository\n"},
		{ExitFail, []string{"diff", newer, hex}, "DIFF_ERR unsupported_format_version=9 (this build reads up to 4) - written by a newer hgit\n"},
		{ExitFail, []string{"check", missing}, "CHECK_ERR not_a_repository\n"},
		{ExitFail, []string{"status", missing, "*", dir}, "STATUS_ERR not_a_repository " + missing + "\n"},
		{ExitFail, []string{"status", newer, "*", dir}, "STATUS_ERR unsupported_format_version=9 (this build reads up to 4) " + newer + "\n"},
		{ExitFail, []string{"statustree", missing, dir}, "STATUS_ERR not_a_repository " + missing + "\nDISPATCH_OK statustree\n"},
		{ExitFail, []string{"merge", missing, "x"}, "MERGE_ERR not_a_repository\n"},
		{ExitFail, []string{"conflicts", missing}, "MERGE_ERR not_a_repository\n"},
		{ExitFail, []string{"offer", missing, "*", "m"}, "OFFER_ERR not_a_repository\nDISPATCH_OK offer\n"},
		{ExitFail, []string{"undo", missing}, "DISPATCH_ERR nothing_to_undo\n"},
		{ExitFail, []string{"undo", r}, "DISPATCH_ERR nothing_to_undo\n"},
		{ExitFail, []string{"redo", r}, "DISPATCH_ERR nothing_to_redo\n"},
		{ExitFail, []string{"operation", "restore", r, "3"}, "DISPATCH_ERR operation_restore_failed 3\n"},
		{ExitFail, []string{"path", "go", r, "ghost"}, "DISPATCH_ERR path_go_failed ghost\n"},
		{ExitFail, []string{"path", "close", r, "main"}, "DISPATCH_ERR path_close_failed main\n"},
		{ExitOK, []string{"history", r}, "HISTORY_EMPTY\n"},
		{ExitFail, []string{"merge", r, "ghost"}, "MERGE_ERR no_head_on_current_path\n"},
		{ExitFail, []string{"resolve", r, "0", "take-ours"}, "RESOLVE_ERR no_merge_in_progress\n"},
		{ExitFail, []string{"see", r, hex}, "SEE_ERR not_found\n"},
		{ExitFail, []string{"historydoc", missing, filepath.Join(dir, "h.DD")}, "HISTORYDOC_ERR not_a_repository\nDISPATCH_OK historydoc\n"},
		{ExitFail, []string{"offer", r, filepath.Join(dir, "*"), strings.Repeat("m", 256)}, "OFFER_ERR message_too_long\nDISPATCH_OK offer\n"},
	} {
		if got := serial(t, c.code, c.args...); got != c.want {
			t.Errorf("%v:\n got %q\nwant %q", c.args, got, c.want)
		}
	}
	// A usage error such as an unparsable index prints nothing on stdout.
	out, errw, code := run("--serial", "resolve", r, "x", "take-ours")
	if code != ExitUsage || out != "" || !strings.Contains(errw, "conflict_index") {
		t.Fatalf("bad index: exit %d, %q, %q", code, out, errw)
	}
}
