package fossil

import (
	"bytes"
	"testing"
)

// FuzzDeltaApply proves DeltaApply never panics on arbitrary source and
// delta bytes, that whatever it accepts it accepts deterministically, and
// that it never exceeds the documented output cap.
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
		if len(out) > maxApplyOutput {
			t.Fatalf("output %d bytes exceeds the documented cap %d", len(out), maxApplyOutput)
		}
		if again, err := DeltaApply(source, delta); err != nil || !bytes.Equal(again, out) {
			t.Fatal("DeltaApply is not deterministic")
		}
	})
}

// FuzzDeltaRoundTrip proves DeltaApply always accepts DeltaMakeReal's own
// output and reproduces the exact target it was built from, for arbitrary
// source/target pairs.
func FuzzDeltaRoundTrip(f *testing.F) {
	f.Add([]byte("the quick brown fox jumps over the lazy dog\n"), []byte("the quick red fox jumps over the lazy cat\n"))
	f.Add([]byte{}, []byte{})
	f.Add([]byte("abcdefgh"), []byte("XXabcdefgh!"))

	f.Fuzz(func(t *testing.T, source, target []byte) {
		delta := DeltaMakeReal(source, target)
		out, err := DeltaApply(source, delta)
		if err != nil {
			t.Fatalf("DeltaApply rejected its own DeltaMakeReal output: %v", err)
		}
		if !bytes.Equal(out, target) {
			t.Fatal("DeltaApply(source, DeltaMakeReal(source, target)) != target")
		}
	})
}
