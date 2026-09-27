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

// ADR 0019 section 3: ParseTolerant never errors on a torn tail - it keeps
// whatever came before it and reports where the tear starts.
func TestParseTolerantStopsAtATornRecordInsteadOfErroring(t *testing.T) {
	h := Header{Version: 4, Count: 1}.Marshal()

	cases := []struct {
		name string
		tail []byte
	}{
		{"huge length", func() []byte { b, _ := hex.DecodeString("ffffffffffffff7f"); return b }()},
		{"short length field", []byte{1, 2, 3}},
		{"data present, hash missing", []byte{5, 0, 0, 0, 0, 0, 0, 0, 'a', 'b', 'c', 'd', 'e'}},
	}
	for _, c := range cases {
		b := append(append([]byte{}, h...), c.tail...)
		a, tornOffset, tornBytes, err := ParseTolerant(b)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(a.Records) != 0 {
			t.Fatalf("%s: %d records, want 0 (the only record present is torn)", c.name, len(a.Records))
		}
		if tornOffset != HeaderLen {
			t.Fatalf("%s: tornOffset=%d, want %d (the header length)", c.name, tornOffset, HeaderLen)
		}
		if tornBytes != len(c.tail) {
			t.Fatalf("%s: tornBytes=%d, want %d", c.name, tornBytes, len(c.tail))
		}
		// Parse itself keeps its strict, all-or-nothing contract.
		if _, err := Parse(b); !errors.Is(err, ErrTruncated) {
			t.Fatalf("%s: Parse must still error, got %v", c.name, err)
		}
	}
}

// A clean archive (nothing torn) reports zero for both torn fields, and
// ParseTolerant/Parse agree on every record.
func TestParseTolerantAgreesWithParseOnACleanArchive(t *testing.T) {
	full := &Archive{Header: Header{Version: 4}, Records: []Record{NewRecord([]byte("one")), NewRecord([]byte("two"))}}
	full.Header.Count = uint64(len(full.Records))
	b := full.Marshal()

	a, tornOffset, tornBytes, err := ParseTolerant(b)
	if err != nil {
		t.Fatal(err)
	}
	if tornOffset != 0 || tornBytes != 0 {
		t.Fatalf("clean archive reported torn: offset=%d bytes=%d", tornOffset, tornBytes)
	}
	if len(a.Records) != 2 {
		t.Fatalf("%d records, want 2", len(a.Records))
	}
	strict, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(strict.Records) != len(a.Records) {
		t.Fatal("Parse and ParseTolerant disagree on a clean archive")
	}
}

// A record that fits but has a bad stored hash is not torn: ParseTolerant
// keeps it and keeps walking past it (Verify is what flags it, unchanged).
func TestParseTolerantDoesNotStopAtACorruptRecord(t *testing.T) {
	full := &Archive{Header: Header{Version: 4}, Records: []Record{NewRecord([]byte("one")), NewRecord([]byte("two"))}}
	full.Header.Count = uint64(len(full.Records))
	b := full.Marshal()
	b[HeaderLen+8] ^= 0xff // corrupt the first record's content, in place

	a, tornOffset, tornBytes, err := ParseTolerant(b)
	if err != nil {
		t.Fatal(err)
	}
	if tornOffset != 0 || tornBytes != 0 {
		t.Fatal("a corrupt-but-complete record must not be reported as torn")
	}
	if len(a.Records) != 2 {
		t.Fatalf("%d records, want both (corruption does not stop the walk)", len(a.Records))
	}
	if total, ok := a.Verify(); total != 2 || ok != 1 {
		t.Fatalf("verify %d/%d, want 1/2", ok, total)
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

func manyRecords(n int) []byte {
	a := &Archive{Header: Header{Version: 4, Count: uint64(n)}}
	for i := 0; i < n; i++ {
		a.Records = append(a.Records, NewObject(Blob, bytes.Repeat([]byte{byte(i)}, 100+i%50)))
	}
	return a.Marshal()
}

// Records are views into the parsed buffer, not per-record copies.
func TestParseDoesNotCopyEachRecord(t *testing.T) {
	b := manyRecords(1000)
	allocs := testing.AllocsPerRun(10, func() { Parse(b) })
	if allocs > 50 {
		t.Fatalf("%.0f allocations to parse 1000 records, want O(log n) for the slice only", allocs)
	}
}

// A record's Data is capped at its own end, so appending to it (or to its
// Content) reallocates and can never overwrite the next record.
func TestParsedRecordCannotGrowIntoItsNeighbor(t *testing.T) {
	b := manyRecords(3)
	orig := append([]byte(nil), b...)
	a, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Records {
		_ = append(a.Records[i].Data, 0xee, 0xee, 0xee)
		_ = append(a.Records[i].Content(), 0xee, 0xee, 0xee)
	}
	if !bytes.Equal(b, orig) || !bytes.Equal(a.Marshal(), orig) {
		t.Fatal("an append to one record changed the buffer")
	}
	if total, ok := a.Verify(); ok != total {
		t.Fatalf("verify %d/%d", ok, total)
	}
}

func BenchmarkParse(b *testing.B) {
	raw := manyRecords(10000)
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(raw); err != nil {
			b.Fatal(err)
		}
	}
}
