package clock

import (
	"math"
	"strings"
	"testing"
)

func TestIsWallClock(t *testing.T) {
	cases := []struct {
		ts   uint64
		want bool
	}{
		{0, false},
		{9547867, false},
		{WallClockThreshold - 1, false},
		{WallClockThreshold, true},
		{1758416400000, true},
	}
	for _, c := range cases {
		if got := IsWallClock(c.ts); got != c.want {
			t.Errorf("IsWallClock(%d) = %v, want %v", c.ts, got, c.want)
		}
	}
}

func TestFormat(t *testing.T) {
	if got := Format(9547867); got != "ticks:9547867" {
		t.Errorf("got %q", got)
	}
	if got := Format(0); got != "ticks:0" {
		t.Errorf("got %q", got)
	}
	if got := Format(1758416400000); got != "2025-09-21T01:00:00.000Z" {
		t.Errorf("got %q", got)
	}
	if got := Format(WallClockThreshold); !strings.HasPrefix(got, "2001-09-09") {
		t.Errorf("got %q", got)
	}
}

func TestFormatYear9999(t *testing.T) {
	if got := Format(253402300799999); got != "9999-12-31T23:59:59.999Z" {
		t.Errorf("got %q", got)
	}
	if got := Format(253402300800000); !strings.HasPrefix(got, "invalid:") {
		t.Errorf("got %q", got)
	}
}

func TestFormatHuge(t *testing.T) {
	if got := Format(math.MaxUint64); got != "invalid:18446744073709551615" {
		t.Errorf("got %q", got)
	}
}

func TestNowReplaceable(t *testing.T) {
	if Now() <= WallClockThreshold {
		t.Fatal("Now not above threshold")
	}
	orig := Now
	t.Cleanup(func() { Now = orig })
	Now = func() uint64 { return 42 }
	if Now() != 42 {
		t.Fatal("not replaced")
	}
	Now = orig
	if Now() == 42 {
		t.Fatal("not restored")
	}
}
