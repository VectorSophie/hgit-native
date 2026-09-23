package attrs

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/ignore"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
)

const (
	bin  = object.ModeBinary
	exec = object.ModeExecutable
)

func TestMode(t *testing.T) {
	tests := []struct {
		name     string
		rules    string
		path     string
		mode     byte
		explicit bool
	}{
		// ADR 0015 examples.
		{"png binary", "*.png binary\n*.hc text,executable\nbuild/* binary\n", "a/x.png", bin, true},
		{"hc text,executable", "*.png binary\n*.hc text,executable\nbuild/* binary\n", "x.hc", exec, true},
		{"build contents binary", "*.png binary\n*.hc text,executable\nbuild/* binary\n", "build/o.dat", bin, true},
		{"build deeper not", "build/* binary\n", "build/sub/o.dat", 0, false},
		{"no match", "*.png binary\n", "x.txt", 0, false},
		// Regression scenario: "TFAttrsScript.txt executable" -> mode 2.
		{"regression executable", "TFAttrsScript.txt executable\n", "TFAttrsScript.txt", exec, false},
		{"regression mm", "TFMM_a.txt executable\n", "TFMM_a.txt", exec, false},
		// Dimensions independent, last match wins per dimension.
		{"last wins text/binary", "*.x binary\n*.x text\n", "a.x", 0, true},
		{"last wins binary", "*.x text\n*.x binary\n", "a.x", bin, true},
		{"dimensions independent", "*.x binary\n*.x executable\n", "a.x", bin | exec, true},
		{"executable never unset", "*.x executable\n*.x text\n", "a.x", exec, true},
		{"explicit text no exec", "*.x text\n", "a.x", 0, true},
		// Line handling.
		{"crlf", "*.png binary\r\n", "a.png", bin, true},
		{"comment and blank", "# *.png binary\n\n", "a.png", 0, false},
		{"unknown attr alone dropped", "*.png frob\n", "a.png", 0, false},
		{"unknown attr with known kept", "*.png frob,binary\n", "a.png", bin, true},
		{"empty tokens", "*.png ,binary,\n", "a.png", bin, true},
		{"no attr list", "*.png\n", "a.png", 0, false},
		{"trailing space only", "*.png \n", "a.png", 0, false},
		{"trailing space after attr is unknown token", "*.png binary \n", "a.png", 0, false},
		{"leading space no pattern", " binary\n", "a.png", 0, false},
		{"slash pattern skipped", "a/b.png binary\n", "a/b.png", 0, false},
		{"dir pattern no subtree", "assets/ binary\n", "x/assets/f.dat", 0, false},
		{"dir pattern last component", "assets/ binary\n", "x/assets", bin, true},
		{"dir pattern root", "assets/ binary\n", "assets", bin, true},
		{"dir pattern same-named file", "assets/ binary\n", "sub/assets", bin, true},
		{"anchored vs nested", "build/* binary\n", "x/build/f", 0, false},
		{"anchored root child", "build/* binary\n", "build/f", bin, true},
		{"tab is not a separator", "*.png\tbinary\n", "a.png", 0, false},
		{"tab in token is unknown", "*.png binary\t\n", "a.png", 0, false},
		{"space then only comma", "*.png ,\n", "a.png", 0, false},
		{"unknown attr foo", "*.png foo\n", "a.png", 0, false},
		{"lone slash pattern", "/ binary\n", "a", 0, false},
		{"second space is inside token", "*.png binary,text extra\n", "a.png", bin, true},
		{"negation not supported (literal)", "!*.png binary\n", "a.png", 0, false},
		{"empty", "", "a", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, e := ParseAttrs(tc.rules).Mode(tc.path)
			if m != tc.mode || e != tc.explicit {
				t.Errorf("Mode(%q) with %q = (%d,%v), want (%d,%v)", tc.path, tc.rules, m, e, tc.mode, tc.explicit)
			}
		})
	}
}

// contract/tests/full-regression.hc lines 253-257: .hgitattributes is
// "TFAttrsScript.txt executable\n" and TFAttrsScript.txt ("echo hi", text)
// is expected as STATUS_MODE_CHANGED 0 -> 2 (fixtures/expected.log line 141).
func TestRegressionScenario(t *testing.T) {
	r := ParseAttrs("TFAttrsScript.txt executable\n")
	auto := DetectBinary([]byte("echo hi"))
	m, explicit := r.Mode("TFAttrsScript.txt")
	if !explicit && auto {
		m |= bin
	}
	if m != 2 {
		t.Fatalf("mode = %d, want 2", m)
	}
}

func TestNilRules(t *testing.T) {
	var r *AttrRules
	if m, e := r.Mode("x"); m != 0 || e {
		t.Fatal("nil rules give no mode")
	}
}

func TestDetectBinary(t *testing.T) {
	mk := func(n, nulAt int) []byte {
		b := bytes.Repeat([]byte{'a'}, n)
		if nulAt >= 0 {
			b[nulAt] = 0
		}
		return b
	}
	if !DetectBinary(mk(8001, 7999)) {
		t.Error("NUL at 7999 is binary")
	}
	if DetectBinary(mk(8001, 8000)) {
		t.Error("NUL at 8000 is beyond the window")
	}
	if !DetectBinary(mk(8000, 0)) || !DetectBinary([]byte{0}) {
		t.Error("NUL at start")
	}
	if DetectBinary(nil) || DetectBinary([]byte{}) {
		t.Error("empty is text")
	}
	if DetectBinary([]byte("plain text")) {
		t.Error("text")
	}
}

func TestFuzzNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	const alpha = "ab*/, \r\n.\x00binarytext"
	gen := func(n int) string {
		b := make([]byte, rng.Intn(n))
		for i := range b {
			b[i] = alpha[rng.Intn(len(alpha))]
		}
		return string(b)
	}
	for i := 0; i < 5000; i++ {
		_, _ = ParseAttrs(gen(40)).Mode(gen(12))
	}
}

// Pins a deliberate deviation: the HolyC prints IGNORE_UNSUPPORTED_LINE,
// ATTR_UNSUPPORTED and ATTR_UNSUPPORTED_LINE for rule lines it cannot use;
// this port never emits them. An unusable line, or an unknown attribute
// token, is dropped without a trace and the lines around it still apply.
func TestUnsupportedRuleLinesAreSilentlyDropped(t *testing.T) {
	ign := ignore.ParseIgnore("a/b\n*.tmp\n")
	if ign.IgnoredLast("a/b") || ign.IgnoredLast("b") || !ign.IgnoredLast("x.tmp") {
		t.Fatal("ignore: unsupported line must do nothing, the next line must still apply")
	}
	att := ParseAttrs("a/b binary\n*.x frobnicate\n*.y frobnicate,executable\n")
	for _, p := range []string{"a/b", "b", "f.x"} {
		if m, e := att.Mode(p); m != 0 || e {
			t.Fatalf("attrs %s: mode %d explicit %v, want nothing", p, m, e)
		}
	}
	if m, _ := att.Mode("f.y"); m != exec {
		t.Fatalf("attrs f.y: mode %d, want executable (unknown token dropped, known one kept)", m)
	}
}
