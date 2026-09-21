// Package workdir lists the files in a working directory that an offer or a
// status considers: the port of Offer.HC's FilesFind enumeration, flat
// (find_mask) and recursive (TreeBuildRecursive's own per-level scan). It
// never prints, and it never reads a file the caller has not asked for.
package workdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Node is one entry of a directory: a name and whether it is a directory.
// Content is not read here - the caller decides what to read, after the
// ignore rules have had their say.
type Node struct {
	Name  string // the name inside its directory, never a path
	IsDir bool
}

// ReadError names a working-directory file that could not be read. An offer
// fails on it rather than silently leaving the file out of the commit; the
// name is relative to the offered root.
type ReadError struct {
	Name string
	Err  error
}

func (e *ReadError) Error() string { return fmt.Sprintf("workdir: cannot read %s: %v", e.Name, e.Err) }
func (e *ReadError) Unwrap() error { return e.Err }

// Match reports whether a FilesFind-style mask matches name. Only '*' (any
// run of bytes, including none) and '?' (exactly one byte) are special;
// everything else is literal and compared byte for byte, so matching is case
// sensitive.
func Match(mask, name string) bool {
	pi, si := 0, 0
	starP, starS := -1, -1
	for si < len(name) {
		switch {
		case pi < len(mask) && (mask[pi] == '?' || mask[pi] == name[si]):
			pi++
			si++
		case pi < len(mask) && mask[pi] == '*':
			starP, starS = pi, si
			pi++
		case starP >= 0: // backtrack: let the last '*' swallow one more byte
			starS++
			pi, si = starP+1, starS
		default:
			return false
		}
	}
	for pi < len(mask) && mask[pi] == '*' {
		pi++
	}
	return pi == len(mask)
}

// ListDir returns dir's entries sorted by name, as the tree walk needs them.
// A symlink is never followed: os.ReadDir reports it by its own type, so a
// symlink to a directory is a plain file here and the walk can never loop.
// Reading such a "file" then fails with a ReadError rather than descending.
func ListDir(dir string) ([]Node, error) {
	entries, err := os.ReadDir(dir) // sorted by name
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(entries))
	for _, e := range entries {
		out = append(out, Node{Name: e.Name(), IsDir: e.IsDir()})
	}
	return out, nil
}

// List returns dir's immediate files whose name matches mask, sorted by name.
// Subdirectories are skipped: FilesFind is non-recursive and a flat offer has
// no subdirectory entries. A mask that matches nothing is not an error.
func List(dir, mask string) ([]Node, error) {
	all, err := ListDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, n := range all {
		if !n.IsDir && Match(mask, n.Name) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Read returns the content of the file at rel inside root. rel is a
// slash-separated path relative to the offered root, and is what a failure
// names.
func Read(root, rel string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, &ReadError{Name: rel, Err: err}
	}
	return b, nil
}
