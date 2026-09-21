package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/cli"
	"github.com/VectorSophie/hgit-native/internal/testfix"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// The regression's offertree steps (contract/tests/full-regression.hc):
// TFTreeRoot/ holds top.txt and SubA/inner.txt; three offers follow - two
// plain ones and a correcttree relating back to the second. The timestamps
// and entity ids below are read out of contract/fixtures/TFullTreeRepo.hgs
// itself, which is what lets the whole tree and commit objects - not only
// their shapes - be compared byte for byte.
const (
	treeTS1, treeTS2, treeTS3 = 527218, 529491, 529858

	fixInnerID = 0xfecf09b6855459dc
	fixSubAID  = 0x9ae14faf060d6dcc
	fixTopID   = 0x02014c040320994e
)

// TestScenarioOfferTreeReplaysRegression replays the regression's offertree
// and correcttree steps natively against TFullTreeRepo.hgs.
func TestScenarioOfferTreeReplaysRegression(t *testing.T) {
	oldClock, oldID := clock.Now, offer.NewEntityID
	t.Cleanup(func() { clock.Now, offer.NewEntityID = oldClock, oldID })

	// TreeBuildRecursive walks SubA before top.txt (byte order), and inside
	// SubA it resolves inner.txt's identity before SubA's own - so the
	// fixture's three ids are handed out in exactly this order.
	ids := []uint64{fixInnerID, fixSubAID, fixTopID}
	n := 0
	offer.NewEntityID = func() uint64 {
		n++
		if n <= len(ids) {
			return ids[n-1]
		}
		t.Errorf("a fourth entity id was generated; the fixture has three")
		return uint64(n)
	}

	base := t.TempDir()
	path := filepath.Join(base, "TFullTreeRepo.hgs")
	root := filepath.Join(base, "TFTreeRoot")
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	doOfferTree := func(ts uint64, opts offer.Options) archive.Hash {
		t.Helper()
		clock.Now = func() uint64 { return ts }
		r, err := repo.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		h, err := offer.OfferTree(r, root, opts)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}

	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "SubA"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("top.txt", "tree top v1")
	write("SubA/inner.txt", "tree inner v1")
	doOfferTree(treeTS1, offer.Options{Message: "tree_first_offer"})

	write("SubA/inner.txt", "tree inner v2 CHANGED")
	head2 := doOfferTree(treeTS2, offer.Options{Message: "tree_second_offer"})

	// correcttree <repo> <head2> 0000000000000000 <dir> tree_correcting_offer
	entity, err := archive.ParseEntityID("0000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	doOfferTree(treeTS3, offer.Options{
		Message:        "tree_correcting_offer",
		Relation:       object.RelCorrects,
		RelationTarget: head2,
		RelationEntity: entity,
	})

	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fix := openFixture(t, "TFullTreeRepo.hgs")

	// Strongest available parity evidence: every record this port wrote is
	// byte-identical to the one TempleOS wrote, in the same order - blobs,
	// both levels of tree, the attrs-free commits and their hashes.
	if len(r.Arc.Records) != len(fix.Arc.Records) {
		t.Fatalf("%d records, want %d", len(r.Arc.Records), len(fix.Arc.Records))
	}
	for i, rec := range r.Arc.Records {
		w := fix.Arc.Records[i]
		if rec.Hash != w.Hash {
			t.Fatalf("record %d (type %d): hash %s, want %s", i, rec.Type(), rec.Hash.Hex(), w.Hash.Hex())
		}
	}

	// Spelled out, so a failure says which property broke rather than only
	// "the bytes differ": entry names, types and the entity-id sharing.
	got, want := fixtureTree(t, r, "tree_second_offer"), fixtureTree(t, fix, "tree_second_offer")
	if len(got.Entries) != 2 || got.Entries[0].Name != "SubA" || got.Entries[1].Name != "top.txt" {
		t.Fatalf("root entries = %+v", got.Entries)
	}
	if got.Entries[0].ChildType != archive.Tree || got.Entries[1].ChildType != archive.Blob {
		t.Fatalf("root entry types = %d %d", got.Entries[0].ChildType, got.Entries[1].ChildType)
	}
	for i, e := range got.Entries {
		if e.EntityID != want.Entries[i].EntityID {
			t.Fatalf("%s entity id = %016x, want %016x", e.Name, e.EntityID, want.Entries[i].EntityID)
		}
	}
	first := fixtureTree(t, r, "tree_first_offer")
	if first.Entries[0].EntityID != got.Entries[0].EntityID || first.Entries[1].EntityID != got.Entries[1].EntityID {
		t.Fatal("both root entries must keep their identity across the second offer")
	}
	gotSub, wantSub := subtree(t, r, got, "SubA"), subtree(t, fix, want, "SubA")
	firstSub := subtree(t, r, first, "SubA")
	if len(gotSub.Entries) != 1 || gotSub.Entries[0].Name != "inner.txt" {
		t.Fatalf("SubA entries = %+v", gotSub.Entries)
	}
	if gotSub.Entries[0].EntityID != wantSub.Entries[0].EntityID ||
		gotSub.Entries[0].EntityID != firstSub.Entries[0].EntityID {
		t.Fatal("the nested file must keep its identity across a nested edit")
	}

	// The correcttree commit's relation fields, against the fixture's.
	gotRel, wantRel := commitNamed(t, r, "tree_correcting_offer"), commitNamed(t, fix, "tree_correcting_offer")
	if gotRel.Relation != object.RelCorrects || gotRel.Relation != wantRel.Relation ||
		gotRel.RelationTarget != wantRel.RelationTarget || gotRel.RelationEntity != wantRel.RelationEntity {
		t.Fatalf("relation = %v %s %#x; want %v %s %#x",
			gotRel.Relation, gotRel.RelationTarget.Hex(), gotRel.RelationEntity,
			wantRel.Relation, wantRel.RelationTarget.Hex(), wantRel.RelationEntity)
	}
	if gotRel.RelationTarget != head2 {
		t.Fatal("correcttree must relate to the commit HEAD pointed at")
	}

	rep := check.Run(r)
	if len(rep.HashBad) != 0 || rep.RefsBroken != 0 || len(rep.Dangling) != 0 {
		t.Fatalf("check report = %+v", rep)
	}
	gotCheck := cli.SerialCheck(rep, nil)
	wantCheck := segment(t, testfix.ExpectedLog(t), "TFULL_CHECK_TREE_BEGIN", "TFULL_CHECK_TREE_END_MARKER")
	var wantObjects int
	if _, err := fmt.Sscanf(wantCheck, "CHECK_OK objects=%d", &wantObjects); err != nil {
		t.Fatalf("cannot read the expected object count from %q: %v", wantCheck, err)
	}
	if rep.Objects != wantObjects {
		t.Fatalf("objects = %d, want %d", rep.Objects, wantObjects)
	}
	if normalize(strings.TrimSpace(gotCheck)) != normalize(wantCheck) {
		t.Fatalf("check output:\n got:\n%s\nwant:\n%s", gotCheck, wantCheck)
	}
}

