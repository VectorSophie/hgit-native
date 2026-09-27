// Package bundle implements the .hgb exchange-bundle and .hgh have-file
// formats (BUNDLE.md, ADR 0020): an incremental, content-addressed way to
// carry objects and proposed path heads between repositories without a
// whole-file copy.
//
// Every record - manifest, objects, footer, have-manifest - reuses the
// archive record framing of FORMAT.md unchanged: [U64 length LE][data]
// [64-byte BLAKE2b-512 of data], where data is one type-tag byte followed by
// content (archive.NewObject already builds exactly this). Only the 16-byte
// file header differs from a repository's: the magic is HGB0/HGH0 instead of
// HGS0, so archive.ParseHeader (which hardcodes "HGS0") cannot be reused for
// it; the record-stream framing is reused via archive.Record/NewObject.
package bundle

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

const (
	HeaderLen = 16
	Version   = 1

	// Type tags for bundle-only records (archive.Type is just a byte; tags
	// 1..5 are the ordinary object types of FORMAT.md, reused unchanged).
	TagManifest     archive.Type = 0x10
	TagFooter       archive.Type = 0x11
	TagHaveManifest archive.Type = 0x12

	ManifestVersion = 1
	FooterVersion   = 1
	HaveVersion     = 1

	// KindBaseline is a bundle with no prerequisites; KindIncremental has at
	// least one.
	KindBaseline    = 1
	KindIncremental = 2

	HashAlgBLAKE2b512 = 1

	// MaxNameLen mirrors repo.MaxPathName (a name must be shorter than 64
	// bytes): label_len and name_len fields are U8 but BUNDLE.md bounds them
	// to 0..63 / 1..63.
	MaxNameLen = 63

	// DefaultHaveSamples is BUNDLE.md's K default: up to this many
	// first-parent ancestors per head in a have-file.
	DefaultHaveSamples = 64
)

var (
	ErrBadMagic       = errors.New("bundle: bad magic")
	ErrBadVersion     = errors.New("bundle: unsupported version")
	ErrTruncated      = errors.New("bundle: truncated")
	ErrMalformed      = errors.New("bundle: malformed record")
	ErrHashMismatch   = errors.New("bundle: record hash mismatch")
	ErrLayout         = errors.New("bundle: manifest/footer not at required position")
	ErrCounts         = errors.New("bundle: record/object counts do not match manifest")
	ErrRequiredCaps   = errors.New("bundle: unknown required capability bit set")
	ErrHashAlg        = errors.New("bundle: unknown hash_alg")
	ErrFooterHash     = errors.New("bundle: footer hash does not match bundle contents")
	ErrNameTooLong    = errors.New("bundle: path name longer than 63 bytes")
	ErrLabelTooLong   = errors.New("bundle: label longer than 63 bytes")
	ErrObjectType     = errors.New("bundle: illegal object type in bundle")
	ErrMissingPrereq  = errors.New("bundle: prerequisite commit missing in recipient")
	ErrUnresolvedRef  = errors.New("bundle: object reference does not resolve")
	ErrHeadNotCommit  = errors.New("bundle: proposed head is not a resolvable commit")
	ErrComposedName   = errors.New("bundle: composed path name too long")
	ErrNoUsablePrereq = errors.New("bundle: have-file's commits are all unusable; falling back to a baseline bundle")
)

// header is the shared 16-byte header shape of both file kinds; only the
// magic differs (HGB0 vs HGH0). archive.Header.Marshal/ParseHeader hardcode
// "HGS0" so they are not reusable here - this is the same 16-byte layout,
// written directly.
type header struct {
	Version  uint16
	Reserved uint16
	Count    uint64
}

func marshalHeader(magic string, h header) []byte {
	b := make([]byte, HeaderLen)
	copy(b, magic)
	binary.LittleEndian.PutUint16(b[4:], h.Version)
	binary.LittleEndian.PutUint16(b[6:], h.Reserved)
	binary.LittleEndian.PutUint64(b[8:], h.Count)
	return b
}

func parseHeader(b []byte, magic string) (header, error) {
	var h header
	if len(b) < HeaderLen {
		return h, ErrTruncated
	}
	if string(b[:4]) != magic {
		return h, ErrBadMagic
	}
	h.Version = binary.LittleEndian.Uint16(b[4:])
	h.Reserved = binary.LittleEndian.Uint16(b[6:])
	h.Count = binary.LittleEndian.Uint64(b[8:])
	if h.Version != Version {
		return h, ErrBadVersion
	}
	return h, nil
}

