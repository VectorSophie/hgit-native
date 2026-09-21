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
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
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
