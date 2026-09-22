package offer_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/fossil"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// freezeClock pins clock.Now for the test's duration.
func freezeClock(t *testing.T, ts uint64) {
	t.Helper()
	old := clock.Now
	clock.Now = func() uint64 { return ts }
	t.Cleanup(func() { clock.Now = old })
}

// seedIDs makes offer.NewEntityID hand out ids in order, then 9000+n.
func seedIDs(t *testing.T, ids ...uint64) {
	t.Helper()
	old := offer.NewEntityID
	n := 0
	offer.NewEntityID = func() uint64 {
		n++
		if n <= len(ids) {
			return ids[n-1]
		}
		return uint64(9000 + n)
	}
	t.Cleanup(func() { offer.NewEntityID = old })
}

type fixture struct {
	t    *testing.T
	dir  string
	path string
	r    *repo.Repo
}

func setup(t *testing.T) *fixture {
	t.Helper()
	freezeClock(t, 1700000000000)
	dir := t.TempDir()
	path := filepath.Join(dir, "r.hgs")
	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, dir: dir, path: path}
}

func (f *fixture) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) remove(name string) {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.dir, name)); err != nil {
		f.t.Fatal(err)
	}
}

// offerMsg reopens the repo from disk (so every offer sees only what was
// saved), offers, and returns the new commit hash.
func (f *fixture) offerMsg(mask, msg string) archive.Hash {
	f.t.Helper()
	h, err := f.offerOpts(mask, offer.Options{Message: msg})
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (f *fixture) offerOpts(mask string, opts offer.Options) (archive.Hash, error) {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.r = r
	return offer.Offer(r, f.dir, mask, opts)
}

func (f *fixture) tree(h archive.Hash) *object.Tree {
	f.t.Helper()
	c, err := f.r.Commit(h)
	if err != nil {
		f.t.Fatal(err)
	}
	tr, err := f.r.Tree(c.Tree)
	if err != nil {
		f.t.Fatal(err)
	}
	return tr
}

func names(tr *object.Tree) []string {
	out := make([]string, len(tr.Entries))
	for i, e := range tr.Entries {
		out[i] = e.Name
	}
	return out
}

func idOf(t *testing.T, tr *object.Tree, name string) uint64 {
	t.Helper()
	e, ok := tr.Find(name)
	if !ok {
		t.Fatalf("%s missing from tree %v", name, names(tr))
	}
	return e.EntityID
}

func TestFirstOfferBuildsTreeAndCommit(t *testing.T) {
	f := setup(t)
	seedIDs(t, 0x11, 0x22)
	f.write("a.txt", "alpha")
	f.write("b.txt", "beta")
	h := f.offerMsg("*.txt", "first")

	c, err := f.r.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Parents) != 0 {
		t.Fatalf("parents = %d, want 0", len(c.Parents))
	}
	if c.Timestamp != 1700000000000 {
		t.Fatalf("timestamp = %d, want the frozen clock", c.Timestamp)
	}
	if string(c.Message) != "first" {
		t.Fatalf("message = %q", c.Message)
	}
	if c.Relation != object.RelNone || c.Attrs != nil {
		t.Fatalf("want no relation and no attrs, got %v %v", c.Relation, c.Attrs)
	}
	tr := f.tree(h)
	if got := names(tr); len(got) != 2 || got[0] != "a.txt" || got[1] != "b.txt" {
		t.Fatalf("names = %v", got)
	}
	if tr.Entries[0].ChildType != archive.Blob {
		t.Fatalf("child type = %d", tr.Entries[0].ChildType)
	}
	want := archive.NewObject(archive.Blob, []byte("alpha")).Hash
	if tr.Entries[0].ChildHash != want {
		t.Fatal("blob hash is not the tagged-content hash")
	}
	if idOf(t, tr, "a.txt") != 0x11 || idOf(t, tr, "b.txt") != 0x22 {
		t.Fatalf("entity ids = %v", tr.Entries)
	}
	if head, ok := f.r.Head(f.r.CurrentPath()); !ok || head != h {
		t.Fatalf("head = %v %v, want the new commit", head, ok)
	}
}

