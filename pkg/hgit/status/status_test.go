package status_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
	"github.com/VectorSophie/hgit-native/pkg/hgit/workdir"
)

// fixture mirrors package offer's own test fixture: one working directory,
// one repo file, offered through the real offer package so status is tested
// against a genuinely-built HEAD, not a hand-rolled one.
type fixture struct {
	t    *testing.T
	dir  string
	path string
	r    *repo.Repo
}

func setup(t *testing.T) *fixture {
	t.Helper()
	old := clock.Now
	clock.Now = func() uint64 { return 1700000000000 }
	t.Cleanup(func() { clock.Now = old })
	dir := t.TempDir()
	path := filepath.Join(dir, "r.hgs")
	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, dir: dir, path: path}
}

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

func (f *fixture) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) mkdir(rel string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir, rel), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) remove(name string) {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.dir, name)); err != nil {
		f.t.Fatal(err)
	}
}

// repoIgnore keeps the repo's own files out of the flat mask/tree walk.
const repoIgnore = "r.hgs\nr.hgs.m\nr.hgs.tmp\n"

func (f *fixture) writeIgnore(extra string) { f.write(".hgitignore", repoIgnore+extra) }

func (f *fixture) offer(mask, msg string) {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := offer.Offer(r, f.dir, mask, offer.Options{Message: msg}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) offerTree(msg string) {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := offer.OfferTree(r, f.dir, offer.Options{Message: msg}); err != nil {
		f.t.Fatal(err)
	}
}

// reopen returns a fresh handle onto the saved repo - the same "status reads
// what offer just saved" shape the scenario replay tests use.
func (f *fixture) reopen() *repo.Repo {
	f.t.Helper()
	r, err := repo.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func kindsAndPaths(t *testing.T, changes []status.Change) []string {
	t.Helper()
	out := make([]string, len(changes))
	for i, c := range changes {
		switch c.Kind {
		case status.Unchanged:
			out[i] = "UNCHANGED " + c.Path
		case status.Modified:
			out[i] = "MODIFIED " + c.Path
		case status.Renamed:
			out[i] = "RENAMED " + c.OldPath + " -> " + c.Path
		case status.New:
			out[i] = "NEW " + c.Path
		case status.Deleted:
			out[i] = "DELETED " + c.Path
		case status.ModeChanged:
			out[i] = "MODE_CHANGED " + c.Path
		case status.TypeChanged:
			out[i] = "TYPE_CHANGED " + c.Path
		}
	}
	return out
}

func TestStatusUnchangedModifiedNewDeleted(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1, 0x2, 0x3)
	f.write("stays.txt", "same")
	f.write("changes.txt", "v1")
	f.write("gone.txt", "bye")
	f.offer("*.txt", "first")

	f.write("changes.txt", "v2")
	f.remove("gone.txt")
	f.write("fresh.txt", "brand new, unrelated content")

	r := f.reopen()
	got, err := status.Status(r, f.dir, "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	// workdir.List returns files sorted by name: changes.txt, stays.txt.
	want := []string{"MODIFIED changes.txt", "UNCHANGED stays.txt", "NEW fresh.txt", "DELETED gone.txt"}
	if got2 := kindsAndPaths(t, got); strings.Join(got2, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %v, want %v", got2, want)
	}
}

func TestStatusRenamedExact(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1)
	f.write("orig.txt", "unchanged content, moved to a new name")
	f.offer("*.txt", "first")

	f.remove("orig.txt")
	f.write("renamed.txt", "unchanged content, moved to a new name")

	r := f.reopen()
	got, err := status.Status(r, f.dir, "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != status.Renamed || got[0].OldPath != "orig.txt" || got[0].Path != "renamed.txt" {
		t.Fatalf("got %+v", got)
	}
}

func TestStatusRenamedFuzzy(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1)
	f.write("orig.txt", "The quick brown fox jumps over the lazy dog repeatedly.")
	f.offer("*.txt", "first")

	f.remove("orig.txt")
	f.write("renamed.txt", "The quick brown fox LEAPS over the lazy dog repeatedly.")

	r := f.reopen()
	got, err := status.Status(r, f.dir, "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != status.Renamed || got[0].OldPath != "orig.txt" || got[0].Path != "renamed.txt" {
		t.Fatalf("got %+v", got)
	}
}

