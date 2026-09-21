# hgit-native status

Snapshot as of 2026-09-21. **Nothing is implemented yet.** The design is
written and the contract is pinned; the next step is an implementation plan,
then the port itself.

## Done

- Repository created; Go module initialised (`go.mod`, Go 1.22).
- `contract/` submodule pinned to `contract-1.8.9` of the TempleOS hgit: format
  spec, ADRs and research, the HolyC source, and 23 golden-fixture files
  (repos and expected output produced on real TempleOS).
- Design written: [`ARCHITECTURE.md`](ARCHITECTURE.md).

## Port progress

Layers and gates are defined in [`ARCHITECTURE.md`](ARCHITECTURE.md#order).

| Layer | Status |
|---|---|
| 1. Bytes (`Blake2b`, `Hgs`, `Object`, `Archive`, `Hex`, `Canon`, `Fossil`) | not started |
| 2. Model (`Tree`, `Commit`, `Index`, `MergeBase`, `Conflict`, `Attrs`, `Ignore`, `Meta`) | not started |
| 3. Commands (`init`, `offer`, `status`, `history`, `see`, `diff`, `check`, ...) | not started |
| 4. Merge and the conflict lifecycle | not started |
| 5. Plain-text views (`graph`, `historydoc`, ...) | not started |
| Conformance tests (pillars A and B) | not started |
| Packaging (Homebrew, Chocolatey, deb) | not started |

## Open

- **Timestamp epoch.** The commit timestamp is a `U64` of seconds with an
  undecided epoch; to be settled in an ADR before the native tool writes any
  commit. See [`ARCHITECTURE.md`](ARCHITECTURE.md#open-decisions).
- Pillar C (reverse interop: TempleOS reads native-written repos) needs a
  QEMU-based script that does not exist yet.
