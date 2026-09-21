// Package clock defines the commit timestamp convention (ADR N-0001).
package clock

import (
	"strconv"
	"time"
)

// WallClockThreshold separates TempleOS uptime ticks (below) from native
// Unix-millisecond values (at or above).
const WallClockThreshold uint64 = 1_000_000_000_000

// maxWall is 9999-12-31T23:59:59.999Z in Unix ms; larger values are not dates.
const maxWall uint64 = 253402300799999

// Now returns Unix time in milliseconds. It is a variable so tests can freeze it.
var Now = func() uint64 { return uint64(time.Now().UnixMilli()) }

// IsWallClock reports whether ts is a native wall-clock value.
func IsWallClock(ts uint64) bool { return ts >= WallClockThreshold }

// Format renders wall-clock values as RFC 3339 UTC with milliseconds,
// smaller values as "ticks:<n>", and values past year 9999 as "invalid:<n>".
func Format(ts uint64) string {
	switch {
	case !IsWallClock(ts):
		return "ticks:" + strconv.FormatUint(ts, 10)
	case ts > maxWall:
		return "invalid:" + strconv.FormatUint(ts, 10)
	}
	return time.UnixMilli(int64(ts)).UTC().Format("2006-01-02T15:04:05.000Z")
}