// TestFuzzyRenameSizeCeiling proves MaxFuzzyRenameBytes exactly: a deleted
// file with an edited-and-renamed counterpart is caught by fuzzy matching
// only while the NEW candidate's own content is at most 511 bytes; at 512 or
// beyond it falls back to two separate NEW/DELETED lines - a real, deliberate
// ceiling (docs/porting-notes.md), not a bug.
func TestFuzzyRenameSizeCeiling(t *testing.T) {
	base := strings.Repeat("a", 500) + " the quick brown fox jumps over"
	edit := func(s string) string { return strings.Replace(s, "quick", "slow!", 1) }

	pad := func(s string, n int) string {
		for len(s) < n {
			s += "z"
		}
		return s[:n]
	}

	cases := []struct {
		name string
		size int
		want status.ChangeKind
	}{
		{"511 bytes: fuzzy match", 511, status.Renamed},
		{"512 bytes: too large, falls back to NEW+DELETED", 512, status.New},
		{"513 bytes: too large, falls back to NEW+DELETED", 513, status.New},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := setup(t)
			f.writeIgnore("")
			seedIDs(t, 0x1)
			orig := pad(base, c.size)
			f.write("orig.txt", orig)
			f.offer("*.txt", "first")

			f.remove("orig.txt")
			f.write("renamed.txt", edit(orig))

			r := f.reopen()
			got, err := status.Status(r, f.dir, "*.txt")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 || got[0].Kind != c.want {
				t.Fatalf("size %d: got %+v, want first change kind %v", c.size, got, c.want)
			}
			if c.want == status.New && len(got) != 2 {
				t.Fatalf("size %d: got %+v, want a NEW and a DELETED line", c.size, got)
			}
		})
	}
}

func TestStatusCompetingRenameCandidatesFirstMatchWins(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1, 0x2)
	f.write("a.txt", "identical content")
	f.write("b.txt", "identical content")
	f.offer("*.txt", "first")

	f.remove("a.txt")
	f.remove("b.txt")
	f.write("c.txt", "identical content") // one new candidate, two deleted candidates with the same hash

	r := f.reopen()
	got, err := status.Status(r, f.dir, "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	renamed, deleted := 0, 0
	var renamedFrom string
	for _, c := range got {
		switch c.Kind {
		case status.Renamed:
			renamed++
			renamedFrom = c.OldPath
		case status.Deleted:
			deleted++
		}
	}
	if renamed != 1 || deleted != 1 {
		t.Fatalf("got %+v, want exactly one RENAMED and one DELETED", got)
	}
	if renamedFrom != "a.txt" { // a.txt is scanned first in HEAD-tree order
		t.Fatalf("renamed from %q, want a.txt (first match wins)", renamedFrom)
	}
}

func TestStatusModeChangedIndependentOfContent(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1)
	f.write("script.txt", "#!/bin/sh\necho hi\n")
	f.offer("*.txt", "first")

	f.write(".hgitattributes", "script.txt executable\n")

	r := f.reopen()
	got, err := status.Status(r, f.dir, "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"UNCHANGED script.txt", "MODE_CHANGED script.txt"}
	if got2 := kindsAndPaths(t, got); strings.Join(got2, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %v, want %v", got2, want)
	}
	if got[1].OldMode != 0 || got[1].NewMode != 2 { // object.ModeExecutable == 2
		t.Fatalf("mode change = %d -> %d, want 0 -> 2", got[1].OldMode, got[1].NewMode)
	}
}

func TestStatusIgnoreNeverHidesTracked(t *testing.T) {
	f := setup(t)
	f.writeIgnore("")
	seedIDs(t, 0x1)
	f.write("tracked.tmp", "kept")
	f.offer("*.tmp", "first")

	// A rule that would hide *.tmp is added AFTER tracked.tmp is already
	// tracked - ADR 0014: ignore never hides an already-tracked file.
	f.write(".hgitignore", repoIgnore+"*.tmp\n")

	r := f.reopen()
	got, err := status.Status(r, f.dir, "*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != status.Unchanged || got[0].Path != "tracked.tmp" {
		t.Fatalf("got %+v, want tracked.tmp still reported", got)
	}
}

func TestStatusEmptyRepoNoOfferingsYet(t *testing.T) {
	f := setup(t)
	f.write("a.txt", "hi")
	r := f.reopen()
	_, err := status.Status(r, f.dir, "*.txt")
	var noOff *status.NoOfferingsYetError
	if !errors.As(err, &noOff) {
		t.Fatalf("err = %v, want *NoOfferingsYetError", err)
	}
	found := false
	for _, e := range noOff.Listing {
		if e.Name == "a.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("listing = %+v, want a.txt", noOff.Listing)
	}
}

func TestStatusUnreadableFileTypedError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	f := setup(t)
	f.writeIgnore("")
	f.write("a.txt", "hi")
	f.offer("*.txt", "first")
	if err := os.Chmod(filepath.Join(f.dir, "a.txt"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(f.dir, "a.txt"), 0o644)

	r := f.reopen()
	_, err := status.Status(r, f.dir, "*.txt")
	var re *workdir.ReadError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want *workdir.ReadError", err)
	}
}
