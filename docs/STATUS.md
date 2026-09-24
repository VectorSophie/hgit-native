# hgit-native status

Snapshot as of 2026-09-24. **The port is fully implemented and tested.**
Every layer in [`ARCHITECTURE.md`](ARCHITECTURE.md#order) is built,
`cmd/hgit` is a real command-line tool, the full 302-line command-line
regression recorded against real TempleOS output passes end to end, and
TempleOS itself has now read a repository this port wrote.

Against ARCHITECTURE.md's "Done means" definition:

- Pillars A and B pass on Linux. The CI workflow that runs them on Linux,
  macOS and Windows is in place but has no recorded run yet, so macOS and
  Windows are not yet confirmed.
- Every command in `help` is covered by A, B or a unit test; the commands
  covered by unit tests only are listed under "Known coverage gap" below.
- **Pillar C** now passes (see below): a repository built entirely by
  `cmd/hgit` (`init` + two `offer`s) was pushed into a real TempleOS guest
  under QEMU, and `Hgit("check ...")`/`Hgit("history ...")` there produced
  the same `CHECK_OK`/history output this port's own `--serial` mode does
  for the identical repository.

No GitHub release and no Homebrew/Chocolatey/apt listing exist for this
repository.

## Done

- Repository created; Go module initialised (`go.mod`, Go 1.22). Only
  external dependency: `golang.org/x/crypto`.
- `contract/` submodule pinned to `contract-1.8.9` of the TempleOS hgit:
  format spec, ADRs and research, the HolyC source, and the golden-fixture
  repos and expected output produced on real TempleOS.
- Timestamp epoch decided: [ADR N-0001](adr/N-0001-timestamp-epoch.md) (Unix
  ms; values below 1e12 are TempleOS ticks); `pkg/hgit/clock`.
- Design written: [`ARCHITECTURE.md`](ARCHITECTURE.md).
- The full port: `pkg/hgit/{archive,object,meta,repo,check,ignore,attrs,
  clock,fossil,workdir,offer,status,mergebase,merge,views}`, plus
  `internal/cli` (dispatch, human-readable and `--serial` output) and
  `cmd/hgit`.
- `internal/testfix` reads fixtures out of the `contract/` submodule for
  every test that needs golden bytes.
- Fuzz tests over every byte-level parser (`archive.Parse`,
  `object.DecodeTree/DecodeCommit/DecodeAttrs/DecodeConflict`,
  `meta.Parse`, `fossil.DeltaApply`), seeded from fixture bytes (constructed
  deltas for `fossil`). A 30-second
  `go test -fuzz` run of each of the eight targets found no crasher. See
  `docs/porting-notes.md`.
- CI (`.github/workflows/ci.yml`): `ubuntu-latest`, `macos-latest`,
  `windows-latest`, each checking out the submodule and running `go vet`,
  `go build`, `go test`.

## Port progress

Layers and gates are defined in [`ARCHITECTURE.md`](ARCHITECTURE.md#order).

| Layer | Status |
|---|---|
| 1. Bytes (`Blake2b`, `Hgs`, `Object`, `Archive`, `Hex`, `Canon`, `Fossil`) | done |
| 2. Model (`Tree`, `Commit`, `Index`, `MergeBase`, `Conflict`, `Attrs`, `Ignore`, `Meta`) | done |
| 3. Commands (`init`, `offer`, `status`, `history`, `see`, `diff`, `check`, ...) | done |
| 4. Merge and the conflict lifecycle | done |
| 5. Plain-text views (`graph`, `historydoc`, ...) | done |
| Conformance tests (pillar B: full command-line replay) | done, passing |
| Fuzz testing of the byte-level parsers | done, no crashers found |
| CI | workflow written; no recorded run yet |
| Pillar C (TempleOS reads a native-written repository, manual) | done, passing |
| Packaging (Homebrew, Chocolatey, deb) | not started |
| TUI / GUI | not started |

## Known coverage gap

`tests/full_replay_test.go` replays every line of
`contract/tests/full-regression.hc` (the real TempleOS session) through
`internal/cli.Run` and diffs the whole 302-line output against
`expected.log`. That regression does not exercise every command. These are
dispatcher-wired and unit-tested (in `internal/cli`, e.g.
`TestEveryCommandRoutes`, and in their own packages) but not covered by an
end-to-end replay against real TempleOS output: `revert`, `reconcile`, `reverttree`, `reconciletree`, `path close`, `operation restore`.

Closing this gap means recording a new TempleOS session that runs them.

## Pillar C: TempleOS reads a native-written repository

Done, 2026-09-24. `tools/interop.py` boots a copy of the TempleOS bundle
under QEMU, pushes a repository `cmd/hgit` built (`init`, then two `offer`s,
producing `Interop.hgs`/`Interop.hgs.m`) into the guest over COM2, and runs
`Hgit("check C:/Home/Interop.hgs")`/`Hgit("history C:/Home/Interop.hgs")`
there. Real output from that run:

```
CHECK_OK objects=7 format_version=4
CHECK_REFS_OK
CHECK_DANGLING_NONE
commit ts=1790168860391 msg=native offer two
commit ts=1790168860389 msg=native offer one
HISTORY_END shown=2
```

The same repository's `--serial check`/`--serial history` through `cmd/hgit`
itself report the identical `CHECK_OK objects=7 format_version=4` /
`CHECK_REFS_OK` / `CHECK_DANGLING_NONE` and the same two commit lines: this
port's repository is real, valid, and readable to TempleOS, not just to
itself.

**Push protocol finding.** The transport carries raw bytes over a serial
line terminated by a single `0x04` (EOT) byte, the same mechanism
`build-bundle.py`/`gen-fixtures.py` use. That works for those tools because
they push `HgitAll.HC`, which is HolyC source text with no embedded `0x04`.
A `.hgs` file is binary and legitimately contains `0x04` as ordinary data
(for example, the format-version byte at header offset 4, whenever the
version is 4) - sent raw, the receiver mistook that data byte for the
terminator and truncated every push at exactly 4 bytes, deterministically.
`tools/interop.py` hex-encodes the payload before sending and decodes it on
the guest side with `HexDigit` (already loaded via `HgitAll.HC`), so the
wire stream is pure ASCII and `0x04` can only ever mean "done". This is a
real, reusable finding for anything that pushes a `.hgs`/`.hgs.m` file (not
just HolyC source) into a TempleOS guest.

## Not done

- **Packaging and release**: no `.deb`, no Homebrew formula, no Chocolatey
  package is built or published for hgit-native. Anyone wanting the tool
  today builds it with `go build ./cmd/hgit` ([`INSTALL.md`](../INSTALL.md)).
- **A TUI or GUI.** `cmd/hgit` is the only interface; `internal/cli` is a
  plain command-line dispatcher, human-readable or `--serial`.
