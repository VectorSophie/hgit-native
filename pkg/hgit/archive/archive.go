// Package archive reads and writes the .hgs object archive (FORMAT.md):
// a 16-byte header followed by [U64 length][data][64-byte BLAKE2b] records.
package archive

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/blake2b"
)

const (
	HeaderLen           = 16
	HashLen             = 64
	MaxSupportedVersion = 4
)

// Object type tags (Object.HC).
type Type byte

const (
	Blob     Type = 1
	Tree     Type = 2
	Commit   Type = 3
	Attrs    Type = 4
	Conflict Type = 5
)

var (
	ErrBadMagic  = errors.New("archive: bad magic")
	ErrTruncated = errors.New("archive: truncated")
)

// UnsupportedVersionError is returned for a repository written by a newer hgit.
type UnsupportedVersionError struct{ Version uint16 }

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("unsupported_format_version=%d (this build reads up to %d) - written by a newer hgit",
		e.Version, MaxSupportedVersion)
}

// Hash is an unkeyed BLAKE2b-512 digest.
type Hash [HashLen]byte

// Sum hashes data.
func Sum(data []byte) Hash { return Hash(blake2b.Sum512(data)) }

func (h Hash) Hex() string { return hex.EncodeToString(h[:]) }

// ParseHex parses 128 hex characters.
func ParseHex(s string) (Hash, error) {
	var h Hash
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != HashLen {
		return h, fmt.Errorf("archive: bad hash %q", s)
	}
	copy(h[:], b)
	return h, nil
}

// Header is the 16-byte archive header. Reserved is preserved so a re-write
// is byte-identical (readers must not reject a nonzero value).
type Header struct {
	Version  uint16
	Reserved uint16
	Count    uint64
}

// ParseHeader checks magic first, then version (same order as HgsReadHeader).
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderLen {
		return Header{}, ErrTruncated
	}
	if b[0] != 'H' || b[1] != 'G' || b[2] != 'S' || b[3] != '0' {
		return Header{}, ErrBadMagic
	}
	h := Header{
		Version:  binary.LittleEndian.Uint16(b[4:]),
		Reserved: binary.LittleEndian.Uint16(b[6:]),
		Count:    binary.LittleEndian.Uint64(b[8:]),
	}
	if h.Version > MaxSupportedVersion {
		return h, &UnsupportedVersionError{Version: h.Version}
	}
	return h, nil
}

func (h Header) Marshal() []byte {
	b := make([]byte, HeaderLen)
	copy(b, "HGS0")
	binary.LittleEndian.PutUint16(b[4:], h.Version)
	binary.LittleEndian.PutUint16(b[6:], h.Reserved)
	binary.LittleEndian.PutUint64(b[8:], h.Count)
	return b
}

// Record is one archive record. Data includes the type-tag byte.
type Record struct {
	Data []byte
	Hash Hash
}

func NewRecord(data []byte) Record { return Record{Data: data, Hash: Sum(data)} }

// NewObject tags content with t (the tag participates in the hash).
func NewObject(t Type, content []byte) Record {
	tagged := make([]byte, 0, len(content)+1)
	tagged = append(tagged, byte(t))
	tagged = append(tagged, content...)
	return NewRecord(tagged)
}

func (r Record) HashOK() bool { return Sum(r.Data) == r.Hash }

func (r Record) Type() Type {
	if len(r.Data) == 0 {
		return 0
	}
	return Type(r.Data[0])
}

func (r Record) Content() []byte {
	if len(r.Data) == 0 {
		return nil
	}
	return r.Data[1:]
}

// Archive is a parsed .hgs file.
type Archive struct {
	Header  Header
	Records []Record
}

// Parse reads the header, then records until EOF (as ArchiveVerify does; the
// header count is exposed but not trusted). Every length is bounds-checked
// before any allocation.
func Parse(b []byte) (*Archive, error) {
	h, err := ParseHeader(b)
	if err != nil {
		return nil, err
	}
	a := &Archive{Header: h}
	pos := HeaderLen
	for pos < len(b) {
		if len(b)-pos < 8 {
			return nil, ErrTruncated
		}
		n := binary.LittleEndian.Uint64(b[pos:])
		pos += 8
		remain := uint64(len(b) - pos)
		if n > remain || remain-n < HashLen {
			return nil, ErrTruncated
		}
		var r Record
		r.Data = append([]byte(nil), b[pos:pos+int(n)]...)
		pos += int(n)
		copy(r.Hash[:], b[pos:pos+HashLen])
		pos += HashLen
		a.Records = append(a.Records, r)
	}
	return a, nil
}

func (a *Archive) Marshal() []byte {
	out := a.Header.Marshal()
	for _, r := range a.Records {
		var l [8]byte
		binary.LittleEndian.PutUint64(l[:], uint64(len(r.Data)))
		out = append(out, l[:]...)
		out = append(out, r.Data...)
		out = append(out, r.Hash[:]...)
	}
	return out
}

// Verify recomputes every record's hash (ArchiveVerify).
func (a *Archive) Verify() (total, ok int) {
	for _, r := range a.Records {
		total++
		if r.HashOK() {
			ok++
		}
	}
	return
}
