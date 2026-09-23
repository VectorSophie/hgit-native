package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
	"github.com/VectorSophie/hgit-native/pkg/hgit/views"
	"github.com/VectorSophie/hgit-native/pkg/hgit/workdir"
)

// openToken is the <prefix>_ERR line for a repository that cannot be
// opened: see.HC/Diff.HC/Merge.HC's own not_a_repository and bad_header, and
// Check.HC's too-new-format message. Commands whose HolyC has no such check
// get the same shape under their own prefix (native-only).
func openToken(prefix string, err error) string {
	var uv *archive.UnsupportedVersionError
	switch {
	case errors.As(err, &uv):
		return prefix + "_ERR " + uv.Error() + "\n"
	case errors.Is(err, os.ErrNotExist):
		return prefix + "_ERR not_a_repository\n"
	}
	return prefix + "_ERR bad_header\n"
}

// statusOpenToken is Status.HC's variant: the repository path is appended,
// and the too-new-format line ends with it instead of "- written by ...".
func statusOpenToken(path string, err error) string {
	var uv *archive.UnsupportedVersionError
	switch {
	case errors.As(err, &uv):
		return fmt.Sprintf("STATUS_ERR unsupported_format_version=%d (this build reads up to %d) %s\n",
			uv.Version, archive.MaxSupportedVersion, path)
	case errors.Is(err, os.ErrNotExist):
		return "STATUS_ERR not_a_repository " + path + "\n"
	}
	return "STATUS_ERR bad_header " + path + "\n"
}

func openMessage(path string, err error) string {
	var uv *archive.UnsupportedVersionError
	switch {
	case errors.As(err, &uv):
		return fmt.Sprintf("%s: written by a newer hgit (format version %d; this build reads up to %d)",
			path, uv.Version, archive.MaxSupportedVersion)
	case errors.Is(err, os.ErrNotExist):
		return path + ": not an hgit repository (no such file)"
	}
	return fmt.Sprintf("%s: not a readable hgit repository: %v", path, err)
}

// open opens a repository, reporting a failure under prefix.
func (c *ctx) open(prefix, path string) (*repo.Repo, bool) {
	r, err := repo.Open(path)
	if err != nil {
		c.fail(openToken(prefix, err), openMessage(path, err))
		return nil, false
	}
	return r, true
}

func code(err error) int {
	if err != nil {
		return ExitFail
	}
	return ExitOK
}

func short(h archive.Hash) string { return h.Hex()[:12] }

// splitMask splits a find_mask at its last separator, as Offer.HC finds the
// directory its .hgitignore/.hgitattributes live in: "C:/Home/TF*.txt" is
// directory "C:/Home/" and pattern "TF*.txt"; a bare pattern is relative to
// the working directory.
func splitMask(mask string) (dir, pattern string) {
	i := strings.LastIndexAny(mask, "/"+string(filepath.Separator))
	if i < 0 {
		return ".", mask
	}
	return mask[:i+1], mask[i+1:]
}

// parseIndex is Hgit.HC's ParseI64, made strict: a non-negative decimal.
func (c *ctx) parseIndex(s, what, usage string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		c.usageErr("", fmt.Sprintf("%s must be a non-negative integer, got %q", what, s), usage)
		return 0, false
	}
	return n, true
}

func runInit(c *ctx, a []string) int {
	if err := repo.Init(a[0]); err != nil {
		return c.fail("DISPATCH_ERR init_failed "+a[0]+"\n", fmt.Sprintf("cannot create repository %s: %v", a[0], err))
	}
	c.say("DISPATCH_OK init "+a[0]+"\n", "Initialized empty hgit repository "+a[0]+"\n")
	return ExitOK
}

// --- offers -------------------------------------------------------------

