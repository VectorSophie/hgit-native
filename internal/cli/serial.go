// Package cli holds the command layer; serial.go owns the exact serial-log
// tokens the TempleOS build printed.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
)

// SerialHistory formats History()'s result as HgitHistory printed it.
func SerialHistory(lines []repo.HistoryLine, err error) string {
	var b strings.Builder
	var nt *repo.NotTypeError
	switch {
	case errors.Is(err, repo.ErrNoHead):
		return "HISTORY_EMPTY\n"
	case errors.Is(err, repo.ErrBrokenChain):
		for _, l := range lines {
			fmt.Fprintf(&b, "commit ts=%d msg=%s\n", l.Timestamp, l.Message)
		}
		return b.String() + "HISTORY_ERR broken_chain\n"
	case errors.As(err, &nt):
		for _, l := range lines {
			fmt.Fprintf(&b, "commit ts=%d msg=%s\n", l.Timestamp, l.Message)
		}
		return b.String() + fmt.Sprintf("HISTORY_ERR not_a_commit type=%d\n", nt.Got)
	}
	if err != nil { // native-only: undecodable commit object
		for _, l := range lines {
			fmt.Fprintf(&b, "commit ts=%d msg=%s\n", l.Timestamp, l.Message)
		}
		return b.String() + "HISTORY_ERR bad_object\n"
	}
	for _, l := range lines {
		fmt.Fprintf(&b, "commit ts=%d msg=%s\n", l.Timestamp, l.Message)
	}
	fmt.Fprintf(&b, "HISTORY_END shown=%d\n", len(lines))
	return b.String()
}

// SerialSee formats See()'s result as HgitSee printed it.
func SerialSee(res *repo.SeeResult, err error) string {
	var nt *repo.NotTypeError
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return "SEE_ERR not_found\n"
	case errors.As(err, &nt):
		return fmt.Sprintf("SEE_ERR not_a_commit type=%d\n", nt.Got)
	case err != nil && res == nil:
		return "SEE_ERR bad_object\n"
	}
	c := res.Commit
	var b strings.Builder
	fmt.Fprintf(&b, "SEE_COMMIT ts=%d parents=%d msg=%s\n", c.Timestamp, len(c.Parents), c.Message)
	for _, p := range c.Parents {
		fmt.Fprintf(&b, "SEE_PARENT %s\n", p.Hex())
	}
	if c.Relation != 0 {
		fmt.Fprintf(&b, "SEE_RELATION tag=%d target=%s entity=%016x\n", c.Relation, c.RelationTarget.Hex(), c.RelationEntity)
	}
	if err != nil { // tree not found
		return b.String() + "SEE_ERR tree_not_found\n"
	}
	fmt.Fprintf(&b, "SEE_TREE entries=%d\n", res.Count)
	for _, e := range res.Entries {
		ind := strings.Repeat("  ", e.Depth)
		fmt.Fprintf(&b, "%s  entry type=%d id=%016x name=%s\n", ind, e.Entry.ChildType, e.Entry.EntityID, e.Entry.Name)
		if e.Missing {
			fmt.Fprintf(&b, "%s    (missing nested tree object)\n", ind)
		}
	}
	return b.String() + "SEE_END\n"
}

// SerialCheck formats a check report as HgitCheck printed it. openErr, if
// non-nil, is the error from opening the repository (the report is then
// ignored).
func SerialCheck(rep check.Report, openErr error) string {
	var uv *archive.UnsupportedVersionError
	switch {
	case errors.As(openErr, &uv):
		return "CHECK_ERR " + uv.Error() + "\n"
	case errors.Is(openErr, os.ErrNotExist):
		return "CHECK_ERR not_a_repository\n"
	case openErr != nil:
		return "CHECK_ERR bad_header\n"
	}
	var b strings.Builder
	if uint64(rep.Objects) != rep.HeaderCount {
		fmt.Fprintf(&b, "CHECK_WARN object_count_mismatch header=%d scanned=%d\n", rep.HeaderCount, rep.Objects)
	}
	if len(rep.HashBad) == 0 {
		fmt.Fprintf(&b, "CHECK_OK objects=%d format_version=%d\n", rep.Objects, rep.FormatVersion)
	} else {
		fmt.Fprintf(&b, "CHECK_FAIL objects=%d ok=%d corrupt=%d format_version=%d\n",
			rep.Objects, rep.Objects-len(rep.HashBad), len(rep.HashBad), rep.FormatVersion)
	}
	for _, br := range rep.Broken {
		fmt.Fprintf(&b, "CHECK_BROKEN_REF %s %s\n", br.Kind, br.Hash.Hex())
	}
	if rep.RefsBroken == 0 {
		b.WriteString("CHECK_REFS_OK\n")
	} else {
		fmt.Fprintf(&b, "CHECK_REFS_FAIL broken=%d\n", rep.RefsBroken)
	}
	names := map[archive.Type]string{archive.Commit: "commit", archive.Tree: "tree", archive.Attrs: "attrs", archive.Conflict: "conflict"}
	for _, d := range rep.Dangling {
		kind := names[d.Type]
		if kind == "" {
			kind = "blob"
		}
		fmt.Fprintf(&b, "CHECK_DANGLING %s %s\n", kind, d.Hash.Hex())
	}
	if len(rep.Dangling) == 0 {
		b.WriteString("CHECK_DANGLING_NONE\n")
	} else {
		fmt.Fprintf(&b, "CHECK_DANGLING_COUNT %d\n", len(rep.Dangling))
	}
	return b.String()
}

