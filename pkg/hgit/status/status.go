// Package status compares a working directory against HEAD's tree: the port
// of Status.HC's flat HgitStatus. It never prints; the exact serial tokens
// live in internal/cli/serial.go's SerialStatus.
//
// Change is shared with the (not yet ported) diff command - Diff.HC uses the
// same UNCHANGED/MODIFIED/RENAMED/NEW/DELETED/MODE_CHANGED vocabulary and the
// same rename-detection rules.
package status

import (
	"errors"
	"os"
	"path"
	"path/filepath"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/attrs"
	"github.com/VectorSophie/hgit-native/pkg/hgit/fossil"
	"github.com/VectorSophie/hgit-native/pkg/hgit/ignore"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/workdir"
)

// MaxFuzzyRenameBytes is Status.HC's fixed 512-byte-per-slot fuzzy-rename
// buffer (probe 108, experiments/108-status-large-file-support/): a new-on-
// disk candidate is buffered for FUZZY matching only when its tagged size
// (content plus the one-byte OBJ_BLOB tag) is <= 512, i.e. its own content is
// at most MaxFuzzyRenameBytes-1 (511) bytes. A candidate over that size still
// gets a real hash and a real classification - only the fuzzy-rename pass
// skips it, exactly as it does a genuinely unrelated file. This is a
// deliberate parity choice with a real ceiling, not a bug: see
// docs/porting-notes.md. The offer path (package offer) has no such ceiling.
const MaxFuzzyRenameBytes = 512

// MaxTreeDepth bounds StatusTree's recursion, the same guard offer.OfferTree
// uses for the same reason (no depth limit in the HolyC itself).
const MaxTreeDepth = offer.MaxTreeDepth

var (
	// ErrNoHead is Status.HC's "STATUS_ERR no_head_but_objects_exist": the
	// archive has records but the current path has no head.
	ErrNoHead = errors.New("status: no head but objects exist")
	// ErrTooDeep is StatusTree's own depth guard.
	ErrTooDeep = errors.New("status: directory tree deeper than MaxTreeDepth")
)

// FileSize is one entry of WorkDirList's placeholder "what's here" listing:
// name and size, exactly what Status.HC prints when the repository has no
// offerings yet.
type FileSize struct {
	Name string
	Size int64
}

// NoOfferingsYetError is Status.HC's rcount==0 branch: STATUS_NO_OFFERINGS_YET
// followed by WorkDirList(), carried here as data instead of being printed
// directly.
type NoOfferingsYetError struct{ Listing []FileSize }

func (e *NoOfferingsYetError) Error() string { return "status: repository has no offerings yet" }

// ChangeKind classifies one Change.
type ChangeKind int

const (
	Unchanged ChangeKind = iota
	Modified
	Renamed
	New
	Deleted
	ModeChanged
	TypeChanged // statustree only
)

// Change is one status/statustree (and, later, diff) line. Path is always
// the current/new name; OldPath is set only for Renamed. OldMode/NewMode are
// set only for ModeChanged.
type Change struct {
	Kind             ChangeKind
	Path             string
	OldPath          string
	OldMode, NewMode byte
}

// Status compares the files in work matching mask against HEAD's tree of the
// current path, the port of HgitStatus. The returned order is exactly the
// HolyC's own print order: UNCHANGED/MODIFIED (each immediately followed by
// its own MODE_CHANGED, if any) in the order workdir.List returns them, then
// RENAMED (exact-hash matches in HEAD-tree order, then fuzzy matches in
// HEAD-tree order), then the leftover NEW (disk-scan order) and DELETED
// (HEAD-tree order).
func Status(r *repo.Repo, work, mask string) ([]Change, error) {
	if len(r.Arc.Records) == 0 {
		listing, _ := listAll(work)
		return nil, &NoOfferingsYetError{Listing: listing}
	}
	tree, headAttrs, err := headState(r)
	if err != nil {
		return nil, err
	}

	ign := ignore.ParseIgnore(readRules(work, ".hgitignore"))
	att := attrs.ParseAttrs(readRules(work, ".hgitattributes"))

	files, err := workdir.List(work, mask)
	if err != nil {
		return nil, err
	}

	var changes []Change
	var news []newCand
	for _, f := range files {
		content, err := workdir.Read(work, f.Name)
		if err != nil {
			return nil, err
		}
		hash := archive.NewObject(archive.Blob, content).Hash

		entry, inTree := tree.Find(f.Name)
		if !inTree {
			if ign.IgnoredLast(f.Name) {
				continue
			}
			news = append(news, newCand{name: f.Name, hash: hash, content: content})
			continue
		}

		kind := Unchanged
		if entry.ChildHash != hash {
			kind = Modified
		}
		changes = append(changes, Change{Kind: kind, Path: f.Name})

		oldMode := headAttrs.FindMode(entry.EntityID)
		newMode := effectiveMode(att, f.Name, content)
		if oldMode != newMode {
			changes = append(changes, Change{Kind: ModeChanged, Path: f.Name, OldMode: oldMode, NewMode: newMode})
		}
	}

	// Pass 2: every HEAD-tree entry not found on disk at all, regardless of
	// mask - Status.HC walks the whole tree here, not just what find_mask
	// matched (see Status.HC's own pass-2 comment).
	var dels []delCand
	for _, e := range tree.Entries {
		if _, err := workdir.Read(work, e.Name); err != nil {
			dels = append(dels, delCand{name: e.Name, hash: e.ChildHash})
		}
	}

	changes = append(changes, matchRenames(r, dels, news)...)
	for _, n := range news {
		if !n.matched {
			changes = append(changes, Change{Kind: New, Path: n.name})
		}
	}
	for _, d := range dels {
		if !d.matched {
			changes = append(changes, Change{Kind: Deleted, Path: d.name})
		}
	}
	return changes, nil
}

