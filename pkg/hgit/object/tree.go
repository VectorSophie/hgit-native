// Package object encodes and decodes hgit's typed object contents
// (the bytes after the type tag): trees, commits, attrs, conflicts.
package object

import (
	"encoding/binary"
	"errors"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

// ErrMalformed is returned for any object content that does not parse.
var ErrMalformed = errors.New("object: malformed")

const minEntryLen = 1 + 0 + 1 + archive.HashLen + 8

type Entry struct {
	Name      string
	ChildType archive.Type
	ChildHash archive.Hash
	EntityID  uint64
}

type Tree struct{ Entries []Entry }

// DecodeTree parses: U32 count, then per entry U8 name_len, name, U8 type,
// 64-byte child hash, U64 entity id.
func DecodeTree(b []byte) (*Tree, error) {
	if len(b) < 4 {
		return nil, ErrMalformed
	}
	count := binary.LittleEndian.Uint32(b)
	pos := 4
	if uint64(count) > uint64(len(b)-pos)/minEntryLen {
		return nil, ErrMalformed
	}
	t := &Tree{Entries: make([]Entry, 0, count)}
	for i := uint32(0); i < count; i++ {
		if pos >= len(b) {
			return nil, ErrMalformed
		}
		nl := int(b[pos])
		pos++
		if len(b)-pos < nl+1+archive.HashLen+8 {
			return nil, ErrMalformed
		}
		var e Entry
		e.Name = string(b[pos : pos+nl])
		pos += nl
		e.ChildType = archive.Type(b[pos])
		pos++
		copy(e.ChildHash[:], b[pos:pos+archive.HashLen])
		pos += archive.HashLen
		e.EntityID = binary.LittleEndian.Uint64(b[pos:])
		pos += 8
		t.Entries = append(t.Entries, e)
	}
	if pos != len(b) {
		return nil, ErrMalformed
	}
	return t, nil
}

// Encode is the inverse of DecodeTree. Names longer than 255 bytes cannot be
// represented and are truncated by the caller's validation, never here.
func (t *Tree) Encode() []byte {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, uint32(len(t.Entries)))
	for _, e := range t.Entries {
		out = append(out, byte(len(e.Name)))
		out = append(out, e.Name...)
		out = append(out, byte(e.ChildType))
		out = append(out, e.ChildHash[:]...)
		var id [8]byte
		binary.LittleEndian.PutUint64(id[:], e.EntityID)
		out = append(out, id[:]...)
	}
	return out
}

func (t *Tree) Find(name string) (Entry, bool) {
	for _, e := range t.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}
