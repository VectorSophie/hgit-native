// Package views renders the information of the TempleOS DolDoc views
// (Graph.HC, HistoryDoc.HC, ReconcileDoc.HC, ConflictDoc.HC) as plain text.
// Only the information is ported, not the DolDoc rendering: colours and links
// are dropped, and a collapsible $TR$ node becomes a "[+] label" line whose
// children are indented six spaces further (see docs/porting-notes.md).
package views

import (
	"errors"
	"fmt"
	"strings"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/attrs"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

const indentStep = 6

// doc is a plain-text document with a current indentation level.
type doc struct {
	b      strings.Builder
	indent int
}

func (d *doc) line(format string, a ...any) {
	d.b.WriteString(strings.Repeat(" ", d.indent))
	fmt.Fprintf(&d.b, format, a...)
	d.b.WriteByte('\n')
}

// node is one $TR$ widget: its label, then body's lines indented beneath it.
func (d *doc) node(label string, body func()) {
	d.line("[+] %s", label)
	d.indent += indentStep
	body()
	d.indent -= indentStep
}

type link struct {
	hash   archive.Hash
	commit *object.Commit
}

// chain walks a first-parent chain from head, newest first, stopping silently
// at a missing, non-commit or undecodable object as the HolyC walks do (and,
// natively, at a cycle).
func chain(r *repo.Repo, head archive.Hash) []link {
	var out []link
	seen := map[archive.Hash]bool{}
	for cur := head; !seen[cur]; {
		seen[cur] = true
		c, err := r.Commit(cur)
		if err != nil {
			break
		}
		out = append(out, link{cur, c})
		if len(c.Parents) == 0 {
			break
		}
		cur = c.Parents[0]
	}
	return out
}

func reverse(ls []link) {
	for i, j := 0, len(ls)-1; i < j; i, j = i+1, j-1 {
		ls[i], ls[j] = ls[j], ls[i]
	}
}

// Graph is HgitGraph: main's first-parent chain as the trunk, and every other
// declared path as its own node right after the main commit its chain first
// reaches, holding that path's own commits (oldest first). A path whose chain
// never reaches main is left out.
func Graph(r *repo.Repo) (string, error) {
	var d doc
	if len(r.Arc.Records) == 0 {
		d.line("(no offerings yet)")
		return d.b.String(), nil
	}
	d.line("hgit history graph")
	d.line("")
	mainHead, ok := r.Head("main")
	if !ok {
		d.line("(no offerings yet)")
		return d.b.String(), nil
	}
	trunk := chain(r, mainHead)
	reverse(trunk)
	pos := map[archive.Hash]int{}
	for i, l := range trunk {
		pos[l.hash] = i
	}

	type branch struct {
		name    string
		commits []link
	}
	forks := map[int][]branch{}
	for _, name := range r.PathList() {
		if name == "main" {
			continue
		}
		head, ok := r.Head(name)
		if !ok {
			continue
		}
		own := chain(r, head)
		for i, l := range own {
			if at, ok := pos[l.hash]; ok {
				own = own[:i]
				reverse(own)
				forks[at] = append(forks[at], branch{name, own})
				break
			}
		}
	}

	commitLine := func(l link) { d.line("%s %s", l.hash.Hex()[:10], l.commit.Message) }
	d.node(fmt.Sprintf("main, %d commits", len(trunk)), func() {
		for i, l := range trunk {
			commitLine(l)
			for _, b := range forks[i] {
				d.node(fmt.Sprintf("[%s] %d commits", b.name, len(b.commits)), func() {
					for _, c := range b.commits {
						commitLine(c)
					}
				})
			}
		}
	})
	return d.b.String(), nil
}

// HistoryDoc is HgitHistoryDoc: the current path's first-parent chain, newest
// first, each commit as its 12-hex hash prefix, timestamp and message.
func HistoryDoc(r *repo.Repo) (string, error) {
	var d doc
	d.line("hgit history")
	d.line("")
	head, ok := r.Head(r.CurrentPath())
	if !ok {
		d.line("(no offerings yet)")
		return d.b.String(), nil
	}
	for _, l := range chain(r, head) {
		d.line("%s %d %s", l.hash.Hex()[:12], l.commit.Timestamp, l.commit.Message)
	}
	return d.b.String(), nil
}

var relNames = map[object.Relation]string{
	object.RelContinues:  "CONTINUES",
	object.RelCorrects:   "CORRECTS",
	object.RelReverts:    "REVERTS",
	object.RelReconciles: "RECONCILES",
}

// emitCommit is ReconcileEmitCommit: the commit's header and, if it carries a
// relation, one node for it. It reports whether there was a relation.
func emitCommit(d *doc, r *repo.Repo, l link) bool {
	c := l.commit
	d.line("%s %s", l.hash.Hex()[:12], c.Message)
	d.line("")
	if c.Relation == object.RelNone {
		return false
	}
	name, ok := relNames[c.Relation]
	if !ok {
		name = "UNKNOWN"
	}
	d.node("relation: "+name, func() {
		d.line("target: %s", c.RelationTarget.Hex())
		if t, err := r.Commit(c.RelationTarget); err == nil {
			d.line("target message: %s", t.Message)
		}
		if c.RelationEntity != 0 {
			d.line("scoped to entity: %016x", c.RelationEntity)
		}
	})
	return true
}

// ReconcileDoc is HgitReconcileDoc: one commit and its relation, if any.
func ReconcileDoc(r *repo.Repo, target archive.Hash) (string, error) {
	var d doc
	d.line("hgit reconciliation")
	d.line("")
	c, err := r.Commit(target)
	var nt *repo.NotTypeError
	switch {
	case errors.Is(err, repo.ErrNotFound):
		d.line("(commit not found)")
	case errors.As(err, &nt):
		d.line("(not a commit)")
	case err != nil: // native-only: a commit that does not decode
		return "", err
	case !emitCommit(&d, r, link{target, c}):
		d.line("(no relation on this commit)")
	}
	return d.b.String(), nil
}

// ReconcileOverview is HgitReconcileOverview: every relation-carrying commit
// on the current path's first-parent chain, newest first; plain commits are
// left out.
func ReconcileOverview(r *repo.Repo) (string, error) {
	var d doc
	d.line("hgit reconciliation overview")
	d.line("")
	head, ok := r.Head(r.CurrentPath())
	if !ok {
		d.line("(no offerings yet)")
		return d.b.String(), nil
	}
	found := 0
	for _, l := range chain(r, head) {
		var one doc
		if emitCommit(&one, r, l) {
			d.b.WriteString(one.b.String())
			found++
		}
	}
	if found == 0 {
		d.line("(no relations found in this repo's history)")
	}
	return d.b.String(), nil
}

const (
	maxTextLines = 12 // CDOC_MAX_LINES
	maxTextCols  = 70 // CDOC_MAX_LINE_CHARS
	textIndent   = 4  // a side's content, beneath its own line
)

// ConflictDoc is HgitConflictDoc: the current path's in-progress merge, one
// node per conflict whose evidence decodes, each side shown structurally,
// with the resolve commands suggested but never chosen.
func ConflictDoc(r *repo.Repo) (string, error) {
	var d doc
	d.line("hgit conflicts")
	d.line("")
	cur := r.CurrentPath()
	rec, ok := r.Meta.Find(cur, meta.TagMergeState)
	if !ok {
		d.line("(no merge in progress on this path)")
		return d.b.String(), nil
	}
	st, err := meta.DecodeMergeState(rec.Payload)
	if err != nil {
		return "", err
	}
	cs, err := merge.Conflicts(r)
	if err != nil {
		return "", err
	}
	d.line("Merging path %s into %s (repo: %s)", st.OtherPath, cur, r.Path)
	d.line("ours head:   %s", st.Ours.Hex()[:12])
	d.line("theirs head: %s", st.Theirs.Hex()[:12])
	d.line("")
	for _, c := range cs {
		o := c.Object
		if o == nil {
			continue
		}
		state := "conflict"
		if c.Resolved {
			state = "resolved"
		}
		d.node(fmt.Sprintf("%d  %s  [%s]", c.Index, o.Path, state), func() {
			var kinds []string
			for _, k := range []struct {
				bit  byte
				name string
			}{{object.KindContent, "content"}, {object.KindMode, "mode"}, {object.KindType, "file-vs-directory"}} {
				if o.Kind&k.bit != 0 {
					kinds = append(kinds, k.name)
				}
			}
			d.line("entity %016x   kind: %s", o.EntityID, strings.Join(kinds, " "))
			side(&d, r, "base  ", o.Base)
			side(&d, r, "ours  ", o.Ours)
			side(&d, r, "theirs", o.Theirs)
			if !c.Resolved {
				d.line("options: hgit resolve <repo> %d take-ours | take-theirs (nothing chosen yet)", c.Index)
				return
			}
			word := "theirs"
			switch {
			case c.Resolution == archive.Hash{}:
				word = "deletion"
			case o.Ours.Present && o.Ours.Hash == c.Resolution:
				word = "ours"
			}
			d.line("resolved to: %s", word)
		})
	}
	d.line("")
	d.line("Finish with: hgit merge continue <repo>   Discard with: hgit merge abort <repo>")
	return d.b.String(), nil
}

// side is CdAppendSide.
func side(d *doc, r *repo.Repo, label string, s object.Side) {
	switch {
	case !s.Present:
		d.line("%s: (absent - deleted or not yet added on this side)", label)
		return
	case s.Type == archive.Tree:
		d.line("%s: (directory)", label)
		return
	}
	var modes []string
	if s.Mode&object.ModeBinary != 0 {
		modes = append(modes, "binary")
	}
	if s.Mode&object.ModeExecutable != 0 {
		modes = append(modes, "executable")
	}
	if s.Mode == 0 {
		modes = append(modes, "text")
	}
	d.line("%s: %s  mode=%s", label, s.Hash.Hex()[:12], strings.Join(modes, " "))
	d.indent += textIndent
	defer func() { d.indent -= textIndent }()
	rec, ok := r.Get(s.Hash)
	switch {
	case !ok:
		d.line("(stored object missing - see hgit check)")
	case s.Mode&object.ModeBinary != 0 || attrs.DetectBinary(rec.Content()):
		d.line("(binary, %d bytes)", len(rec.Content()))
	default:
		text(d, rec.Content())
	}
}

// text is CdAppendText: the first maxTextLines lines, each cut at maxTextCols
// characters, non-printables shown as '.', then "... (more)" if anything was
// left unread.
func text(d *doc, b []byte) {
	var cur []byte
	lines, i := 0, 0
	for ; i < len(b) && lines < maxTextLines; i++ {
		switch c := b[i]; {
		case c == '\n':
			d.line("%s", cur)
			cur = cur[:0]
			lines++
		case len(cur) >= maxTextCols:
		case c < 32 || c > 126:
			cur = append(cur, '.')
		default:
			cur = append(cur, c)
		}
	}
	if len(cur) > 0 {
		d.line("%s", cur)
	}
	if i < len(b) {
		d.line("... (more)")
	}
}