// parseRecords reads archive-framed records from b[pos:] until the end of b.
// It mirrors archive.Parse's loop (which cannot be called directly: it
// re-derives its own header first, with the "HGS0" magic). Every length is
// bounds-checked before any slice or allocation.
func parseRecords(b []byte, pos int) ([]archive.Record, error) {
	var out []archive.Record
	for pos < len(b) {
		if len(b)-pos < 8 {
			return nil, ErrTruncated
		}
		n := binary.LittleEndian.Uint64(b[pos:])
		pos += 8
		remain := uint64(len(b) - pos)
		if n > remain || remain-n < archive.HashLen {
			return nil, ErrTruncated
		}
		var r archive.Record
		end := pos + int(n)
		r.Data = b[pos:end:end]
		pos = end
		copy(r.Hash[:], b[pos:pos+archive.HashLen])
		pos += archive.HashLen
		out = append(out, r)
	}
	return out, nil
}

func marshalRecords(recs []archive.Record) []byte {
	var out []byte
	for _, r := range recs {
		var l [8]byte
		binary.LittleEndian.PutUint64(l[:], uint64(len(r.Data)))
		out = append(out, l[:]...)
		out = append(out, r.Data...)
		out = append(out, r.Hash[:]...)
	}
	return out
}

// HeadEntry is one manifest or have-manifest (name, head) pair.
type HeadEntry struct {
	Name string
	Head archive.Hash
}

func encodeName(out []byte, name string) ([]byte, error) {
	if len(name) == 0 || len(name) > MaxNameLen {
		return nil, ErrNameTooLong
	}
	out = append(out, byte(len(name)))
	return append(out, name...), nil
}

// Manifest is the tag-0x10 record content (BUNDLE.md "Manifest").
type Manifest struct {
	RequiredCaps uint32
	OptionalCaps uint32
	HashAlg      byte
	Kind         byte
	CreatedMs    uint64
	Label        string
	Prereqs      []archive.Hash
	Heads        []HeadEntry
	ObjectCount  uint32
	ObjectBytes  uint64
}

func (m Manifest) Encode() ([]byte, error) {
	if len(m.Label) > MaxNameLen {
		return nil, ErrLabelTooLong
	}
	var out []byte
	out = append(out, ManifestVersion)
	var u4 [4]byte
	binary.LittleEndian.PutUint32(u4[:], m.RequiredCaps)
	out = append(out, u4[:]...)
	binary.LittleEndian.PutUint32(u4[:], m.OptionalCaps)
	out = append(out, u4[:]...)
	out = append(out, m.HashAlg, m.Kind)
	var u8 [8]byte
	binary.LittleEndian.PutUint64(u8[:], m.CreatedMs)
	out = append(out, u8[:]...)
	out = append(out, byte(len(m.Label)))
	out = append(out, m.Label...)
	binary.LittleEndian.PutUint32(u4[:], uint32(len(m.Prereqs)))
	out = append(out, u4[:]...)
	for _, h := range m.Prereqs {
		out = append(out, h[:]...)
	}
	binary.LittleEndian.PutUint32(u4[:], uint32(len(m.Heads)))
	out = append(out, u4[:]...)
	for _, he := range m.Heads {
		var err error
		out, err = encodeName(out, he.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, he.Head[:]...)
	}
	binary.LittleEndian.PutUint32(u4[:], m.ObjectCount)
	out = append(out, u4[:]...)
	binary.LittleEndian.PutUint64(u8[:], m.ObjectBytes)
	out = append(out, u8[:]...)
	return out, nil
}

func need(b []byte, pos, n int) bool { return len(b)-pos >= n }

