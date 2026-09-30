package cli

import (
	"errors"
	"fmt"

	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
)

// HgitVersion is Hgit.HC's HGIT_VERSION: the TempleOS release this port
// matches, i.e. the version baked into the pinned contract/ fixture, not
// this package's own release version. Right now both happen to read 1.9.0,
// but that's a coincidence of timing, not something to rely on going
// forward - a future hgit-native release could still pin an older or newer
// contract version than its own release number.
const HgitVersion = "1.9.0"

// NativeVersion is this hgit-native BUILD's own release version - a
// conceptually distinct thing from HgitVersion above (which is not this).
// It is a var, not a const, specifically so a release build can set it
// without editing source: `go build -ldflags
// "-X github.com/VectorSophie/hgit-native/internal/cli.NativeVersion=vX.Y.Z"`
// (see .github/workflows/release.yml's Build step). Any build that isn't
// built that way - a plain `go build ./cmd/hgit`, `go install`, a local dev
// build - keeps this "dev", an honest fallback rather than a stale or
// guessed number. This is exactly the bug this var exists to prevent: v1.9.0
// was tagged and released with only HgitVersion in existence, so `hgit
// version` on that release reports whatever HgitVersion happened to be at
// the tagged commit (1.8.9, since the later contract-pin/version-string fix
// landed on main only after the tag) - a real, user-visible defect, not a
// hypothetical one.
var NativeVersion = "dev"

// Version is `--serial version`'s first line - unchanged, and it must stay
// that way: this exact string is compared byte-for-byte against a real
// TempleOS build's own output in tests/views_scenario_test.go. It reports
// HgitVersion only, on purpose - NativeVersion is a second, separate line
// (NativeVersionLine below), never folded into this one, so that parity
// comparison is completely unaffected by anything to do with this port's
// own release version.
func Version() string { return "HGIT_VERSION " + HgitVersion + "\n" }

// NativeVersionLine is `--serial version`'s second line: this port's own
// release version, script-friendly and exactly what the release workflow's
// own "does the built binary's version match the git tag" check parses
// (see release.yml's smoke-test job). Never part of Version() itself.
func NativeVersionLine() string { return "HGIT_NATIVE_VERSION " + NativeVersion + "\n" }

// Help is HgitHelp, verbatim - including `interactive`, which only exists on
// TempleOS, and the <dest.DD> arguments of the DolDoc views (see
// docs/porting-notes.md).
func Help() string { return help }

const help = "HELP_BEGIN\n" +
	"HELP_VERSION " + HgitVersion + "\n" +
	"HELP_CMD init <repo_path>\n" +
	"HELP_CMD status <repo_path> <find_mask> <dir_prefix>\n" +
	"HELP_CMD offer <repo_path> <find_mask> <message>\n" +
	"HELP_CMD offertree <repo_path> <dir_path> <message> (recursive, ADR 0010)\n" +
	"HELP_CMD merge <repo_path> <other_path_name> (a real conflict now persists - see 'conflicts'/'resolve'/'merge continue'/'merge abort', ADR 0016)\n" +
	"HELP_CMD merge continue <repo_path> (finish a merge once every conflict is resolved, ADR 0016)\n" +
	"HELP_CMD merge abort <repo_path> (discard an in-progress merge's conflicts, ADR 0016)\n" +
	"HELP_CMD conflicts <repo_path> (list the current path's in-progress merge conflicts, ADR 0016)\n" +
	"HELP_CMD resolve <repo_path> <conflict_index> take-ours|take-theirs (an absent side = resolve to deletion; ADR 0016)\n" +
	"HELP_CMD conflictdoc <repo_path> <dest.DD> (DolDoc view of the in-progress merge's conflicts)\n" +
	"HELP_CMD statustree <repo_path> <dir_path> (recursive status, ADR 0010)\n" +
	"HELP_CMD history <repo_path>\n" +
	"HELP_CMD see <repo_path> <commit_hex>\n" +
	"HELP_CMD diff <repo_path> <commit_hex>\n" +
	"HELP_CMD check <repo_path>\n" +
	"HELP_CMD historydoc <repo_path> <dest.DD>\n" +
	"HELP_CMD reconciledoc <repo_path> <commit_hex> <dest.DD>\n" +
	"HELP_CMD reconcileoverview <repo_path> <dest.DD>\n" +
	"HELP_CMD graph <repo_path> <dest.DD>\n" +
	"HELP_CMD correct <repo_path> <target_hex> <entity_hex> <find_mask> <message>\n" +
	"HELP_CMD revert <repo_path> <target_hex> <entity_hex> <find_mask> <message>\n" +
	"HELP_CMD reconcile <repo_path> <target_hex> <entity_hex> <find_mask> <message>\n" +
	"HELP_CMD correcttree <repo_path> <target_hex> <entity_hex> <dir_path> <message> (recursive, ADR 0010)\n" +
	"HELP_CMD reverttree <repo_path> <target_hex> <entity_hex> <dir_path> <message> (recursive, ADR 0010)\n" +
	"HELP_CMD reconciletree <repo_path> <target_hex> <entity_hex> <dir_path> <message> (recursive, ADR 0010)\n" +
	"HELP_CMD   (entity_hex: 0000000000000000 for none, ADR 0006)\n" +
	"HELP_CMD undo <repo_path>\n" +
	"HELP_CMD redo <repo_path>\n" +
	"HELP_CMD operation history <repo_path>\n" +
	"HELP_CMD operation restore <repo_path> <op_index>\n" +
	"HELP_CMD path list <repo_path>\n" +
	"HELP_CMD path new <repo_path> <name>\n" +
	"HELP_CMD path go <repo_path> <name>\n" +
	"HELP_CMD path close <repo_path> <name>\n" +
	"HELP_CMD export <src_repo_path> <dest_repo_path>\n" +
	"HELP_CMD import <src_repo_path> <dest_repo_path>\n" +
	"HELP_CMD interactive (show hgit's output on the TempleOS screen and turn AutoComplete off - run this first when typing at the console)\n" +
	"HELP_CMD help\n" +
	"HELP_CMD version\n" +
	"HELP_CMD logo\n" +
	"HELP_END\n"

