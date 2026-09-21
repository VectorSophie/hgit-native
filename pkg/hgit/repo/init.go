package repo

import (
	"errors"
	"os"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

// InitFormatVersion is the format version a freshly created repository
// declares (Init.HC, last bumped by ADR 0016).
const InitFormatVersion = 4

// ErrExists is returned when something already occupies the path.
var ErrExists = errors.New("repo: a file already exists at that path")

// Init creates an empty repository at path: the 16-byte header, version 4,
// zero objects, and nothing else. Like HgitInit it never overwrites an
// existing file. The <path>.m metadata file is created lazily, by the first
// Save, so a repository with no metadata yet is just the one file.
func Init(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrExists
		}
		return err
	}
	if _, err := f.Write(archive.Header{Version: InitFormatVersion}.Marshal()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
