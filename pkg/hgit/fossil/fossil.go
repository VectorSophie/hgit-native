// Package fossil ports Fossil.HC: Fossil's delta wire format (base-64
// integers, checksum, literal and copy segments), a single-longest-match
// delta maker, and the similarity percentage used for rename detection.
package fossil

import (
	"errors"
	"math"
)

// RenameSimilarityThreshold is FOSSIL_RENAME_SIMILARITY_THRESHOLD: a
// similarity of at least this many percent counts as a rename.
const RenameSimilarityThreshold = 50

// minCopyLen is FOSSIL_MIN_COPY_LEN.
const minCopyLen = 4

// maxApplyOutput caps the size DeltaApply will ever allocate.
const maxApplyOutput = 256 << 20

const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz~"

// PutInt encodes v as a Fossil base-64 integer, most significant group first.
// Like the HolyC, a negative v encodes to nothing.
func PutInt(v int64) []byte {
	if v == 0 {
		return []byte{'0'}
	}
	var tmp [11]byte
	n := 0
	for v > 0 {
		tmp[n] = digits[v&0x3f]
		n++
		v >>= 6
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = tmp[n-1-i]
	}
	return out
}

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	case c == '_':
		return 36
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 37
	case c == '~':
		return 63
	}
	return -1
}

// GetInt reads digits from buf[pos:] up to the first non-digit and returns the
// value and the position after the last digit. It errors on no digits or on a
// value that does not fit int64.
func GetInt(buf []byte, pos int) (value int64, next int, err error) {
	if pos < 0 || pos > len(buf) {
		return 0, pos, errors.New("fossil: position out of range")
	}
	next = pos
	for next < len(buf) {
		d := digitValue(buf[next])
		if d < 0 {
			break
		}
		if value > math.MaxInt64>>6 {
			return 0, pos, errors.New("fossil: integer overflow")
		}
		value = value<<6 | int64(d)
		next++
	}
	if next == pos {
		return 0, pos, errors.New("fossil: expected integer")
	}
	return value, next, nil
}

// Checksum sums data as big-endian 32-bit words, zero-padding a short tail.
func Checksum(data []byte) uint32 {
	var sum uint32
	i := 0
	for ; i+4 <= len(data); i += 4 {
		sum += uint32(data[i])<<24 | uint32(data[i+1])<<16 | uint32(data[i+2])<<8 | uint32(data[i+3])
	}
	var tail uint32
	for k := 0; i+k < len(data); k++ {
		tail |= uint32(data[i+k]) << (24 - 8*uint(k))
	}
	return sum + tail
}

func literal(out, b []byte) []byte {
	if len(b) == 0 {
		return out
	}
	out = append(out, PutInt(int64(len(b)))...)
	out = append(out, ':')
	return append(out, b...)
}

func trailer(out, target []byte) []byte {
	out = append(out, PutInt(int64(Checksum(target)))...)
	return append(out, ';')
}

// DeltaMakeTrivial builds a delta holding the whole target as one literal.
func DeltaMakeTrivial(source, target []byte) []byte {
	out := append(PutInt(int64(len(target))), '\n')
	return trailer(literal(out, target), target)
}

// findLongestMatch returns the first-found longest common substring (scan
// order: source offset, then target offset; strictly longer wins).
// ponytail: O(len(source)*len(target)) exactly as the HolyC; a suffix
// automaton would give the same answer faster if large files matter.
func findLongestMatch(source, target []byte) (srcOff, tgtOff, length int) {
	srcOff, tgtOff = -1, -1
	for si := range source {
		for ti := range target {
			n := 0
			for si+n < len(source) && ti+n < len(target) && source[si+n] == target[ti+n] {
				n++
			}
			if n > length {
				length, srcOff, tgtOff = n, si, ti
			}
		}
	}
	return
}

// DeltaMakeReal encodes target as [literal prefix][one copy][literal suffix]
// around the single longest match, or all-literal if that is under 4 bytes.
func DeltaMakeReal(source, target []byte) []byte {
	so, to, n := findLongestMatch(source, target)
	out := append(PutInt(int64(len(target))), '\n')
	if n >= minCopyLen {
		out = literal(out, target[:to])
		out = append(out, PutInt(int64(n))...)
		out = append(out, '@')
		out = append(out, PutInt(int64(so))...)
		out = append(out, ',')
		out = literal(out, target[to+n:])
	} else {
		out = literal(out, target)
	}
	return trailer(out, target)
}

// SimilarityPercent is the percentage of target covered by the single longest
// match against source (integer division); 0 for an empty target.
func SimilarityPercent(source, target []byte) int {
	if len(target) == 0 {
		return 0
	}
	_, _, n := findLongestMatch(source, target)
	return n * 100 / len(target)
}

var errMalformed = errors.New("fossil: malformed delta")

// DeltaApply applies delta to source, verifying every bound, the declared
// target length and the trailing checksum.
func DeltaApply(source, delta []byte) ([]byte, error) {
	declared, pos, err := GetInt(delta, 0)
	if err != nil {
		return nil, err
	}
	if pos >= len(delta) || delta[pos] != '\n' {
		return nil, errMalformed
	}
	pos++
	// Output is at most one full-source copy per 4+ delta bytes, and never
	// more than the hard cap.
	limit := int64(len(delta)) * int64(max(len(source), 1))
	if declared > maxApplyOutput || declared > limit {
		return nil, errors.New("fossil: declared target length not producible")
	}
	out := make([]byte, 0, declared)
	for int64(len(out)) < declared {
		segLen, p, err := GetInt(delta, pos)
		if err != nil {
			return nil, err
		}
		pos = p
		if pos >= len(delta) {
			return nil, errMalformed
		}
		op := delta[pos]
		pos++
		if segLen > declared-int64(len(out)) {
			return nil, errors.New("fossil: segment exceeds declared length")
		}
		switch op {
		case ':':
			if segLen > int64(len(delta)-pos) {
				return nil, errors.New("fossil: literal beyond delta")
			}
			out = append(out, delta[pos:pos+int(segLen)]...)
			pos += int(segLen)
		case '@':
			off, p, err := GetInt(delta, pos)
			if err != nil {
				return nil, err
			}
			pos = p
			if pos >= len(delta) || delta[pos] != ',' {
				return nil, errMalformed
			}
			pos++
			if off > int64(len(source)) || segLen > int64(len(source))-off {
				return nil, errors.New("fossil: copy beyond source")
			}
			out = append(out, source[off:off+segLen]...)
		default:
			return nil, errMalformed
		}
	}
	sum, pos, err := GetInt(delta, pos)
	if err != nil {
		return nil, err
	}
	if pos >= len(delta) || delta[pos] != ';' {
		return nil, errMalformed
	}
	if sum != int64(Checksum(out)) {
		return nil, errors.New("fossil: checksum mismatch")
	}
	return out, nil
}
