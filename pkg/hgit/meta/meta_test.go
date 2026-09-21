package meta

import (
	"bytes"
	"errors"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

func TestSetReplacesAndFindIsLastWins(t *testing.T) {
	f := &File{}
	f.Set("main", TagHead, []byte{1})
	f.Set("main", TagHead, []byte{2})
	if len(f.All("main", TagHead)) != 1 {
		t.Fatal("Set must splice out the old record")
	}
	f.Append("main", TagOpLog, []byte{1})
	f.Append("main", TagOpLog, []byte{2})
	if r, _ := f.Find("main", TagOpLog); r.Payload[0] != 2 {
		t.Fatal("Find must be last-match-wins")
	}
	if r, ok := f.PopLast("main", TagOpLog); !ok || r.Payload[0] != 2 || len(f.All("main", TagOpLog)) != 1 {
		t.Fatal("PopLast must remove only the last")
	}
}

func TestRoundTripAndMalformed(t *testing.T) {
	f := &File{}
	f.Set("", TagCurrent, []byte("main"))
	f.Set("feature", TagPathDeclared, nil)
	b := f.Marshal()
	back, err := Parse(b)
	if err != nil || !bytes.Equal(back.Marshal(), b) {
		t.Fatalf("round trip: %v", err)
	}
	for name, c := range map[string][]byte{
		"name overrun":    {5, 'a'},
		"missing tag":     {1, 'a'},
		"payload overrun": {1, 'a', TagHead, 9, 1},
	} {
		if _, err := Parse(c); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if f, err := Parse(nil); err != nil || len(f.Records) != 0 {
		t.Fatal("empty input is an empty file")
	}
}

func TestOpLogEntry(t *testing.T) {
	e := OpLogEntry{Timestamp: 345402, Prev: archive.Hash{}, New: archive.Sum([]byte("n"))}
	enc := e.Encode()
	if len(enc) != 136 {
		t.Fatalf("len=%d", len(enc))
	}
	back, err := DecodeOpLog(enc)
	if err != nil || back != e {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := DecodeOpLog(enc[:100]); !errors.Is(err, ErrMalformed) {
		t.Fatal("short payload must fail")
	}
}