func DecodeManifest(b []byte) (*Manifest, error) {
	m := &Manifest{}
	pos := 0
	if !need(b, pos, 1) {
		return nil, ErrMalformed
	}
	mv := b[pos]
	pos++
	if mv != ManifestVersion {
		return nil, fmt.Errorf("%w: manifest_version %d", ErrMalformed, mv)
	}
	if !need(b, pos, 4+4+1+1+8+1) {
		return nil, ErrMalformed
	}
	m.RequiredCaps = binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	m.OptionalCaps = binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	m.HashAlg = b[pos]
	pos++
	m.Kind = b[pos]
	pos++
	m.CreatedMs = binary.LittleEndian.Uint64(b[pos:])
	pos += 8
	ll := int(b[pos])
	pos++
	if !need(b, pos, ll) {
		return nil, ErrMalformed
	}
	m.Label = string(b[pos : pos+ll])
	pos += ll

	if !need(b, pos, 4) {
		return nil, ErrMalformed
	}
	prereqCount := binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	if !need(b, pos, int(prereqCount)*archive.HashLen) {
		return nil, ErrMalformed
	}
	for i := uint32(0); i < prereqCount; i++ {
		var h archive.Hash
		copy(h[:], b[pos:])
		pos += archive.HashLen
		m.Prereqs = append(m.Prereqs, h)
	}

	if !need(b, pos, 4) {
		return nil, ErrMalformed
	}
	headCount := binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	for i := uint32(0); i < headCount; i++ {
		if !need(b, pos, 1) {
			return nil, ErrMalformed
		}
		nl := int(b[pos])
		pos++
		if nl == 0 || !need(b, pos, nl+archive.HashLen) {
			return nil, ErrMalformed
		}
		he := HeadEntry{Name: string(b[pos : pos+nl])}
		pos += nl
		copy(he.Head[:], b[pos:])
		pos += archive.HashLen
		m.Heads = append(m.Heads, he)
	}

	if !need(b, pos, 4+8) {
		return nil, ErrMalformed
	}
	m.ObjectCount = binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	m.ObjectBytes = binary.LittleEndian.Uint64(b[pos:])
	pos += 8
	if pos != len(b) {
		return nil, ErrMalformed
	}
	return m, nil
}

// HaveManifest is the tag-0x12 record content (BUNDLE.md "Have manifest").
type HaveManifest struct {
	Label   string
	Heads   []HeadEntry
	Samples []archive.Hash
}

func (h HaveManifest) Encode() ([]byte, error) {
	if len(h.Label) > MaxNameLen {
		return nil, ErrLabelTooLong
	}
	var out []byte
	out = append(out, HaveVersion, byte(len(h.Label)))
	out = append(out, h.Label...)
	var u4 [4]byte
	binary.LittleEndian.PutUint32(u4[:], uint32(len(h.Heads)))
	out = append(out, u4[:]...)
	for _, he := range h.Heads {
		var err error
		out, err = encodeName(out, he.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, he.Head[:]...)
	}
	binary.LittleEndian.PutUint32(u4[:], uint32(len(h.Samples)))
	out = append(out, u4[:]...)
	for _, s := range h.Samples {
		out = append(out, s[:]...)
	}
	return out, nil
}

func DecodeHaveManifest(b []byte) (*HaveManifest, error) {
	h := &HaveManifest{}
	pos := 0
	if !need(b, pos, 2) {
		return nil, ErrMalformed
	}
	hv := b[pos]
	pos++
	if hv != HaveVersion {
		return nil, fmt.Errorf("%w: have_version %d", ErrMalformed, hv)
	}
	ll := int(b[pos])
	pos++
	if !need(b, pos, ll+4) {
		return nil, ErrMalformed
	}
	h.Label = string(b[pos : pos+ll])
	pos += ll
	headCount := binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	for i := uint32(0); i < headCount; i++ {
		if !need(b, pos, 1) {
			return nil, ErrMalformed
		}
		nl := int(b[pos])
		pos++
		if nl == 0 || !need(b, pos, nl+archive.HashLen) {
			return nil, ErrMalformed
		}
		he := HeadEntry{Name: string(b[pos : pos+nl])}
		pos += nl
		copy(he.Head[:], b[pos:])
		pos += archive.HashLen
		h.Heads = append(h.Heads, he)
	}
	if !need(b, pos, 4) {
		return nil, ErrMalformed
	}
	sampleCount := binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	if !need(b, pos, int(sampleCount)*archive.HashLen) {
		return nil, ErrMalformed
	}
	for i := uint32(0); i < sampleCount; i++ {
		var s archive.Hash
		copy(s[:], b[pos:])
		pos += archive.HashLen
		h.Samples = append(h.Samples, s)
	}
	if pos != len(b) {
		return nil, ErrMalformed
	}
	return h, nil
}

// footerContent builds the tag-0x11 record content: footer_version then the
// 64-byte hash of everything before the footer record.
func footerContent(bundleHash archive.Hash) []byte {
	out := make([]byte, 0, 1+archive.HashLen)
	out = append(out, FooterVersion)
	return append(out, bundleHash[:]...)
}

func decodeFooter(b []byte) (version byte, h archive.Hash, err error) {
	if len(b) != 1+archive.HashLen {
		return 0, h, ErrMalformed
	}
	version = b[0]
	copy(h[:], b[1:])
	return version, h, nil
}
