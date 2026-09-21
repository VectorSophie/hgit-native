# Porting notes

Deviations from the HolyC original, known gaps, and findings made while
porting. Newest first; entries are dated. Starts with what is known before any
Go is written.

## 2026-09-22: the recursive offertree and the relation wrappers (task 11c)

Ports `Offer.HC`'s `TreeBuildRecursive`, `HgitOfferTreeWithRelation` and
`HgitOfferTree` (ADR 0010), and the relation dispatch of `Hgit.HC`'s
`HgitOfferRelatedCmd`/`HgitOfferTreeRelatedCmd` (ADR 0005).

- **Byte-exact parity with `TFullTreeRepo.hgs`, whole file.** Replaying the
  regression's two `offertree` steps and its `correcttree` step with the clock
  frozen to the fixture's own three timestamps and `NewEntityID` seeded with
  the fixture's own three entity ids reproduces all fifteen records - every
  blob, both levels of tree, and all three commit hashes - in the same order
  (`tests/offertree_scenario_test.go`). The regression's `FileWrite` lengths
  there are the literal lengths exactly (11, 13, 21), with no trailing NUL,
  unlike the flat scenario's 97-byte writes. The ignore step (`TFIgnoreRepo`,
  `objects=4`) is replayed too: `.hgitignore` is itself an ordinary offered
  file and is tracked, which is what makes that count 4.
- **Object append order.** `TreeBuildRecursive` appends depth first in
  directory order: a subdirectory's blobs and nested trees, then that
  subdirectory's own tree object, then the parent's remaining files, then the
  root tree, then the attrs object if any, then the commit. That order is
  record order in the `.hgs`, so it is load-bearing for parity and is
  reproduced exactly.
- **Object count.** The flat path computes `rcount + entries + 2 + attrs`;
  `offertree` instead re-scans the finished archive with `IndexBuild`, which
  does **not** deduplicate - it indexes every record. Both therefore equal
  "number of records", which is what `repo.Save` already writes into the
  header, so no separate counting path is needed.
- **`TREE_LEVEL_MAX` (4096), `COMMIT_HEADROOM`, `ARCHIVE_HEADROOM`,
  `OBJECT_RECORD_OVERHEAD` and `ARCHIVE_SANITY_MAX` are not ported.** Every one
  of them exists only to size a `MAlloc`'d buffer or a fixed stack array up
  front. Go slices grow, so none of them changes observable behaviour: no entry
  is ever skipped for want of room, and `tree_content[2048]`'s
  `OFFER_SKIP tree_full` case cannot arise. The HolyC's per-level 2048/4096
  byte caps are a limit this port does not have.
- **Depth limit: `offer.MaxTreeDepth` (1024) is this port's own.** The HolyC
  has no depth limit at all - `TREE_LEVEL_MAX` is a byte cap, not a depth - so
  a pathological directory tree would recurse until the TempleOS stack gave
  out. Rather than overflow Go's stack, `OfferTree` returns `ErrTooDeep`. 1024
  sits above anything a real filesystem can hand back (Linux's `PATH_MAX` of
  4096 bytes cannot express more than about 2048 single-character levels).
- **Ignore matching is now the HolyC's exact `IsIgnored(name, rel_dir)` on
  both paths.** `IsIgnored` never sees a full path: a NAME pattern globs the
  candidate's basename, a DIR pattern is `StrCmp(pattern, name)` - the
  candidate's own last component, **files and directories alike** - and a
  DIR_CONTENTS pattern is `StrCmp(pattern, rel_dir)`. That is exactly
  `ignore.Pattern.MatchLast`, so `ignore.Rules.IgnoredLast` was added and both
  `Offer` and `OfferTree` use it. This closes the flat path's previous
  divergence, raised in 11b's review: `Rules.Ignored(name, false)` did not hide
  a plain *file* named exactly like a directory rule, where the HolyC does.
  `Rules.Ignored(relPath, isDir)` is kept unchanged for status and diff, which
  do match against whole paths.