// TestScenarioOfferTreeReplaysIgnoreStep replays the regression's ignore
// step, which is an offertree too: the .hgitignore is itself offered, x.tmp
// is not, and the object count matches the log.
func TestScenarioOfferTreeReplaysIgnoreStep(t *testing.T) {
	oldClock, oldID := clock.Now, offer.NewEntityID
	t.Cleanup(func() { clock.Now, offer.NewEntityID = oldClock, oldID })
	clock.Now = func() uint64 { return 530000 }
	next := uint64(0)
	offer.NewEntityID = func() uint64 { next++; return next }

	base := t.TempDir()
	path := filepath.Join(base, "TFIgnoreRepo.hgs")
	root := filepath.Join(base, "TFIgnoreRoot")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ name, content string }{
		{".hgitignore", "*.tmp\n"}, {"keep.txt", "kept"}, {"x.tmp", "ignored"},
	} {
		if err := os.WriteFile(filepath.Join(root, f.name), []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var ignored []string
	h, err := offer.OfferTree(r, root, offer.Options{
		Message:   "ignore_test_offer",
		OnIgnored: func(name string) { ignored = append(ignored, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ignored) != 1 || ignored[0] != "x.tmp" {
		t.Fatalf("OnIgnored = %v, want [x.tmp]", ignored)
	}
	c, err := r.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := r.Tree(c.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Entries) != 2 || tr.Entries[0].Name != ".hgitignore" || tr.Entries[1].Name != "keep.txt" {
		t.Fatalf("entries = %+v, want .hgitignore and keep.txt", tr.Entries)
	}

	rep := check.Run(r)
	wantCheck := segment(t, testfix.ExpectedLog(t), "TFULL_IGNORE_CHECK_BEGIN", "TFULL_IGNORE_CHECK_END_MARKER")
	var wantObjects int
	if _, err := fmt.Sscanf(wantCheck, "CHECK_OK objects=%d", &wantObjects); err != nil {
		t.Fatal(err)
	}
	if rep.Objects != wantObjects {
		t.Fatalf("objects = %d, want %d", rep.Objects, wantObjects)
	}
	if normalize(strings.TrimSpace(cli.SerialCheck(rep, nil))) != normalize(wantCheck) {
		t.Fatalf("check output:\n%s\nwant:\n%s", cli.SerialCheck(rep, nil), wantCheck)
	}
}

func commitNamed(t *testing.T, r *repo.Repo, msg string) *object.Commit {
	t.Helper()
	lines, err := r.History()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Message == msg {
			c, err := r.Commit(l.Hash)
			if err != nil {
				t.Fatal(err)
			}
			return c
		}
	}
	t.Fatalf("no commit named %q", msg)
	return nil
}

func subtree(t *testing.T, r *repo.Repo, parent *object.Tree, name string) *object.Tree {
	t.Helper()
	e, ok := parent.Find(name)
	if !ok || e.ChildType != archive.Tree {
		t.Fatalf("%s is not a tree entry of %+v", name, parent.Entries)
	}
	tr, err := r.Tree(e.ChildHash)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}
