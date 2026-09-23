package fossil

import (
	"bytes"
	"testing"
)

// FuzzDeltaApply proves DeltaApply never panics on arbitrary source and
// delta bytes, and that whatever it accepts it accepts deterministically.
func FuzzDeltaApply(f *testing.F) {
	src := []byte("the quick brown fox jumps over the lazy dog\n")
	tgt := []byte("the quick red fox jumps over the lazy cat\n")
	f.Add(src, DeltaMakeReal(src, tgt))
	f.Add(src, DeltaMakeTrivial(src, tgt))
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, source, delta []byte) {
		out, err := DeltaApply(source, delta)
		if err != nil {
			return
		}
		if again, err := DeltaApply(source, delta); err != nil || !bytes.Equal(again, out) {
			t.Fatal("DeltaApply is not deterministic")
		}
	})
}