- **A tracked name is never hidden, at directory level too.** The HolyC
  computes `name_was_tracked` from the parent tree before consulting the ignore
  rules, and that flag is deliberately independent of the type-mismatch reset
  below it: a directory whose old entry happened to be a *file* is still
  "tracked" and still descended into, even though no old identity is carried
  forward. Mirrored. A consequence worth stating: a *new* file inside an
  already-tracked directory that a later `SubA/` rule covers is **not** hidden,
  because that rule is matched against the file's own name, not its ancestor.
- **A directory with nothing trackable produces no entry.** Zero child entries
  means no tree object is stored and no entry is encoded - empty on disk, or
  every child ignored, or only empty children. Confirmed in `Offer.HC`
  (`if (GetU32LE(child_tree_content, 0) > 0)`), and the reason `check` never
  sees an empty tree record.
- **`.hgitignore`/`.hgitattributes` are read from the offered root only**, once
  per offer, and their patterns are matched against the path relative to that
  root (`rel_dir` grows as the recursion descends; the rule set never does).
  Both files are themselves ordinary offered files and end up tracked.
- **Attrs order.** One commit-level `OBJ_ATTRS` object lists every entity with
  a non-zero mode across all levels, appended in traversal order - a nested
  file's entry precedes a root file that sorts after its directory. That order
  is the object's bytes, so it is reproduced rather than sorted.
- **Relations are options, not new functions.** `HgitOfferRelatedCmd` and
  `HgitOfferTreeRelatedCmd` only parse three tokens and call the same offer
  with a tag, so `correct`/`revert`/`reconcile` are `offer.Offer` with
  `Options.Relation` set to `object.RelCorrects`/`RelReverts`/`RelReconciles`
  and `Options.RelationTarget` naming the commit, and the `...tree` variants
  are the same options passed to `offer.OfferTree`. No wrapper functions were
  added. Token-to-error mapping for the later CLI task:
  `DISPATCH_ERR bad_hash <hex>` is `archive.ParseHex`'s error, and
  `DISPATCH_ERR bad_entity_id <hex>` is the new `archive.ParseEntityID`'s.
  `ParseEntityID` takes exactly 16 hex digits (`Hex.HC`'s `HexToU64` reads a
  fixed 16 and refuses any non-digit; `HexDigit` accepts `A-F` as well as
  `a-f`), and `"0000000000000000"` is the valid "not entity-scoped" sentinel,
  not an error.
- **An unreadable file fails the offer, by name.** `workdir.Read` returns a
  `*workdir.ReadError` carrying the offered-root-relative path. The HolyC's
  `HgitFileRead` would return NULL and the loop would store a zero-length
  blob, silently committing nothing where a file exists; refusing is the
  deliberate deviation. A symlink to a directory lands here too: `os.ReadDir`
  reports a symlink by its own type, so the walk never follows one and never
  loops, and reading it fails by name instead of descending.
- **A file an ignore rule will drop is never opened.** The HolyC's recursive
  loop calls `HgitFileRead` *before* its ignore check and throws the bytes
  away; this port checks first. Observable only in that an unreadable ignored
  file no longer fails the offer.
- **`MaxMessageLen = 255`, restated precisely** (the 11b comment was
  imprecise). The format stores the message length in a **U32**, so the field
  is not the constraint. The HolyC's constraint is its fixed
  `U8 commit_content[512]`: fixed fields cost 64+1+8+4+1+1 = 79 bytes for a
  root offering with no relation and no attrs, and
  64+1+64+8+4+1+(64+8)+1+64 = 279 bytes in the worst case (one parent, a
  relation and an attrs object), leaving room for 433 and 233 message bytes
  respectively. This port refuses anything over 255 - one flat limit, the same
  U8 ceiling a tree entry's name has - rather than truncating. It is therefore
  slightly more permissive than the HolyC's worst case (a 234..255-byte message
  on a commit with a parent, a relation and attrs would have overflowed that
  buffer in TempleOS) and much stricter than its best case.
