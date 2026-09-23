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
	b, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(b)
	if b2, _ := back.Marshal(); err != nil || !bytes.Equal(b2, b) {
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

func TestMergeStateAndConflictRecordRoundTrip(t *testing.T) {
	s := MergeState{Ours: archive.Sum([]byte("o")), Theirs: archive.Sum([]byte("t")), OtherPath: "feat"}
	enc := s.Encode()
	if len(enc) != 132 { // 64 + 64 + len("feat")
		t.Fatalf("len=%d", len(enc))
	}
	back, err := DecodeMergeState(enc)
	if err != nil || back != s {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	if _, err := DecodeMergeState(enc[:100]); !errors.Is(err, ErrMalformed) {
		t.Fatal("short payload must fail")
	}

	c := ConflictRecord{Conflict: archive.Sum([]byte("c")), Resolved: true, Resolution: archive.Sum([]byte("r"))}
	cenc := c.Encode()
	if len(cenc) != 129 {
		t.Fatalf("len=%d", len(cenc))
	}
	cback, err := DecodeConflictRecord(cenc)
	if err != nil || cback != c {
		t.Fatalf("round trip: %v %+v", err, cback)
	}
	if _, err := DecodeConflictRecord(cenc[:128]); !errors.Is(err, ErrMalformed) {
		t.Fatal("short payload must fail")
	}
}

// Name and payload lengths are one byte on disk; Marshal must refuse what it
// cannot encode rather than wrap the length and corrupt the file.
func TestMarshalRefusesOverLongFields(t *testing.T) {
	long := string(bytes.Repeat([]byte("x"), 256))
	for name, f := range map[string]*File{
		"name":    {Records: []Record{{Name: long, Tag: TagHead}}},
		"payload": {Records: []Record{{Name: "main", Tag: TagMergeState, Payload: []byte(long)}}},
	} {
		if b, err := f.Marshal(); !errors.Is(err, ErrFieldTooLong) || b != nil {
			t.Errorf("%s: got %d bytes, err %v", name, len(b), err)
		}
	}
	ok := &File{Records: []Record{{Name: long[:255], Tag: TagHead, Payload: []byte(long[:255])}}}
	b, err := ok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if back, err := Parse(b); err != nil || back.Records[0].Name != long[:255] || len(back.Records[0].Payload) != 255 {
		t.Fatalf("255-byte fields must round-trip: %v", err)
	}
}
