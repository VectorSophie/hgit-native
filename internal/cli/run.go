package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// Exit codes. A command that ran and reported a failure (a missing
// repository, an unresolved merge, a corrupt archive) exits ExitFail; a
// command line that could not be run at all (unknown command, wrong argument
// count, an unparsable hash or index) exits ExitUsage.
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

// ctx is one invocation: the output mode and the two streams.
type ctx struct {
	serial    bool
	out, errw io.Writer
}

// say prints the serial text with --serial, the human text otherwise.
func (c *ctx) say(serial, human string) {
	if c.serial {
		io.WriteString(c.out, serial)
	} else {
		io.WriteString(c.out, human)
	}
}

// fail reports a command failure - the serial token on stdout, or a message
// on stderr - and returns ExitFail.
func (c *ctx) fail(serial, human string) int {
	if c.serial {
		io.WriteString(c.out, serial)
	} else {
		fmt.Fprintf(c.errw, "hgit: %s\n", human)
	}
	return ExitFail
}

// usageErr reports a command line that cannot be run: in --serial mode the
// HolyC's own DISPATCH_ERR token when it has one (serial may be ""), a
// message and the command's usage on stderr otherwise.
func (c *ctx) usageErr(serial, human, usage string) int {
	if c.serial && serial != "" {
		io.WriteString(c.out, serial)
		return ExitUsage
	}
	fmt.Fprintf(c.errw, "hgit: %s\n", human)
	if usage != "" {
		fmt.Fprintf(c.errw, "usage: hgit %s\n", usage)
	}
	return ExitUsage
}

// command is one subcommand: its name as Hgit.HC dispatches it (two words for
// the `operation`/`path`/`merge` sub-forms), its argument shape, and how many
// positional arguments it takes. rest marks a trailing free-text message: the
// arguments from index min on are joined with single spaces, as Hgit.HC
// takes everything after its last fixed token verbatim.
type command struct {
	name     string
	args     string
	min, max int
	rest     bool
	run      func(c *ctx, a []string) int
}

func (cm command) usage() string { return strings.TrimSpace(cm.name + " " + cm.args) }

// commands is Hgit.HC's dispatch table, in HgitHelp's order. `interactive`
// is not here: it only switches the TempleOS console's screen echo and
// AutoComplete (see docs/porting-notes.md).
var commands []command

func init() {
	commands = []command{
		{"init", "<repo_path>", 1, 1, false, runInit},
		{"status", "<repo_path> <find_mask> [<dir_prefix>]", 2, 3, false, runStatus},
		{"offer", "<repo_path> <find_mask> <message>", 2, 2, true, runOffer},
		{"offertree", "<repo_path> <dir_path> <message>", 2, 2, true, runOfferTree},
		{"merge", "<repo_path> <other_path_name>", 2, 2, false, runMerge},
		{"merge continue", "<repo_path>", 1, 1, false, runMergeContinue},
		{"merge abort", "<repo_path>", 1, 1, false, runMergeAbort},
		{"conflicts", "<repo_path>", 1, 1, false, runConflicts},
		{"resolve", "<repo_path> <conflict_index> take-ours|take-theirs", 3, 3, false, runResolve},
		{"conflictdoc", "<repo_path> [<dest_file>]", 1, 2, false, runConflictDoc},
		{"statustree", "<repo_path> <dir_path>", 2, 2, false, runStatusTree},
		{"history", "<repo_path>", 1, 1, false, runHistory},
		{"see", "<repo_path> <commit_hex>", 2, 2, false, runSee},
		{"diff", "<repo_path> <commit_hex>", 2, 2, false, runDiff},
		{"check", "<repo_path>", 1, 1, false, runCheck},
		{"historydoc", "<repo_path> [<dest_file>]", 1, 2, false, runHistoryDoc},
		{"reconciledoc", "<repo_path> <commit_hex> [<dest_file>]", 2, 3, false, runReconcileDoc},
		{"reconcileoverview", "<repo_path> [<dest_file>]", 1, 2, false, runReconcileOverview},
		{"graph", "<repo_path> [<dest_file>]", 1, 2, false, runGraph},
		{"correct", "<repo_path> <target_hex> <entity_hex> <find_mask> <message>", 4, 4, true, relation("correct")},
		{"revert", "<repo_path> <target_hex> <entity_hex> <find_mask> <message>", 4, 4, true, relation("revert")},
		{"reconcile", "<repo_path> <target_hex> <entity_hex> <find_mask> <message>", 4, 4, true, relation("reconcile")},
		{"correcttree", "<repo_path> <target_hex> <entity_hex> <dir_path> <message>", 4, 4, true, relation("correcttree")},
		{"reverttree", "<repo_path> <target_hex> <entity_hex> <dir_path> <message>", 4, 4, true, relation("reverttree")},
		{"reconciletree", "<repo_path> <target_hex> <entity_hex> <dir_path> <message>", 4, 4, true, relation("reconciletree")},
		{"undo", "<repo_path>", 1, 1, false, runUndo},
		{"redo", "<repo_path>", 1, 1, false, runRedo},
		{"operation history", "<repo_path>", 1, 1, false, runOpHistory},
		{"operation restore", "<repo_path> <op_index>", 2, 2, false, runOpRestore},
		{"path list", "<repo_path>", 1, 1, false, runPathList},
		{"path new", "<repo_path> <name>", 2, 2, false, runPathNew},
		{"path go", "<repo_path> <name>", 2, 2, false, runPathGo},
		{"path close", "<repo_path> <name>", 2, 2, false, runPathClose},
		{"export", "<src_repo_path> <dest_repo_path>", 2, 2, false, copyCmd("export")},
		{"import", "<src_repo_path> <dest_repo_path>", 2, 2, false, copyCmd("import")},
		{"help", "", 0, 0, false, runHelp},
		{"version", "", 0, 0, false, runVersion},
		{"logo", "", 0, 0, false, runLogo},
	}
}