- **"Nothing is written unless the whole offer succeeds" was overstated** and
  the code comment has been corrected. Every *input* is validated before the
  first object is stored, but `repo.Save` writes the `.hgs` before the `.m`: a
  failure between them leaves the new objects on disk with HEAD not moved,
  which `check` reports as dangling. Recoverable, not atomic.

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
- **No fuzzy-rename size ceiling in `offer`, because the HolyC has none.**
  Probe 106 lifted every per-file cap on the flat path, and
  `HgitOfferWithRelation` hands the whole file to `OfferFindFuzzyRename`
  whatever its size. The only criterion is
  `fossil.RenameSimilarityThreshold` (50), best score wins, ties to the entry
  seen first. The fixed 512-byte candidate slots belong to **status and diff**,
  not to offer: `Status.HC` lines ~172-181 and ~251 buffer each new file in a
  512-byte slot and give a larger file content length 0 (similarity 0), and
  `Diff.HC` ~329 scores against those same slots. The status/statustree/diff
  port **must** mirror that ceiling as a named constant in its own package, or
  it will report renames the HolyC does not. Offer must not.
- **Cost of the unbounded scan.** `fossil`'s longest-common-substring search is
  O(n*m*min(n,m)) worst case, and offer runs it once per (parent-tree blob x
  new file that matched neither by name nor by hash) pair - unbounded, exactly
  as the HolyC. Large files with an edited rename are slow rather than skipped.
  If that ever matters, the fix is a better matcher, not a size cap.
- **Tree entry order.** `Offer.HC` never sorts: entries land in `FilesFind`
  order. The fixture's trees are in lexicographic order by name, which is also
  what `os.ReadDir` returns, so `workdir.List` sorts by name and the entry
  order matches what TempleOS wrote.
- **`find_mask`.** Implemented as `workdir.Match`: only `*` (any run) and `?`
  (exactly one byte) are special, matched against the file name alone, case
  sensitive. `workdir.List` skips subdirectories; the HolyC's flat loop does
  not check `attr & 16` and would `FileRead` a directory entry, which no real
  mask exercises.
- **Duplicate object records are kept, as `ObjectPut` keeps them.**
  `ObjectPut` is a plain append with no content-hash dedup, so an offer stores
  a fresh record for every matched file even when the bytes are unchanged, and
  for the attrs object even when it is identical to the parent commit's.
  Decoding `TFullRepo.hgs` shows exactly that: after two offers it holds 14
  records, of which `never touched` (`e523b977...`) and the one-entry attrs
  object (`2004939e...`) each appear twice, and `check` reports
  `CHECK_OK objects=14`. `repo.Append` mirrors that (always appends, bumps the
  header count, leaves the hash index pointing at the first occurrence) and is
  what `offer` uses for blobs, the tree, the attrs object and the commit.
  `repo.Put` remains as the deduplicating variant - after this change nothing
  on the write path uses it, only tests - and is documented as unusable
  wherever the HolyC would have appended.
- **`check` and duplicates.** `Check.HC` counts every record in `objects=`,
  but its reachability pass coalesces duplicates (a record whose hash matches
  a reachable one is reachable) and still prints one `CHECK_DANGLING` line per
  unreachable record. `check.Run` marks reachability by hash and walks the
  record list for dangling, which gives the same answers; a unit test now
  pins that.
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

- **Scenario-comparison finding.** The tests' `normalize` helper masked
  `ts=\d+` unanchored, so `objec`**`ts=14`** was rewritten too and every
  `CHECK_OK objects=N` comparison silently passed. Anchored to `\bts=`. That
  exposed one real, expected mismatch: `TFullMergeRepo.hgs` is saved at the
  scenario's end, five objects past its `TFULL_CHECK_MERGED` segment, so only
  that entry's count is masked, explicitly and with a reason.

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
- **Fuzzy-rename buffer ceiling**: the HolyC's caller (Status.HC lines ~172-181, 251) only buffers files of 512 bytes or less for fuzzy rename detection; the status/diff port must mirror that ceiling as a named constant. Narrowed by the 11b entry above: `Offer.HC` has no such ceiling, so `offer` must not either.
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