func offerReason(err error) string {
	var re *workdir.ReadError
	switch {
	case errors.Is(err, offer.ErrMessageTooLong):
		return "message_too_long"
	case errors.Is(err, offer.ErrNameTooLong), errors.Is(err, repo.ErrNameTooLong):
		return "name_too_long"
	case errors.Is(err, offer.ErrEntityID):
		return "entity_id_unavailable"
	case errors.Is(err, offer.ErrTooDeep):
		return "too_deep"
	case errors.As(err, &re):
		return "unreadable " + re.Name
	}
	return "io_error"
}

// doOffer is every offering command: flat (mask set) or recursive (tree),
// plain or with a relation in opts. Hgit.HC prints the dispatch line after
// the offer unconditionally, so --serial does too; the exit code carries
// failure.
func (c *ctx) doOffer(cmd, repoPath, work, mask string, tree bool, opts offer.Options) int {
	prefix := "OFFER"
	if tree {
		prefix = "OFFERTREE"
	}
	dispatch := "DISPATCH_OK " + cmd + "\n"
	r, ok := c.open(prefix, repoPath)
	if !ok {
		c.say(dispatch, "")
		return ExitFail
	}
	abs := work
	if p, err := filepath.Abs(work); err == nil {
		abs = p
	}
	opts.OnIgnored = func(name string) {
		p := filepath.Join(abs, filepath.FromSlash(name)) // TempleOS printed full_name
		c.say("OFFER_IGNORED "+p+"\n", "ignored "+p+"\n")
	}
	var h archive.Hash
	var err error
	if tree {
		h, err = offer.OfferTree(r, work, opts)
	} else {
		h, err = offer.Offer(r, work, mask, opts)
	}
	if err != nil {
		c.fail(prefix+"_ERR "+offerReason(err)+"\n", fmt.Sprintf("%s: %v", cmd, err))
		c.say(dispatch, "")
		return ExitFail
	}
	c.say(dispatch, fmt.Sprintf("Offered %s on %s: %s\n", short(h), r.CurrentPath(), opts.Message))
	return ExitOK
}

func runOffer(c *ctx, a []string) int {
	dir, pat := splitMask(a[1])
	return c.doOffer("offer", a[0], dir, pat, false, offer.Options{Message: a[2]})
}

func runOfferTree(c *ctx, a []string) int {
	return c.doOffer("offertree", a[0], a[1], "", true, offer.Options{Message: a[2]})
}

var relTags = map[string]object.Relation{
	"correct": object.RelCorrects, "revert": object.RelReverts, "reconcile": object.RelReconciles,
}

// relation is HgitOfferRelatedCmd / HgitOfferTreeRelatedCmd: <repo>
// <target_hex> <entity_hex> <find_mask|dir_path> <message...>.
func relation(cmd string) func(*ctx, []string) int {
	tree := strings.HasSuffix(cmd, "tree")
	tag := relTags[strings.TrimSuffix(cmd, "tree")]
	return func(c *ctx, a []string) int {
		target, err := archive.ParseHex(a[1])
		if err != nil {
			return c.usageErr("DISPATCH_ERR bad_hash "+a[1]+"\n", fmt.Sprintf("%s: bad commit hash %q", cmd, a[1]), "")
		}
		entity, err := archive.ParseEntityID(a[2])
		if err != nil {
			return c.usageErr("DISPATCH_ERR bad_entity_id "+a[2]+"\n",
				fmt.Sprintf("%s: bad entity id %q (16 hex digits, 0000000000000000 for none)", cmd, a[2]), "")
		}
		opts := offer.Options{Message: a[4], Relation: tag, RelationTarget: target, RelationEntity: entity}
		if tree {
			return c.doOffer(cmd, a[0], a[3], "", true, opts)
		}
		dir, pat := splitMask(a[3])
		return c.doOffer(cmd, a[0], dir, pat, false, opts)
	}
}

// --- status, statustree, diff, history, see, check --------------------