func TestFirstOfferLogsZeroPrevHead(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "alpha")
	h := f.offerMsg("*.txt", "first")

	recs := f.r.Meta.All(f.r.CurrentPath(), meta.TagOpLog)
	if len(recs) != 1 {
		t.Fatalf("oplog records = %d, want 1", len(recs))
	}
	e, err := meta.DecodeOpLog(recs[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	var zero archive.Hash
	if e.Prev != zero {
		t.Fatal("prev head should be all zero for a root offering")
	}
	if e.New != h || e.Timestamp != 1700000000000 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestOfferClearsRedoLog(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "alpha")
	f.offerMsg("*.txt", "first")
	f.r.Meta.Append(f.r.CurrentPath(), meta.TagRedoLog, meta.OpLogEntry{}.Encode())
	if err := f.r.Save(); err != nil {
		t.Fatal(err)
	}
	f.write("a.txt", "alpha2")
	f.offerMsg("*.txt", "second")
	if got := f.r.Meta.All(f.r.CurrentPath(), meta.TagRedoLog); len(got) != 0 {
		t.Fatalf("redo log = %d records, want cleared", len(got))
	}
}

func TestSecondOfferCarriesEntityIDForward(t *testing.T) {
	f := setup(t)
	seedIDs(t, 0x11, 0x22)
	f.write("stays.txt", "same")
	f.write("edited.txt", "v1")
	first := f.offerMsg("*.txt", "first")
	tr1 := f.tree(first)

	f.write("edited.txt", "version two, quite different")
	second := f.offerMsg("*.txt", "second")
	tr2 := f.tree(second)

	if idOf(t, tr2, "stays.txt") != idOf(t, tr1, "stays.txt") {
		t.Fatal("unchanged file lost its entity id")
	}
	if idOf(t, tr2, "edited.txt") != idOf(t, tr1, "edited.txt") {
		t.Fatal("modified file lost its entity id")
	}
	c, err := f.r.Commit(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Parents) != 1 || c.Parents[0] != first {
		t.Fatalf("parents = %v, want the first commit", c.Parents)
	}
}

func TestExactContentRenameCarriesEntityID(t *testing.T) {
	f := setup(t)
	f.write("orig.txt", "moved verbatim")
	first := f.offerMsg("*.txt", "first")
	want := idOf(t, f.tree(first), "orig.txt")

	f.remove("orig.txt")
	f.write("renamed.txt", "moved verbatim")
	second := f.offerMsg("*.txt", "second")
	if got := idOf(t, f.tree(second), "renamed.txt"); got != want {
		t.Fatalf("entity id = %#x, want the renamed file's %#x", got, want)
	}
}

func TestNewFileGetsFreshEntityID(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "alpha")
	first := f.offerMsg("*.txt", "first")
	old := idOf(t, f.tree(first), "a.txt")

	f.write("new.txt", "nothing like the other one at all")
	second := f.offerMsg("*.txt", "second")
	tr := f.tree(second)
	if idOf(t, tr, "new.txt") == old {
		t.Fatal("an unrelated new file must not inherit an identity")
	}
	if idOf(t, tr, "new.txt") == 0 {
		t.Fatal("entity id must never be zero")
	}
}

// varied returns n bytes with no long internal repeat, so the longest common
// substring after a one-byte edit is the length of the untouched side.
func varied(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(byte('a' + (i*7+i/26)%26))
	}
	return b.String()
}

func fuzzyRenameDetected(t *testing.T, src, edited string) bool {
	t.Helper()
	f := setup(t)
	f.write("orig.txt", src)
	first := f.offerMsg("*.txt", "first")
	want := idOf(t, f.tree(first), "orig.txt")

	f.remove("orig.txt")
	f.write("renamed.txt", edited)
	second := f.offerMsg("*.txt", "second")
	return idOf(t, f.tree(second), "renamed.txt") == want
}

// Offer.HC passes the whole file to OfferFindFuzzyRename - probe 106 lifted
// every per-file cap on the flat path - so size is not a criterion here. (The
// 512-byte candidate slots belong to Status.HC, not to offer.)
func TestFuzzyRenameOfALargeFile(t *testing.T) {
	for _, n := range []int{512, 513, 4096} {
		src := varied(n)
		edited := []byte(src)
		edited[n-1] ^= 0x20 // one byte changed
		if !fuzzyRenameDetected(t, src, string(edited)) {
			t.Errorf("a %d-byte rename-with-edit should carry its entity id", n)
		}
	}
}

