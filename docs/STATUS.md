# hgit-native status

Snapshot as of 2026-09-23. **The port is implemented.** Every layer in
[`ARCHITECTURE.md`](ARCHITECTURE.md#order) is built, `cmd/hgit` is a real
command-line tool, and the full 302-line command-line regression recorded
against real TempleOS output passes end to end. Packaging and release are
out of scope for this plan (see "Out of scope" below); no GitHub release
and no Homebrew/Chocolatey/apt listing exist for this repository.

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
  `meta.Parse`), seeded from real fixture bytes. `go test -fuzz=... -
  fuzztime=30s ./pkg/hgit/...` for each of the six targets found no
  crasher (over 10.3M total executions). See `docs/porting-notes.md`.
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
| CI | done |
| Packaging (Homebrew, Chocolatey, deb) | out of scope for this plan |
| TUI / GUI | out of scope for this plan |

## Known coverage gap

`tests/full_replay_test.go` replays every line of
`contract/tests/full-regression.hc` (the real TempleOS session) through
`internal/cli.Run` and diffs the whole 302-line output against
`expected.log`. That regression does not exercise every command, so the
following are covered only by the dispatcher's own unit tests in
`internal/cli` (e.g. `TestEveryCommandRoutes`), not by an end-to-end replay
against real TempleOS output:

- `revert`
- `reconcile`
- the tree variants of any command with a `...tree` form the regression
  does not reach
- `path close`
- `operation restore`

This is the same gap task 16 flagged when it landed; closing it means
recording a new TempleOS session, which is outside this plan.

## Out of scope for this plan

- **Packaging and release**: no `.deb`, no Homebrew formula, no Chocolatey
  package is built or published for hgit-native from this plan. Anyone
  wanting the tool today builds it with `go build ./cmd/hgit`
  ([`INSTALL.md`](../INSTALL.md)).
- **Pillar C** (reverse interop: TempleOS reads a repository written by
  hgit-native) needs a QEMU-based script that does not exist.
- **A TUI or GUI.** `cmd/hgit` is the only interface; `internal/cli` is a
  plain command-line dispatcher, human-readable or `--serial`.
- **Manual TempleOS interop testing** (the plan's own final, out-of-scope
  task) is not attempted here.
