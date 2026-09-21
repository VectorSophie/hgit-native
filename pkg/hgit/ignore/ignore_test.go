package ignore

import (
	"math/rand"
	"testing"
)

const adrRules = "*.tmp\n*.bak\nbuild/\ngenerated/*\n!important.hc\n"

func TestIgnored(t *testing.T) {
	tests := []struct {
		name  string
		rules string
		path  string
		isDir bool
		want  bool
	}{
		// ADR 0014 examples.
		{"name any depth root", adrRules, "x.tmp", false, true},
		{"name any depth nested", adrRules, "a/b/x.tmp", false, true},
		{"bak", adrRules, "old.bak", false, true},
		{"plain file kept", adrRules, "keep.txt", false, false},
		{"build dir", adrRules, "build", true, true},
		{"nested build dir", adrRules, "SubA/build", true, true},
		{"file under build", adrRules, "build/output.txt", false, true},
		{"file under nested build", adrRules, "SubA/build/deep/o.txt", false, true},
		{"generated direct child", adrRules, "generated/x.txt", false, true},
		{"generated grandchild not", adrRules, "generated/sub/y.txt", false, false},
		{"generated elsewhere not", adrRules, "other/generated/x.txt", false, false},
		{"generated dir itself not", adrRules, "generated", true, false},
		{"negation reincludes", "*.hc\n!important.hc\n", "important.hc", false, false},
		{"negation only its name", "*.hc\n!important.hc\n", "other.hc", false, true},
		{"last match wins ignore", "!a.tmp\n*.tmp\n", "a.tmp", false, true},
		{"last match wins negate", "*.tmp\n!a.tmp\n*.bak\n", "a.tmp", false, false},
		{"negate before nothing", "!a.tmp\n", "a.tmp", false, false},
		// Name glob: * stays within one name.
		{"star middle", "a*z\n", "abcz", false, true},
		{"star empty run", "a*z\n", "az", false, true},
		{"star no match", "a*z\n", "abc", false, false},
		{"name matches basename only", "x.tmp\n", "dir/x.tmp", false, true},
		{"name does not match dir component as file", "sub\n", "sub/f.txt", false, false},
		{"name matches a directory too", "sub\n", "sub", true, true},
		// Line handling.
		{"crlf", "*.tmp\r\nbuild/\r\n", "x.tmp", false, true},
		{"crlf dir", "*.tmp\r\nbuild/\r\n", "build", true, true},
		{"blank and comment", "\n# *.tmp\n\n", "x.tmp", false, false},
		{"comment then rule", "# c\n*.tmp\n", "x.tmp", false, true},
		{"trailing space is literal", "*.tmp \n", "x.tmp", false, false},
		{"trailing space matches space name", "*.tmp \n", "x.tmp ", false, true},
		{"no trailing newline", "*.tmp", "x.tmp", false, true},
		{"unsupported slash line skipped", "a/b.txt\n", "a/b.txt", false, false},
		{"empty rules", "", "x.tmp", false, false},
		{"lone bang", "!\n", "x", false, false},
		{"lone slash-star", "/*\n", "x", false, false},
		{"anchored multi segment", "a/b/*\n", "a/b/c.txt", false, true},
		{"anchored multi segment deeper", "a/b/*\n", "a/b/c/d.txt", false, false},
		// Directory pattern vs same-named file.
		{"dir pattern vs file", "build/\n", "build", false, false},
		{"dir pattern vs file in subdir", "build/\n", "src/build", false, false},
		{"dir pattern exact name only", "build/\n", "rebuild", true, false},
		{"long pattern skipped", string(make([]byte, 300)) + "\n", "x", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseIgnore(tc.rules).Ignored(tc.path, tc.isDir); got != tc.want {
				t.Errorf("Ignored(%q,%v) with %q = %v, want %v", tc.path, tc.isDir, tc.rules, got, tc.want)
			}
		})
	}
}

// Regression scenario: .hgitignore is "*.tmp\n"; keep.txt stays, x.tmp is
// reported OFFER_IGNORED.
func TestRegressionScenario(t *testing.T) {
	r := ParseIgnore("*.tmp\n")
	if !r.Ignored("x.tmp", false) || r.Ignored("keep.txt", false) {
		t.Fatal("regression IGNORE verdicts differ")
	}
}

func TestNilRules(t *testing.T) {
	var r *Rules
	if r.Ignored("x", false) {
		t.Fatal("nil rules ignore nothing")
	}
}

func TestFuzzNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const alpha = "ab*/!# \r\n.\x00"
	gen := func(n int) string {
		b := make([]byte, rng.Intn(n))
		for i := range b {
			b[i] = alpha[rng.Intn(len(alpha))]
		}
		return string(b)
	}
	for i := 0; i < 5000; i++ {
		r := ParseIgnore(gen(30))
		_ = r.Ignored(gen(12), rng.Intn(2) == 0)
	}
}
