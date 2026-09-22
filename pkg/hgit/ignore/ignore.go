// Package ignore implements the .hgitignore grammar (ADR 0014) as pure
// functions over the file's text. It never touches the filesystem.
//
// The safety rule "ignore never hides an already-tracked file" is enforced by
// the CALLER, which must consult the old tree first and only ask IgnoredLast
// about untracked names. Nothing here tracks anything.
package ignore

import "strings"

// Kind is a pattern kind.
type Kind uint8

const (
	KindName        Kind = iota // "*.tmp": basename glob, any depth
	KindDir                     // "build/": directory of that name, any depth, whole subtree
	KindDirContents             // "generated/*": direct children of a root-anchored directory
)

// maxPattern mirrors the HolyC 256-byte slot: patterns of 256 bytes or more
// are silently not stored there; here they are skipped, documented, not wrapped.
const maxPattern = 256

// Pattern is one parsed pattern.
type Pattern struct {
	Kind Kind
	Text string // pattern without its "/" or "/*" suffix
}

// ParsePattern classifies one pattern (no "!", no attributes). ok is false for
// unsupported lines: empty, other slash-containing patterns, or over-length.
func ParsePattern(p string) (Pattern, bool) {
	var pt Pattern
	switch {
	case strings.HasSuffix(p, "/*"):
		pt = Pattern{KindDirContents, p[:len(p)-2]}
	case strings.HasSuffix(p, "/"):
		pt = Pattern{KindDir, p[:len(p)-1]}
	case strings.Contains(p, "/"):
		return pt, false
	default:
		pt = Pattern{KindName, p}
	}
	if len(pt.Text) == 0 || len(pt.Text) >= maxPattern {
		return pt, false
	}
	return pt, true
}

// Match reports whether the pattern matches relPath (slash separated, relative
// to the repo root) for the KindName/KindDirContents kinds - MatchLast defers
// to it for those. Name patterns match the basename only. DirContents match
// when the parent directory equals the pattern text exactly. A KindDir
// pattern never reaches here: MatchLast handles it directly, and no other
// caller exists - the ADR 0014 rule that ignore never descends into a
// directory (so a Dir pattern is only ever compared with a path's own last
// component, never an ancestor) means nothing needs the whole-subtree,
// isDir-aware match this used to also perform.
func (p Pattern) Match(relPath string) bool {
	dir, base := "", relPath
	if i := strings.LastIndexByte(relPath, '/'); i >= 0 {
		dir, base = relPath[:i], relPath[i+1:]
	}
	switch p.Kind {
	case KindName:
		return globMatch(p.Text, base)
	case KindDirContents:
		return dir == p.Text
	}
	return false
}

// MatchLast is the attributes flavour of Match: a Dir pattern matches only
// when the path's own last component equals it; Name and DirContents behave
// as in Match.
func (p Pattern) MatchLast(relPath string) bool {
	if p.Kind == KindDir {
		base := relPath[strings.LastIndexByte(relPath, '/')+1:]
		return base == p.Text
	}
	return p.Match(relPath)
}

// IgnoredLast is the exact shape of the HolyC's IsIgnored(name, rel_dir),
// which never sees a full path: a name pattern globs the candidate's own last
// component, a directory pattern is compared with that last component for
// directories and plain files alike (never with an ancestor - the HolyC never
// descends into an ignored directory, so it never needs to), and a "dir/*"
// pattern is compared with relPath's directory part. Last matching rule wins.
//
// Every caller - both offer paths, and status/statustree - uses this; there
// is no other production entry point into the ignore rules.
func (r *Rules) IgnoredLast(relPath string) bool {
	if r == nil {
		return false
	}
	ignored := false
	for _, ru := range r.rules {
		if ru.pat.MatchLast(relPath) {
			ignored = !ru.neg
		}
	}
	return ignored
}

// globMatch: only '*' is special (any run, including empty); the rest literal.
func globMatch(pat, s string) bool {
	pi, si := 0, 0
	starP, starS := -1, -1
	for si < len(s) {
		switch {
		case pi < len(pat) && pat[pi] == s[si]:
			pi++
			si++
		case pi < len(pat) && pat[pi] == '*':
			starP, starS = pi, si
			pi++
		case starP >= 0:
			pi = starP + 1
			starS++
			si = starS
		default:
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}

type rule struct {
	neg bool
	pat Pattern
}

// Rules is a parsed .hgitignore.
type Rules struct{ rules []rule }

// ParseIgnore parses .hgitignore text. Lines end at "\n" with one trailing
// "\r" removed; nothing else is trimmed (a trailing space is part of the
// pattern). Empty lines and lines starting with "#" are skipped, a leading "!"
// negates, and unsupported lines are skipped.
func ParseIgnore(src string) *Rules {
	r := &Rules{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || line[0] == '#' {
			continue
		}
		neg := line[0] == '!'
		if neg {
			line = line[1:]
		}
		if pt, ok := ParsePattern(line); ok {
			r.rules = append(r.rules, rule{neg, pt})
		}
	}
	return r
}
