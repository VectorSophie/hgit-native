package archive

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func TestSumMatchesRFC7693(t *testing.T) {
	want := "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d1" +
		"7d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923"
	if got := Sum([]byte("abc")).Hex(); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestHexRoundTrip(t *testing.T) {
	h := Sum([]byte("x"))
	back, err := ParseHex(h.Hex())
	if err != nil || back != h {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := ParseHex("zz"); err == nil {
		t.Fatal("want error for bad hex")
	}
}

func TestFormatVerifiedTestVector(t *testing.T) {
	// FORMAT.md "Verified test vector": two 5-byte records, 170 bytes total.
	a := &Archive{Header: Header{Version: 4, Count: 2}}
	a.Records = []Record{NewRecord([]byte("hgitA")), NewRecord([]byte("hgitB"))}
	b := a.Marshal()
	if len(b) != 16+2*77 {
		t.Fatalf("len=%d want 170", len(b))
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if total, ok := back.Verify(); total != 2 || ok != 2 {
		t.Fatalf("verify %d/%d", ok, total)
	}
	if !bytes.Equal(back.Marshal(), b) {
		t.Fatal("not byte-identical")
	}
}

func TestTypeTagChangesHash(t *testing.T) {
	c := []byte{1, 2, 3}
	if NewObject(Blob, c).Hash == NewObject(Tree, c).Hash {
		t.Fatal("same bytes as different types must hash differently")
	}
	r := NewObject(Tree, c)
	if r.Type() != Tree || !bytes.Equal(r.Content(), c) {
		t.Fatal("type/content accessors wrong")
	}
}

func TestParseHeaderErrors(t *testing.T) {
	if _, err := ParseHeader([]byte("HG")); !errors.Is(err, ErrTruncated) {
		t.Fatalf("short: %v", err)
	}
	bad := Header{Version: 4}.Marshal()
	bad[0] = 'X'
	if _, err := ParseHeader(bad); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("magic: %v", err)
	}
	newer := Header{Version: 9}.Marshal()
	_, err := ParseHeader(newer)
	var uv *UnsupportedVersionError
	if !errors.As(err, &uv) || uv.Version != 9 {
		t.Fatalf("version: %v", err)
	}
	want := "unsupported_format_version=9 (this build reads up to 4) - written by a newer hgit"
	if err.Error() != want {
		t.Fatalf("message %q", err.Error())
	}
}

func TestParseRejectsTruncatedAndHugeLengths(t *testing.T) {
	h := Header{Version: 4, Count: 1}.Marshal()
	// record claiming 2^63 bytes must fail without allocating
	huge, _ := hex.DecodeString("ffffffffffffff7f")
	if _, err := Parse(append(append([]byte{}, h...), huge...)); !errors.Is(err, ErrTruncated) {
		t.Fatalf("huge: %v", err)
	}
	// length field cut short
	if _, err := Parse(append(append([]byte{}, h...), 1, 2, 3)); !errors.Is(err, ErrTruncated) {
		t.Fatalf("short length: %v", err)
	}
	// data present but hash missing
	rec := append(append([]byte{}, h...), 5, 0, 0, 0, 0, 0, 0, 0, 'a', 'b', 'c', 'd', 'e')
	if _, err := Parse(rec); !errors.Is(err, ErrTruncated) {
		t.Fatalf("missing hash: %v", err)
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	a := &Archive{Header: Header{Version: 4, Count: 1}, Records: []Record{NewRecord([]byte("hello"))}}
	a.Records[0].Data[0] ^= 0xff
	if total, ok := a.Verify(); total != 1 || ok != 0 {
		t.Fatalf("verify %d/%d", ok, total)
	}
}

func TestParseEntityID(t *testing.T) {
	good := map[string]uint64{
		"0000000000000000": 0,
		"fecf09b6855459dc": 0xfecf09b6855459dc,
		"FECF09B6855459DC": 0xfecf09b6855459dc, // HexDigit accepts A-F too
		"ffffffffffffffff": ^uint64(0),
	}
	for s, want := range good {
		got, err := ParseEntityID(s)
		if err != nil || got != want {
			t.Errorf("ParseEntityID(%q) = %#x, %v; want %#x", s, got, err, want)
		}
	}
	for _, s := range []string{"", "0", "fecf09b6855459d", "fecf09b6855459dc0", "fecf09b6855459dg", "fecf09b6855459d "} {
		if _, err := ParseEntityID(s); err == nil {
			t.Errorf("ParseEntityID(%q) accepted a bad entity id", s)
		}
	}
}