// Logo is HgitLogo: Logo.HC's rows of docs/brand/ascii-hgit2.txt, trailing
// spaces included.
func Logo() string { return logo }

const logo = "                                      @@@                                      \n" +
	"                                    @@@@@@@                                    \n" +
	"                                  @@@@@@@@@@@                                  \n" +
	"                                @@@@@@@ @@@@@@@                                \n" +
	"                              @@@@@@@@  @@@@@@@@@                              \n" +
	"                            @@@@@@@@@  @@%@@@@@@@@@                            \n" +
	"                          @@@@@@@@@@%  %@% @@@@@@@@@                           \n" +
	"                         @@@@@@@@@@@@@  %@@%@@@@@@@@@@                         \n" +
	"                       @@@@@@@@@% %@@@@   @@@@ @@@@@@@@%                       \n" +
	"                     @@@@@@*@@@@%   %@@  @@@@% @@@@@@@@@@%                     \n" +
	"                   @@@@@@@@ @@@@@@  %@@ %@@%  @@@@@@@@@@@@@@                   \n" +
	"                 @@@@@@@@@@ @@@%@@%%@%  @@@  @@@%@@ %@@@@@@@@%                 \n" +
	"                @@@@@@@@%@@@@@@%@@@@@%  @@@@ @@@ @@% @@@@@@@@@@                \n" +
	"               %@@@@@@@@@@@@@@%  @@@@@%  %@@@@@@ %@@@%@@@@@@@@@%               \n" +
	"                @@@@@@@@ %@%@@@%    %@@@  @@@@   @@@@@@%@@@@@@%                \n" +
	"                  @@@@@@@@@ %@@@@@@%  @@  @%   %@@@@@% @@@@@@                  \n" +
	"                    @@@@@@@%    %@@@  %%    %@@@@@ @@%@@@@@                    \n" +
	"                      @@@@@@@@@% %@@@%%%  %@@@*   %@@@@@@                      \n" +
	"                       %@@@@@%@%   %@@@#  @@   #%@@@@@@%                       \n" +
	"                         @@@@@@%%%#  %@       %@@@@@@%                         \n" +
	"                           @@@@@@@@@%     %@@@@@@@@@                           \n" +
	"                             @@@@@@@@@   %@@@@@@@@                             \n" +
	"                               @@@@@@@   %@@@@@@                               \n" +
	"                                 @@@%  %% %@@@                                 \n" +
	"                                   @@@@@@@@@                                   \n" +
	"                                    @@@@@@%                                    \n"

// SerialView is the line Hgit.HC prints after writing a DolDoc view
// (historydoc, reconciledoc, reconcileoverview, graph): always DISPATCH_OK.
func SerialView(cmd string) string { return "DISPATCH_OK " + cmd + "\n" }

// SerialConflictDoc is the line HgitConflictDoc prints after writing its
// document: the conflict record count, or "none" with no merge in progress.
func SerialConflictDoc(cs []merge.ConflictInfo, err error) string {
	switch {
	case errors.Is(err, merge.ErrNoMergeInProgress):
		return "CONFLICTDOC_OK none\n"
	case err != nil: // native-only: a malformed conflict record
		return "CONFLICTDOC_ERR bad_record\n"
	}
	return fmt.Sprintf("CONFLICTDOC_OK conflicts=%d\n", len(cs))
}
