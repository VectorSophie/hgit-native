# ADR N-0001: Timestamp epoch

## Status

Accepted, 2026-09-22.

## Context

The commit timestamp is a `U64` in the commit metadata. The format spec does
not say what it counts.

In the TempleOS source it is `cnts.jiffies`
(`contract/src/hgit-cli/Hgit.HC`, lines 42, 156, 189 and 321-364): ticks since
boot, with `JIFFY_FREQ` = 1000 per second. It is uptime, not wall-clock time.
`ts=9547867` in the fixtures is about 2.6 hours of uptime.

History order comes from parent links, never from timestamps, so nothing in
the format or in any command depends on the value. The only risk is display:
if a native tool wrote real time next to boot ticks, a reader would show
either a nonsense date for a TempleOS commit or a huge tick count for a native
one.

## Decision

Native writers store Unix time in **milliseconds** in the existing `U64`
field. The unit matches TempleOS's 1000 ticks per second.

A reader treats a value below `1_000_000_000_000` (before 2001-09-09 in ms)
as TempleOS uptime ticks, wall-clock time unknown, and shows it as a relative
tick count, never as a date. Native values always exceed the threshold, so the
two ranges are unambiguous.

Nothing on disk changes. `contract/FORMAT.md` is not edited here: `contract/`
is read-only, and this repo will not fork the spec. A clarifying line
(timestamp is `U64`; TempleOS writes uptime ticks, native writes Unix ms)
belongs in the contract repo's `FORMAT.md` at its next tag.

Implemented in `pkg/hgit/clock`.

## Consequences

| Value | Reader shows |
|---|---|
| below `1_000_000_000_000` | `ticks:<n>` (TempleOS uptime, no date) |
| threshold up to year 9999 (`253402300799999`) | RFC 3339 UTC, millisecond precision |
| above year 9999 | `invalid:<n>` |

- Mixed-origin repos are fine: each commit is classified on its own value.
  Timestamps are not comparable across the two ranges and are not used for
  ordering.
- Threshold limit: a TempleOS machine would need 31.7 years of uptime to reach
  it, which is implausible. A native clock set before 2001-09-09 is not
  supported; its commits would display as ticks.
- TempleOS reading a native-written repo sees a large number it does not
  interpret; the field stays a valid `U64`.

## Alternatives considered

- **Store seconds.** Overlaps TempleOS tick values (9.5 million ticks looks
  like a 1970 date) and cannot be told apart reliably.
- **Store a TempleOS-epoch converted time.** TempleOS has no boot-time anchor
  in the repo, so there is nothing to convert from.
- **New field or format version bump.** Breaks compatibility with the
  original tool for a display concern only.
- **Leave it opaque and undecided.** Native writers still have to write
  something, and readers still have to show it.

## Rejected / not doing

- Editing `contract/FORMAT.md` in this repo.
- Using timestamps to order history.
- Guessing dates for TempleOS commits.