// printStatus is what status and statustree share: the merge-in-progress
// banner, then the change report.
func (c *ctx) printStatus(r *repo.Repo, work string, changes []status.Change, err error) int {
	cs, cerr := merge.Conflicts(r)
	var noOff *status.NoOfferingsYetError
	isNoOff := errors.As(err, &noOff)
	if c.serial {
		io.WriteString(c.out, SerialMergeBanner(cs, cerr)+SerialStatus(changes, err))
		if isNoOff {
			return ExitOK
		}
		return code(err)
	}
	io.WriteString(c.out, humanMergeBanner(cs, cerr))
	switch {
	case isNoOff:
		fmt.Fprintf(c.out, "No offerings yet. Files in %s:\n", work)
		for _, f := range noOff.Listing {
			fmt.Fprintf(c.out, "  %s  %d\n", f.Name, f.Size)
		}
		return ExitOK
	case err != nil:
		return c.fail("", "status: "+err.Error())
	}
	io.WriteString(c.out, humanChanges(changes, "Nothing changed.\n"))
	return ExitOK
}

func runStatus(c *ctx, a []string) int {
	dir, pat := splitMask(a[1])
	work := dir
	if len(a) == 3 && a[2] != "" {
		if dir != "." && filepath.Clean(dir) != filepath.Clean(a[2]) {
			return c.usageErr("", fmt.Sprintf("status: <dir_prefix> %q is not the directory of <find_mask> %q", a[2], a[1]),
				"status <repo_path> <find_mask> [<dir_prefix>]")
		}
		work = a[2]
	}
	r, err := repo.Open(a[0])
	if err != nil {
		return c.fail(statusOpenToken(a[0], err), openMessage(a[0], err))
	}
	changes, err := status.Status(r, work, pat)
	return c.printStatus(r, work, changes, err)
}

// runStatusTree prints Hgit.HC's DISPATCH_OK statustree whatever happened,
// as the HolyC does.
func runStatusTree(c *ctx, a []string) int {
	ret := ExitFail
	if r, err := repo.Open(a[0]); err != nil {
		c.fail(statusOpenToken(a[0], err), openMessage(a[0], err))
	} else {
		changes, err := status.StatusTree(r, a[1])
		ret = c.printStatus(r, a[1], changes, err)
	}
	c.say("DISPATCH_OK statustree\n", "")
	return ret
}

// parseCommit is the <commit_hex> argument: a malformed one is Hgit.HC's
// DISPATCH_ERR bad_hash, checked before the repository is opened.
func (c *ctx) parseCommit(cmd, s string) (archive.Hash, bool) {
	h, err := archive.ParseHex(s)
	if err != nil {
		c.usageErr("DISPATCH_ERR bad_hash "+s+"\n", fmt.Sprintf("%s: bad commit hash %q (128 hex digits)", cmd, s), "")
		return h, false
	}
	return h, true
}

func runDiff(c *ctx, a []string) int {
	h, ok := c.parseCommit("diff", a[1])
	if !ok {
		return ExitUsage
	}
	r, ok := c.open("DIFF", a[0])
	if !ok {
		return ExitFail
	}
	changes, err := status.Diff(r, h)
	if c.serial {
		io.WriteString(c.out, SerialDiff(changes, err))
		return code(err)
	}
	if err != nil {
		return c.fail("", "diff: "+err.Error())
	}
	io.WriteString(c.out, humanChanges(changes, "No changes.\n"))
	return ExitOK
}

func runHistory(c *ctx, a []string) int {
	r, ok := c.open("HISTORY", a[0])
	if !ok {
		return ExitFail
	}
	lines, err := r.History()
	if errors.Is(err, repo.ErrNoHead) {
		c.say(SerialHistory(lines, err), "No offerings yet on "+r.CurrentPath()+".\n")
		return ExitOK
	}
	if c.serial {
		io.WriteString(c.out, SerialHistory(lines, err))
		return code(err)
	}
	for _, l := range lines {
		fmt.Fprintf(c.out, "%s  %s  %s\n", short(l.Hash), formatTS(l.Timestamp), l.Message)
	}
	if err != nil {
		return c.fail("", "history: "+err.Error())
	}
	return ExitOK
}

