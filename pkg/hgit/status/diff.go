// diff.go ports Diff.HC: `hgit diff <repo> <commit>`, one commit's tree
// against its own parent's (the FIRST parent for a merge commit, the same
// convention git show/git log -p use - probe 111). It lives in package
// status, not a package of its own, because Diff.HC is Status.HC's
// committed-trees counterpart down to the rename passes: it reuses Change,
// the rename matcher and the mode lookup unchanged, with no exported
// plumbing invented to share them.
//
// One real difference from Status.HC, stated in Diff.HC's own header and
// visible in its rename pass 2: both sides here are already-stored blobs,
// resolved through the index on demand, so there is no equivalent of
// Status.HC's 512-byte fuzzy-rename buffer (MaxFuzzyRenameBytes). Diff
// compares candidates of any size.
package status

import (
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// Diff reports what commit changed relative to its first parent, the port of
// HgitDiff. A root commit has no parent, so every entry is New. The returned
// order is the HolyC's own print order, one directory level at a time:
// per new-tree entry (recursed subtrees inline, then TYPE_CHANGED/MODIFIED
// and its own MODE_CHANGED), then wholly deleted subdirectories, then the
// renames (exact, then fuzzy), then the leftover New and Deleted.
//
// The commit lookup fails with repo.ErrNotFound or *repo.NotTypeError, and
// its root tree with repo.ErrTreeNotFound - HgitDiff's own three DIFF_ERR
// cases. A broken reference deeper in (a nested tree hash absent from the
// archive) is not an error here: DiffResolveTreeByHash returns FALSE and
// HgitDiff carries on with that side treated as an empty tree, leaving
// broken references for `check` to report.
func Diff(r *repo.Repo, commit archive.Hash) ([]Change, error) {
	c, err := r.Commit(commit)
	if err != nil {
		return nil, err
	}
	newTree, err := r.Tree(c.Tree)
	if err != nil {
		return nil, repo.ErrTreeNotFound
	}
	d := &differ{r: r, newAttrs: commitAttrs(r, c)}

	var oldTree *object.Tree
	if len(c.Parents) > 0 {
		if p, err := r.Commit(c.Parents[0]); err == nil {
			oldTree, _ = r.Tree(p.Tree) // unresolved -> nil, the HolyC's own NULL
			d.oldAttrs = commitAttrs(r, p)
		}
	}
	return d.walk(oldTree, newTree, "", 0)
}

// differ carries what every level of the walk shares: the archive, and both
// commits' attrs lists (entity IDs are unique repo-wide, so the same two
// lists serve every nesting depth).
type differ struct {
	r                  *repo.Repo
	oldAttrs, newAttrs *object.Attrs
}

// walk is DiffPrintTreeChanges for one directory level. Either side may be
// nil - a valid base case, not a special case: that is how a wholly new or
// deleted (or unresolvable) subdirectory is recursed into.
func (d *differ) walk(old, cur *object.Tree, prefix string, depth int) ([]Change, error) {
	if depth > MaxTreeDepth {
		return nil, ErrTooDeep
	}
	var changes []Change
	var news []newCand
	var dels []delCand

	if cur != nil {
		for _, e := range cur.Entries {
			full := prefix + e.Name
			oldEntry, found := find(old, e.Name)

			if e.ChildType == archive.Tree {
				switch {
				case found && oldEntry.ChildType == archive.Tree:
					if oldEntry.ChildHash == e.ChildHash {
						continue // identical subtree: nothing changed anywhere below
					}
					child, err := d.recurse(d.tree(oldEntry.ChildHash), d.tree(e.ChildHash), full, depth)
					if err != nil {
						return nil, err
					}
					changes = append(changes, child...)
				case found:
					changes = append(changes, Change{Kind: TypeChanged, Path: full})
				default:
					child, err := d.recurse(nil, d.tree(e.ChildHash), full, depth)
					if err != nil {
						return nil, err
					}
					changes = append(changes, child...)
				}
				continue
			}

			switch {
			case found && oldEntry.ChildType == archive.Blob:
				if oldEntry.ChildHash != e.ChildHash {
					changes = append(changes, Change{Kind: Modified, Path: full})
				}
				// ADR 0015: mode is a separate dimension from content,
				// checked whatever the content verdict above was.
				oldMode := d.oldAttrs.FindMode(oldEntry.EntityID)
				newMode := d.newAttrs.FindMode(e.EntityID)
				if oldMode != newMode {
					changes = append(changes, Change{Kind: ModeChanged, Path: full, OldMode: oldMode, NewMode: newMode})
				}
			case found:
				changes = append(changes, Change{Kind: TypeChanged, Path: full})
			default:
				news = append(news, newCand{name: full, hash: e.ChildHash, content: d.content(e.ChildHash)})
			}
		}
	}

	// Deletion pass over the old tree: an entry still present in the new
	// tree by name (any type) was already handled above.
	if old != nil {
		for _, e := range old.Entries {
			if _, stillThere := find(cur, e.Name); stillThere {
				continue
			}
			full := prefix + e.Name
			if e.ChildType == archive.Tree {
				child, err := d.recurse(d.tree(e.ChildHash), nil, full, depth)
				if err != nil {
					return nil, err
				}
				changes = append(changes, child...)
				continue
			}
			dels = append(dels, delCand{name: full, hash: e.ChildHash})
		}
	}

	changes = append(changes, matchRenames(d.r, dels, news)...)
	for _, n := range news {
		if !n.matched {
			changes = append(changes, Change{Kind: New, Path: n.name})
		}
	}
	for _, del := range dels {
		if !del.matched {
			changes = append(changes, Change{Kind: Deleted, Path: del.name})
		}
	}
	return changes, nil
}

// recurse descends one directory level, prefixing names with "<full>/".
func (d *differ) recurse(old, cur *object.Tree, full string, depth int) ([]Change, error) {
	return d.walk(old, cur, full+"/", depth+1)
}

// tree is DiffResolveTreeByHash: the tree stored under h, or nil (an empty
// tree, never an error) when it is not in the archive or does not parse.
func (d *differ) tree(h archive.Hash) *object.Tree {
	t, err := d.r.Tree(h)
	if err != nil {
		return nil
	}
	return t
}

// content is the blob stored under h, or nil when it is not in the archive -
// the HolyC skips such a candidate in its fuzzy pass, and nil content can
// never reach the similarity threshold either (fossil.SimilarityPercent
// scores an empty target 0).
func (d *differ) content(h archive.Hash) []byte {
	rec, ok := d.r.Get(h)
	if !ok {
		return nil
	}
	return rec.Content()
}

// commitAttrs is a commit's own OBJ_ATTRS list, or nil when it has none or
// the object is missing - HgitDiff leaves the pointer NULL either way.
func commitAttrs(r *repo.Repo, c *object.Commit) *object.Attrs {
	if c.Attrs == nil {
		return nil
	}
	a, err := r.Attrs(*c.Attrs)
	if err != nil {
		return nil
	}
	return a
}
