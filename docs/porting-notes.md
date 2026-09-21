# Porting notes

Deviations from the HolyC original, known gaps, and findings made while
porting. Newest first; entries are dated. Starts with what is known before any
Go is written.

## 2026-09-22: init, working-directory listing and the flat offer (task 11b)

Ports `Init.HC` and `Offer.HC`'s flat path (`HgitOffer`,
`HgitOfferWithRelation`, `OfferFindFuzzyRename`) plus the `find_mask`
enumeration from `WorkDir.HC`. The recursive `offertree` path is not ported
yet; `offer.CarryEntityID`/`offer.FindFuzzyRename` are shared helpers it can
reuse per directory level.

- **Byte-exact blob parity achieved.** The regression's
  `FileWrite(name, <95-char literal>, 97)` stores 97 bytes: the literal, its
  terminating NUL and one byte past it, which the fixture's own blob records
  show is `'C'`. Replaying the first two offers with those exact bytes
  reproduces every blob hash in `TFullRepo.hgs` (`tests/offer_scenario_test.go`).
  The NUL makes that file binary, which is why both fixture commits carry a
  one-entry `OBJ_ATTRS` object - reproduced too.
- **Fuzzy-rename ceiling (deviation, deliberate).** `MaxFuzzyRenameBytes = 512`
  keeps files over 512 bytes out of fuzzy (edited-during-rename) detection, on
  either side of the comparison. This mirrors `Status.HC`'s fixed 512-byte
  candidate slots, but `Offer.HC`'s own flat path has **no** such ceiling: probe
  106 lifted every per-file cap there and passes the whole file to
  `OfferFindFuzzyRename`. So this is stricter than the offer HolyC, taken for
  parity with status and to bound an O(n*m*min(n,m)) scan; raising it is a
  one-line change with no format consequence. Exact-name and exact-content
  identity are unaffected at any size.
- **Tree entry order.** `Offer.HC` never sorts: entries land in `FilesFind`
  order. The fixture's trees are in lexicographic order by name, which is also
  what `os.ReadDir` returns, so `workdir.List` sorts by name and the entry
  order matches what TempleOS wrote.
- **`find_mask`.** Implemented as `workdir.Match`: only `*` (any run) and `?`
  (exactly one byte) are special, matched against the file name alone, case
  sensitive. `workdir.List` skips subdirectories; the HolyC's flat loop does
  not check `attr & 16` and would `FileRead` a directory entry, which no real
  mask exercises.
- **Object deduplication (pre-existing, visible here first).** `ObjectPut`
  appends unconditionally, so TempleOS stores a fresh copy of every unchanged
  file's blob on every offer - `TFullRepo.hgs` holds `never touched` three
  times and reports `objects=14` after two offers. `repo.Put` stores one copy
  per hash, so a natively produced repo has fewer records and a lower object
  count for the same history. Content and hashes are identical; only the record
  count differs. The later CLI task cannot expect `expected.log`'s
  `CHECK_OK objects=N` numbers from a natively built repo.
- **Removed HolyC buffer limits.** No `tree_content[2048]` entry cap (so
  `OFFER_SKIP tree_full` can never fire), no `ARCHIVE_SANITY_MAX` or computed
  archive capacity (so `OFFER_REFUSED archive_absurdly_large` can never fire),
  and no per-file size cap - confirmed absent from the current `Offer.HC` too,
  none was invented.
- **Length refusals instead of overflow.** The HolyC builds its commit in a
  512-byte stack buffer; a long message would overflow it. `Offer` refuses a
  message over 255 bytes (`ErrMessageTooLong`) and a name over 255 bytes
  (`ErrNameTooLong`, what a tree entry's U8 `name_len` can encode - unreachable
  on Linux, whose own `NAME_MAX` is 255). Nothing is ever truncated.
- **Nothing to offer is not an error.** An empty mask match records a commit
  with an empty tree, exactly as the HolyC does.
- **`Init`** uses `O_CREATE|O_EXCL` rather than `HgitInit`'s read-then-write
  check; same refusal, no race. It writes only the 16-byte header at format
  version 4 - the `.m` metadata file is created lazily by the first `Save`.
- **Entity ids** come from `crypto/rand` (`Tree.HC` uses two `RandU32`), never
  0, and `offer.NewEntityID` is replaceable for tests. If the system random
  source fails the offer is refused (`ErrEntityID`) rather than using a weak id.
- **Redo log.** `OpLogAppend` clears the current path's redo log; the port
  does the same, by draining `meta.TagRedoLog` records for that path.

Tokens the HolyC prints, and what the Go returns instead (`pkg/hgit/...` never
prints; the CLI task maps these back):