func runSee(c *ctx, a []string) int {
	h, ok := c.parseCommit("see", a[1])
	if !ok {
		return ExitUsage
	}
	r, ok := c.open("SEE", a[0])
	if !ok {
		return ExitFail
	}
	res, err := r.See(h)
	if c.serial {
		io.WriteString(c.out, SerialSee(res, err))
		return code(err)
	}
	if res == nil {
		return c.fail("", "see: "+err.Error())
	}
	io.WriteString(c.out, humanSee(h, res))
	if err != nil {
		return c.fail("", "see: "+err.Error())
	}
	return ExitOK
}

func runCheck(c *ctx, a []string) int {
	r, err := repo.Open(a[0])
	var rep check.Report
	if err == nil {
		rep = check.Run(r)
	}
	ret := ExitOK
	if err != nil || len(rep.HashBad) > 0 || rep.RefsBroken > 0 {
		ret = ExitFail
	}
	if c.serial {
		io.WriteString(c.out, SerialCheck(rep, err))
		return ret
	}
	if err != nil {
		return c.fail("", openMessage(a[0], err))
	}
	io.WriteString(c.out, humanCheck(rep))
	return ret
}

// --- merge and its conflict lifecycle -----------------------------------

func mergeCode(res merge.Result, err error) int {
	if err != nil || len(res.Conflicts) > 0 {
		return ExitFail
	}
	return ExitOK
}

func (c *ctx) mergeReport(serial string, res merge.Result, err error) int {
	if c.serial {
		io.WriteString(c.out, serial)
		return mergeCode(res, err)
	}
	io.WriteString(c.out, humanAutos(res.Autos))
	if err != nil {
		return c.fail("", humanMergeErr(err))
	}
	io.WriteString(c.out, humanMergeResult(res))
	return mergeCode(res, err)
}

func runMerge(c *ctx, a []string) int {
	r, ok := c.open("MERGE", a[0])
	if !ok {
		return ExitFail
	}
	res, err := merge.Merge(r, a[1])
	return c.mergeReport(SerialMerge(a[1], res, err), res, err)
}

func runMergeContinue(c *ctx, a []string) int {
	r, ok := c.open("MERGE", a[0])
	if !ok {
		return ExitFail
	}
	res, err := merge.Continue(r)
	return c.mergeReport(SerialMergeContinue(res, err), res, err)
}

func runMergeAbort(c *ctx, a []string) int {
	r, ok := c.open("MERGE", a[0])
	if !ok {
		return ExitFail
	}
	if err := merge.Abort(r); err != nil {
		return c.fail(SerialMergeAbort(err), humanMergeErr(err))
	}
	c.say(SerialMergeAbort(nil), "Merge aborted; its conflicts are discarded.\n")
	return ExitOK
}

func runConflicts(c *ctx, a []string) int {
	r, ok := c.open("MERGE", a[0])
	if !ok {
		return ExitFail
	}
	cs, err := merge.Conflicts(r)
	if errors.Is(err, merge.ErrNoMergeInProgress) {
		c.say(SerialConflicts(cs, err), "No merge in progress.\n")
		return ExitOK
	}
	if c.serial {
		io.WriteString(c.out, SerialConflicts(cs, err))
		return code(err)
	}
	if err != nil {
		return c.fail("", "conflicts: "+err.Error())
	}
	io.WriteString(c.out, humanConflicts(cs))
	return ExitOK
}

func runResolve(c *ctx, a []string) int {
	idx, ok := c.parseIndex(a[1], "<conflict_index>", "resolve <repo_path> <conflict_index> take-ours|take-theirs")
	if !ok {
		return ExitUsage
	}
	r, ok := c.open("MERGE", a[0])
	if !ok {
		return ExitFail
	}
	path, err := merge.Resolve(r, idx, a[2])
	if err != nil {
		return c.fail(SerialResolve(idx, a[2], path, err), "resolve: "+err.Error())
	}
	c.say(SerialResolve(idx, a[2], path, nil), fmt.Sprintf("Resolved conflict %d (%s) with %s.\n", idx, path, a[2]))
	return ExitOK
}

