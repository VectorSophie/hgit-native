package object

import (
	"bytes"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

// seedsByType parses every .hgs fixture and returns the content (tag byte
// stripped) of every record of the given type, for use as fuzz seeds.
func seedsByType(t testing.TB, typ archive.Type) [][]byte {
	t.Helper()
	var out [][]byte
	for _, name := range testfix.Names(t, ".hgs") {
		a, err := archive.Parse(testfix.Read(t, name))
		if err != nil {
			continue
		}
		for _, r := range a.Records {
			if r.Type() == typ {
				out = append(out, r.Content())
			}
		}
	}
	return out
}

func FuzzDecodeTree(f *testing.F) {
	for _, b := range seedsByType(f, archive.Tree) {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		tr, err := DecodeTree(b)
		if err != nil {
			return
		}
		enc := tr.Encode()
		tr2, err := DecodeTree(enc)
		if err != nil {
			t.Fatalf("re-decode of our own Encode failed: %v", err)
		}
		if !bytes.Equal(tr2.Encode(), enc) {
			t.Fatal("decode/encode not stable")
		}
	})
}

func FuzzDecodeCommit(f *testing.F) {
	for _, b := range seedsByType(f, archive.Commit) {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := DecodeCommit(b)
		if err != nil {
			return
		}
		enc := c.Encode()
		c2, err := DecodeCommit(enc)
		if err != nil {
			t.Fatalf("re-decode of our own Encode failed: %v", err)
		}
		if !bytes.Equal(c2.Encode(), enc) {
			t.Fatal("decode/encode not stable")
		}
	})
}

func FuzzDecodeAttrs(f *testing.F) {
	for _, b := range seedsByType(f, archive.Attrs) {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		a, err := DecodeAttrs(b)
		if err != nil {
			return
		}
		enc := a.Encode()
		a2, err := DecodeAttrs(enc)
		if err != nil {
			t.Fatalf("re-decode of our own Encode failed: %v", err)
		}
		if !bytes.Equal(a2.Encode(), enc) {
			t.Fatal("decode/encode not stable")
		}
	})
}

func FuzzDecodeConflict(f *testing.F) {
	for _, b := range seedsByType(f, archive.Conflict) {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := DecodeConflict(b)
		if err != nil {
			return
		}
		enc := c.Encode()
		c2, err := DecodeConflict(enc)
		if err != nil {
			t.Fatalf("re-decode of our own Encode failed: %v", err)
		}
		if !bytes.Equal(c2.Encode(), enc) {
			t.Fatal("decode/encode not stable")
		}
	})
}