| HolyC output | Go |
| --- | --- |
| `DISPATCH_ERR init_failed <path>` | `repo.ErrExists` (or the OS error) from `repo.Init` |
| `DISPATCH_OK offer` | `Offer` returns the commit hash and a nil error |
| `OFFER_IGNORED <name>` | `Options.OnIgnored(name)` callback, one call per hidden file |
| `OFFER_SKIP tree_full <name>` | not reachable: no entry-count cap |
| `OFFER_REFUSED archive_absurdly_large size=<n>` | not reachable: no capacity accounting |
| (would overflow the commit buffer) | `offer.ErrMessageTooLong` |
| (unencodable name) | `offer.ErrNameTooLong` |
| (no equivalent) | `offer.ErrEntityID`, `repo.ErrNameTooLong` from `SetHead`, and any I/O error |

## 2026-09-22: fossil delta and similarity (task 11a)

- **Rename threshold**: Offer.HC/Status.HC/Diff.HC accept a candidate when
  `sim >= FOSSIL_RENAME_SIMILARITY_THRESHOLD` (50), best score wins. Exported
  as `fossil.RenameSimilarityThreshold`. Regression rename-with-edit
  (TFOrig -> TFRenamed, "jumps" -> "LEAPS") scores 73 on the 95 visible
  bytes (the HolyC passes 97 to FileWrite, i.e. the string's NUL plus one
  byte past it; that gives about 74, above the threshold either way).
- **Same algorithm, same numbers**: single longest common substring, scan by
  source offset then target offset, strictly longer wins; min copy length 4;
  similarity is `best*100/len(target)` with integer division, 0 for an empty
  target (an empty source scores 0 too, identical non-empty inputs 100).
  Cost is O(n*m*min(n,m)) worst case on repetitive content; the Go has no size ceiling and does not
  truncate, so very large files are slow rather than skipped.
- **Fuzzy-rename buffer ceiling**: the HolyC's caller (Status.HC lines ~172-181, 251) only buffers files of 512 bytes or less for fuzzy rename detection; the offer/status port will mirror that ceiling as a named constant.
- **Ceilings live in callers, not Fossil.HC**: the HolyC Fossil functions have
  none; Status.HC buffers fuzzy-rename candidates in 512-byte slots and gives a
  larger file content length 0 (similarity 0). The later offer/status ports
  must decide whether to mirror that; nothing here does.
- **Empty target (deviation)**: the HolyC maker emits `0\n0:0;`, which its own
  applier rejects (no segment loop runs, then `:` is read where `;` belongs).
  Go emits no segment for an empty literal: `0\n0;`, which round-trips.
  Non-empty deltas are byte-identical to the HolyC.
- **DeltaApply is stricter than the HolyC**: it errors instead of overrunning
  when a segment exceeds the declared length, a literal runs past the delta, or
  a copy leaves the source; integers that overflow int64 and checksums above
  32 bits are errors (HolyC masked). Declared output is rejected above 256 MiB
  or above `len(delta)*max(len(source),1)`, so a hostile delta cannot force a
  large allocation. Bytes after the final `;` are ignored, as in the HolyC.
- **PutInt of a negative number** returns an empty slice, as the HolyC does;
  callers pass lengths and offsets only.

## 2026-09-21: ignore and attrs matchers (task 9)

- **`*` never meets `/`**: matching is on the basename (name patterns) so the
  question does not arise; glob is the HolyC one (`*` = any run, nothing else
  special).
- **Ignore: dir pattern needs `isDir`** (deviation): the HolyC compared names
  with no dir/file distinction. `Ignored("build", false)` is false here. A dir
  pattern also matches any ancestor component so files under `build/` report
  ignored (the HolyC relied on the caller not descending).
- **Caller contract for ignore**: the caller knows `isDir`, never descends
  into an ignored directory, and checks tracking first. A negation that would
  re-include a file inside an ignored directory is never evaluated by the
  HolyC (no descent), and callers following the contract match that.
- **Attrs match the last component only, as in the HolyC**: a dir pattern
  (`assets/ binary`) is compared with the path's basename, dir/file agnostic,
  no subtree, never an ancestor (`x/assets/f.dat` gets nothing). Anchored
  `build/* binary` matches direct children of root `build` only.
- **Attrs line parsing verified against Attrs.HC**: split on the first space
  only (tabs are not separators); `sp>0 && sp<len-1`; tokens split on `,`,
  empty tokens skipped, unknown tokens ignored while known ones in the same
  rule still apply; a rule with no known token is dropped. HolyC truncates
  tokens to 31 bytes and StrCmp stops at an embedded NUL, so `text\0x` would
  match there; not mirrored.
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