// --- views ---------------------------------------------------------------

// emitDoc writes a view document to dest, or to stdout when no destination
// was given.
func (c *ctx) emitDoc(prefix, dest, doc string) int {
	if dest == "" {
		io.WriteString(c.out, doc)
		return ExitOK
	}
	if err := os.WriteFile(dest, []byte(doc), 0o644); err != nil {
		return c.fail(prefix+"_ERR write_failed\n", err.Error())
	}
	c.say("", "Wrote "+dest+"\n")
	return ExitOK
}

// view is historydoc/reconcileoverview/graph/reconciledoc: Hgit.HC prints
// their DISPATCH_OK line whatever the document says, so --serial does too.
func (c *ctx) view(cmd, repoPath, dest string, render func(*repo.Repo) (string, error)) int {
	prefix := strings.ToUpper(cmd)
	ret := ExitFail
	if r, ok := c.open(prefix, repoPath); ok {
		if doc, err := render(r); err != nil {
			c.fail(prefix+"_ERR bad_object\n", cmd+": "+err.Error())
		} else {
			ret = c.emitDoc(prefix, dest, doc)
		}
	}
	c.say(SerialView(cmd), "")
	return ret
}

func optArg(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}

func runHistoryDoc(c *ctx, a []string) int {
	return c.view("historydoc", a[0], optArg(a, 1), views.HistoryDoc)
}

func runReconcileOverview(c *ctx, a []string) int {
	return c.view("reconcileoverview", a[0], optArg(a, 1), views.ReconcileOverview)
}

func runGraph(c *ctx, a []string) int { return c.view("graph", a[0], optArg(a, 1), views.Graph) }

func runReconcileDoc(c *ctx, a []string) int {
	h, err := archive.ParseHex(a[1])
	if err != nil {
		return c.usageErr("DISPATCH_ERR reconciledoc_bad_hex\n", fmt.Sprintf("reconciledoc: bad commit hash %q", a[1]), "")
	}
	return c.view("reconciledoc", a[0], optArg(a, 2), func(r *repo.Repo) (string, error) { return views.ReconcileDoc(r, h) })
}

// runConflictDoc prints HgitConflictDoc's own CONFLICTDOC_OK line; Hgit.HC
// adds no dispatch line for it.
func runConflictDoc(c *ctx, a []string) int {
	r, ok := c.open("CONFLICTDOC", a[0])
	if !ok {
		return ExitFail
	}
	doc, err := views.ConflictDoc(r)
	if err != nil {
		return c.fail("CONFLICTDOC_ERR bad_record\n", "conflictdoc: "+err.Error())
	}
	if ret := c.emitDoc("CONFLICTDOC", optArg(a, 1), doc); ret != ExitOK {
		return ret
	}
	cs, err := merge.Conflicts(r)
	c.say(SerialConflictDoc(cs, err), "")
	if errors.Is(err, merge.ErrNoMergeInProgress) {
		return ExitOK
	}
	return code(err)
}

// --- undo, redo, operation log, paths, export/import -------------------

// boolCmd runs a command whose HolyC reports success as a Bool: every
// failure, opening the repository included, collapses into one token.
func (c *ctx) boolCmd(repoPath string, op func(*repo.Repo) error, serial func(error) string, done func(*repo.Repo) string) int {
	r, err := repo.Open(repoPath)
	if err != nil {
		return c.fail(serial(err), openMessage(repoPath, err))
	}
	if err := op(r); err != nil {
		return c.fail(serial(err), err.Error())
	}
	c.say(serial(nil), done(r))
	return ExitOK
}

func runUndo(c *ctx, a []string) int {
	return c.boolCmd(a[0], (*repo.Repo).Undo, SerialUndo,
		func(r *repo.Repo) string { return "Undid the last operation on " + r.CurrentPath() + ".\n" })
}

func runRedo(c *ctx, a []string) int {
	return c.boolCmd(a[0], (*repo.Repo).Redo, SerialRedo,
		func(r *repo.Repo) string { return "Redid the last undone operation on " + r.CurrentPath() + ".\n" })
}