// The only criterion is fossil.RenameSimilarityThreshold, on the percentage of
// the new file covered by the single longest match.
func TestFuzzyRenameThresholdIsTheOnlyCriterion(t *testing.T) {
	at := strings.Repeat("z", 50)
	if got := fossil.SimilarityPercent([]byte(varied(50)+at), []byte(varied(50)+strings.Repeat("y", 50))); got != fossil.RenameSimilarityThreshold {
		t.Fatalf("test inputs score %d, want exactly %d", got, fossil.RenameSimilarityThreshold)
	}
	if !fuzzyRenameDetected(t, varied(50)+at, varied(50)+strings.Repeat("y", 50)) {
		t.Error("a candidate scoring exactly the threshold is a rename")
	}
	if got := fossil.SimilarityPercent([]byte(varied(49)+strings.Repeat("z", 51)), []byte(varied(49)+strings.Repeat("y", 51))); got != fossil.RenameSimilarityThreshold-1 {
		t.Fatalf("test inputs score %d, want %d", got, fossil.RenameSimilarityThreshold-1)
	}
	if fuzzyRenameDetected(t, varied(49)+strings.Repeat("z", 51), varied(49)+strings.Repeat("y", 51)) {
		t.Error("a candidate one point below the threshold is not a rename")
	}
}

// Best score wins, over every blob in the parent tree.
func TestFuzzyRenameBestScoreWins(t *testing.T) {
	f := setup(t)
	f.write("weak.txt", strings.Repeat("a", 100))
	f.write("strong.txt", strings.Repeat("a", 50)+strings.Repeat("b", 50))
	first := f.offerMsg("*.txt", "first")
	tr1 := f.tree(first)

	f.remove("weak.txt")
	f.remove("strong.txt")
	f.write("moved.txt", strings.Repeat("a", 20)+strings.Repeat("b", 80))
	second := f.offerMsg("*.txt", "second")
	got := idOf(t, f.tree(second), "moved.txt")
	if got == idOf(t, tr1, "weak.txt") {
		t.Fatal("the weaker candidate won")
	}
	if got != idOf(t, tr1, "strong.txt") {
		t.Fatal("the best-scoring candidate should have carried its entity id")
	}
}

