// Package meta reads and writes the per-repo metadata file (<repo>.hgs.m):
// HEAD per path, declared paths, current path, operation and redo logs, and
// in-progress merge state. Port of Meta.HC.
package meta

import (
	"encoding/binary"
	"errors"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

const (
	TagHead         byte = 1
	TagPathDeclared byte = 2
	TagCurrent      byte = 3
	TagOpLog        byte = 4
	TagRedoLog      byte = 5
	TagMergeState   byte = 6
	TagConflict     byte = 7
)

var (
	ErrMalformed    = errors.New("meta: malformed")
	ErrFieldTooLong = errors.New("meta: record name or payload longer than 255 bytes")
)

type Record struct {
	Name    string
	Tag     byte
	Payload []byte
}

type File struct{ Records []Record }

// Parse reads records until EOF: U8 name_len, name, U8 tag, U8 payload_len, payload.
func Parse(b []byte) (*File, error) {
	f := &File{}
	pos := 0
	for pos < len(b) {
		nl := int(b[pos])
		pos++
		if len(b)-pos < nl+2 {
			return nil, ErrMalformed
		}
		name := string(b[pos : pos+nl])
		pos += nl
		tag := b[pos]
		pl := int(b[pos+1])
		pos += 2
		if len(b)-pos < pl {
			return nil, ErrMalformed
		}
		f.Records = append(f.Records, Record{Name: name, Tag: tag, Payload: append([]byte(nil), b[pos:pos+pl]...)})
		pos += pl
	}
	return f, nil
}

// Marshal encodes every record. Name and payload lengths are single bytes on
// disk, so a record with either over 255 bytes is refused with
// ErrFieldTooLong rather than written with a wrapped length.
func (f *File) Marshal() ([]byte, error) {
	var out []byte
	for _, r := range f.Records {
		if len(r.Name) > 255 || len(r.Payload) > 255 {
			return nil, ErrFieldTooLong
		}
		out = append(out, byte(len(r.Name)))
		out = append(out, r.Name...)
		out = append(out, r.Tag, byte(len(r.Payload)))
		out = append(out, r.Payload...)
	}
	return out, nil
}

// All returns every record for (name, tag), oldest first.
func (f *File) All(name string, tag byte) []Record {
	var out []Record
	for _, r := range f.Records {
		if r.Name == name && r.Tag == tag {
			out = append(out, r)
		}
	}
	return out
}

// Find is last-match-wins (MetaFind).
func (f *File) Find(name string, tag byte) (Record, bool) {
	for i := len(f.Records) - 1; i >= 0; i-- {
		if r := f.Records[i]; r.Name == name && r.Tag == tag {
			return r, true
		}
	}
	return Record{}, false
}

func (f *File) remove(name string, tag byte) {
	kept := f.Records[:0]
	for _, r := range f.Records {
		if !(r.Name == name && r.Tag == tag) {
			kept = append(kept, r)
		}
	}
	f.Records = kept
}

// Set splices out every existing (name, tag) record, then appends (MetaSpliceOut).
func (f *File) Set(name string, tag byte, payload []byte) {
	f.remove(name, tag)
	f.Append(name, tag, payload)
}

func (f *File) Append(name string, tag byte, payload []byte) {
	f.Records = append(f.Records, Record{Name: name, Tag: tag, Payload: append([]byte(nil), payload...)})
}

// PopLast removes and returns the last (name, tag) record (MetaSpliceOutLast).
func (f *File) PopLast(name string, tag byte) (Record, bool) {
	for i := len(f.Records) - 1; i >= 0; i-- {
		if r := f.Records[i]; r.Name == name && r.Tag == tag {
			f.Records = append(f.Records[:i], f.Records[i+1:]...)
			return r, true
		}
	}
	return Record{}, false
}

// OpLogEntry is the payload of an OPLOG/REDOLOG record: U64 timestamp,
// 64-byte prev head, 64-byte new head (136 bytes).
type OpLogEntry struct {
	Timestamp uint64
	Prev, New archive.Hash
}

func DecodeOpLog(p []byte) (OpLogEntry, error) {
	var e OpLogEntry
	if len(p) != 8+2*archive.HashLen {
		return e, ErrMalformed
	}
	e.Timestamp = binary.LittleEndian.Uint64(p)
	copy(e.Prev[:], p[8:])
	copy(e.New[:], p[8+archive.HashLen:])
	return e, nil
}

func (e OpLogEntry) Encode() []byte {
	out := make([]byte, 8, 8+2*archive.HashLen)
	binary.LittleEndian.PutUint64(out, e.Timestamp)
	out = append(out, e.Prev[:]...)
	return append(out, e.New[:]...)
}

// MergeState is the payload of a MERGE_STATE record: the two heads a merge
// was started from, then the other path's name. The base is deliberately not
// stored - it is re-derivable from the two heads, since the object graph only
// ever grows (MetaMergeStateWrite).
type MergeState struct {
	Ours, Theirs archive.Hash
	OtherPath    string
}

func DecodeMergeState(p []byte) (MergeState, error) {
	var s MergeState
	if len(p) < 2*archive.HashLen {
		return s, ErrMalformed
	}
	copy(s.Ours[:], p)
	copy(s.Theirs[:], p[archive.HashLen:])
	s.OtherPath = string(p[2*archive.HashLen:])
	return s, nil
}

func (s MergeState) Encode() []byte {
	out := make([]byte, 0, 2*archive.HashLen+len(s.OtherPath))
	out = append(out, s.Ours[:]...)
	out = append(out, s.Theirs[:]...)
	return append(out, s.OtherPath...)
}

// ConflictRecord is the payload of a CONFLICT record: the OBJ_CONFLICT
// object's hash, whether it has been resolved, and what it resolved to (all
// zero while unresolved, and also once resolved to "absent").
type ConflictRecord struct {
	Conflict   archive.Hash
	Resolved   bool
	Resolution archive.Hash
}

func DecodeConflictRecord(p []byte) (ConflictRecord, error) {
	var c ConflictRecord
	if len(p) != 2*archive.HashLen+1 {
		return c, ErrMalformed
	}
	copy(c.Conflict[:], p)
	c.Resolved = p[archive.HashLen] != 0
	copy(c.Resolution[:], p[archive.HashLen+1:])
	return c, nil
}

func (c ConflictRecord) Encode() []byte {
	out := make([]byte, 0, 2*archive.HashLen+1)
	out = append(out, c.Conflict[:]...)
	var resolved byte
	if c.Resolved {
		resolved = 1
	}
	out = append(out, resolved)
	return append(out, c.Resolution[:]...)
}
