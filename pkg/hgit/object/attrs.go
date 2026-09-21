package object

import "encoding/binary"

const (
	ModeBinary     byte = 0x01
	ModeExecutable byte = 0x02
)

type AttrEntry struct {
	EntityID uint64
	Mode     byte
}

// Attrs is a commit's (entity id -> non-default mode) list.
type Attrs struct{ Entries []AttrEntry }

func DecodeAttrs(b []byte) (*Attrs, error) {
	if len(b) < 4 {
		return nil, ErrMalformed
	}
	n := binary.LittleEndian.Uint32(b)
	if uint64(n) != uint64(len(b)-4)/9 || (len(b)-4)%9 != 0 {
		return nil, ErrMalformed
	}
	a := &Attrs{Entries: make([]AttrEntry, 0, n)}
	for i, pos := uint32(0), 4; i < n; i, pos = i+1, pos+9 {
		a.Entries = append(a.Entries, AttrEntry{
			EntityID: binary.LittleEndian.Uint64(b[pos:]),
			Mode:     b[pos+8],
		})
	}
	return a, nil
}

func (a *Attrs) Encode() []byte {
	out := make([]byte, 4, 4+9*len(a.Entries))
	binary.LittleEndian.PutUint32(out, uint32(len(a.Entries)))
	for _, e := range a.Entries {
		var id [8]byte
		binary.LittleEndian.PutUint64(id[:], e.EntityID)
		out = append(out, id[:]...)
		out = append(out, e.Mode)
	}
	return out
}