// SerialStatus formats Status's result as HgitStatus printed it.
func SerialStatus(changes []status.Change, err error) string { return serialStatus(changes, err) }

// SerialStatusTree formats StatusTree's result as HgitStatusTree printed it -
// the same tokens and the same STATUS_END, since the HolyC's own two
// functions share both.
func SerialStatusTree(changes []status.Change, err error) string { return serialStatus(changes, err) }

// SerialDiff formats Diff's result as HgitDiff printed it. The three
// DIFF_ERR cases are HgitDiff's own; "not_a_repository"/"bad_header" belong
// to opening the archive, which the caller does (SerialCheck's own openErr
// pattern), so they are not produced here.
func SerialDiff(changes []status.Change, err error) string {
	var nt *repo.NotTypeError
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return "DIFF_ERR commit_not_found\n"
	case errors.As(err, &nt):
		return fmt.Sprintf("DIFF_ERR not_a_commit type=%d\n", nt.Got)
	case errors.Is(err, repo.ErrTreeNotFound):
		return "DIFF_ERR tree_not_found\n"
	case errors.Is(err, status.ErrTooDeep):
		// Native-only: the HolyC's own recursion has no depth guard.
		return "DIFF_ERR too_deep\n"
	case err != nil: // native-only: an undecodable commit object
		return "DIFF_ERR bad_object\n"
	}
	var b strings.Builder
	for _, c := range changes {
		switch c.Kind {
		case status.Modified:
			fmt.Fprintf(&b, "DIFF_MODIFIED %s\n", c.Path)
		case status.Renamed:
			fmt.Fprintf(&b, "DIFF_RENAMED %s -> %s\n", c.OldPath, c.Path)
		case status.New:
			fmt.Fprintf(&b, "DIFF_NEW %s\n", c.Path)
		case status.Deleted:
			fmt.Fprintf(&b, "DIFF_DELETED %s\n", c.Path)
		case status.ModeChanged:
			fmt.Fprintf(&b, "DIFF_MODE_CHANGED %s %d -> %d\n", c.Path, c.OldMode, c.NewMode)
		case status.TypeChanged:
			fmt.Fprintf(&b, "DIFF_TYPE_CHANGED %s\n", c.Path)
		}
	}
	b.WriteString("DIFF_END\n")
	return b.String()
}

func serialStatus(changes []status.Change, err error) string {
	var noOff *status.NoOfferingsYetError
	switch {
	case errors.As(err, &noOff):
		var b strings.Builder
		b.WriteString("STATUS_NO_OFFERINGS_YET\n")
		for _, e := range noOff.Listing {
			fmt.Fprintf(&b, "%s  %d\n", e.Name, e.Size)
		}
		return b.String()
	case errors.Is(err, status.ErrNoHead):
		return "STATUS_ERR no_head_but_objects_exist\n"
	case err != nil: // native-only: an error Status.HC's own preamble never reaches
		return "STATUS_ERR bad_object\n"
	}
	var b strings.Builder
	for _, c := range changes {
		switch c.Kind {
		case status.Unchanged:
			fmt.Fprintf(&b, "STATUS_UNCHANGED %s\n", c.Path)
		case status.Modified:
			fmt.Fprintf(&b, "STATUS_MODIFIED %s\n", c.Path)
		case status.Renamed:
			fmt.Fprintf(&b, "STATUS_RENAMED %s -> %s\n", c.OldPath, c.Path)
		case status.New:
			fmt.Fprintf(&b, "STATUS_NEW %s\n", c.Path)
		case status.Deleted:
			fmt.Fprintf(&b, "STATUS_DELETED %s\n", c.Path)
		case status.ModeChanged:
			fmt.Fprintf(&b, "STATUS_MODE_CHANGED %s %d -> %d\n", c.Path, c.OldMode, c.NewMode)
		case status.TypeChanged:
			fmt.Fprintf(&b, "STATUS_TYPE_CHANGED %s\n", c.Path)
		}
	}
	b.WriteString("STATUS_END\n")
	return b.String()
}

// The path, undo/redo and operation commands print their dispatch line as
// part of their own output (Hgit.HC does it inline in each branch, unlike
// `history`/`check`/`status`, which print none), so these formatters carry
// it too - that is what the fixture's own segments contain.

// SerialPathList formats PathList()'s result as `path list` printed it.
func SerialPathList(names []string) string {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "PATH %s\n", n)
	}
	b.WriteString("DISPATCH_OK path_list\n")
	return b.String()
}

