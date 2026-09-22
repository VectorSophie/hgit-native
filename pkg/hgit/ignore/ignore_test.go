package ignore

import (
	"math/rand"
	"strings"
	"testing"
)

const adrRules = "*.tmp\n*.bak\nbuild/\ngenerated/*\n!important.hc\n"

// TestIgnoredLastTable is the table TestIgnored used to run against the now-
// deleted Ignored(path, isDir); IgnoredLast(name, rel_dir) is the only shape
// the HolyC ever calls (see ignore.go's own comment), so every case here is
// expressed as one relPath the way a caller (offer, status/statustree)
// actually builds it: rel_dir/name joined once. A KindDir pattern is compared
// with the last path component only, files and directories alike - there is
// no "matches an ancestor directory" case any more, because nothing ever
// descends into a directory ignore already hid.
func TestIgnoredLastTable(t *testing.T) {
	tests := []struct {
		name  string
		rules string
		path  string
		want  bool
	}{
		// ADR 0014 examples.
		{"name any depth root", adrRules, "x.tmp", true},
		{"name any depth nested", adrRules, "a/b/x.tmp", true},
		{"bak", adrRules, "old.bak", true},
		{"plain file kept", adrRules, "keep.txt", false},
		{"build dir", adrRules, "build", true},
		{"nested build dir", adrRules, "SubA/build", true},
		{"generated direct child", adrRules, "generated/x.txt", true},
		{"generated grandchild not", adrRules, "generated/sub/y.txt", false},
		{"generated elsewhere not", adrRules, "other/generated/x.txt", false},
		{"generated dir itself not", adrRules, "generated", false},
		{"negation reincludes", "*.hc\n!important.hc\n", "important.hc", false},
		{"negation only its name", "*.hc\n!important.hc\n", "other.hc", true},
		{"last match wins ignore", "!a.tmp\n*.tmp\n", "a.tmp", true},
		{"last match wins negate", "*.tmp\n!a.tmp\n*.bak\n", "a.tmp", false},
		{"negate before nothing", "!a.tmp\n", "a.tmp", false},
		// Name glob: * stays within one name.
		{"star middle", "a*z\n", "abcz", true},
		{"star empty run", "a*z\n", "az", true},
		{"star no match", "a*z\n", "abc", false},
		{"name matches basename only", "x.tmp\n", "dir/x.tmp", true},
		{"name does not match dir component as file", "sub\n", "sub/f.txt", false},
		{"name matches a directory too", "sub\n", "sub", true},
		// Line handling.
		{"crlf", "*.tmp\r\nbuild/\r\n", "x.tmp", true},
		{"crlf dir", "*.tmp\r\nbuild/\r\n", "build", true},
		{"blank and comment", "\n# *.tmp\n\n", "x.tmp", false},
		{"comment then rule", "# c\n*.tmp\n", "x.tmp", true},
		{"trailing space is literal", "*.tmp \n", "x.tmp", false},
		{"trailing space matches space name", "*.tmp \n", "x.tmp ", true},
		{"no trailing newline", "*.tmp", "x.tmp", true},
		{"unsupported slash line skipped", "a/b.txt\n", "a/b.txt", false},
		{"empty rules", "", "x.tmp", false},
		{"lone bang", "!\n", "x", false},
		{"lone slash-star", "/*\n", "x", false},
		{"anchored multi segment", "a/b/*\n", "a/b/c.txt", true},
		{"anchored multi segment deeper", "a/b/*\n", "a/b/c/d.txt", false},
		// A KindDir pattern vs a same-named plain FILE: the HolyC hides it too
		// (IsIgnored never sees isDir at all).
		{"dir pattern vs same-named file", "build/\n", "build", true},
		{"dir pattern vs file in subdir", "build/\n", "src/build", true},
		{"dir pattern exact name only", "build/\n", "rebuild", false},
		{"255-byte pattern accepted", strings.Repeat("a", 255) + "\n", strings.Repeat("a", 255), true},
		{"256-byte pattern rejected", strings.Repeat("a", 256) + "\n", strings.Repeat("a", 256), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseIgnore(tc.rules).IgnoredLast(tc.path); got != tc.want {
				t.Errorf("IgnoredLast(%q) with %q = %v, want %v", tc.path, tc.rules, got, tc.want)
			}
		})
	}
}

// contract/tests/full-regression.hc lines 235-237: .hgitignore is "*.tmp\n"; keep.txt stays, x.tmp is
// reported OFFER_IGNORED.
func TestRegressionScenario(t *testing.T) {
	r := ParseIgnore("*.tmp\n")
	if !r.IgnoredLast("x.tmp") || r.IgnoredLast("keep.txt") {
		t.Fatal("regression IGNORE verdicts differ")
	}
}

func TestNilRules(t *testing.T) {
	var r *Rules
	if r.IgnoredLast("x") {
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
		_ = r.IgnoredLast(gen(12))
	}
}

// IgnoredLast is the exact shape of the HolyC's IsIgnored(name, rel_dir):
// a directory rule is compared with the candidate's own last component only,
// files and directories alike, and a "dir/*" rule with its parent directory.
func TestIgnoredLast(t *testing.T) {
	r := ParseIgnore("*.tmp\nbuild/\ngenerated/*\n")
	cases := []struct {
		path string
		want bool
	}{
		{"x.tmp", true},
		{"SubA/x.tmp", true},
		{"build", true},           // the directory itself, and equally a plain FILE of that name - the HolyC hides both
		{"build/keep.txt", false}, // never reached: the caller does not descend
		{"SubA/build", true},
		{"generated/a.txt", true},
		{"deep/generated/a.txt", false}, // DIR_CONTENTS compares the whole rel dir
		{"keep.txt", false},
	}
	for _, c := range cases {
		if got := r.IgnoredLast(c.path); got != c.want {
			t.Errorf("IgnoredLast(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	var nilRules *Rules
	if nilRules.IgnoredLast("anything") {
		t.Error("no rules must ignore nothing")
	}
}
