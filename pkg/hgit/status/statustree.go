// statustree.go ports StatusTreeWalk/HgitStatusTree: the recursive directory
// walk `hgit statustree` uses in place of HgitStatus's single flat pass.
package status

import (
	"path/filepath"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/attrs"
	"github.com/VectorSophie/hgit-native/pkg/hgit/ignore"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/workdir"
)

// StatusTree walks dir and every subdirectory under it recursively, the port
// of HgitStatusTree. Unlike Status, an unchanged file is never reported at
// all - Status.HC's StatusTreeWalk simply never prints STATUS_UNCHANGED
// (only STATUS_MODIFIED, when content differs); mode is still checked and
// reported independently either way. Rename detection (exact-hash, then
// fuzzy) is scoped to one directory level at a time and blob-only - a file
// moved between directories is not detected as a rename (ADR 0010's own
// deliberately deferred item).
func StatusTree(r *repo.Repo, dir string) ([]Change, error) {
	if len(r.Arc.Records) == 0 {
		listing, _ := listAll(dir)
		return nil, &NoOfferingsYetError{Listing: listing}
	}
	tree, headAttrs, err := headState(r)
	if err != nil {
		return nil, err
	}

	w := &treeWalker{
		r:         r,
		root:      dir,
		ign:       ignore.ParseIgnore(readRules(dir, ".hgitignore")),
		att:       attrs.ParseAttrs(readRules(dir, ".hgitattributes")),
		headAttrs: headAttrs,
	}
	return w.walk("", tree, 0)
}

type treeWalker struct {
	r         *repo.Repo
	root      string
	ign       *ignore.Rules
	att       *attrs.AttrRules
	headAttrs *object.Attrs
}

// walk is StatusTreeWalk for one directory level. relDir is the directory's
// path relative to the walked root ("" at the root, slash separated); old is
// the tree that occupied the same relative position in HEAD, or nil.
func (w *treeWalker) walk(relDir string, old *object.Tree, depth int) ([]Change, error) {
	if depth > MaxTreeDepth {
		return nil, ErrTooDeep
	}
	nodes, err := listDirSafe(filepath.Join(w.root, filepath.FromSlash(relDir)))
	if err != nil {
		return nil, err
	}

	var changes []Change
	var news []newCand
	var dels []delCand

	for _, n := range nodes {
		rel := joinRel(relDir, n.Name)
		oldEntry, tracked := find(old, n.Name)

		if n.IsDir {
			switch {
			case tracked && oldEntry.ChildType == archive.Tree:
				oldSub, _ := w.r.Tree(oldEntry.ChildHash) // unresolved -> nil, same as HEAD's own NULL fallback
				child, err := w.walk(rel, oldSub, depth+1)
				if err != nil {
					return nil, err
				}
				changes = append(changes, child...)
			case tracked:
				changes = append(changes, Change{Kind: TypeChanged, Path: rel})
			case w.ign.IgnoredLast(rel):
				// ADR 0014: not tracked, and ignored - never descended into.
			default:
				child, err := w.walk(rel, nil, depth+1)
				if err != nil {
					return nil, err
				}
				changes = append(changes, child...)
			}
			continue
		}

		content, err := workdir.Read(w.root, rel)
		if err != nil {
			return nil, err
		}
		hash := archive.NewObject(archive.Blob, content).Hash

		switch {
		case tracked && oldEntry.ChildType == archive.Blob:
			if hash != oldEntry.ChildHash {
				changes = append(changes, Change{Kind: Modified, Path: rel})
			}
			oldMode := w.headAttrs.FindMode(oldEntry.EntityID)
			newMode := effectiveMode(w.att, rel, content)
			if oldMode != newMode {
				changes = append(changes, Change{Kind: ModeChanged, Path: rel, OldMode: oldMode, NewMode: newMode})
			}
		case tracked:
			changes = append(changes, Change{Kind: TypeChanged, Path: rel})
		case w.ign.IgnoredLast(rel):
			// ADR 0014: not tracked, and ignored - never reported as NEW.
		default:
			news = append(news, newCand{name: rel, hash: hash, content: fuzzyTarget(content)})
		}
	}

	// Deletion pass: a HEAD-tree-only entry at this level - gone from disk
	// entirely, or replaced by the other kind (still reported by the
	// new-node pass above as TYPE_CHANGED, so skipped here to avoid a
	// duplicate line).
	//
	// For an old TREE-typed entry, "is it still real" is Status.HC's own
	// FilesFind("<dir_path><tname>*", 0) != NULL - a PREFIX glob against
	// this level's own disk entries, not an exact-name/is-a-directory
	// check. hasPrefixSibling reproduces that exactly, including its two
	// real consequences: a same-named plain file satisfies it (so a
	// directory-to-file type change reports only TYPE_CHANGED, never a
	// crash from trying to list a file as a directory), and an unrelated
	// sibling whose name merely starts with the same prefix (e.g. "Sub"
	// vs. "SubNotes.txt") also satisfies it, suppressing the deletion
	// recursion into "Sub" even though "Sub" itself is genuinely gone.
	if old != nil {
		for _, e := range old.Entries {
			rel := joinRel(relDir, e.Name)
			if e.ChildType == archive.Tree {
				if hasPrefixSibling(nodes, e.Name) {
					continue // still "real" per Status.HC's own prefix-glob check
				}
				oldSub, _ := w.r.Tree(e.ChildHash)
				child, err := w.walk(rel, oldSub, depth+1)
				if err != nil {
					return nil, err
				}
				changes = append(changes, child...)
				continue
			}
			if _, err := workdir.Read(w.root, rel); err != nil {
				dels = append(dels, delCand{name: rel, hash: e.ChildHash})
			}
		}
	}

	changes = append(changes, matchRenames(w.r, dels, news)...)
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

// listDirSafe is workdir.ListDir, except a missing directory (already
// deleted from disk - the deletion pass above recurses into one on purpose)
// is an empty listing, not an error, mirroring FilesFind's own behaviour on
// a directory that no longer exists.
func listDirSafe(dir string) ([]workdir.Node, error) {
	nodes, err := workdir.ListDir(dir)
	if isNotExist(err) {
		return nil, nil
	}
	return nodes, err
}
