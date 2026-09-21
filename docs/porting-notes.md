# Porting notes

Deviations from the HolyC original, known gaps, and findings made while
porting. Newest first; entries are dated. Starts with what is known before any
Go is written.

## 2026-09-21: repo, history, see (task 7)

- **Index is a map.** `Repo` indexes records with `map[Hash]int` (first
  occurrence wins, as the linear scan did); the unwired HolyC hash table is not
  ported.
- **History/See return errors, not text.** `ErrNoHead` (HISTORY_EMPTY),
  `ErrBrokenChain`, `*NotTypeError`; the serial tokens live in
  `internal/cli/serial.go`. `History` also stops on a parent cycle (reported as
  broken chain); the HolyC would loop forever. `See` guards nested-tree cycles
  the same way (reported as missing nested tree).
- **Entity ids print lowercase hex** (`U64ToHex`), 16 digits.
- **Final-state fixture.** `TFullRepo.hgs` includes the later `correct` commit,
  so history has 3 commits; the scenario's 2-commit `TFULL_HISTORY` segment is
  checked as its tail, and both SEE segments match exactly. Mid-scenario replay
  is left to pillar B.
- **Save is temp+rename, `.hgs` then `.m`,** and syncs `Header.Count`. If a
  Windows rename onto an open file fails, the error is returned, untried.
- **`SetHead` returns `ErrNameTooLong`** for names over 255 bytes (HolyC caps
  path names at 63 at creation). `meta.File.Set/Append` still wrap silently on
  names or payloads over 255 bytes; callers must check first.
- **`HISTORY_ERR bad_object` is a native-only token**: emitted when a commit
  object fails to decode (the HolyC has no such state), with no `HISTORY_END`.
- **`Save` does not fsync** the temp file before renaming. A crash between the
  two renames can leave a new `.hgs` with an old `.m`; `check` detects it later.
- **Known limit:** `See`'s nested-tree walk re-walks a shared subtree once per
  reference, so a deliberately shared-DAG tree can blow up exponentially.

## 2026-09-21: known before the port starts

- **Regression coverage gap.** The TempleOS regression scenario
  (`contract/tests/full-regression.hc`) does not exercise `revert`,
  `reconcile`, `path close` or `operation restore`. In the port these are
  covered by unit tests only; `revert` and `reconcile` share code with
  `correct`. Any divergence found later is the trigger to extend the scenario.
- **`Fossil.HC` is a pure algorithm.** It implements Fossil's delta format and
  ports directly; it is not a TempleOS file layer.
- **DolDoc views are not reproduced.** `historydoc`, `reconciledoc`,
  `conflictdoc` and `graph` are ported as plain-text/ANSI output carrying the
  same information, not the same rendering.
- **Atomic writes are a native-side choice.** TempleOS wrote repo files
  directly; the port writes a temp file and renames. This does not change the
  format.
- **Fixtures are final-state.** `expected.log` is one output stream from a
  single scenario, so command output is checked by re-running the scenario
  (pillar B), not by running each command against a fixture.
