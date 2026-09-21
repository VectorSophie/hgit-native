package object

import (
	"bytes"
	"errors"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

func TestTreeRoundTrip(t *testing.T) {
	tr := &Tree{Entries: []Entry{
		{Name: "a.txt", ChildType: archive.Blob, ChildHash: archive.Sum([]byte("a")), EntityID: 0x1122334455667788},
		{Name: "sub", ChildType: archive.Tree, ChildHash: archive.Sum([]byte("s")), EntityID: 7},
	}}
	enc := tr.Encode()
	if len(enc) != 4+(1+5+1+64+8)+(1+3+1+64+8) {
		t.Fatalf("len=%d", len(enc))
	}
	back, err := DecodeTree(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.Encode(), enc) {
		t.Fatal("round trip differs")
	}
	if e, ok := back.Find("sub"); !ok || e.EntityID != 7 {
		t.Fatalf("find: %v %v", e, ok)
	}
	if _, ok := back.Find("nope"); ok {
		t.Fatal("found a missing name")
	}
}

func TestDecodeTreeRejectsMalformed(t *testing.T) {
	cases := map[string][]byte{
		"empty":      nil,
		"huge count": {0xff, 0xff, 0xff, 0x7f},
		"cut entry":  {1, 0, 0, 0, 5, 'a'},
		"trailing":   append((&Tree{}).Encode(), 0),
	}
	for name, c := range cases {
		if _, err := DecodeTree(c); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
