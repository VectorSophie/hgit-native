package tests

import (
	"bytes"
	"errors"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
)

// Pillar A: native reads what TempleOS wrote.
func TestFixturesArchiveRoundTrip(t *testing.T) {
	for _, name := range testfix.Names(t, ".hgs") {
		if name == "TFConfNewer.hgs" {
			continue // covered below
		}
		t.Run(name, func(t *testing.T) {
			raw := testfix.Read(t, name)
			a, err := archive.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if a.Header.Version != 4 {
				t.Fatalf("version %d", a.Header.Version)
			}
			if uint64(len(a.Records)) != a.Header.Count {
				t.Fatalf("header count %d, parsed %d", a.Header.Count, len(a.Records))
			}
			if total, ok := a.Verify(); total != ok {
				t.Fatalf("hash verify %d/%d", ok, total)
			}
			if !bytes.Equal(a.Marshal(), raw) {
				t.Fatal("re-serialised bytes differ from TempleOS's")
			}
		})
	}
}

func TestFixtureNewerFormatRejected(t *testing.T) {
	_, err := archive.Parse(testfix.Read(t, "TFConfNewer.hgs"))
	var uv *archive.UnsupportedVersionError
	if !errors.As(err, &uv) || uv.Version != 9 {
		t.Fatalf("got %v", err)
	}
}

func TestFixtureObjectsRoundTrip(t *testing.T) {
	treeCount := 0
	commitCount := 0
	attrsCount := 0
	conflictCount := 0
	for _, name := range testfix.Names(t, ".hgs") {
		if name == "TFConfNewer.hgs" {
			continue
		}
		a, err := archive.Parse(testfix.Read(t, name))
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range a.Records {
			var enc []byte
			switch r.Type() {
			case archive.Tree:
				treeCount++
				tr, err := object.DecodeTree(r.Content())
				if err != nil {
					t.Fatalf("%s #%d tree: %v", name, i, err)
				}
				enc = tr.Encode()
			case archive.Commit:
				commitCount++
				c, err := object.DecodeCommit(r.Content())
				if err != nil {
					t.Fatalf("%s #%d commit: %v", name, i, err)
				}
				enc = c.Encode()
			case archive.Attrs:
				attrsCount++
				a, err := object.DecodeAttrs(r.Content())
				if err != nil {
					t.Fatalf("%s #%d attrs: %v", name, i, err)
				}
				enc = a.Encode()
			case archive.Conflict:
				conflictCount++
				c, err := object.DecodeConflict(r.Content())
				if err != nil {
					t.Fatalf("%s #%d conflict: %v", name, i, err)
				}
				enc = c.Encode()
			default:
				continue
			}
			if !bytes.Equal(enc, r.Content()) {
				t.Fatalf("%s #%d %v: re-encode differs", name, i, r.Type())
			}
		}
	}
	if treeCount == 0 {
		t.Fatal("no trees decoded")
	}
	if commitCount == 0 {
		t.Fatal("no commits decoded")
	}
	if attrsCount == 0 {
		t.Fatal("no attrs decoded")
	}
	if conflictCount == 0 {
		t.Fatal("no conflicts decoded")
	}
}

func TestFixtureMetaRoundTrip(t *testing.T) {
	count := 0
	for _, name := range testfix.Names(t, ".hgs.m") {
		count++
		raw := testfix.Read(t, name)
		f, err := meta.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(f.Marshal(), raw) {
			t.Fatalf("%s: re-serialised bytes differ", name)
		}
	}
	if count == 0 {
		t.Fatalf("no .hgs.m fixtures processed")
	}
}
