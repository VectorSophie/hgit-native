package cli

// human.go formats the default, human-readable output: the same results the
// Serial* functions print as tokens, as plain sentences and aligned lists.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
)

func formatTS(ts uint64) string { return clock.Format(ts) }

// modeName spells a mode byte out: "normal", or its flags joined by "+".
func modeName(m byte) string {
	var parts []string
	if m&object.ModeBinary != 0 {
		parts = append(parts, "binary")
	}
	if m&object.ModeExecutable != 0 {
		parts = append(parts, "executable")
	}
	if rest := m &^ (object.ModeBinary | object.ModeExecutable); rest != 0 {
		parts = append(parts, fmt.Sprintf("0x%02x", rest))
	}
	if len(parts) == 0 {
		return "normal"
	}
	return strings.Join(parts, "+")
}

// humanChanges lists every change but the unchanged files, or none if
// nothing changed.
func humanChanges(cs []status.Change, none string) string {
	var b strings.Builder
	for _, ch := range cs {
		switch ch.Kind {
		case status.Modified:
			fmt.Fprintf(&b, "  modified:  %s\n", ch.Path)
		case status.Renamed:
			fmt.Fprintf(&b, "  renamed:   %s -> %s\n", ch.OldPath, ch.Path)
		case status.New:
			fmt.Fprintf(&b, "  new:       %s\n", ch.Path)
		case status.Deleted:
			fmt.Fprintf(&b, "  deleted:   %s\n", ch.Path)
		case status.ModeChanged:
			fmt.Fprintf(&b, "  mode:      %s (%s -> %s)\n", ch.Path, modeName(ch.OldMode), modeName(ch.NewMode))
		case status.TypeChanged:
			fmt.Fprintf(&b, "  type:      %s (file <-> directory)\n", ch.Path)
		}
	}
	if b.Len() == 0 {
		return none
	}
	return b.String()
}

func humanMergeBanner(cs []merge.ConflictInfo, err error) string {
	if err != nil {
		return ""
	}
	unresolved := 0
	for _, c := range cs {
		if !c.Resolved {
			unresolved++
		}
	}
	return fmt.Sprintf("A merge is in progress: %d conflict(s), %d unresolved - see 'hgit conflicts'.\n", len(cs), unresolved)
}

var relWords = map[object.Relation]string{
	object.RelContinues: "continues", object.RelCorrects: "corrects",
	object.RelReverts: "reverts", object.RelReconciles: "reconciles",
}

