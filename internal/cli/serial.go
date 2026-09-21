// Package cli holds the command layer; serial.go owns the exact serial-log
// tokens the TempleOS build printed.
package cli

import (
	"errors"
	"fmt"
	"strings"

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
