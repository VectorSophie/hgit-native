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

var ErrMalformed = errors.New("meta: malformed")

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

func (f *File) Marshal() []byte {
	var out []byte
	for _, r := range f.Records {
		out = append(out, byte(len(r.Name)))
		out = append(out, r.Name...)
		out = append(out, r.Tag, byte(len(r.Payload)))
		out = append(out, r.Payload...)
	}
	return out
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
