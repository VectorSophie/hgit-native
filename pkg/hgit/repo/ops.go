package repo

import (
	"errors"
	"os"

	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
)

// MaxPathName is Paths.HC's HGIT_MAX_PATH_NAME: a path name must be shorter
// than this, so 63 bytes is the longest one PathNew, SetHead and PathGo
// accept.
const MaxPathName = 64

var (
	ErrBadPathName   = errors.New("repo: empty or over-long path name")
	ErrPathExists    = errors.New("repo: path already exists")
	ErrNoSuchPath    = errors.New("repo: no such path")
	ErrCloseMain     = errors.New("repo: main cannot be closed")
	ErrNothingToUndo = errors.New("repo: nothing to undo")
	ErrNothingToRedo = errors.New("repo: nothing to redo")
	ErrBadOpIndex    = errors.New("repo: no operation at that index")
)

// PathExists reports whether name is "main" (always implicit) or declared.
func (r *Repo) PathExists(name string) bool {
	if name == "main" {
		return true
	}
	_, ok := r.Meta.Find(name, meta.TagPathDeclared)
	return ok
}

// PathList is MetaPathList's order: "main" first, then each declared path in
// creation order.
func (r *Repo) PathList() []string {
	out := []string{"main"}
	for _, rec := range r.Meta.Records {
		if rec.Tag == meta.TagPathDeclared {
			out = append(out, rec.Name)
		}
	}
	return out
}

// PathNew declares name, pointing its HEAD at the current path's HEAD right
// now - all-zero if the current path has no offerings yet, which PathNew
// records as an all-zero HEAD rather than as no HEAD at all (MetaWriteHead).
// It does not switch the current path.
func (r *Repo) PathNew(name string) error {
	if len(name) == 0 || len(name) >= MaxPathName {
		return ErrBadPathName
	}
	if r.PathExists(name) {
		return ErrPathExists
	}
	head, _ := r.Head(r.CurrentPath())
	if err := r.SetHead(name, head); err != nil {
		return err
	}
	r.Meta.Append(name, meta.TagPathDeclared, nil)
	return r.Save()
}

// PathGo switches the current path to name.
func (r *Repo) PathGo(name string) error {
	if !r.PathExists(name) {
		return ErrNoSuchPath
	}
	if len(name) >= MaxPathName {
		return ErrNameTooLong
	}
	r.Meta.Set("", meta.TagCurrent, []byte(name))
	return r.Save()
}

// PathClose un-declares name: it stops being listed and `path go` on it
// fails. Its HEAD and operation-log records are left behind, orphaned but
// intact. Closing the current path switches back to "main". "main" itself
// cannot be closed.
func (r *Repo) PathClose(name string) error {
	if name == "main" {
		return ErrCloseMain
	}
	if !r.PathExists(name) {
		return ErrNoSuchPath
	}
	for {
		if _, ok := r.Meta.PopLast(name, meta.TagPathDeclared); !ok {
			break
		}
	}
	if r.CurrentPath() == name {
		r.Meta.Set("", meta.TagCurrent, []byte("main"))
	}
	return r.Save()
}

// AppendOp logs one HEAD transition for path and clears that path's redo log,
// since new work invalidates whatever was undone (OpLogAppend). It does not
// save; the caller does, once.
func (r *Repo) AppendOp(path string, e meta.OpLogEntry) {
	r.Meta.Append(path, meta.TagOpLog, e.Encode())
	for {
		if _, ok := r.Meta.PopLast(path, meta.TagRedoLog); !ok {
			return
		}
	}
}

// Undo pops the current path's newest operation, moves HEAD back to its prev
// head and pushes that same entry onto the redo log.
func (r *Repo) Undo() error {
	p := r.CurrentPath()
	rec, ok := r.Meta.PopLast(p, meta.TagOpLog)
	if !ok {
		return ErrNothingToUndo
	}
	e, err := meta.DecodeOpLog(rec.Payload)
	if err != nil {
		return err
	}
	if err := r.SetHead(p, e.Prev); err != nil {
		return err
	}
	r.Meta.Append(p, meta.TagRedoLog, rec.Payload)
	return r.Save()
}

// Redo is Undo's inverse: it pops the newest redo entry, moves HEAD to its
// new head and pushes the entry back onto the operation log. Unlike a real
// operation it must not clear the redo log, so it does not go through
// AppendOp.
func (r *Repo) Redo() error {
	p := r.CurrentPath()
	rec, ok := r.Meta.PopLast(p, meta.TagRedoLog)
	if !ok {
		return ErrNothingToRedo
	}
	e, err := meta.DecodeOpLog(rec.Payload)
	if err != nil {
		return err
	}
	if err := r.SetHead(p, e.New); err != nil {
		return err
	}
	r.Meta.Append(p, meta.TagOpLog, rec.Payload)
	return r.Save()
}

// OperationHistory returns the current path's logged operations, oldest
// first. Read-only.
func (r *Repo) OperationHistory() ([]meta.OpLogEntry, error) {
	recs := r.Meta.All(r.CurrentPath(), meta.TagOpLog)
	out := make([]meta.OpLogEntry, 0, len(recs))
	for _, rec := range recs {
		e, err := meta.DecodeOpLog(rec.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// OperationRestore jumps the current path's HEAD to the new head recorded by
// the operation at index (0-based, oldest first, the numbering
// OperationHistory returns). It deliberately leaves both the operation log
// and the redo log untouched: reconciling an arbitrary restore with undo/redo
// afterwards is out of scope (OpLogRestore).
func (r *Repo) OperationRestore(index int) error {
	recs := r.Meta.All(r.CurrentPath(), meta.TagOpLog)
	if index < 0 || index >= len(recs) {
		return ErrBadOpIndex
	}
	e, err := meta.DecodeOpLog(recs[index].Payload)
	if err != nil {
		return err
	}
	if err := r.SetHead(r.CurrentPath(), e.New); err != nil {
		return err
	}
	return r.Save()
}

// Export copies a repository's whole state - the object file and its .m
// metadata file - to dest. A source file that does not exist is skipped
// silently (CopyFileIfExists), so a repo that was never touched beyond init
// copies its object file alone; the destination is overwritten if it exists.
func Export(src, dest string) error { return copyRepo(src, dest) }

// Import is Export's other direction and the same copy underneath
// (HgitCopyRepo): it replaces whatever is at dest with src's whole state.
func Import(src, dest string) error { return copyRepo(src, dest) }

func copyRepo(src, dest string) error {
	for _, suffix := range []string{"", ".m"} {
		b, err := os.ReadFile(src + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeAtomic(dest+suffix, b); err != nil {
			return err
		}
	}
	return nil
}
