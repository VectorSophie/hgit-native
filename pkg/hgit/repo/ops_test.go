package repo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
)

// fresh makes an empty repo and opens it.
func fresh(t *testing.T, name string) *Repo {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := Init(p); err != nil {
		t.Fatal(err)
	}
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func hash(b byte) archive.Hash {
	var h archive.Hash
	for i := range h {
		h[i] = b
	}
	return h
}

func TestPathNewCopiesCurrentHeadAndDeclares(t *testing.T) {
	r := fresh(t, "P.hgs")
	if err := r.SetHead("main", hash(1)); err != nil {
		t.Fatal(err)
	}
	if err := r.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	h, ok := r.Head("feature")
	if !ok || h != hash(1) {
		t.Fatalf("feature head = %x, %v", h, ok)
	}
	if r.CurrentPath() != "main" {
		t.Fatalf("path new must not switch current, got %q", r.CurrentPath())
	}
	// persisted
	r2, err := Open(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := r2.PathList(); strings.Join(got, ",") != "main,feature" {
		t.Fatalf("path list = %v", got)
	}
}

func TestPathNewOnHeadlessCurrentWritesZeroHead(t *testing.T) {
	r := fresh(t, "P.hgs")
	if err := r.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	h, ok := r.Head("feature")
	if !ok {
		t.Fatal("PathNew must write an all-zero HEAD record, as MetaWriteHead does")
	}
	if h != (archive.Hash{}) {
		t.Fatalf("head = %x, want all zero", h)
	}
}

func TestPathNewErrors(t *testing.T) {
	r := fresh(t, "P.hgs")
	if err := r.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want error
	}{
		{"", ErrBadPathName},
		{strings.Repeat("x", 64), ErrBadPathName},
		{"feature", ErrPathExists},
		{"main", ErrPathExists},
	} {
		if err := r.PathNew(tc.name); !errors.Is(err, tc.want) {
			t.Fatalf("PathNew(%q) = %v, want %v", tc.name, err, tc.want)
		}
	}
	if err := r.PathNew(strings.Repeat("x", 63)); err != nil {
		t.Fatalf("63-byte name: %v", err)
	}
}

func TestPathGo(t *testing.T) {
	r := fresh(t, "P.hgs")
	if err := r.PathGo("nope"); !errors.Is(err, ErrNoSuchPath) {
		t.Fatalf("PathGo undeclared = %v", err)
	}
	if err := r.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.PathGo("feature"); err != nil {
		t.Fatal(err)
	}
	if r.CurrentPath() != "feature" {
		t.Fatalf("current = %q", r.CurrentPath())
	}
	if err := r.PathGo("main"); err != nil {
		t.Fatal(err)
	}
	if r.CurrentPath() != "main" {
		t.Fatalf("current = %q", r.CurrentPath())
	}
}

func TestPathClose(t *testing.T) {
	r := fresh(t, "P.hgs")
	if err := r.PathClose("main"); !errors.Is(err, ErrCloseMain) {
		t.Fatalf("close main = %v", err)
	}
	if err := r.PathClose("nope"); !errors.Is(err, ErrNoSuchPath) {
		t.Fatalf("close missing = %v", err)
	}
	if err := r.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetHead("feature", hash(2)); err != nil {
		t.Fatal(err)
	}
	if err := r.PathGo("feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.PathClose("feature"); err != nil {
		t.Fatal(err)
	}
	if r.CurrentPath() != "main" {
		t.Fatalf("closing the current path must fall back to main, got %q", r.CurrentPath())
	}
	if got := r.PathList(); strings.Join(got, ",") != "main" {
		t.Fatalf("path list = %v", got)
	}
	if _, ok := r.Head("feature"); !ok {
		t.Fatal("PathClose must leave the closed path's HEAD record behind, orphaned")
	}
	if err := r.PathGo("feature"); !errors.Is(err, ErrNoSuchPath) {
		t.Fatalf("path go on a closed path = %v", err)
	}
}

// logOp is what offer does on every commit.
func logOp(t *testing.T, r *Repo, ts uint64, prev, new archive.Hash) {
	t.Helper()
	r.AppendOp(r.CurrentPath(), meta.OpLogEntry{Timestamp: ts, Prev: prev, New: new})
	if err := r.SetHead(r.CurrentPath(), new); err != nil {
		t.Fatal(err)
	}
}

func TestUndoRedoRoundTrip(t *testing.T) {
	r := fresh(t, "P.hgs")
	logOp(t, r, 10, archive.Hash{}, hash(1))
	logOp(t, r, 20, hash(1), hash(2))

	if err := r.Undo(); err != nil {
		t.Fatal(err)
	}
	if h, _ := r.Head("main"); h != hash(1) {
		t.Fatalf("head after undo = %x", h)
	}
	if err := r.Undo(); err != nil {
		t.Fatal(err)
	}
	if h, _ := r.Head("main"); h != (archive.Hash{}) {
		t.Fatalf("head after second undo = %x", h)
	}
	if err := r.Undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("third undo = %v", err)
	}
	if err := r.Redo(); err != nil {
		t.Fatal(err)
	}
	if err := r.Redo(); err != nil {
		t.Fatal(err)
	}
	if h, _ := r.Head("main"); h != hash(2) {
		t.Fatalf("head after redo = %x", h)
	}
	if err := r.Redo(); !errors.Is(err, ErrNothingToRedo) {
		t.Fatalf("third redo = %v", err)
	}
	ops, err := r.OperationHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || ops[0].Timestamp != 10 || ops[1].Timestamp != 20 {
		t.Fatalf("oplog after round trip = %+v", ops)
	}
}

