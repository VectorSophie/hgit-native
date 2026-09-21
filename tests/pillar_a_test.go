package tests

import (
	"bytes"
	"errors"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
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
