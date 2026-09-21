package object

import (
	"bytes"
	"errors"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

func TestAttrsRoundTrip(t *testing.T) {
	a := &Attrs{Entries: []AttrEntry{{EntityID: 1, Mode: ModeBinary}, {EntityID: 2, Mode: ModeBinary | ModeExecutable}}}
	enc := a.Encode()
	if len(enc) != 4+2*9 {
		t.Fatalf("len=%d", len(enc))
	}
	back, err := DecodeAttrs(enc)
	if err != nil || !bytes.Equal(back.Encode(), enc) {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := DecodeAttrs([]byte{9, 0, 0, 0}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("count overrun: %v", err)
	}
}

func TestConflictRoundTrip(t *testing.T) {
	c := &Conflict{
		Kind: KindContent | KindMode, EntityID: 42, Path: "dir/file.txt",
		Base:   Side{Present: true, Type: archive.Blob, Mode: 0, Hash: archive.Sum([]byte("b"))},
		Ours:   Side{Present: true, Type: archive.Blob, Mode: ModeExecutable, Hash: archive.Sum([]byte("o"))},
		Theirs: Side{},
	}
	enc := c.Encode()
	back, err := DecodeConflict(enc)
	if err != nil || !bytes.Equal(back.Encode(), enc) {
		t.Fatalf("round trip: %v", err)
	}
	if back.Theirs.Present || !back.Ours.Present || back.Path != "dir/file.txt" {
		t.Fatalf("fields: %+v", back)
	}
	if _, err := DecodeConflict(enc[:len(enc)-3]); !errors.Is(err, ErrMalformed) {
		t.Fatalf("truncated: %v", err)
	}
}