func TestUndoOnEmptyLogIsAnError(t *testing.T) {
	r := fresh(t, "P.hgs")
	if err := r.Undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo = %v", err)
	}
	if err := r.Redo(); !errors.Is(err, ErrNothingToRedo) {
		t.Fatalf("redo = %v", err)
	}
}

func TestOpLogIsPerPath(t *testing.T) {
	r := fresh(t, "P.hgs")
	logOp(t, r, 10, archive.Hash{}, hash(1))
	if err := r.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.PathGo("feature"); err != nil {
		t.Fatal(err)
	}
	if ops, err := r.OperationHistory(); err != nil || len(ops) != 0 {
		t.Fatalf("feature oplog = %v, %v", ops, err)
	}
	if err := r.Undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo on a fresh path = %v", err)
	}
}

func TestAppendOpClearsRedoLog(t *testing.T) {
	r := fresh(t, "P.hgs")
	logOp(t, r, 10, archive.Hash{}, hash(1))
	if err := r.Undo(); err != nil {
		t.Fatal(err)
	}
	logOp(t, r, 30, archive.Hash{}, hash(3)) // new work invalidates the undone entry
	if err := r.Redo(); !errors.Is(err, ErrNothingToRedo) {
		t.Fatalf("redo after new work = %v", err)
	}
}

func TestOperationRestoreJumpsHeadAndLeavesBothLogsAlone(t *testing.T) {
	r := fresh(t, "P.hgs")
	logOp(t, r, 10, archive.Hash{}, hash(1))
	logOp(t, r, 20, hash(1), hash(2))
	logOp(t, r, 30, hash(2), hash(3))
	if err := r.Undo(); err != nil { // one entry now sits on the redo log
		t.Fatal(err)
	}

	if err := r.OperationRestore(0); err != nil {
		t.Fatal(err)
	}
	if h, _ := r.Head("main"); h != hash(1) {
		t.Fatalf("head after restore 0 = %x, want the entry's NEW head", h)
	}
	ops, err := r.OperationHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("restore must not append to or pop from the oplog, got %d entries", len(ops))
	}
	// the redo log is untouched too: the undone entry is still replayable
	if err := r.Redo(); err != nil {
		t.Fatalf("redo after restore = %v", err)
	}
	if h, _ := r.Head("main"); h != hash(3) {
		t.Fatalf("head after redo = %x", h)
	}

	for _, i := range []int{-1, 3, 99} {
		if err := r.OperationRestore(i); !errors.Is(err, ErrBadOpIndex) {
			t.Fatalf("restore(%d) = %v", i, err)
		}
	}
	if h, _ := r.Head("main"); h != hash(3) {
		t.Fatalf("a failed restore must not move HEAD, got %x", h)
	}
}

func TestExportImportCopyBothFiles(t *testing.T) {
	r := fresh(t, "P.hgs")
	if err := r.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "Exported.hgs")
	if err := Export(r.Path, dest); err != nil {
		t.Fatal(err)
	}
	r2, err := Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := r2.PathList(); strings.Join(got, ",") != "main,feature" {
		t.Fatalf("exported path list = %v", got)
	}

	back := filepath.Join(t.TempDir(), "Imported.hgs")
	if err := Import(dest, back); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", ".m"} {
		a, err := os.ReadFile(dest + suffix)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(back + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Fatalf("%q differs after import", suffix)
		}
	}
}

func TestExportWithoutMetaFileCopiesOnlyTheObjectFile(t *testing.T) {
	r := fresh(t, "P.hgs")
	if _, err := os.Stat(r.Path + ".m"); err == nil {
		t.Fatal("Init must not write a .m file")
	}
	dest := filepath.Join(t.TempDir(), "Exported.hgs")
	if err := Export(r.Path, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest + ".m"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dest .m = %v, want absent", err)
	}
	if _, err := Open(dest); err != nil {
		t.Fatalf("object-only copy must still open: %v", err)
	}
}

func TestExportOfAMissingSourceIsASilentNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := Export(filepath.Join(dir, "nope.hgs"), filepath.Join(dir, "out.hgs")); err != nil {
		t.Fatalf("CopyFileIfExists no-ops on a missing source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.hgs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("out.hgs = %v, want absent", err)
	}
}

func TestImportOverwritesAnExistingDestination(t *testing.T) {
	src := fresh(t, "Src.hgs")
	if err := src.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	dst := fresh(t, "Dst.hgs")
	if err := Import(src.Path, dst.Path); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dst.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.PathList(); strings.Join(got, ",") != "main,feature" {
		t.Fatalf("destination path list = %v, want the source's", got)
	}
}
