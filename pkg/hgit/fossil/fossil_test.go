package fossil

import (
	"bytes"
	"math"
	"math/rand"
	"strings"
	"testing"
)

func TestIntRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, 63, 64, 4095, 4096, 1 << 31, math.MaxInt64} {
		b := PutInt(v)
		got, next, err := GetInt(b, 0)
		if err != nil || got != v || next != len(b) {
			t.Fatalf("%d: got %d next %d err %v (enc %q)", v, got, next, err, b)
		}
	}
	if string(PutInt(0)) != "0" || string(PutInt(63)) != "~" || string(PutInt(64)) != "10" || string(PutInt(36)) != "_" {
		t.Fatal("encoding differs from Fossil alphabet")
	}
}

func TestGetIntErrors(t *testing.T) {
	for _, in := range []string{"", ";", "@1", "zzzzzzzzzzzzzzzz", "~~~~~~~~~~~"} {
		if _, _, err := GetInt([]byte(in), 0); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
	if _, _, err := GetInt([]byte("1"), 5); err == nil {
		t.Error("pos beyond buffer: expected error")
	}
	v, next, err := GetInt([]byte("1A:"), 0)
	if err != nil || v != 64+10 || next != 2 {
		t.Errorf("stop at delimiter: %d %d %v", v, next, err)
	}
}

func TestChecksum(t *testing.T) {
	if got := Checksum([]byte("hello there, big wide world!")); got != 1432286299 {
		t.Fatalf("got %d", got)
	}
	if Checksum(nil) != 0 {
		t.Fatal("empty")
	}
	if Checksum([]byte{1}) != 1<<24 || Checksum([]byte{1, 2, 3}) != 0x01020300 {
		t.Fatal("tail padding")
	}
	if Checksum([]byte{0xff, 0xff, 0xff, 0xff, 0, 0, 0, 2}) != 1 {
		t.Fatal("wraparound")
	}
}

var cases = map[string][2]string{
	"empty":       {"", ""},
	"emptySource": {"", "some target text"},
	"emptyTarget": {"some source text", ""},
	"identical":   {"the quick brown fox jumps", "the quick brown fox jumps"},
	"prefix":      {"the quick brown fox jumps over", "A NEW BEGINNING brown fox jumps over"},
	"suffix":      {"the quick brown fox jumps over", "the quick brown fox jumps UNDER!!"},
	"middle": {
		"The quick brown fox jumps over the lazy dog. The quick brown fox jumps over the lazy dog again.",
		"The quick brown fox LEAPS over the lazy dog. The quick brown fox jumps over the lazy dog again."},
	"different": {"aaaaaaaaaaaa", "ZZZZZZZZ!!"},
	"repeated":  {strings.Repeat("abcd", 50), strings.Repeat("abcd", 80)},
}

func TestDeltaRoundTrip(t *testing.T) {
	for name, c := range cases {
		for mk, f := range map[string]func(a, b []byte) []byte{"trivial": DeltaMakeTrivial, "real": DeltaMakeReal} {
			d := f([]byte(c[0]), []byte(c[1]))
			got, err := DeltaApply([]byte(c[0]), d)
			if err != nil || string(got) != c[1] {
				t.Errorf("%s/%s: %q err %v (delta %q)", name, mk, got, err, d)
			}
		}
	}
	// real one actually compresses the middle edit
	c := cases["middle"]
	if d := DeltaMakeReal([]byte(c[0]), []byte(c[1])); len(d) >= len(c[1]) {
		t.Errorf("no compression: %d", len(d))
	}
}

func TestDeltaMakeRealDeterministic(t *testing.T) {
	c := cases["middle"]
	a := DeltaMakeReal([]byte(c[0]), []byte(c[1]))
	for i := 0; i < 5; i++ {
		if !bytes.Equal(a, DeltaMakeReal([]byte(c[0]), []byte(c[1]))) {
			t.Fatal("nondeterministic")
		}
	}
}

func TestDeltaApplyRejects(t *testing.T) {
	src := []byte("0123456789")
	cs := string(PutInt(uint64Sum("abc")))
	bad := map[string]string{
		"bad checksum":      "3\n3:abc" + string(PutInt(1)) + ";",
		"truncated":         "3\n3:ab",
		"truncated no trl":  "3\n3:abc",
		"copy beyond src":   "5\n5@8,0;",
		"literal beyond":    "9\n9:abc" + cs + ";",
		"huge declared":     "~~~~~~~~~~\n1:a" + cs + ";",
		"declared > bound":  "1000000\n1:a" + cs + ";",
		"segment overshoot": "1\n3:abc" + cs + ";",
		"unknown op":        "3\n3#abc" + cs + ";",
		"no header nl":      "3 3:abc",
		"length mismatch":   "4\n3:abc" + cs + ";",
	}
	for name, d := range bad {
		if _, err := DeltaApply(src, []byte(d)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if got, err := DeltaApply(src, []byte("3\n3:abc"+cs+";")); err != nil || string(got) != "abc" {
		t.Errorf("sanity: %q %v", got, err)
	}
}

func uint64Sum(s string) int64 { return int64(Checksum([]byte(s))) }

func TestDeltaApplyFuzzNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	src := []byte(cases["middle"][0])
	good := DeltaMakeReal(src, []byte(cases["middle"][1]))
	alpha := []byte("0123456789AZ_az~\n@,:;")
	for i := 0; i < 20000; i++ {
		var d []byte
		if i%2 == 0 {
			d = make([]byte, rng.Intn(40))
			for j := range d {
				if rng.Intn(3) == 0 {
					d[j] = byte(rng.Intn(256))
				} else {
					d[j] = alpha[rng.Intn(len(alpha))]
				}
			}
		} else {
			d = append([]byte(nil), good...)
			for k := rng.Intn(4) + 1; k > 0 && len(d) > 0; k-- {
				d[rng.Intn(len(d))] = alpha[rng.Intn(len(alpha))]
			}
			if rng.Intn(4) == 0 {
				d = d[:rng.Intn(len(d)+1)]
			}
		}
		DeltaApply(src, d)
		DeltaApply(nil, d)
	}
}

func TestSimilarityPercent(t *testing.T) {
	orig := "The quick brown fox jumps over the lazy dog. The quick brown fox jumps over the lazy dog again."
	ren := "The quick brown fox LEAPS over the lazy dog. The quick brown fox jumps over the lazy dog again."
	if p := SimilarityPercent([]byte(orig), []byte(ren)); p < RenameSimilarityThreshold || p != 73 {
		t.Errorf("rename-with-edit: %d", p)
	}
	if p := SimilarityPercent([]byte(orig), []byte("brand new content, unrelated")); p >= RenameSimilarityThreshold {
		t.Errorf("unrelated: %d", p)
	}
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"aaa", "", 0}, {"", "aaa", 0}, {"", "", 0},
		{"same", "same", 100},
		{"aaaaaaaaaaaa", "ZZZZZZZZ!!", 0},
		{"line one\nline two\n", "line one\nline two\nline three\n", 62},
	} {
		if got := SimilarityPercent([]byte(c.a), []byte(c.b)); got != c.want {
			t.Errorf("%q,%q: %d want %d", c.a, c.b, got, c.want)
		}
	}
}
