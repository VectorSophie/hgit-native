package meta

import (
	"bytes"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/testfix"
)

// FuzzParse proves Parse never panics on arbitrary bytes, and that anything
// it does accept round-trips stably through Marshal/Parse.
func FuzzParse(f *testing.F) {
	for _, name := range testfix.Names(f, ".hgs.m") {
		f.Add(testfix.Read(f, name))
	}
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, b []byte) {
		file, err := Parse(b)
		if err != nil {
			return
		}
		enc, err := file.Marshal()
		if err != nil {
			t.Fatalf("Marshal of a parsed file failed: %v", err)
		}
		file2, err := Parse(enc)
		if err != nil {
			t.Fatalf("re-parse of our own Marshal failed: %v", err)
		}
		if enc2, _ := file2.Marshal(); !bytes.Equal(enc2, enc) {
			t.Fatalf("decode/encode not stable")
		}
	})
}
