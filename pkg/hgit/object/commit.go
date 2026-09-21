package object

import (
	"encoding/binary"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

type Relation byte

const (
	RelNone       Relation = 0
	RelContinues  Relation = 1
	RelCorrects   Relation = 2
	RelReverts    Relation = 3
	RelReconciles Relation = 4
)

type Commit struct {
	Tree           archive.Hash
	Parents        []archive.Hash
	Timestamp      uint64
	Message        []byte
	Relation       Relation
	RelationTarget archive.Hash // meaningful only if Relation != RelNone
	RelationEntity uint64       // 0 = not entity-scoped
	Attrs          *archive.Hash
}

// DecodeCommit parses the layout in FORMAT.md. The relation tag and the
// has_attrs byte are optional trailing fields: content that ends before them
// (an older commit) decodes as RelNone / no attrs. Encode always writes both.
func DecodeCommit(b []byte) (*Commit, error) {
	c := &Commit{}
	pos := 0
	need := func(n int) bool { return len(b)-pos >= n }
	if !need(archive.HashLen + 1) {
		return nil, ErrMalformed
	}
	copy(c.Tree[:], b[pos:])
	pos += archive.HashLen
	pc := int(b[pos])
	pos++
	if !need(pc*archive.HashLen + 8 + 4) {
		return nil, ErrMalformed
	}
	for i := 0; i < pc; i++ {
		var h archive.Hash
		copy(h[:], b[pos:])
		pos += archive.HashLen
		c.Parents = append(c.Parents, h)
	}
	c.Timestamp = binary.LittleEndian.Uint64(b[pos:])
	pos += 8
	ml := binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	if uint64(ml) > uint64(len(b)-pos) {
		return nil, ErrMalformed
	}
	c.Message = append([]byte(nil), b[pos:pos+int(ml)]...)
	pos += int(ml)

	if pos < len(b) { // relation_tag
		c.Relation = Relation(b[pos])
		pos++
		if c.Relation != RelNone {
			if !need(archive.HashLen + 8) {
				return nil, ErrMalformed
			}
			copy(c.RelationTarget[:], b[pos:])
			pos += archive.HashLen
			c.RelationEntity = binary.LittleEndian.Uint64(b[pos:])
			pos += 8
		}
	}
	if pos < len(b) { // has_attrs
		has := b[pos]
		pos++
		if has != 0 {
			if !need(archive.HashLen) {
				return nil, ErrMalformed
			}
			var h archive.Hash
			copy(h[:], b[pos:])
			c.Attrs = &h
		}
	}
	return c, nil
}

func (c *Commit) Encode() []byte {
	out := append([]byte(nil), c.Tree[:]...)
	out = append(out, byte(len(c.Parents)))
	for _, p := range c.Parents {
		out = append(out, p[:]...)
	}
	var u8 [8]byte
	binary.LittleEndian.PutUint64(u8[:], c.Timestamp)
	out = append(out, u8[:]...)
	var u4 [4]byte
	binary.LittleEndian.PutUint32(u4[:], uint32(len(c.Message)))
	out = append(out, u4[:]...)
	out = append(out, c.Message...)
	out = append(out, byte(c.Relation))
	if c.Relation != RelNone {
		out = append(out, c.RelationTarget[:]...)
		binary.LittleEndian.PutUint64(u8[:], c.RelationEntity)
		out = append(out, u8[:]...)
	}
	if c.Attrs != nil {
		out = append(out, 1)
		out = append(out, c.Attrs[:]...)
	} else {
		out = append(out, 0)
	}
	return out
}
