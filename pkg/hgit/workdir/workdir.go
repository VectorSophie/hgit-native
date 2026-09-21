// Package workdir lists the files in a working directory that a flat offer or
// status considers: the port of WorkDir.HC's FilesFind enumeration and the
// find_mask handling in Offer.HC. It never prints.
package workdir

import (
	"os"
	"path/filepath"
)

// File is one matched working-directory file.
type File struct {
	Name    string // the name inside dir, never a path
	Content []byte
}

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

// List returns dir's immediate files whose name matches mask, sorted by name,
// each with its content. Subdirectories are skipped: FilesFind is
// non-recursive and a flat offer has no subdirectory entries. A mask that
// matches nothing is not an error; an unreadable directory or file is.
func List(dir, mask string) ([]File, error) {
	entries, err := os.ReadDir(dir) // sorted by name
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || !Match(mask, e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, File{Name: e.Name(), Content: b})
	}
	return out, nil
}
