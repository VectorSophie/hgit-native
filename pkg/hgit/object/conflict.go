package object

import (
	"encoding/binary"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

const (
	KindContent byte = 0x01
	KindMode    byte = 0x02
	KindType    byte = 0x04
)

type Side struct {
	Present bool
	Type    archive.Type
	Mode    byte
	Hash    archive.Hash
}

// Conflict is one merge conflict's persistent base/ours/theirs evidence.
type Conflict struct {
	Kind               byte
	EntityID           uint64
	Path               string
	Base, Ours, Theirs Side
}

func DecodeConflict(b []byte) (*Conflict, error) {
	c := &Conflict{}
	if len(b) < 1+8+1 {
		return nil, ErrMalformed
	}
	c.Kind = b[0]
	c.EntityID = binary.LittleEndian.Uint64(b[1:])
	pl := int(b[9])
	pos := 10
	if len(b)-pos < pl {
		return nil, ErrMalformed
	}
	c.Path = string(b[pos : pos+pl])
	pos += pl
	for _, s := range []*Side{&c.Base, &c.Ours, &c.Theirs} {
		if pos >= len(b) {
			return nil, ErrMalformed
		}
		present := b[pos]
		pos++
		if present == 0 {
			continue
		}
		if len(b)-pos < 2+archive.HashLen {
			return nil, ErrMalformed
		}
		s.Present = true
		s.Type = archive.Type(b[pos])
		s.Mode = b[pos+1]
		copy(s.Hash[:], b[pos+2:])
		pos += 2 + archive.HashLen
	}
	if pos != len(b) {
		return nil, ErrMalformed
	}
	return c, nil
}

func (c *Conflict) Encode() []byte {
	out := []byte{c.Kind}
	var id [8]byte
	binary.LittleEndian.PutUint64(id[:], c.EntityID)
	out = append(out, id[:]...)
	out = append(out, byte(len(c.Path)))
	out = append(out, c.Path...)
	for _, s := range []Side{c.Base, c.Ours, c.Theirs} {
		if !s.Present {
			out = append(out, 0)
			continue
		}
		out = append(out, 1, byte(s.Type), s.Mode)
		out = append(out, s.Hash[:]...)
	}
	return out
}