func runOpRestore(c *ctx, a []string) int {
	idx, ok := c.parseIndex(a[1], "<op_index>", "operation restore <repo_path> <op_index>")
	if !ok {
		return ExitUsage
	}
	return c.boolCmd(a[0], func(r *repo.Repo) error { return r.OperationRestore(idx) },
		func(err error) string { return SerialOperationRestore(idx, err) },
		func(r *repo.Repo) string { return fmt.Sprintf("Restored %s to operation %d.\n", r.CurrentPath(), idx) })
}

func runPathNew(c *ctx, a []string) int {
	return c.boolCmd(a[0], func(r *repo.Repo) error { return r.PathNew(a[1]) },
		func(err error) string { return SerialPathNew(a[1], err) },
		func(*repo.Repo) string { return "Created path " + a[1] + ".\n" })
}

func runPathGo(c *ctx, a []string) int {
	return c.boolCmd(a[0], func(r *repo.Repo) error { return r.PathGo(a[1]) },
		func(err error) string { return SerialPathGo(a[1], err) },
		func(*repo.Repo) string { return "Switched to path " + a[1] + ".\n" })
}

func runPathClose(c *ctx, a []string) int {
	return c.boolCmd(a[0], func(r *repo.Repo) error { return r.PathClose(a[1]) },
		func(err error) string { return SerialPathClose(a[1], err) },
		func(*repo.Repo) string { return "Closed path " + a[1] + ".\n" })
}

// runOpHistory and runPathList: Hgit.HC prints their dispatch line
// unconditionally.
func runOpHistory(c *ctx, a []string) int {
	r, ok := c.open("OPLOG", a[0])
	if !ok {
		c.say("DISPATCH_OK operation_history\n", "")
		return ExitFail
	}
	ops, err := r.OperationHistory()
	if c.serial {
		io.WriteString(c.out, SerialOperationHistory(ops, err))
		return code(err)
	}
	if err != nil {
		return c.fail("", "operation history: "+err.Error())
	}
	if len(ops) == 0 {
		fmt.Fprintf(c.out, "No operations logged on %s.\n", r.CurrentPath())
	}
	for i, op := range ops {
		fmt.Fprintf(c.out, "%3d  %s  %s -> %s\n", i, formatTS(op.Timestamp), short(op.Prev), short(op.New))
	}
	return ExitOK
}

func runPathList(c *ctx, a []string) int {
	r, ok := c.open("PATH", a[0])
	if !ok {
		c.say("DISPATCH_OK path_list\n", "")
		return ExitFail
	}
	names := r.PathList()
	if c.serial {
		io.WriteString(c.out, SerialPathList(names))
		return ExitOK
	}
	for _, n := range names {
		mark := "  "
		if n == r.CurrentPath() {
			mark = "* "
		}
		fmt.Fprintf(c.out, "%s%s\n", mark, n)
	}
	return ExitOK
}

func copyCmd(cmd string) func(*ctx, []string) int {
	verb, copyFn := "Exported", repo.Export
	if cmd == "import" {
		verb, copyFn = "Imported", repo.Import
	}
	return func(c *ctx, a []string) int {
		err := copyFn(a[0], a[1])
		if err != nil {
			return c.fail(SerialCopyRepo(cmd, err), fmt.Sprintf("%s: %v", cmd, err))
		}
		c.say(SerialCopyRepo(cmd, nil), fmt.Sprintf("%s %s to %s.\n", verb, a[0], a[1]))
		return ExitOK
	}
}

// --- discoverability -----------------------------------------------------

func runHelp(c *ctx, _ []string) int {
	c.say(Help(), usage())
	return ExitOK
}

func runVersion(c *ctx, _ []string) int {
	c.say(Version(), "hgit "+HgitVersion+"\n")
	return ExitOK
}

func runLogo(c *ctx, _ []string) int {
	io.WriteString(c.out, Logo())
	return ExitOK
}
