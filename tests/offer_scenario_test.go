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

// The regression writes these two with FileWrite(name, <95-char literal>, 97):
// 97 bytes from a 95-character literal is the literal, its terminating NUL and
// one byte past it. The fixture's own blob records show that trailing byte is
// 'C', so these are the exact bytes TempleOS stored - which is what makes the
// blob-hash comparison below real parity evidence rather than a re-derivation.
const (
	origContent = "The quick brown fox jumps over the lazy dog. " +
		"The quick brown fox jumps over the lazy dog again.\x00C"
	renamedContent = "The quick brown fox LEAPS over the lazy dog. " +
		"The quick brown fox jumps over the lazy dog again.\x00C"
)

// fixtureTree returns the tree of the fixture commit whose message is msg.
func fixtureTree(t *testing.T, r *repo.Repo, msg string) *object.Tree {
	t.Helper()
	lines, err := r.History()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Message != msg {
			continue
		}
		c, err := r.Commit(l.Hash)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := r.Tree(c.Tree)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	t.Fatalf("no commit named %q in the fixture", msg)
	return nil
}

func byName(tr *object.Tree) map[string]object.Entry {
	m := make(map[string]object.Entry, len(tr.Entries))
	for _, e := range tr.Entries {
		m[e.Name] = e
	}
	return m
}