func TestFuzzyRenameBelowThresholdIsNotARename(t *testing.T) {
	f := setup(t)
	f.write("orig.txt", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	first := f.offerMsg("*.txt", "first")
	old := idOf(t, f.tree(first), "orig.txt")

	f.remove("orig.txt")
	f.write("renamed.txt", "zyxwvutsrqponmlkjihgfedcbZYXWV")
	second := f.offerMsg("*.txt", "second")
	if idOf(t, f.tree(second), "renamed.txt") == old {
		t.Fatal("dissimilar content must not carry an identity forward")
	}
}

func TestIgnoreHidesUntrackedButNeverTracked(t *testing.T) {
	f := setup(t)
	f.write("kept.txt", "kept")
	f.write("later.txt", "tracked before the rule existed")
	f.offerMsg("*.txt", "first")

	f.write(".hgitignore", "later.txt\nhidden.txt\n")
	f.write("hidden.txt", "should not be recorded")
	var ignored []string
	h, err := f.offerOpts("*.txt", offer.Options{
		Message:   "second",
		OnIgnored: func(n string) { ignored = append(ignored, n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	got := names(f.tree(h))
	if len(got) != 2 || got[0] != "kept.txt" || got[1] != "later.txt" {
		t.Fatalf("names = %v, want kept.txt and the already-tracked later.txt", got)
	}
	if len(ignored) != 1 || ignored[0] != "hidden.txt" {
		t.Fatalf("ignored = %v", ignored)
	}
}

func TestAttrsObjectOnlyWhenSomeFileHasAMode(t *testing.T) {
	f := setup(t)
	f.write("plain.txt", "just text")
	h := f.offerMsg("*.txt", "plain")
	if c, _ := f.r.Commit(h); c.Attrs != nil {
		t.Fatal("an all-plain-text offering must not store an attrs object")
	}

	f.write(".hgitattributes", "run.txt executable\n")
	f.write("run.txt", "script")
	f.write("bin.txt", "has a \x00 byte")
	h = f.offerMsg("*.txt", "modes")
	c, err := f.r.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	if c.Attrs == nil {
		t.Fatal("want an attrs object")
	}
	rec, ok := f.r.Get(*c.Attrs)
	if !ok || rec.Type() != archive.Attrs {
		t.Fatal("attrs object missing")
	}
	a, err := object.DecodeAttrs(rec.Content())
	if err != nil {
		t.Fatal(err)
	}
	tr := f.tree(h)
	want := map[uint64]byte{
		idOf(t, tr, "run.txt"): object.ModeExecutable,
		idOf(t, tr, "bin.txt"): object.ModeBinary,
	}
	if len(a.Entries) != 2 {
		t.Fatalf("attrs entries = %+v, want 2", a.Entries)
	}
	for _, e := range a.Entries {
		if want[e.EntityID] != e.Mode {
			t.Fatalf("mode for %#x = %#x, want %#x", e.EntityID, e.Mode, want[e.EntityID])
		}
	}
}

func TestExplicitTextBeatsBinaryDetection(t *testing.T) {
	f := setup(t)
	f.write(".hgitattributes", "bin.txt text\n")
	f.write("bin.txt", "has a \x00 byte")
	h := f.offerMsg("*.txt", "text")
	if c, _ := f.r.Commit(h); c.Attrs != nil {
		t.Fatal("an explicit text attribute must suppress binary detection")
	}
}

func TestRelationCommit(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "alpha")
	first := f.offerMsg("*.txt", "first")

	f.write("a.txt", "alpha fixed")
	h, err := f.offerOpts("*.txt", offer.Options{
		Message:        "correcting",
		Relation:       object.RelCorrects,
		RelationTarget: first,
		RelationEntity: 0x2222,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.r.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	if c.Relation != object.RelCorrects || c.RelationTarget != first || c.RelationEntity != 0x2222 {
		t.Fatalf("relation fields = %v %v %#x", c.Relation, c.RelationTarget, c.RelationEntity)
	}
}

func TestEmptyMatchCommitsAnEmptyTree(t *testing.T) {
	f := setup(t)
	h, err := f.offerOpts("*.txt", offer.Options{Message: "nothing here"})
	if err != nil {
		t.Fatalf("the HolyC records an empty tree rather than refusing: %v", err)
	}
	if got := f.tree(h); len(got.Entries) != 0 {
		t.Fatalf("entries = %v, want none", names(got))
	}
}

func TestMessageTooLong(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "alpha")
	_, err := f.offerOpts("*.txt", offer.Options{Message: strings.Repeat("x", 256)})
	if !errors.Is(err, offer.ErrMessageTooLong) {
		t.Fatalf("err = %v, want ErrMessageTooLong", err)
	}
	if _, err := os.Stat(f.path + ".m"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused offer must not have written anything")
	}
}

// A 255-byte name is the longest a tree entry's U8 name_len can encode, and
// also the longest Linux allows, so offer.ErrNameTooLong is a guard that no
// real directory can trip; the boundary itself is what is testable.
func TestLongestEncodableName(t *testing.T) {
	f := setup(t)
	name := strings.Repeat("n", 251) + ".txt"
	f.write(name, "at the limit")
	h := f.offerMsg("*.txt", "long name")
	if got := names(f.tree(h)); len(got) != 1 || got[0] != name {
		t.Fatalf("names = %v", got)
	}
}

func TestSavedRepoReopensAndChecksClean(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "alpha")
	f.offerMsg("*.txt", "first")
	f.write("a.txt", "alpha two")
	f.write("b.txt", "has a \x00 byte")
	h := f.offerMsg("*.txt", "second")

	for _, p := range []string{f.path, f.path + ".m"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	r, err := repo.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if head, ok := r.Head(r.CurrentPath()); !ok || head != h {
		t.Fatal("head did not round-trip")
	}
	rep := check.Run(r)
	if len(rep.HashBad) != 0 || rep.RefsBroken != 0 || len(rep.Dangling) != 0 {
		t.Fatalf("check report = %+v", rep)
	}
	if rep.HeaderCount != uint64(rep.Objects) {
		t.Fatalf("header count %d != objects %d", rep.HeaderCount, rep.Objects)
	}
	lines, err := r.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Message != "second" || lines[1].Message != "first" {
		t.Fatalf("history = %+v", lines)
	}
}

// TestFlatOfferIgnoresPlainFileMatchingDirPattern: a flat Offer has no
// subdirectory entries at all (workdir.List skips directories outright), but
// a plain FILE literally named "build" is still hidden by a "build/"
// .hgitignore rule - Ignore.HC's IsIgnored compares the DIR pattern against
// the candidate's own last name component only, never checking whether the
// candidate is actually a directory.
func TestFlatOfferIgnoresPlainFileMatchingDirPattern(t *testing.T) {
	f := setup(t)
	f.write(".hgitignore", "build/\n")
	f.write("build", "a plain file, not a directory")
	f.write("keep.txt", "kept")
	var ignored []string
	h, err := f.offerOpts("*", offer.Options{
		Message:   "first",
		OnIgnored: func(n string) { ignored = append(ignored, n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	got := names(f.tree(h))
	for _, n := range got {
		if n == "build" {
			t.Fatalf("names = %v, want the plain file \"build\" hidden", got)
		}
	}
	if len(ignored) != 1 || ignored[0] != "build" {
		t.Fatalf("ignored = %v, want [build]", ignored)
	}
}
