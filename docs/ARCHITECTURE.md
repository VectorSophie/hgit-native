# hgit-native architecture

hgit-native is hgit for ordinary operating systems: Windows, macOS and Linux,
as a single native executable. It is a Go port of
[hgit](https://github.com/VectorSophie/hgit), the version-control system
written in HolyC for TempleOS. Same commands, same on-disk format, no VM.

## What this is, and is not

- **Is:** a standalone VCS. `hgit offer`, `hgit history`, `hgit merge`, ... in
  a normal terminal, installable with `choco install hgit`, `brew install`,
  `apt install`, or by downloading a binary.
- **Is not:** a drop-in Git replacement. Repos are `.hgs` archives, not Git
  repos. There are no remotes; sharing is `export` / `import`. It cannot open
  Git repos or talk to GitHub. Git interop is a possible later phase, not v1.

## Relationship to the TempleOS hgit

The two projects share a **contract**, and only the contract:

- `contract/` is a git submodule pinned to a tag of the TempleOS repo
  (currently `contract-1.8.9`). It provides `FORMAT.md` (the `.hgs` v4 format),
  the ADRs and research, the HolyC source being ported, and `fixtures/`:
  repos and expected output produced on real TempleOS.
- The TempleOS repo is feature-frozen but still owns the contract: format
  changes and fixture fixes land there first, then this repo bumps its pin.
- A repo made by either implementation must open in the other.

Concept and format documentation lives in the contract and is not copied.
Only usage documentation (command syntax, per-OS install) is written here.

## Repository layout

```
contract/            submodule -> hgit @ pinned tag (spec, source, fixtures)
cmd/hgit/            entrypoint
pkg/hgit/            the library: all VCS logic, no printing, no terminal code
internal/cli/        argument parsing, output modes, exit codes
tests/               conformance tests (see Testing)
docs/                this file, porting notes, per-OS install and quirks
packaging/           Homebrew, Chocolatey, deb: ship the binary, no VM
```

`pkg/hgit` is a library on purpose. The CLI, a future TUI and a future GUI are
thin front-ends over it, so a new front-end never touches format code or the
parity tests. The package layout mirrors the HolyC `src/hgit-core` and
`src/hgit-cli` split: each HolyC file has an obvious Go counterpart, which is
what keeps a ~9.5k-line port reviewable.

## Port mechanics

### Order

Each layer has a gate; the next layer starts only when it passes against the
fixtures.

| Layer | HolyC files | Gate |
|---|---|---|
| 1. Bytes | `Blake2b`, `Hgs`, `Object`, `Archive`, `Hex`, `Canon`, `Fossil` | Read every fixture repo, re-verify all object hashes, re-serialize byte-identically |
| 2. Model | `Tree`, `Commit`, `Index`, `MergeBase`, `Conflict`, `Attrs`, `Ignore`, `Meta` | Decode trees, commits, HEAD, paths and conflicts in the fixtures; match `expected.log` values |
| 3. Commands | `Init`, `Offer`, `Status`, `History`, `See`, `Diff`, `Check`, `OpLog`, `Paths`, `WorkDir`, `Portable` | Replay `expected.log` per command |
| 4. Merge | `Merge` and its conflict flow | Merge, conflict and resolve sequences match |
| 5. Views | `Graph`, `HistoryDoc`, `ReconcileDoc`, `ConflictDoc`, `Logo` | Plain-text equivalents |

Scope of the first release is full parity with hgit 1.8.9, not a subset.

### Types

| HolyC | Go |
|---|---|
| `I64`, `U8`, `U16`, `U32`, `U64`, `Bool` | `int64`, `byte`, `uint16`, `uint32`, `uint64`, `bool` |
| `U8 *` buffers, `MAlloc`/`Free` | `[]byte` |
| `PutU64LE` / `GetU64LE` | `encoding/binary`, little-endian |
| NUL-terminated strings | `string` / `[]byte` |
| unkeyed BLAKE2b-512 (RFC 7693) | `golang.org/x/crypto/blake2b` (the only external dependency) |
| `FALSE` returns | `(value, error)` |

### What replaces the TempleOS-specific parts

- **Paths.** `C:/Home/x.hgs` becomes a normal OS path via `filepath`. The
  two-file repo layout (`<repo>.hgs` + `<repo>.hgs.m`) is unchanged: it is the
  format.
- **Output.** hgit-on-TempleOS prints machine tokens (`DISPATCH_OK offer`,
  `STATUS_MODIFIED file`, ...). That token stream is the parity contract. The
  CLI has a human mode (default: normal formatting, colour) and a hidden
  `--serial` mode that prints exactly the HolyC token stream for the
  conformance tests.
- **DolDoc views** (`historydoc`, `reconciledoc`, `conflictdoc`, `graph`) are
  TempleOS's rich-text format. They become plain-text/ANSI output carrying the
  same information; the rendering is not reproduced. They are the natural seed
  for a TUI.
- **`Hgit("...")` string dispatch** becomes real subcommands with flags and
  `--help`. Command names and semantics are identical.

## Testing

`expected.log` is a single output stream from one scenario; each command ran
against the repo state at that moment. Fixtures hold only the final state. So
parity is checked three ways:

| Pillar | Proves | How |
|---|---|---|
| A. Cross-read | Native reads what TempleOS wrote | Open every fixture; `check`, `history`, `see`; re-serialize and require byte identity |
| B. Scenario replay | Native behaves like TempleOS | Re-run the full-regression scenario from scratch with `--serial`; diff the token stream against `expected.log` after normalising timestamps and hashes |
| C. Reverse interop | TempleOS reads what native wrote | Push native-written repos into the TempleOS guest and run `check` / `history`. Needs QEMU (~10 min), so it is a manual script, not CI |

Unit tests underneath: RFC 7693 BLAKE2b vectors, Fossil delta vectors, hex and
varint round-trips.

**Known gap.** The TempleOS regression does not exercise `revert`,
`reconcile`, `path close` or `operation restore`. These are covered by unit
tests only (`revert`/`reconcile` share code with `correct`). Any divergence
found later is the trigger to extend the scenario.

**CI** runs Linux, macOS and Windows. Test loaders normalise line endings when
reading `expected.log` (git `core.autocrlf` can rewrite it to CRLF in a
Windows checkout); `.hgs` files contain NUL bytes and are treated as binary.

**Done means:** A and B pass on all three OSes, C passes on at least one
manual run, and every command in `help` is covered by A, B or a unit test, with
any exception named in `docs/porting-notes.md`.

## Error handling

- The library returns typed, wrapped errors; the CLI maps them to exit codes.
  `--serial` prints the same `*_ERR` tokens as TempleOS, including the
  too-new-format message (`unsupported_format_version=9 (this build reads up to
  4)`), exercised by the `TFConfNewer` fixture.
- Readers never panic on bad input. Every declared length is checked against
  the remaining bytes before allocation. Go native fuzzing runs against each
  parser (archive, meta, delta, commit) with the invariant "error, never panic".
- Writes are atomic (temp file + rename). A repo is two files, so the object
  file is written first (adding objects is harmless) and metadata second;
  `check` detects a mismatch after a crash. This is an implementation choice,
  not a format change.
- No locking in v1. An exclusive lockfile (`<repo>.hgs.lock`) is added when
  concurrent front-ends (for example a TUI and a CLI on one repo) matter.

## Open decisions

1. **Timestamp epoch.** The commit timestamp is a `U64` in seconds and the
   format leaves its epoch undecided; the fixtures hold TempleOS-clock values.
   If the native tool wrote Unix seconds, history order would break across
   implementations. Plan: treat it as an opaque number and settle the epoch in
   an ADR before the native tool writes any commit. This is the one open item
   that can affect the format.
2. **View parity.** Only the information in the DolDoc views is ported, not
   their rendering, unless a stronger requirement appears.
3. **Native file handling.** Line endings and permissions when `offer` reads
   real working files follow the existing attributes/modes feature exactly.

## Roadmap beyond v1

TUI and GUI front-ends over `pkg/hgit`; optional Git import/export or remotes.
Neither is in scope for the first release.
