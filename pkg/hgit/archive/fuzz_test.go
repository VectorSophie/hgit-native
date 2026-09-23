package archive

import (
	"bytes"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/testfix"
)

// FuzzParse proves Parse never panics on arbitrary bytes, and that anything
// it does accept round-trips stably through Marshal/Parse.
func FuzzParse(f *testing.F) {
	for _, name := range testfix.Names(f, ".hgs") {
		f.Add(testfix.Read(f, name))
	}
	f.Add([]byte("HGS0"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, b []byte) {
		a, err := Parse(b)
		if err != nil {
			return
		}
		enc := a.Marshal()
		a2, err := Parse(enc)
		if err != nil {
			t.Fatalf("re-parse of our own Marshal failed: %v", err)
		}
		if !bytes.Equal(a2.Marshal(), enc) {
			t.Fatalf("decode/encode not stable")
		}
	})
}
