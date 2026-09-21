// Package attrs implements the .hgitattributes pattern side (ADR 0015) as pure
// functions over the file's text and content bytes. The pattern engine is
// shared with package ignore.
package attrs

import (
	"bytes"
	"strings"

	"github.com/VectorSophie/hgit-native/pkg/hgit/ignore"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
)

type rule struct {
	pat          ignore.Pattern
	sets, values byte // bit 0: text/binary, bit 1: executable
}

// AttrRules is a parsed .hgitattributes.
type AttrRules struct{ rules []rule }

// ParseAttrs parses "<pattern> <attr>[,<attr>...]" lines. Split on the FIRST
// space; a trailing space or CRLF is not trimmed beyond one "\r", so
// "*.png binary " carries the unknown token "binary " and the rule is dropped.
// Unknown tokens are ignored; a rule is kept only if at least one known token
// (text, binary, executable) is present. No negation. Slash-containing
// non-"/" "/*" patterns are skipped.
func ParseAttrs(src string) *AttrRules {
	r := &AttrRules{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || line[0] == '#' {
			continue
		}
		sp := strings.IndexByte(line, ' ')
		if sp <= 0 || sp >= len(line)-1 {
			continue
		}
		pt, ok := ignore.ParsePattern(line[:sp])
		if !ok {
			continue
		}
		var sets, values byte
		for _, tok := range strings.Split(line[sp+1:], ",") {
			switch tok {
			case "text":
				sets |= 1
			case "binary":
				sets |= 1
				values |= 1
			case "executable":
				sets |= 2
				values |= 2
			}
		}
		if sets != 0 {
			r.rules = append(r.rules, rule{pt, sets, values})
		}
	}
	return r
}

// Mode resolves relPath's mode from the rules alone; the last matching rule
// wins per dimension. explicit reports whether a rule set text/binary; when it
// is false the caller must OR in object.ModeBinary if DetectBinary(content).
// Executable is never auto-detected. Rules cannot tell files from directories
// (as in the HolyC), so a dir pattern matches a same-named file too.
func (r *AttrRules) Mode(relPath string) (mode byte, explicit bool) {
	if r == nil {
		return 0, false
	}
	execSet := false
	for _, ru := range r.rules {
		if !ru.pat.Match(relPath, true) {
			continue
		}
		if ru.sets&1 != 0 {
			explicit = true
			mode = mode&^object.ModeBinary | ru.values&1*object.ModeBinary
		}
		if ru.sets&2 != 0 {
			execSet = ru.values&2 != 0
		}
	}
	if execSet {
		mode |= object.ModeExecutable
	}
	return mode, explicit
}

// DetectBinary reports a NUL byte within the first 8000 bytes.
func DetectBinary(content []byte) bool {
	if len(content) > 8000 {
		content = content[:8000]
	}
	return bytes.IndexByte(content, 0) >= 0
}