// newCand is a not-yet-classified on-disk file: a possible NEW, or a rename
// target.
type newCand struct {
	name    string
	hash    archive.Hash
	content []byte
	matched bool
}

// delCand is a HEAD-tree entry no longer found on disk: a possible DELETED,
// or a rename source.
type delCand struct {
	name    string
	hash    archive.Hash
	matched bool
}

// fuzzyTarget returns content's fuzzy-match candidate, or nil (never matches,
// per fossil.SimilarityPercent) when it is too large to have been buffered -
// see MaxFuzzyRenameBytes.
func fuzzyTarget(content []byte) []byte {
	if len(content) > MaxFuzzyRenameBytes-1 {
		return nil
	}
	return content
}

// matchRenames ports the exact-hash pass then the fuzzy pass, in that order,
// mutating dels/news' matched flags and returning the RENAMED changes in the
// HolyC's own print order (all exact matches, HEAD-tree order, then all fuzzy
// matches, HEAD-tree order). "First/best match wins" - no cross-candidate
// disambiguation - exactly as ADR 0009 documents for Offer.HC's own version.
func matchRenames(r *repo.Repo, dels []delCand, news []newCand) []Change {
	var out []Change
	for i := range dels {
		for j := range news {
			if news[j].matched {
				continue
			}
			if dels[i].hash == news[j].hash {
				out = append(out, Change{Kind: Renamed, Path: news[j].name, OldPath: dels[i].name})
				dels[i].matched, news[j].matched = true, true
				break
			}
		}
	}
	for i := range dels {
		if dels[i].matched {
			continue
		}
		rec, ok := r.Get(dels[i].hash)
		if !ok {
			continue
		}
		source := rec.Content()
		bestJ, bestSim := -1, -1
		for j := range news {
			if news[j].matched {
				continue
			}
			if sim := fossil.SimilarityPercent(source, fuzzyTarget(news[j].content)); sim > bestSim {
				bestSim, bestJ = sim, j
			}
		}
		if bestJ >= 0 && bestSim >= fossil.RenameSimilarityThreshold {
			out = append(out, Change{Kind: Renamed, Path: news[bestJ].name, OldPath: dels[i].name})
			dels[i].matched, news[bestJ].matched = true, true
		}
	}
	return out
}

// effectiveMode is ADR 0015's mode resolution, exactly as offer.go's own
// addFile uses it: an explicit .hgitattributes rule wins, otherwise auto-
// detected binary is OR'd in. Executable is never auto-detected.
func effectiveMode(att *attrs.AttrRules, rel string, content []byte) byte {
	mode, explicit := att.Mode(rel)
	if !explicit && attrs.DetectBinary(content) {
		mode |= object.ModeBinary
	}
	return mode
}

// headState resolves the current path's head commit, its tree and its attrs
// list (nil if none) - shared by Status and StatusTree.
func headState(r *repo.Repo) (*object.Tree, *object.Attrs, error) {
	h, ok := r.Head(r.CurrentPath())
	if !ok {
		return nil, nil, ErrNoHead
	}
	c, err := r.Commit(h)
	if err != nil {
		return nil, nil, err
	}
	t, err := r.Tree(c.Tree)
	if err != nil {
		return nil, nil, err
	}
	var a *object.Attrs
	if c.Attrs != nil {
		if aa, err := r.Attrs(*c.Attrs); err == nil {
			a = aa
		}
	}
	return t, a, nil
}

// find is object.Tree.Find, nil-tree safe.
func find(t *object.Tree, name string) (object.Entry, bool) {
	if t == nil {
		return object.Entry{}, false
	}
	return t.Find(name)
}

// readRules returns a rules file's text, or "" when absent/unreadable -
// IgnoreLoad/AttrsRulesLoad's own treatment of a missing file.
func readRules(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(b)
}

// listAll is WorkDirList: every real entry directly inside dir, with its
// size, sorted by name. Unlike Go's os.ReadDir, TempleOS's FilesFind("*",0)
// also yields "." and ".."; WorkDirList never filters them (its own comment
// says a caller that cares should), but nothing here can produce them in the
// first place, so there is nothing to skip - a real, harmless deviation
// (docs/porting-notes.md).
func listAll(dir string) ([]FileSize, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]FileSize, 0, len(entries))
	for _, e := range entries {
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		out = append(out, FileSize{Name: e.Name(), Size: size})
	}
	return out, nil
}

// isDir reports whether p exists and is a directory.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// isNotExist reports whether err is (or wraps) "does not exist".
func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

// joinRel joins a status-relative directory and name the way every walk here
// threads it: path.Join, never filepath - these are always slash-separated,
// repo-relative names, not OS paths.
func joinRel(dir, name string) string { return path.Join(dir, name) }
