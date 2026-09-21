package object

import (
	"bytes"
	"errors"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

func TestCommitRoundTrip(t *testing.T) {
	ah := archive.Sum([]byte("attrs"))
	cases := []*Commit{
		{Tree: archive.Sum([]byte("t")), Timestamp: 9543345, Message: []byte("first_offer")},
		{Tree: archive.Sum([]byte("t2")), Parents: []archive.Hash{archive.Sum([]byte("p1")), archive.Sum([]byte("p2"))},
			Timestamp: 1, Message: []byte("merge"), Relation: RelCorrects,
			RelationTarget: archive.Sum([]byte("target")), RelationEntity: 0xdeadbeef, Attrs: &ah},
	}
	for i, c := range cases {
		enc := c.Encode()
		back, err := DecodeCommit(enc)
		if err != nil {
			t.Fatalf("#%d: %v", i, err)
		}
		if !bytes.Equal(back.Encode(), enc) {
			t.Fatalf("#%d: round trip differs", i)
		}
		if back.Timestamp != c.Timestamp || string(back.Message) != string(c.Message) ||
			len(back.Parents) != len(c.Parents) || back.Relation != c.Relation {
			t.Fatalf("#%d: fields differ: %+v", i, back)
		}
	}
}

func TestDecodeCommitLegacyWithoutTrailingFields(t *testing.T) {
	// A commit that ends right after the message has no relation and no attrs.
	c := &Commit{Tree: archive.Sum([]byte("t")), Message: []byte("m")}
	full := c.Encode()
	legacy := full[:len(full)-2] // drop relation_tag and has_attrs bytes
	back, err := DecodeCommit(legacy)
	if err != nil || back.Relation != RelNone || back.Attrs != nil {
		t.Fatalf("legacy: %v %+v", err, back)
	}
}

func TestDecodeCommitRejectsMalformed(t *testing.T) {
	good := (&Commit{Tree: archive.Sum([]byte("t")), Message: []byte("hello")}).Encode()
	cases := map[string][]byte{
		"empty":           nil,
		"short tree":      good[:10],
		"parent overrun":  append(append([]byte{}, good[:64]...), 200),
		"message overrun": good[:len(good)-3],
	}
	for name, c := range cases {
		if _, err := DecodeCommit(c); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