func humanSee(h archive.Hash, res *repo.SeeResult) string {
	c := res.Commit
	var b strings.Builder
	fmt.Fprintf(&b, "commit  %s\ndate    %s\n", h.Hex(), formatTS(c.Timestamp))
	for _, p := range c.Parents {
		fmt.Fprintf(&b, "parent  %s\n", p.Hex())
	}
	if c.Relation != object.RelNone {
		word := relWords[c.Relation]
		if word == "" {
			word = fmt.Sprintf("relation %d", c.Relation)
		}
		fmt.Fprintf(&b, "%-7s %s", word, c.RelationTarget.Hex())
		if c.RelationEntity != 0 {
			fmt.Fprintf(&b, " (entity %016x)", c.RelationEntity)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n    %s\n\n", c.Message)
	for _, e := range res.Entries {
		name := e.Entry.Name
		if e.Entry.ChildType == archive.Tree {
			name += "/"
		}
		fmt.Fprintf(&b, "  %s%s\n", strings.Repeat("  ", e.Depth), name)
		if e.Missing {
			fmt.Fprintf(&b, "  %s  (missing tree object)\n", strings.Repeat("  ", e.Depth))
		}
	}
	return b.String()
}

var checkKinds = map[archive.Type]string{archive.Commit: "commit", archive.Tree: "tree", archive.Attrs: "attrs", archive.Conflict: "conflict"}

func humanCheck(rep check.Report) string {
	var b strings.Builder
	if uint64(rep.Objects) != rep.HeaderCount {
		fmt.Fprintf(&b, "warning: the header counts %d objects, the archive holds %d\n", rep.HeaderCount, rep.Objects)
	}
	if len(rep.HashBad) == 0 {
		fmt.Fprintf(&b, "%d objects, all intact (format version %d)\n", rep.Objects, rep.FormatVersion)
	} else {
		fmt.Fprintf(&b, "%d objects, %d CORRUPT (format version %d)\n", rep.Objects, len(rep.HashBad), rep.FormatVersion)
	}
	for _, br := range rep.Broken {
		fmt.Fprintf(&b, "broken reference: %s %s\n", strings.ReplaceAll(br.Kind, "_", " "), br.Hash.Hex())
	}
	for _, d := range rep.Dangling {
		kind := checkKinds[d.Type]
		if kind == "" {
			kind = "blob"
		}
		fmt.Fprintf(&b, "unreachable %s %s\n", kind, d.Hash.Hex())
	}
	if rep.RefsBroken == 0 && len(rep.HashBad) == 0 {
		b.WriteString("ok\n")
	}
	return b.String()
}

func humanAutos(as []merge.Auto) string {
	var b strings.Builder
	for _, a := range as {
		switch a.Kind {
		case merge.AutoTookTheirs:
			fmt.Fprintf(&b, "  took theirs: %s\n", a.Path)
		case merge.AutoDeleted:
			fmt.Fprintf(&b, "  deleted:     %s\n", a.Path)
		case merge.AutoRenamed:
			fmt.Fprintf(&b, "  renamed:     %s -> %s\n", a.Path, a.NewName)
		case merge.AutoRenameRefused:
			fmt.Fprintf(&b, "  renamed differently on both sides: %s\n", a.Path)
		}
	}
	return b.String()
}

func humanMergeErr(err error) string {
	var un *merge.UnresolvedError
	switch {
	case errors.As(err, &un):
		idx := make([]string, len(un.Indices))
		for i, n := range un.Indices {
			idx[i] = fmt.Sprint(n)
		}
		return fmt.Sprintf("merge: %d conflict(s) still unresolved (%s) - see 'hgit conflicts' and 'hgit resolve'",
			len(un.Indices), strings.Join(idx, ", "))
	case errors.Is(err, merge.ErrAmbiguousRename):
		return "merge refused: both sides renamed one file differently; nothing changed"
	case errors.Is(err, merge.ErrInProgress):
		return "a merge is already in progress - resolve it and 'hgit merge continue', or 'hgit merge abort'"
	}
	return err.Error()
}

func humanMergeResult(res merge.Result) string {
	switch {
	case res.UpToDate:
		return "Already up to date.\n"
	case res.FastForward:
		return "Fast-forwarded to " + short(res.Commit) + ".\n"
	case len(res.Conflicts) == 0:
		return "Merged: " + short(res.Commit) + ".\n"
	}
	var b strings.Builder
	for _, c := range res.Conflicts {
		fmt.Fprintf(&b, "  CONFLICT:    %s\n", c.Object.Path)
	}
	fmt.Fprintf(&b, "Merge stopped with %d conflict(s); nothing was committed.\n"+
		"Resolve each with 'hgit resolve', then run 'hgit merge continue' (or 'hgit merge abort').\n", len(res.Conflicts))
	return b.String()
}

func kindWords(k byte) string {
	var parts []string
	for _, w := range []struct {
		bit  byte
		word string
	}{{object.KindContent, "content"}, {object.KindMode, "mode"}, {object.KindType, "type"}} {
		if k&w.bit != 0 {
			parts = append(parts, w.word)
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, "+")
}

func humanConflicts(cs []merge.ConflictInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d conflict(s):\n", len(cs))
	for _, c := range cs {
		state := "unresolved"
		if c.Resolved {
			state = "resolved (" + resolutionWord(c) + ")"
		}
		if c.Object == nil {
			fmt.Fprintf(&b, "  [%d] %s: evidence object missing or unreadable - see 'hgit check'\n", c.Index, state)
			continue
		}
		fmt.Fprintf(&b, "  [%d] %s  %s (%s)\n", c.Index, c.Object.Path, state, kindWords(c.Object.Kind))
		for _, s := range []struct {
			name string
			side object.Side
		}{{"base", c.Object.Base}, {"ours", c.Object.Ours}, {"theirs", c.Object.Theirs}} {
			if !s.side.Present {
				fmt.Fprintf(&b, "        %-6s  absent\n", s.name)
				continue
			}
			kind := "file"
			if s.side.Type == archive.Tree {
				kind = "directory"
			}
			fmt.Fprintf(&b, "        %-6s  %s, %s, %s\n", s.name, kind, modeName(s.side.Mode), s.side.Hash.Hex()[:16])
		}
	}
	return b.String()
}
