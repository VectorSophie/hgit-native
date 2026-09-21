# Porting notes

Deviations from the HolyC original, known gaps, and findings made while
porting. Newest first; entries are dated. Starts with what is known before any
Go is written.

## 2026-09-21: ignore and attrs matchers (task 9)

- **`*` never meets `/`**: matching is on the basename (name patterns) so the
  question does not arise; glob is the HolyC one (`*` = any run, nothing else
  special).
- **Dir pattern needs `isDir`** (deviation): the HolyC compared names with no
  dir/file distinction. `Ignored("build", false)` is false here (a file named
  `build` is not a directory). A dir pattern also matches any ancestor
  component, so files under `build/` report ignored (HolyC relied on the caller
  not descending). `Mode` has no `isDir`, so attrs dir patterns match a
  same-named file as in the HolyC.
- **No trimming**: only one trailing `\r` is removed. `*.tmp ` (trailing space)
  is the literal pattern `*.tmp `. In attrs, `*.png binary ` has token
  `binary ` (unknown) and the rule is dropped.
- **Unsupported lines are skipped silently** (slash-containing non-`/`,`/*`
  patterns, 256+ byte patterns, empty after `!`); the HolyC also printed
  `IGNORE_UNSUPPORTED_LINE`/`ATTR_UNSUPPORTED*`; packages never print, so the
  caller-facing diagnostics are not produced here yet.
- **`Mode` returns rule-derived bits only**; `explicit` means a rule set
  text/binary. Caller ORs `ModeBinary` when `!explicit && DetectBinary`.
  Attrs have no negation; `!` is a literal pattern character.
- Attrs reuse `ignore.ParsePattern`/`Pattern.Match` (exported for that).

## 2026-09-21: check (task 8)

- **Roots follow Check.HC, not the brief.** Roots are every declared path's
  HEAD (plus main) and every META_TAG_CONFLICT object of a path with a merge
  state. The in-progress merge's ours/theirs heads and the resolution hash are
  not roots in the HolyC (they are HEADs / reachable through the conflict
  object anyway), so they are not here either.
- **Report carries extra fields** (`Broken []BrokenRef`, `HeaderCount`) so
  `CHECK_BROKEN_REF <kind> <hash>`, `CHECK_WARN object_count_mismatch` and
  `CHECK_FAIL` print exactly. `CHECK_ERR` for a newer format / unreadable file
  comes from the `repo.Open` error via `SerialCheck(rep, openErr)`.
- **Native-only tokens:** an undecodable commit or tree object is reported as
  `commit_malformed` / `tree_malformed` broken refs (HolyC read garbage).
- **Reachability is by hash with an explicit stack**, so duplicate records of
  one hash are all reachable (HolyC's coalescing pass) and a forged cyclic
  graph terminates.
- **Scenario check:** all seven final-state fixtures (exported, imported, tree,
  merged, ignore, attrs, mergemode) matched their expected.log segments
  exactly, so no mid-scenario mismatch was found. CHECK_BEFORE/AFTER_UNDO, the
  conflict-merge and HARDEN broken-ref segments need mid-scenario state and
  are left to the replay task; the conflict-root and missing-conflict cases
  are covered by in-memory unit tests.

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