// SerialPathNew, SerialPathGo and SerialPathClose format the three mutating
// path sub-commands. Every failure reason collapses into one token, exactly
// as the HolyC's Bool return does.
func SerialPathNew(name string, err error) string { return serialPath("path_new", name, err) }

func SerialPathGo(name string, err error) string { return serialPath("path_go", name, err) }

func SerialPathClose(name string, err error) string { return serialPath("path_close", name, err) }

func serialPath(cmd, name string, err error) string {
	if err != nil {
		return fmt.Sprintf("DISPATCH_ERR %s_failed %s\n", cmd, name)
	}
	return fmt.Sprintf("DISPATCH_OK %s %s\n", cmd, name)
}

// SerialUndo formats Undo()'s result.
func SerialUndo(err error) string {
	if err != nil {
		return "DISPATCH_ERR nothing_to_undo\n"
	}
	return "DISPATCH_OK undo\n"
}

// SerialRedo formats Redo()'s result.
func SerialRedo(err error) string {
	if err != nil {
		return "DISPATCH_ERR nothing_to_redo\n"
	}
	return "DISPATCH_OK redo\n"
}

// SerialOperationHistory formats OperationHistory()'s result as
// `operation history` printed it: oldest first, hashes in full hex.
func SerialOperationHistory(ops []meta.OpLogEntry, err error) string {
	if err != nil { // native-only: an undecodable log entry
		return "OPLOG_ERR bad_entry\nDISPATCH_OK operation_history\n"
	}
	if len(ops) == 0 {
		return "OPLOG_EMPTY\nDISPATCH_OK operation_history\n"
	}
	var b strings.Builder
	for i, op := range ops {
		fmt.Fprintf(&b, "OP %d ts=%d prev=%s new=%s\n", i, op.Timestamp, op.Prev.Hex(), op.New.Hex())
	}
	b.WriteString("DISPATCH_OK operation_history\n")
	return b.String()
}

// SerialOperationRestore formats OperationRestore()'s result.
func SerialOperationRestore(index int, err error) string {
	if err != nil {
		return fmt.Sprintf("DISPATCH_ERR operation_restore_failed %d\n", index)
	}
	return fmt.Sprintf("DISPATCH_OK operation_restore %d\n", index)
}

// SerialCopyRepo formats `export`/`import`, which share one implementation
// and differ only in the name they print. The HolyC always prints DISPATCH_OK
// (its copy helper cannot report failure); a real I/O error is native-only.
func SerialCopyRepo(cmd string, err error) string {
	if err != nil {
		return fmt.Sprintf("DISPATCH_ERR %s_failed\n", cmd)
	}
	return fmt.Sprintf("DISPATCH_OK %s\n", cmd)
}

// SerialMerge formats a merge attempt as HgitMerge printed it. The
// MERGE_AUTO notices come first, as they do there (printed during the walk),
// then either MERGE_OK or the conflict report.
func SerialMerge(otherPath string, res merge.Result, err error) string {
	switch {
	case errors.Is(err, merge.ErrInProgress):
		return "MERGE_ERR conflicts_already_in_progress - resolve them ('hgit conflicts'/'hgit resolve') then 'hgit merge continue', or 'hgit merge abort' first\n"
	case errors.Is(err, repo.ErrNoHead):
		return "MERGE_ERR no_head_on_current_path\n"
	case errors.Is(err, repo.ErrNoSuchPath):
		return fmt.Sprintf("MERGE_ERR other_path_not_found %s\n", otherPath)
	case errors.Is(err, merge.ErrNoCommonAncestor):
		return "MERGE_ERR no_common_ancestor\n"
	case errors.Is(err, merge.ErrTreeNotFound):
		return "MERGE_ERR tree_not_found\n"
	case errors.Is(err, merge.ErrNestedTree):
		// Native-only: the recursive merge is not ported yet.
		return "MERGE_ERR nested_tree\n"
	case err != nil: // native-only: a malformed object or a failed save
		return "MERGE_ERR bad_object\n"
	case res.UpToDate:
		return "MERGE_ALREADY_UP_TO_DATE\n"
	case res.FastForward:
		return "MERGE_FASTFORWARD\n"
	}
	var b strings.Builder
	for _, a := range res.Autos {
		switch a.Kind {
		case merge.AutoTookTheirs:
			fmt.Fprintf(&b, "MERGE_AUTO took-theirs %s\n", a.Path)
		case merge.AutoDeleted:
			fmt.Fprintf(&b, "MERGE_AUTO deleted %s\n", a.Path)
		}
	}
	if len(res.Conflicts) == 0 {
		return b.String() + "MERGE_OK\n"
	}
	for _, c := range res.Conflicts {
		fmt.Fprintf(&b, "MERGE_CONFLICT %s\n", c.Object.Path)
	}
	fmt.Fprintf(&b, "MERGE_ABORTED conflicts=%d\n", len(res.Conflicts))
	b.WriteString("MERGE_CONFLICTS_PERSISTED - see 'hgit conflicts', 'hgit resolve', 'hgit merge continue'/'hgit merge abort'\n")
	return b.String()
}