// TestScenarioOfferReplaysRegression replays the regression's first two offers
// natively and compares the result against contract/fixtures/TFullRepo.hgs.
func TestScenarioOfferReplaysRegression(t *testing.T) {
	oldClock, oldID := clock.Now, offer.NewEntityID
	t.Cleanup(func() { clock.Now, offer.NewEntityID = oldClock, oldID })
	clock.Now = func() uint64 { return 345402 }
	next := uint64(0)
	offer.NewEntityID = func() uint64 { next++; return next }

	dir := t.TempDir()
	path := filepath.Join(dir, "TFullRepo.hgs")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	doOffer := func(msg string) archive.Hash {
		t.Helper()
		r, err := repo.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		h, err := offer.Offer(r, dir, "TF*.txt", offer.Options{Message: msg})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}

	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	write("TFStays.txt", "never touched")
	write("TFToModify.txt", "version one")
	write("TFToDelete.txt", "going away")
	write("TFOrig.txt", origContent)
	doOffer("first_offer")

	write("TFToModify.txt", "version two")
	if err := os.Remove(filepath.Join(dir, "TFToDelete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "TFOrig.txt")); err != nil {
		t.Fatal(err)
	}
	write("TFRenamed.txt", renamedContent)
	write("TFGenuinelyNew.txt", "brand new content, unrelated")
	clock.Now = func() uint64 { return 394453 }
	doOffer("second_offer")

	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fix := openFixture(t, "TFullRepo.hgs")

	// (c) history
	lines, err := r.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Message != "second_offer" || lines[1].Message != "first_offer" {
		t.Fatalf("history = %+v", lines)
	}

	got1, want1 := fixtureTree(t, r, "first_offer"), fixtureTree(t, fix, "first_offer")
	got2, want2 := fixtureTree(t, r, "second_offer"), fixtureTree(t, fix, "second_offer")

	// (a) same entry names, order and child types; (d) same blob hashes.
	for _, p := range []struct {
		label     string
		got, want *object.Tree
	}{{"first", got1, want1}, {"second", got2, want2}} {
		if len(p.got.Entries) != len(p.want.Entries) {
			t.Fatalf("%s tree: %d entries, want %d", p.label, len(p.got.Entries), len(p.want.Entries))
		}
		for i, e := range p.got.Entries {
			w := p.want.Entries[i]
			if e.Name != w.Name || e.ChildType != w.ChildType {
				t.Fatalf("%s tree entry %d = %q/%d, want %q/%d", p.label, i, e.Name, e.ChildType, w.Name, w.ChildType)
			}
			if e.ChildHash != w.ChildHash {
				t.Fatalf("%s tree: blob hash for %s = %s, want %s", p.label, e.Name, e.ChildHash.Hex(), w.ChildHash.Hex())
			}
		}
	}

	// (b) the identity pattern: carried, renamed, new.
	g1, g2, w1, w2 := byName(got1), byName(got2), byName(want1), byName(want2)
	for _, n := range []string{"TFStays.txt", "TFToModify.txt"} {
		if g2[n].EntityID != g1[n].EntityID {
			t.Fatalf("%s should carry its entity id forward", n)
		}
		if w2[n].EntityID != w1[n].EntityID {
			t.Fatalf("fixture invariant broken for %s", n)
		}
	}
	if g2["TFRenamed.txt"].EntityID != g1["TFOrig.txt"].EntityID {
		t.Fatal("TFOrig.txt -> TFRenamed.txt should be detected as the same entity")
	}
	if w2["TFRenamed.txt"].EntityID != w1["TFOrig.txt"].EntityID {
		t.Fatal("fixture invariant broken for the rename")
	}
	for _, id := range []uint64{g1["TFStays.txt"].EntityID, g1["TFToModify.txt"].EntityID, g1["TFOrig.txt"].EntityID} {
		if g2["TFGenuinelyNew.txt"].EntityID == id {
			t.Fatal("TFGenuinelyNew.txt must get a fresh identity")
		}
	}

	// The renamed file's content carries an embedded NUL, so both commits
	// carry a one-entry attrs object marking that entity binary - as the
	// fixture's own OBJ_ATTRS records do.
	for _, msg := range []string{"first_offer", "second_offer"} {
		gotMode := attrsOf(t, r, msg)
		if wantMode := attrsOf(t, fix, msg); len(gotMode) != len(wantMode) {
			t.Fatalf("%s attrs: %d entries, want %d", msg, len(gotMode), len(wantMode))
		}
		if len(gotMode) != 1 {
			t.Fatalf("%s attrs = %+v, want one binary entry", msg, gotMode)
		}
		if gotMode[0].Mode != object.ModeBinary {
			t.Fatalf("%s mode = %#x, want binary", msg, gotMode[0].Mode)
		}
	}

	// (3) the produced repo is internally consistent, and reports the same
	// `check` tokens - object count included - as the regression's own
	// post-second-offer check. That count only matches because a record is
	// appended per offered file, unchanged files included, as ObjectPut does.
	rep := check.Run(r)
	if len(rep.HashBad) != 0 || rep.RefsBroken != 0 || len(rep.Dangling) != 0 {
		t.Fatalf("check report = %+v", rep)
	}
	gotCheck := cli.SerialCheck(rep, nil)
	wantCheck := segment(t, testfix.ExpectedLog(t), "TFULL_CHECK_BEGIN", "TFULL_CHECK_END_MARKER")
	var wantObjects int
	if _, err := fmt.Sscanf(wantCheck, "CHECK_OK objects=%d", &wantObjects); err != nil {
		t.Fatalf("cannot read the expected object count from %q: %v", wantCheck, err)
	}
	if rep.Objects != wantObjects {
		t.Fatalf("objects = %d, want %d - the same records TempleOS wrote", rep.Objects, wantObjects)
	}
	if normalize(strings.TrimSpace(gotCheck)) != normalize(wantCheck) {
		t.Fatalf("check output:\n got:\n%s\nwant:\n%s", gotCheck, wantCheck)
	}
}

func attrsOf(t *testing.T, r *repo.Repo, msg string) []object.AttrEntry {
	t.Helper()
	lines, err := r.History()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Message != msg {
			continue
		}
		c, err := r.Commit(l.Hash)
		if err != nil {
			t.Fatal(err)
		}
		if c.Attrs == nil {
			return nil
		}
		rec, ok := r.Get(*c.Attrs)
		if !ok {
			t.Fatalf("%s: attrs object missing", msg)
		}
		a, err := object.DecodeAttrs(rec.Content())
		if err != nil {
			t.Fatal(err)
		}
		return a.Entries
	}
	t.Fatalf("no commit named %q", msg)
	return nil
}
