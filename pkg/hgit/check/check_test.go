package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

func newRepo(t *testing.T) *repo.Repo {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.hgs")
	if err := os.WriteFile(p, archive.Header{Version: 4}.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := repo.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func commit(r *repo.Repo, tree archive.Hash, parents ...archive.Hash) archive.Hash {
	c := &object.Commit{Tree: tree, Parents: parents, Message: []byte("m")}
	return r.Put(archive.Commit, c.Encode())
}

func tree(r *repo.Repo, kids ...archive.Hash) archive.Hash {
	t := &object.Tree{}
	for i, k := range kids {
		t.Entries = append(t.Entries, object.Entry{Name: string(rune('a' + i)), ChildType: archive.Blob, ChildHash: k, EntityID: uint64(i + 1)})
	}
	return r.Put(archive.Tree, t.Encode())
}

func TestFixturesClean(t *testing.T) {
	for _, name := range testfix.Names(t, ".hgs") {
		if name == "TFConfNewer.hgs" {
			continue
		}
		p := filepath.Join(t.TempDir(), name)
		os.WriteFile(p, testfix.Read(t, name), 0o644)
		os.WriteFile(p+".m", testfix.Read(t, name+".m"), 0o644)
		r, err := repo.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := archive.Parse(testfix.Read(t, name))
		rep := Run(r)
		if len(rep.HashBad) != 0 || rep.RefsBroken != 0 || rep.Objects != len(a.Records) || rep.FormatVersion != 4 {
			t.Errorf("%s: %+v (records %d)", name, rep, len(a.Records))
		}
	}
}

func TestCorruptObject(t *testing.T) {
	r := newRepo(t)
	h := r.Put(archive.Blob, []byte("x"))
	r.Arc.Records[0].Data[1] ^= 0xff
	rep := Run(r)
	if len(rep.HashBad) != 1 || rep.HashBad[0] != h {
		t.Fatalf("%+v", rep)
	}
}

func TestMissingRef(t *testing.T) {
	r := newRepo(t)
	var ghost archive.Hash
	ghost[0] = 7
	tr := tree(r, ghost)
	c := commit(r, tr, ghost)
	r.SetHead("main", c)
	rep := Run(r)
	if rep.RefsBroken != 2 || len(rep.Broken) != 2 || rep.Broken[0].Kind != "tree_child_missing" || rep.Broken[1].Kind != "commit_parent_missing" {
		t.Fatalf("%+v", rep)
	}
}

func TestDanglingAndReachable(t *testing.T) {
	r := newRepo(t)
	b := r.Put(archive.Blob, []byte("b"))
	orphan := r.Put(archive.Blob, []byte("orphan"))
	c := commit(r, tree(r, b))
	r.SetHead("main", c)
	rep := Run(r)
	if len(rep.Dangling) != 1 || rep.Dangling[0] != (DanglingObj{archive.Blob, orphan}) || rep.RefsBroken != 0 {
		t.Fatalf("%+v", rep)
	}
}

func TestConflictRoot(t *testing.T) {
	r := newRepo(t)
	side := r.Put(archive.Blob, []byte("side"))
	cf := &object.Conflict{Kind: 1, Path: "c.txt", Ours: object.Side{Present: true, Type: archive.Blob, Hash: side}}
	ch := r.Put(archive.Conflict, cf.Encode())
	r.SetHead("main", commit(r, tree(r)))
	rep := Run(r)
	if len(rep.Dangling) != 2 {
		t.Fatalf("unrooted conflict + side should dangle: %+v", rep)
	}
	r.Meta.Set("main", meta.TagMergeState, make([]byte, 128))
	r.Meta.Append("main", meta.TagConflict, append(ch[:], make([]byte, 65)...))
	rep = Run(r)
	if len(rep.Dangling) != 0 || rep.RefsBroken != 0 {
		t.Fatalf("%+v", rep)
	}
	// Once `hgit resolve` writes a real resolution hash, nothing changes: the
	// resolution is always one of the conflict object's own present sides, so
	// it is already reachable through the conflict - no second root is needed,
	// and the HolyC marks only the conflict hash too.
	r.Meta.Set("main", meta.TagConflict, append(append(append([]byte{}, ch[:]...), 1), side[:]...))
	if rep = Run(r); len(rep.Dangling) != 0 || rep.RefsBroken != 0 {
		t.Fatalf("resolved conflict: %+v", rep)
	}
	// missing conflict object and missing side
	r.Meta.Append("main", meta.TagConflict, append(make([]byte, 64), make([]byte, 65)...))
	if rep = Run(r); rep.RefsBroken != 1 || rep.Broken[0].Kind != "conflict_object_missing" {
		t.Fatalf("%+v", rep)
	}
}

func TestSharedSubtreeAndCycle(t *testing.T) {
	r := newRepo(t)
	b := r.Put(archive.Blob, []byte("b"))
	shared := tree(r, b)
	top := &object.Tree{Entries: []object.Entry{
		{Name: "x", ChildType: archive.Tree, ChildHash: shared, EntityID: 1},
		{Name: "y", ChildType: archive.Tree, ChildHash: shared, EntityID: 2}}}
	c := commit(r, r.Put(archive.Tree, top.Encode()))
	r.SetHead("main", c)
	if rep := Run(r); len(rep.Dangling) != 0 || rep.Objects != 4 {
		t.Fatalf("%+v", rep)
	}
	// forge a self-referencing tree (hash need not match content); must terminate
	self := archive.NewObject(archive.Tree, (&object.Tree{Entries: []object.Entry{{Name: "s", ChildType: archive.Tree, EntityID: 1}}}).Encode())
	e := object.Tree{Entries: []object.Entry{{Name: "s", ChildType: archive.Tree, ChildHash: self.Hash, EntityID: 1}}}
	self = archive.Record{Data: append([]byte{byte(archive.Tree)}, e.Encode()...), Hash: self.Hash}
	r.Arc.Records = append(r.Arc.Records, self)
	r.SetHead("cyc", commit(r, self.Hash))
	r.Meta.Append("cyc", meta.TagPathDeclared, nil)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r, err := repo.Open(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	Run(r) // terminating is the assertion
}

func TestNewerFormat(t *testing.T) {
	_, err := repo.Open(testfix.Path("TFConfNewer.hgs"))
	if _, ok := err.(*archive.UnsupportedVersionError); !ok {
		t.Fatalf("%v", err)
	}
}

// ObjectPut is a plain append, so one hash can occupy several archive
// positions. Check.HC counts every record in objects= but coalesces
// duplicates for reachability: a duplicate of a reachable object is
// reachable, and a duplicate of a dangling one is reported once per record.
func TestDuplicateRecords(t *testing.T) {
	r := newRepo(t)
	b := r.Append(archive.Blob, []byte("b"))
	if r.Append(archive.Blob, []byte("b")) != b {
		t.Fatal("same content, same hash")
	}
	orphan := r.Append(archive.Blob, []byte("orphan"))
	r.Append(archive.Blob, []byte("orphan"))
	c := commit(r, tree(r, b))
	r.SetHead("main", c)

	rep := Run(r)
	if rep.Objects != 6 {
		t.Fatalf("objects = %d, want every record counted (6)", rep.Objects)
	}
	want := []DanglingObj{{archive.Blob, orphan}, {archive.Blob, orphan}}
	if len(rep.Dangling) != len(want) || rep.Dangling[0] != want[0] || rep.Dangling[1] != want[1] {
		t.Fatalf("dangling = %+v, want both orphan records", rep.Dangling)
	}
	if rep.RefsBroken != 0 {
		t.Fatalf("refs broken = %d", rep.RefsBroken)
	}
}