func lookup(name string) (command, bool) {
	for _, cm := range commands {
		if cm.name == name {
			return cm, true
		}
	}
	return command{}, false
}

// usage is the human command list: every command with its arguments.
// --serial is deliberately absent.
func usage() string {
	var b strings.Builder
	b.WriteString("usage: hgit <command> [arguments]\n\ncommands:\n")
	for _, cm := range commands {
		fmt.Fprintf(&b, "  hgit %s\n", cm.usage())
	}
	b.WriteString("\nentity_hex is 16 hex digits; 0000000000000000 means none.\n" +
		"Run 'hgit <command> -h' for one command's usage.\n")
	return b.String()
}

// trunc31 is Hgit.HC's U8 cmd[32]: at most 31 bytes of a command name.
func trunc31(s string) string {
	if len(s) > 31 {
		return s[:31]
	}
	return s
}

// Run executes one hgit command line (args excludes the program name),
// writing to stdout and stderr, and returns the exit code. It never panics on
// any argv.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hgit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // printed below, so --help never lists the hidden flag
	serial := fs.Bool("serial", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			io.WriteString(stdout, usage())
			return ExitOK
		}
		io.WriteString(stderr, usage())
		return ExitUsage
	}
	c := &ctx{serial: *serial, out: stdout, errw: stderr}
	a := fs.Args()
	if len(a) == 0 { // Hgit("") is help
		return runHelp(c, nil)
	}
	name, a := a[0], a[1:]

	// The two-word commands. Like Hgit.HC, `merge` treats a first argument of
	// "continue" or "abort" as its sub-form and anything else as a repository.
	switch name {
	case "operation", "path":
		sub := ""
		if len(a) > 0 {
			sub, a = a[0], a[1:]
		}
		if _, ok := lookup(name + " " + sub); !ok {
			return c.usageErr(fmt.Sprintf("DISPATCH_ERR unknown_%s_subcommand %s\n", name, trunc31(sub)),
				fmt.Sprintf("unknown %s subcommand %q - try 'hgit help'", name, sub), "")
		}
		name += " " + sub
	case "merge":
		if len(a) > 0 && (a[0] == "continue" || a[0] == "abort") {
			name, a = name+" "+a[0], a[1:]
		}
	}
	cm, ok := lookup(name)
	if !ok {
		return c.usageErr("DISPATCH_ERR unknown_command "+trunc31(name)+"\nDISPATCH_HINT try 'help' for a full command list\n",
			fmt.Sprintf("unknown command %q - try 'hgit help'", name), "")
	}

	sfs := flag.NewFlagSet("hgit "+name, flag.ContinueOnError)
	sfs.SetOutput(stderr)
	sfs.Usage = func() {}
	if err := sfs.Parse(a); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stdout, "usage: hgit %s\n", cm.usage())
			return ExitOK
		}
		fmt.Fprintf(stderr, "usage: hgit %s\n", cm.usage())
		return ExitUsage
	}
	pos := sfs.Args()
	if len(pos) < cm.min || (!cm.rest && len(pos) > cm.max) {
		return c.usageErr("", fmt.Sprintf("%s: wrong number of arguments", name), cm.usage())
	}
	if cm.rest {
		pos = append(pos[:cm.max:cm.max], strings.Join(pos[cm.max:], " "))
	}
	return cm.run(c, pos)
}
