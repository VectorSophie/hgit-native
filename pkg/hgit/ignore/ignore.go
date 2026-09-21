// Package ignore implements the .hgitignore grammar (ADR 0014) as pure
// functions over the file's text. It never touches the filesystem.
//
// The safety rule "ignore never hides an already-tracked file" is enforced by
// the CALLER, which must consult the old tree first and only ask Ignored about
// untracked names. Nothing here tracks anything.
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
// to the repo root). Name patterns match the basename only, files and
// directories alike. Dir patterns match any directory component with that
// exact name (the path's own last component only when isDir), which covers the
// whole subtree. DirContents match when the parent directory equals the
// pattern text exactly.
func (p Pattern) Match(relPath string, isDir bool) bool {
	dir, base := "", relPath
	if i := strings.LastIndexByte(relPath, '/'); i >= 0 {
		dir, base = relPath[:i], relPath[i+1:]
	}
	switch p.Kind {
	case KindName:
		return globMatch(p.Text, base)
	case KindDir:
		if isDir && base == p.Text {
			return true
		}
		if dir == "" {
			return false
		}
		for _, c := range strings.Split(dir, "/") {
			if c == p.Text {
				return true
			}
		}
	case KindDirContents:
		return dir == p.Text
	}
	return false
}

// MatchLast is the attributes flavour of Match: no dir/file distinction and no
// subtree, so a Dir pattern matches only when the path's own last component
// equals it; Name and DirContents behave as in Match.
func (p Pattern) MatchLast(relPath string) bool {
	if p.Kind == KindDir {
		base := relPath[strings.LastIndexByte(relPath, '/')+1:]
		return base == p.Text
	}
	return p.Match(relPath, false)
}

// IgnoredLast is the exact shape of the HolyC's IsIgnored(name, rel_dir),
// which never sees a full path: a name pattern globs the candidate's own last
// component, a directory pattern is compared with that last component for
// directories and plain files alike (never with an ancestor - the HolyC never
// descends into an ignored directory, so it never needs to), and a "dir/*"
// pattern is compared with relPath's directory part. Last matching rule wins.
//
// Both offer paths use this. Ignored above keeps the path-and-isDir shape
// that status and diff will want.
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

// Ignored reports whether relPath is ignored: the last matching rule wins.
//
// Caller contract: the caller must know isDir, must never descend into a
// directory reported ignored, and must consult tracking first (ADR 0014). A
// negation that would re-include a file inside an ignored directory is never
// evaluated by the HolyC because it does not descend; callers that follow that
// contract get the same behaviour, although Ignored on such a path alone
// reports true from the directory rule unless the negation matches later.
func (r *Rules) Ignored(relPath string, isDir bool) bool {
	ignored := false
	if r == nil {
		return false
	}
	for _, ru := range r.rules {
		if ru.pat.Match(relPath, isDir) {
			ignored = !ru.neg
		}
	}
	return ignored
}
