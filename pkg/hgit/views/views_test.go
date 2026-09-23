package views_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/views"
)

type fx struct {
	t    *testing.T
	dir  string
	path string
}

func setup(t *testing.T) *fx {
	t.Helper()
	oldClock, oldID := clock.Now, offer.NewEntityID
	t.Cleanup(func() { clock.Now, offer.NewEntityID = oldClock, oldID })
	ts := uint64(1000)
	clock.Now = func() uint64 { ts++; return ts }
	next := uint64(0)
	offer.NewEntityID = func() uint64 { next++; return next }
	f := &fx{t: t, dir: t.TempDir()}
	f.path = filepath.Join(f.dir, "R.hgs")
	if err := repo.Init(f.path); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fx) open() *repo.Repo {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fx) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) offerWith(o offer.Options) archive.Hash {
	f.t.Helper()
	h, err := offer.Offer(f.open(), f.dir, "*.txt", o)
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (f *fx) offer(msg string) archive.Hash { return f.offerWith(offer.Options{Message: msg}) }

func (f *fx) pathNew(name string) {
	f.t.Helper()
	if err := f.open().PathNew(name); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) pathGo(name string) {
	f.t.Helper()
	if err := f.open().PathGo(name); err != nil {
		f.t.Fatal(err)
	}
}

func short(h archive.Hash, n int) string { return h.Hex()[:n] }

func check(t *testing.T, got string, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestGraphEmptyRepo(t *testing.T) {
	f := setup(t)
	got, err := views.Graph(f.open())
	check(t, got, err, "(no offerings yet)\n")
}

// Every other path attaches right after the main commit its first-parent
// chain reaches first: side forks at the root, feature after two, and nested
// (made from feature) also attaches after two, carrying feature's own commit
// too - Graph.HC's documented branch-of-a-branch simplification. idle has no
// commit of its own and still gets its (empty) node.
func TestGraphAttachesEachPathAtItsForkPoint(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "1")
	one := f.offer("one")
	f.pathNew("side")
	f.write("a.txt", "2")
	two := f.offer("two")
	f.pathNew("feature")
	f.pathNew("idle")
	f.pathGo("feature")
	f.write("a.txt", "3")
	three := f.offer("three_on_feature")
	f.pathNew("nested")
	f.pathGo("nested")
	f.write("a.txt", "n")
	nest := f.offer("on_nested")
	f.pathGo("side")
	f.write("a.txt", "s")
	side := f.offer("on_side")
	f.pathGo("main")
	f.write("a.txt", "4")
	four := f.offer("four")

	got, err := views.Graph(f.open())
	check(t, got, err, "hgit history graph\n\n"+
		"[+] main, 3 commits\n"+
		"      "+short(one, 10)+" one\n"+
		"      [+] [side] 1 commits\n"+
		"            "+short(side, 10)+" on_side\n"+
		"      "+short(two, 10)+" two\n"+
		"      [+] [feature] 1 commits\n"+
		"            "+short(three, 10)+" three_on_feature\n"+
		"      [+] [idle] 0 commits\n"+
		"      [+] [nested] 2 commits\n"+
		"            "+short(three, 10)+" three_on_feature\n"+
		"            "+short(nest, 10)+" on_nested\n"+
		"      "+short(four, 10)+" four\n")
}

// A path whose chain never reaches main (here: a path created before main had
// a commit, so its head is all-zero) is left out, as Graph.HC leaves it out.
func TestGraphOmitsAPathThatNeverReachesMain(t *testing.T) {
	f := setup(t)
	f.pathNew("early")
	f.write("a.txt", "1")
	one := f.offer("one")
	got, err := views.Graph(f.open())
	check(t, got, err, "hgit history graph\n\n[+] main, 1 commits\n      "+short(one, 10)+" one\n")
}

func TestHistoryDoc(t *testing.T) {
	f := setup(t)
	got, err := views.HistoryDoc(f.open())
	check(t, got, err, "hgit history\n\n(no offerings yet)\n")

	f.write("a.txt", "1")
	one := f.offer("one")
	f.write("a.txt", "2")
	two := f.offer("two")
	r := f.open()
	ts := func(h archive.Hash) string {
		c, err := r.Commit(h)
		if err != nil {
			t.Fatal(err)
		}
		return strconv.FormatUint(c.Timestamp, 10)
	}
	got, err = views.HistoryDoc(r)
	check(t, got, err, "hgit history\n\n"+
		short(two, 12)+" "+ts(two)+" two\n"+
		short(one, 12)+" "+ts(one)+" one\n")
}

// relRepo: one, two, a CORRECTS of one scoped to entity 0xab, a plain four,
// and a REVERTS of two with no entity.
func relRepo(t *testing.T) (f *fx, one, two, corr, rev archive.Hash) {
	f = setup(t)
	f.write("a.txt", "1")
	one = f.offer("one")
	f.write("a.txt", "2")
	two = f.offer("two")
	f.write("a.txt", "3")
	corr = f.offerWith(offer.Options{Message: "fix_one", Relation: object.RelCorrects, RelationTarget: one, RelationEntity: 0xab})
	f.write("a.txt", "4")
	f.offer("four")
	f.write("a.txt", "5")
	rev = f.offerWith(offer.Options{Message: "undo_two", Relation: object.RelReverts, RelationTarget: two})
	return
}

func TestReconcileDoc(t *testing.T) {
	f, one, two, corr, _ := relRepo(t)
	r := f.open()

	got, err := views.ReconcileDoc(r, corr)
	check(t, got, err, "hgit reconciliation\n\n"+
		short(corr, 12)+" fix_one\n\n"+
		"[+] relation: CORRECTS\n"+
		"      target: "+one.Hex()+"\n"+
		"      target message: one\n"+
		"      scoped to entity: 00000000000000ab\n")

	got, err = views.ReconcileDoc(r, two)
	check(t, got, err, "hgit reconciliation\n\n"+short(two, 12)+" two\n\n(no relation on this commit)\n")

	var missing archive.Hash
	got, err = views.ReconcileDoc(r, missing)
	check(t, got, err, "hgit reconciliation\n\n(commit not found)\n")

	c, _ := r.Commit(corr)
	got, err = views.ReconcileDoc(r, c.Tree)
	check(t, got, err, "hgit reconciliation\n\n(not a commit)\n")
}

func TestReconcileOverview(t *testing.T) {
	f := setup(t)
	got, err := views.ReconcileOverview(f.open())
	check(t, got, err, "hgit reconciliation overview\n\n(no offerings yet)\n")

	f.write("a.txt", "1")
	f.offer("one")
	got, err = views.ReconcileOverview(f.open())
	check(t, got, err, "hgit reconciliation overview\n\n(no relations found in this repo's history)\n")

	f, one, two, corr, rev := relRepo(t)
	got, err = views.ReconcileOverview(f.open())
	check(t, got, err, "hgit reconciliation overview\n\n"+
		short(rev, 12)+" undo_two\n\n"+
		"[+] relation: REVERTS\n"+
		"      target: "+two.Hex()+"\n"+
		"      target message: two\n"+
		short(corr, 12)+" fix_one\n\n"+
		"[+] relation: CORRECTS\n"+
		"      target: "+one.Hex()+"\n"+
		"      target message: one\n"+
		"      scoped to entity: 00000000000000ab\n")
}

func TestConflictDocNoMerge(t *testing.T) {
	f := setup(t)
	got, err := views.ConflictDoc(f.open())
	check(t, got, err, "hgit conflicts\n\n(no merge in progress on this path)\n")
}

// A real merge conflict: c.txt edited on both sides.
func TestConflictDocRealTextConflict(t *testing.T) {
	f := setup(t)
	f.write("c.txt", "base_c\n")
	f.offer("base")
	f.pathNew("cf")
	f.pathGo("cf")
	f.write("c.txt", "feat_c\n")
	theirs := f.offer("cf_edit")
	f.pathGo("main")
	f.write("c.txt", "main_c\n")
	ours := f.offer("main_edit")
	if _, err := merge.Merge(f.open(), "cf"); err != nil {
		t.Fatal(err)
	}
	r := f.open()
	cs, _ := merge.Conflicts(r)
	c := cs[0].Object

	got, err := views.ConflictDoc(r)
	check(t, got, err, "hgit conflicts\n\n"+
		"Merging path cf into main (repo: "+f.path+")\n"+
		"ours head:   "+short(ours, 12)+"\n"+
		"theirs head: "+short(theirs, 12)+"\n\n"+
		"[+] 0  c.txt  [conflict]\n"+
		"      entity 0000000000000001   kind: content\n"+
		"      base  : "+short(c.Base.Hash, 12)+"  mode=text\n"+
		"          base_c\n"+
		"      ours  : "+short(c.Ours.Hash, 12)+"  mode=text\n"+
		"          main_c\n"+
		"      theirs: "+short(c.Theirs.Hash, 12)+"  mode=text\n"+
		"          feat_c\n"+
		"      options: hgit resolve <repo> 0 take-ours | take-theirs (nothing chosen yet)\n"+
		"\nFinish with: hgit merge continue <repo>   Discard with: hgit merge abort <repo>\n")
}

// Hand-built conflict records cover every side shape ConflictDoc.HC names:
// absent, directory, binary by mode, binary by content, executable text, a
// long text (line and column caps), a missing stored object, a resolved
// record, and a record whose evidence object is missing (skipped).
func TestConflictDocSideShapes(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "1")
	head := f.offer("one")
	r := f.open()

	var long strings.Builder
	for i := 0; i < 14; i++ {
		long.WriteString(strings.Repeat("x", 75) + "$\t\n")
	}
	text := r.Put(archive.Blob, []byte(long.String()))
	bin := r.Put(archive.Blob, []byte("ab\x00cd"))
	sh := r.Put(archive.Blob, []byte("#!/bin/sh"))
	tree := r.Put(archive.Tree, (&object.Tree{}).Encode())
	var gone archive.Hash
	gone[0] = 9

	put := func(c object.Conflict, rec meta.ConflictRecord) {
		rec.Conflict = r.Put(archive.Conflict, c.Encode())
		r.Meta.Append("main", meta.TagConflict, rec.Encode())
	}
	r.Meta.Set("main", meta.TagMergeState, meta.MergeState{Ours: head, Theirs: head, OtherPath: "cf"}.Encode())
	put(object.Conflict{Kind: object.KindContent | object.KindMode, EntityID: 0x10, Path: "t.txt",
		Base:   object.Side{},
		Ours:   object.Side{Present: true, Type: archive.Blob, Mode: object.ModeBinary, Hash: bin},
		Theirs: object.Side{Present: true, Type: archive.Blob, Mode: 0, Hash: text}}, meta.ConflictRecord{})
	put(object.Conflict{Kind: object.KindType, EntityID: 0x11, Path: "d",
		Base:   object.Side{Present: true, Type: archive.Blob, Mode: object.ModeExecutable, Hash: sh},
		Ours:   object.Side{Present: true, Type: archive.Tree, Hash: tree},
		Theirs: object.Side{Present: true, Type: archive.Blob, Mode: 0, Hash: bin}}, meta.ConflictRecord{Resolved: true, Resolution: sh})
	put(object.Conflict{Kind: object.KindContent, EntityID: 0x12, Path: "g.txt",
		Base: object.Side{Present: true, Type: archive.Blob, Hash: gone},
		Ours: object.Side{Present: true, Type: archive.Blob, Hash: sh}}, meta.ConflictRecord{Resolved: true})
	r.Meta.Append("main", meta.TagConflict, meta.ConflictRecord{Conflict: gone}.Encode())
	put(object.Conflict{Kind: object.KindContent, EntityID: 0x13, Path: "o.txt",
		Ours:   object.Side{Present: true, Type: archive.Blob, Hash: sh},
		Theirs: object.Side{Present: true, Type: archive.Blob, Hash: bin}}, meta.ConflictRecord{Resolved: true, Resolution: sh})

	line := "          " + strings.Repeat("x", 70) + "\n"
	got, err := views.ConflictDoc(r)
	check(t, got, err, "hgit conflicts\n\n"+
		"Merging path cf into main (repo: "+f.path+")\n"+
		"ours head:   "+short(head, 12)+"\n"+
		"theirs head: "+short(head, 12)+"\n\n"+
		"[+] 0  t.txt  [conflict]\n"+
		"      entity 0000000000000010   kind: content mode\n"+
		"      base  : (absent - deleted or not yet added on this side)\n"+
		"      ours  : "+short(bin, 12)+"  mode=binary\n"+
		"          (binary, 5 bytes)\n"+
		"      theirs: "+short(text, 12)+"  mode=text\n"+
		strings.Repeat(line, 12)+
		"          ... (more)\n"+
		"      options: hgit resolve <repo> 0 take-ours | take-theirs (nothing chosen yet)\n"+
		"[+] 1  d  [resolved]\n"+
		"      entity 0000000000000011   kind: file-vs-directory\n"+
		"      base  : "+short(sh, 12)+"  mode=executable\n"+
		"          #!/bin/sh\n"+
		"      ours  : (directory)\n"+
		"      theirs: "+short(bin, 12)+"  mode=text\n"+
		"          (binary, 5 bytes)\n"+
		"      resolved to: theirs\n"+
		"[+] 2  g.txt  [resolved]\n"+
		"      entity 0000000000000012   kind: content\n"+
		"      base  : "+short(gone, 12)+"  mode=text\n"+
		"          (stored object missing - see hgit check)\n"+
		"      ours  : "+short(sh, 12)+"  mode=text\n"+
		"          #!/bin/sh\n"+
		"      theirs: (absent - deleted or not yet added on this side)\n"+
		"      resolved to: deletion\n"+
		"[+] 4  o.txt  [resolved]\n"+
		"      entity 0000000000000013   kind: content\n"+
		"      base  : (absent - deleted or not yet added on this side)\n"+
		"      ours  : "+short(sh, 12)+"  mode=text\n"+
		"          #!/bin/sh\n"+
		"      theirs: "+short(bin, 12)+"  mode=text\n"+
		"          (binary, 5 bytes)\n"+
		"      resolved to: ours\n"+
		"\nFinish with: hgit merge continue <repo>   Discard with: hgit merge abort <repo>\n")
}
