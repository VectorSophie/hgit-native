# Porting notes

Deviations from the HolyC original, known gaps, and findings made while
porting. Newest first; entries are dated. Starts with what is known before any
Go is written.

## 2026-09-23: merge objects written in walk order, and only when kept

The walk no longer appends to the repository. Every object `MergeTreesRecursive`
would `ObjectPut` - a merged subtree (line 534) or an unresolved conflict's
evidence (lines 620 and 745) - is queued in ONE list, in the order found, and
written in that order only by `persist` (conflicts) or `finish` (success,
subtrees before the root tree, attrs and commit, as `MergeFinishSuccess`
follows the walk's puts). Two bugs are closed by this:
- **Archive order**: conflicts used to be appended after the walk, so a
  conflict found before a clean sibling subtree (`a.txt` before `zsub/`) was
  written after it. The `.hgs` bytes and `check`'s `CHECK_DANGLING` order now
  follow walk order, as TempleOS writes them. Pinned by
  `TestConflictAndCleanSubtreeAreWrittenInWalkOrder`.
- **Stray in-memory records**: a rename/rename refusal, and `Continue`'s
  `ErrStillConflicted`, used to leave the walk's subtrees in the caller's
  `*repo.Repo`, where a later `Save` would have persisted them. The queue is
  now dropped instead, matching the HolyC freeing its scratch archive.
  `TestNestedAmbiguousRenameRefusesTheWholeMerge` checks the record count and
  header count on the same in-memory repository.

`fullName` gains a comment: only a prefix of 255+ bytes makes it cut one byte
more than the HolyC, whose own 256-byte buffer already overflows there.

## 2026-09-22: nested trees and rename-aware merge (task 14d)

Completes `Merge.HC`'s `MergeTreesRecursive` in `pkg/hgit/merge`: the tree
branch (probe 100) and the per-level rename normalization
(`MergeNormalizeSide`/`MergeFindEntityInTree`, ADR 0017). The walk is now a
`walker` whose `level` calls itself one directory deeper; `Merge` and
`Continue` share it exactly as before. This supersedes two 14b/14c entries
below: `ErrNestedTree`/`MERGE_ERR nested_tree` is gone (the HolyC refuses no
nested case, so nothing remains in its scope), and `TFULL_CONFLICT_MERGE` is
now replayed with its rename and compared in full, object count included.

- **Normalization, ported condition by condition.** Per level, ours then
  theirs, each against the OTHER side's un-normalized tree. A BLOB entry is
  renamed back to its base name when: its own name is absent from the base;
  exactly one base blob and exactly one blob on that side carry its entity
  id; that side has nothing at the base name; and the other side either
  still has the base name (any type) or carries the entity exactly once under
  this same new name (the same rename on both sides - both sides normalize,
  one mapping is kept, first wins). If the other side carries it exactly once
  under a DIFFERENT name, that is rename/rename: `MERGE_RENAME_RENAME <base
  name>` is noted, the entry is left as is, and the walk goes on. Any other
  shape (other side deleted it, several carriers) normalizes nothing and
  refuses nothing: rename-vs-delete keeps the renamed file, as ADR 0017 says.
  Subtrees are never rename-normalized. The merged entry is written under the
  new name, and every notice/conflict path uses it (`r2.txt`); the
  `MERGE_AUTO renamed` notice itself prints prefix + base name `->` bare new
  name, exactly as the HolyC format string does.
- **Refusal happens after the whole walk, before anything is written.** The
  HolyC sets the global `merge_rename_refused` and keeps merging - building
  subtrees and conflict evidence in its scratch archive - then checks the flag
  right after `MergeTreesRecursive` returns and before the conflict branch's
  `FileWrite`, freeing the scratch archive. So nothing reaches disk: no
  objects, no merge state, HEAD unchanged. The port does the same wasted work
  but queues its objects instead of appending them (see the 2026-09-23
  entry), so a refusal leaves the caller's in-memory `*repo.Repo` untouched
  too. `Continue` does not consult the flag, as `HgitMergeContinue` does not.
- **Notice order is the HolyC's print order.** `MERGE_RENAME_RENAME` lines
  are returned in `Result.Autos` (`AutoRenameRefused`) alongside
  `AutoRenamed`/`AutoTookTheirs`/`AutoDeleted`, in walk order, and
  `cli.SerialMerge` prints them before `MERGE_REFUSED
  ambiguous_rename_rename - ...`, as the HolyC prints them before its refusal.
- **Wasted-work tradeoff ported, not avoided**: a clean subtree is appended
  the moment its recursion succeeds, so when a sibling conflicts the saved
  archive holds that unreferenced subtree (reported dangling by `check`), per
  `Merge.HC`'s header. `merge continue` re-walks and appends it again (no
  dedup, as `ObjectPut`). Pinned by
  `TestCleanSiblingSubtreeIsWrittenEvenWhenAnotherConflicts`.
- **Finding, ported as is: a directory deleted on one side leaves an EMPTY
  subtree in the merge.** Only the base and the other side have it, all agree
  it is a tree, so it recurses; every entry inside is a clean deletion, the
  recursion succeeds, and the (empty) merged subtree is written and entered
  like any other. Pinned by `TestDeletedSubdirectoryLeavesAnEmptySubtree`.
  This is inconsistent with `offer`/`offertree`, which never track an empty
  directory (`buildTree` skips a subtree with no entries): `merge` can now
  produce a tree that `offertree` cannot, so the next `offertree` of the same
  working directory drops that entry again, and history shows a tree change
  nobody made. Faithful to the HolyC, so the behavior is kept.
- **Hazard, ported as is and pinned: two entries with one name.** Ours
  renames `r.txt` to `r2.txt` while theirs keeps `r.txt` and separately adds
  an unrelated `r2.txt`. Ours normalizes (theirs still has the base name), the
  `r.txt` decision is written under `r2.txt`, and theirs' own `r2.txt` is
  taken too, so the merged tree holds two entries named `r2.txt` (Merge.HC
  lines 267-285 and 430: nothing checks the new name against the other side).
  `TestRenameOntoTheOtherSidesNewNameGivesTwoEntries` pins it.
- **Deviation, from undefined behavior**: a subtree's entity id is ours',
  else theirs'. When NEITHER has it (deleted on both sides) the HolyC reads an
  uninitialized `theirs_entity_id`; the port writes 0.
- **Paths**: `full_name` is prefix + merged name, the name cut so the whole
  stays within 254 bytes; a subtree's prefix is that plus `/`. Conflict paths
  and `took-theirs`/`deleted` notices are full paths at every depth.

## 2026-09-22: the conflict lifecycle (task 14c)

Ports `Merge.HC`'s `HgitConflicts`, `HgitResolve`, `HgitMergeContinue` and
`HgitMergeAbort` into `pkg/hgit/merge/lifecycle.go`: `Conflicts`, `Resolve`,
`Continue` and `Abort`. The three-way walk task 14b wrote is now shared by
`Merge` and `Continue` (`walk`), as is the commit tail (`finish`,
`MergeFinishSuccess`), with a resolution table threaded through exactly as
`MergeTreesRecursive`'s own `resolution_conflict_hashes`/`resolution_hashes`
arguments thread it there.

- **What `resolution_hash` stores**: the CHOSEN SIDE's own content hash, taken
  straight out of the `OBJ_CONFLICT` evidence (`hash[side*64..]` in
  `HgitResolve`), i.e. the blob (or tree) hash that side's tree entry pointed
  at - not a hash of the conflict, not a new object. When the chosen side is
  ABSENT, all 64 bytes stay zero, and that zero IS the resolution: "resolve to
  deletion". `Continue` omits the entry entirely for a zero resolution, takes
  ours' entry whole when ours is present and its content hash equals the
  resolution, and takes theirs' entry whole otherwise - the same three-way
  branch `MergeTreesRecursive` uses. The resolved side is taken with its OWN
  mode, not the three-way winning mode the conflict made meaningless, and a
  `KindType` resolution writes no attrs entry at all.
- **`resolve` marks only the record.** No object is written and no tree is
  built; the merged tree is rebuilt from scratch at `merge continue` time.
  Bounds are checked, a bad selector is refused, and re-resolving is ALLOWED -
  the HolyC has no already-resolved check, and `MetaConflictSetResolved`
  deliberately rewrites the record IN PLACE so the index `conflicts` printed
  stays stable. Ported as-is, pinned by `TestResolveAgainOverwritesInPlace`.
- **Error precedence in `resolve`** is the HolyC's own order: no merge in
  progress, then index out of range, then missing object, then malformed
  object, and only then the selector check - so `resolve <bad-index> nonsense`
  reports the index, not the selector.
- **`merge continue`** re-derives the base with `FindMergeBase` (the merge
  state deliberately does not store it: the object graph only grows, so it is
  deterministic), rebuilds the FULL tree, writes the two-parent commit (ours
  then theirs), appends the oplog entry, clears the redo log, moves HEAD, and
  only then clears the in-progress state. The commit message carries the
  HolyC's v1.8.4 provenance suffix, `" [resolved <path>=ours|theirs|deleted]"`,
  including both of its length caps (150/170) and the 254-byte message cap.
- **`merge abort` clears the metadata and nothing else**, confirmed against
  the HolyC directly: `HgitMergeAbort` is four lines - the in-progress check,
  `MetaMergeStateClear`, and the notice. It never touches HEAD, the oplog, the
  archive or the working directory, because a conflicting merge never touched
  them either; that is exactly why "restores the EXACT pre-merge state" needs
  no restoration logic. `MetaMergeStateClear` itself clears BOTH the
  merge-state record and every conflict record for that path, in that order,
  and is the same call `merge continue` uses on success.
- **`OBJ_CONFLICT` objects are never deleted** by either ending, so `check`
  reports them dangling afterwards - the ADR's own accepted tradeoff, asserted
  in both the unit tests and the fixture replays.
- **`check` needs no new root for resolutions.** A resolution hash is always
  one of the conflict object's own present sides, and `CheckMarkReachable`
  already follows those, so marking the conflict hash alone (which `Check.HC`
  does, and task 8 ported) keeps a resolved conflict's content reachable.
  `TestConflictRoot` now covers a record with a real resolution hash.
- **`STATUS_MERGE_IN_PROGRESS` lives in `internal/cli`**, as
  `SerialMergeBanner`, not in `pkg/hgit/status`. The line reports repository
  metadata (the merge state and conflict records), not the working-directory
  comparison that package exists to do, and `HgitStatus`/`HgitStatusTree`
  print it from a block that is purely a preamble to their own work. Keeping
  it in the command layer leaves both status packages and their task 12a tests
  untouched, and `status` and `statustree` compose it the same way.
- **New tokens**: `CONFLICTS_NONE`, `CONFLICTS_COUNT`/`CONFLICT` and its side
  lines, `RESOLVE_OK`, `RESOLVE_ERR no_merge_in_progress` /
  `conflict_not_found <i>` / `conflict_object_missing` /
  `conflict_object_malformed` / `unknown_selector`, `MERGE_ERR
  no_merge_in_progress`, `MERGE_ERR conflict_unresolved <i>`, `MERGE_ERR
  conflicts_still_unresolved count=N`, `MERGE_ERR
  internal_still_conflicted_after_resolution`, `MERGE_ABORT_OK`,
  `STATUS_MERGE_IN_PROGRESS`. Native-only: `CONFLICTS_ERR bad_record` (a
  conflict record of the wrong length, which the HolyC reads by fixed offset
  instead).
- **That `bad_record` deviation has a second consequence, on `status`.**
  Reading the conflict records is all-or-nothing here, so one wrong-length
  record makes `conflicts` report `CONFLICTS_ERR bad_record` AND makes
  `SerialMergeBanner` print nothing at all - no `STATUS_MERGE_IN_PROGRESS`
  line, although a merge really is in progress. `HgitStatus` and
  `HgitStatusTree` (Status.HC:89-99) count and read those records by fixed
  offset, so they would still print the banner, with whatever the corrupt
  bytes imply. The native behaviour is the more cautious of the two - it never
  prints a count derived from bytes it could not parse - but it is a real
  difference; `check` remains the command that names the damage, and `merge
  abort` still clears it.
- **Deviations**: (1) `MERGE_ERR internal_still_conflicted_after_resolution`
  omits the HolyC's
  `count=` suffix. (2) Metadata and archive are saved once, at the end,
  instead of the HolyC's several `FileWrite`s - the same single-`Save`
  convention every earlier task uses. (3) `TFULL_CONFLICT_MERGE` is replayed
  without its rename (ADR 0017 rename-aware merge is not ported), so its four
  `MERGE_AUTO renamed`/`took-theirs r2.txt` lines and the `CHECK_OK
  objects=15` count they inflate are excluded from that one comparison; every
  conflict-lifecycle line of the segment is compared exactly, and the three
  base/ours/theirs evidence hashes are compared UNMASKED against the golden
  log's own bytes (the segment comparison normalizes 16-hex tokens, so it
  alone would not catch a wrong hash) - they match.
  `TFULL_CONFLICT_ABORT` is replayed in full except its leading `DISPATCH_OK`
  lines and its `CONFLICTDOC_OK` line (`conflictdoc`, v1.8.6, is not ported).
  `TFULL_HARDEN` is replayed in full, including its object count.

## 2026-09-22: flat three-way merge (task 14b)

Ports `Merge.HC`'s `HgitMerge` - the trivial cases, the flat three-way merge
of content and of mode, and ADR 0016 conflict persistence - into
`pkg/hgit/merge`: `Merge(r *repo.Repo, otherPath string) (Result, error)`.
"Ours" is the current path, read from the repository, exactly as `HgitMerge`
does, so there is no `oursPath` argument. Not ported here: recursion into
nested trees (`MergeTreesRecursive`'s tree branch), rename normalization
(`MergeNormalizeSide`), and the lifecycle commands (`conflicts`, `resolve`,
`merge continue`, `merge abort`), all later sub-pieces.

- **Ported exactly**: the decision table (identical or both absent -> ours;
  unchanged on theirs -> ours; unchanged on ours -> theirs; otherwise a
  conflict), with an absent entry treated as a value like any other. That is
  what makes **edit-vs-delete a real content conflict** (the deleting side is
  recorded `present=false` in the `OBJ_CONFLICT` evidence), delete-vs-
  unchanged a clean deletion, delete-on-both a clean deletion, and
  **add-vs-add with different content a real conflict** (reachable at the flat
  level: the base is simply absent on all three sides' evidence).
- **Mode is a separate three-way decision**, made for every surviving entity
  regardless of the content branch, and `Conflict.Kind` is a bitmask, so one
  entity can carry `KindContent|KindMode` at once - as in the HolyC.
- **Documented HolyC limitation, ported and pinned by a test, not fixed**: a
  mode-only change on the side a file is DELETED from is not flagged. The
  content decision alone decides the delete, and the deleting side contributes
  mode 0, so ours==base on mode and "theirs" wins a mode nothing will use.
  `TestModeChangeOnTheDeletedSideIsNotAConflict` asserts exactly that,
  deliberately. (The narrower corner where three distinct modes meet - base
  non-zero, one side deleting, the other setting a third mode - does still
  raise a mode conflict on a deleted entry, because the HolyC's mode
  comparison runs unconditionally; that too is ported verbatim.)
- **Kind mismatch (`OBJ_TREE` on one side, `OBJ_BLOB` on another)**: ported as
  the real `KindType` conflict, with mode 0 on every side, as `Merge.HC` does.
  Finding: a flat offer never writes `OBJ_TREE` entries, so this branch is
  unreachable through the flat commands alone; it becomes reachable only once
  `offertree` commits are merged, which is why it is ported now rather than
  deferred.
- **Deviation: a subtree that every side agrees is a tree is refused**
  (`ErrNestedTree`, `MERGE_ERR nested_tree`) instead of being recursed into.
  The HolyC recurses; this port does not implement that yet and refuses rather
  than merging a tree by its hash alone, which would silently drop one side's
  nested edits. Native-only output with no HolyC counterpart; it disappears
  when the recursive merge lands.
- **Deviation: typed errors instead of printed tokens.** `pkg/hgit` never
  prints, so every `MERGE_*` token lives in `cli.SerialMerge`, and the
  `MERGE_AUTO took-theirs`/`deleted` notices the HolyC prints mid-walk are
  returned as `Result.Autos` (taking ours is silent there, so it has no
  `Auto` here). `MERGE_ERR bad_header`/`not_a_repository` belong to opening
  the archive, which the caller does, as with `check`.
- **Conflict persistence**: on any conflict no merge commit is created and
  HEAD does not move, but every `OBJ_CONFLICT` object is appended (via
  `Append`, matching `ObjectPut`'s unconditional semantics and therefore the
  object counts TempleOS wrote), `META_TAG_MERGE_STATE` is written (ours +
  theirs + other path name; the base is deliberately not stored, being
  re-derivable) and one unresolved `META_TAG_CONFLICT` record per conflict is
  appended, then the repository is saved. `meta.MergeState` and
  `meta.ConflictRecord` carry those two payload shapes.
- **`Repo.AppendOp` fits both the merge commit and the fast-forward**:
  `OpLogAppend` is `MetaOpLogAppend` + `MetaRedoLogClear` on the current path,
  which is precisely what `AppendOp` does, so it is reused unchanged.
- **Fixture segments reproduced exactly**: `TFULL_MERGE`, `TFULL_CHECK_MERGED`
  (18 objects), `TFULL_MERGE_FF`, `TFULL_MERGEMODE_MERGE`,
  `TFULL_MERGEMODE_CHECK` (16 objects), replayed natively in
  `tests/merge_scenario_test.go`. `TFULL_CONFLICT_MERGE` and
  `TFULL_CONFLICT_ABORT` are deferred: they drive `conflicts`, `resolve`,
  `merge continue` and `merge abort`, and the first also exercises the
  rename-aware merge - none of which exist yet.
- **Finding worth recording**: the merge repository's fixture shows an
  `OBJ_ATTRS` object per offer, because the regression's `FileWrite` lengths
  are one byte past each literal, so those files end in a NUL and are binary
  by detection. Replaying with the literal alone produces 14 objects, not 18.

## 2026-09-22: merge-base (task 14a)

Ports `MergeBase.HC` (the FIXED, post-probe-109 version) into
`pkg/hgit/mergebase`: `FindMergeBase(r *repo.Repo, a, b archive.Hash) (archive.Hash, bool, error)`.
Does not port `Merge.HC` (the merge command itself); that is a later
sub-piece.

- **Algorithm ported exactly**: `collectAllAncestors` builds a's complete
  ancestor set via BFS over every parent edge (not just `parent[0]`),
  mirroring `CollectAllAncestors`. `FindMergeBase` then does a breadth-first
  walk from `b`, level by level over every one of `b`'s parents, returning
  the first node found in a's ancestor set - the closest-to-`b` common
  ancestor, exactly as `MergeBase.HC` documents it (not Git's exact
  "recursive merge-base"). Both commits count as their own ancestor: `a==b`,
  and either being an ancestor of the other (fast-forward), fall out of the
  same walk with no special-casing needed, matching the HolyC.
- **No common ancestor**: `MergeBase.HC`'s `FindMergeBase` returns `Bool
  found` (`FALSE` if the BFS from `b` exhausts without matching `a`'s
  ancestor set). Ported as `(archive.Hash, false, nil)` - `found=false` with
  a nil error, since a disconnected history is not a malformed graph, just
  an absent answer.
- **Criss-cross**: `MergeBase.HC`'s own comments (and probe 109's README)
  say explicitly that genuine criss-cross ambiguity (multiple valid lowest
  common ancestors, none descending from the other) is a real, separate,
  deliberately out-of-scope limitation - the HolyC does not attempt to
  disambiguate, it just returns whichever common ancestor its
  breadth-first-from-`b` walk reaches first. Ported unchanged: no
  disambiguation added. `TestCrissCross` pins the deterministic (but
  arbitrary, parent-order-dependent) choice this produces for one
  constructed scenario, not a "correct" answer.
- **Deviation: malformed graph now returns an error instead of silently
  degrading.** `MergeBase.HC`'s C-style `IndexLookup` failure on an
  unresolvable parent hash is simply not entered (the `if` guarding it is
  skipped), so that node silently stops expanding, as if it had no
  parents - the search can still terminate and even still succeed, just
  based on an incomplete ancestor set, with no signal that anything was
  wrong. That is a real risk of a silently-wrong answer feeding straight
  into a real three-way merge. This port instead returns the error from
  `repo.Repo.Commit` (`ErrNotFound`/`*NotTypeError`) up through
  `FindMergeBase` as soon as a parent hash fails to resolve to a real
  commit, rather than pretending the graph ends there. `TestMalformedGraph`
  pins this. No other observable behaviour changes: a well-formed graph
  (the only kind the real archive ever produces) behaves identically either
  way.
- Cycle safety is not a special case: both BFS passes use a visited-set
  membership check before enqueueing, the same technique
  `HashSetAdd`/`HashSetContains` already used in the HolyC, so a cyclic or
  diamond-shaped graph can never be re-expanded or looped on; no separate
  depth/step limit was needed or added.
- No scenario test drawn from `contract/tests/full-regression.hc`: its own
  merge-base computation only happens inside `Hgit("merge ...")`, i.e.
  behind the merge command itself, which this sub-piece does not build.
  Once the merge command lands, its own tests can exercise this package
  end-to-end; for now the fixture-built unit tests above (linear chain, two
  branches from a root, fast-forward both directions, `a==b`, no common
  ancestor, probe 109's real merge-second-parent scenario, criss-cross,
  malformed graph, a long chain proving BFS termination) are the only
  coverage.

## 2026-09-22: named paths, undo/redo/operation log, export/import (task 13)

Ports `Paths.HC`, `OpLog.HC` and `Portable.HC`. They live in
`pkg/hgit/repo` as `ops.go`, not in a separate package: every one of these
functions is a read-modify-write of `Repo.Meta` plus `CurrentPath`/`Head`/
`SetHead`/`Save`, all of which `Repo` already owns, and `Paths.HC`'s read
side (`CurrentPathGet`/`CurrentHeadRead`) was already ported into `repo.go`.
A `pkg/hgit/ops` package would have had to either take `*repo.Repo` and
reach into its metadata anyway or force `Repo` to export more of its
internals; neither buys anything. The surface is
`(*Repo).PathNew/PathGo/PathList/PathClose/PathExists`,
`(*Repo).Undo/Redo/OperationHistory/OperationRestore/AppendOp` and the
package-level `Export`/`Import`.

- **`OpLogAppend`'s "log and clear the redo log" is now `(*Repo).AppendOp`**,
  shared with `offer`, which had its own inline copy. Behaviour is
  unchanged; `Redo` deliberately does not go through it, because re-applying
  an undone operation must not clear the redo log (the HolyC calls
  `MetaOpLogAppend` directly for exactly that reason).
- **`operation restore <index>` touches nothing but HEAD.** It reads the
  operation at `index` (0-based, oldest first, the numbering `operation
  history` prints) and writes that entry's `new` head. It does not pop, does
  not append a new operation, and does not clear or alter the redo log -
  `OpLogRestore`'s own note says reconciling an arbitrary restore with
  undo/redo afterwards is out of scope. Pinned by
  `TestOperationRestoreJumpsHeadAndLeavesBothLogsAlone`, which redoes across
  a restore and checks the oplog length on both sides.
- **Deviation: `PathNameFits`/`HGIT_MAX_SAFE_PATH_LEN` is not ported.** That
  33-character ceiling is a RedSea/`FileWrite` limit on the full path string
  (ADR 0003), not a property of hgit's own format, and `Paths.HC`'s current
  version already reduced it to a check on `repo_path + ".m"` alone - the
  path NAME no longer enters a filename at all. On a host filesystem the
  limit does not exist, and enforcing it would reject every ordinary
  absolute path. The name checks that remain are the HolyC's own
  `HGIT_MAX_PATH_NAME` (empty or >= 64 bytes is `ErrBadPathName`) and the
  metadata format's 255-byte record-name limit (`ErrNameTooLong`).
- **`path new` writes an all-zero HEAD record** when the current path has
  none, rather than leaving the new path headless - `MetaWriteHead` is
  called unconditionally, so `Head(name)` on such a path returns ok with an
  all-zero hash, exactly as `MetaReadHead` would. Mirrored deliberately
  (`TestPathNewOnHeadlessCurrentWritesZeroHead`).
- **`path close` leaves everything behind.** It removes only the
  `PATH_DECLARED` record; the closed path's HEAD and operation-log records
  stay in the metadata file, orphaned and unreachable (ADR 0012 decision 5).
  It refuses `main` and an undeclared name, and closing the current path
  switches current back to `main`.
- **`export` and `import` are one copy in two directions** (`HgitCopyRepo`),
  copying `<repo>` and `<repo>.m`. A missing source file is skipped
  silently, so a repo never touched beyond `init` copies its object file
  alone and still opens (`Meta` defaults to main/empty). The destination is
  overwritten if it exists, and a stale destination `.m` is left in place
  when the source has none - both are `CopyFileIfExists`'s own behaviour.
  Deviation: a real I/O error is returned rather than ignored, and the
  writes go through the same atomic temp-file-plus-rename `Save` uses.

Error tokens (`internal/cli/serial.go`). The path, undo/redo and operation
commands print their `DISPATCH_*` line inline in `Hgit.HC`'s own branch, so
unlike `SerialCheck`/`SerialHistory` these formatters include it - that is
what the fixture segments contain.

| sentinel | token |
| --- | --- |
| `ErrBadPathName`, `ErrPathExists`, `ErrNameTooLong` (from `PathNew`) | `DISPATCH_ERR path_new_failed <name>` |
| `ErrNoSuchPath` (from `PathGo`) | `DISPATCH_ERR path_go_failed <name>` |
| `ErrCloseMain`, `ErrNoSuchPath` (from `PathClose`) | `DISPATCH_ERR path_close_failed <name>` |
| `ErrNothingToUndo` | `DISPATCH_ERR nothing_to_undo` |
| `ErrNothingToRedo` | `DISPATCH_ERR nothing_to_redo` |
| `ErrBadOpIndex` | `DISPATCH_ERR operation_restore_failed <index>` |
| empty operation log | `OPLOG_EMPTY` |
| `meta.ErrMalformed` from a log entry | `OPLOG_ERR bad_entry` (native-only) |
| copy I/O failure | `DISPATCH_ERR export_failed` / `import_failed` (native-only) |

The HolyC collapses every `path new` failure reason into the one
`path_new_failed` token (its functions return `Bool`), and the port keeps
that: the distinct sentinels exist for callers, not for the serial log.

**Fixture parity: `TFULL_CHECK_AFTER_UNDO`, `TFULL_OPHISTORY`,
`TFULL_PATHLIST`, `TFULL_CHECK_EXPORTED` and `TFULL_CHECK_IMPORTED` all
match** (`tests/ops_scenario_test.go`, with only `normalize`'s masking),
including the exported repo's `objects=25` - the replay runs the regression
from `init` through both offers, undo, redo, the feature path, the
`correct` offering and the export/import pair.

## 2026-09-22: `diff` (task 12b)

Ports `Diff.HC` (`DiffResolveTreeByHash`, `DiffPrintTreeChanges`, `HgitDiff`):
one commit's tree against its first parent's, with the serial tokens in
`internal/cli/serial.go`'s `SerialDiff`.

- **It lives in `pkg/hgit/status` as `diff.go`, not a `pkg/hgit/diff`
  package.** `Diff.HC` is Status.HC's committed-trees counterpart down to the
  rename passes, and the port reuses `Change`, `newCand`/`delCand`,
  `matchRenames` and `find` unchanged. A separate package would have meant
  exporting all of that, or duplicating the two rename passes; neither buys
  anything. The entry point is `status.Diff(r, commit) ([]Change, error)`.
- **No 512-byte fuzzy-rename ceiling in `diff`, unlike `status`.**
  `Diff.HC`'s rename pass 2 resolves BOTH sides through `IndexLookup` +
  `rbuf` on demand and passes the full record length to
  `FossilSimilarityPercent`; there is no fixed-size buffer anywhere in the
  file, and its header says so explicitly ("no need to buffer full file
  content ahead of time the way `Status.HC`'s NEW-on-disk candidates
  require"). `MaxFuzzyRenameBytes` is therefore Status/StatusTree's alone.
  Pinned by `TestDiffFuzzyRenameHasNoSizeCeiling` (a 1760-byte pair that
  `status` would not have buffered still comes back RENAMED). The refactor
  that made this possible moves `fuzzyTarget` from inside `matchRenames` to
  the two Status/StatusTree call sites that buffer a candidate - behaviour
  for `status` is unchanged, and its own 511/512/513 tests still pass.
- **A broken nested tree reference is not an error and does not abort.**
  `DiffResolveTreeByHash` returns FALSE and `DiffPrintTreeChanges` recurses
  with that side NULL, i.e. as an empty tree, so the other side's entries are
  reported NEW/DELETED and the rest of the diff continues; broken references
  stay `check`'s job. `differ.tree` mirrors this (nil on missing, malformed
  or wrong-typed), tested by
  `TestDiffBrokenNestedTreeReferenceIsAnEmptyTree`. Only the COMMIT's own
  root tree is an error (`DIFF_ERR tree_not_found`), as in `HgitDiff`.
- **Deviation: `DIFF_ERR too_deep` and a `MaxTreeDepth` guard on the
  recursion.** `DiffPrintTreeChanges` has no depth limit at all. Tree hashes
  are content-addressed, so a cycle cannot arise from anything this port
  writes, but a hand-corrupted archive can name any hash it likes and
  TempleOS would recurse until it died. The guard is the same one
  `StatusTree`/`OfferTree` already use, and the token is native-only, in the
  same class as `SerialStatus`'s own `STATUS_ERR bad_object`.
- **Deviation: names are never truncated.** `DiffPrintTreeChanges` builds
  each path in a 256-byte buffer and clamps it at 254; Go paths are strings
  and are printed whole. Reaching the clamp needs a path over 254 bytes,
  which nothing in the fixtures or this port produces.
- **Deviation: a root tree that resolves but is not a tree object.**
  `HgitDiff` only does an `IndexLookup` and then reads the bytes as a tree;
  the Go port reports `DIFF_ERR tree_not_found` rather than decoding whatever
  happened to be there. Same for a parent commit whose record is not a commit
  (treated as "no old tree", so everything reads as new).
- **Fixture parity: `TFULL_DIFF` and `TFULL_ATTRS_DIFF` match byte for byte**
  (`tests/diff_scenario_test.go`, with only `normalize`'s masking).
  **`TFULL_MERGEMODE_DIFF` is deferred**: it diffs a real merge commit
  produced by `hgit merge`, and no merge command exists in this port yet. It
  is not faked - the merge-commit behaviour itself (first parent only, probe
  111) is covered by `TestDiffMergeCommitUsesFirstParentOnly`, which builds a
  genuine two-parent commit object directly; the segment should be replayed
  for real by whichever task ports merge.

## 2026-09-22: `status` and `statustree` (task 12a)

Ports `Status.HC`'s `HgitStatus` (flat) and `StatusTreeWalk`/`HgitStatusTree`
(recursive, ADR 0010) into `pkg/hgit/status`, with the exact serial tokens in
`internal/cli/serial.go`'s `SerialStatus`/`SerialStatusTree`. `diff`
(Diff.HC) is a later task; the `Change` type here is shaped for it to reuse.

- **Byte-exact parity with three of the fixture's segments.**
  `tests/status_scenario_test.go` replays the regression natively up to
  `TFULL_STATUS`, `TFULL_STATUSTREE` and `TFULL_ATTRS_STATUS` and compares the
  formatted output against `contract/fixtures/expected.log` with only
  `normalize`'s hash/entity/timestamp masking - no other slack. `TFULL_STATUS`
  exercises every classification at once (UNCHANGED, MODIFIED, a fuzzy RENAME
  with an edit, NEW, DELETED); `TFULL_STATUSTREE` proves statustree never
  prints STATUS_UNCHANGED; `TFULL_ATTRS_STATUS` proves STATUS_MODE_CHANGED
  fires independently of an unchanged content line. The `TFULL_STATUSTREE`
  segment carries a trailing `DISPATCH_OK statustree` line that belongs to the
  (not-yet-built) CLI dispatcher, not to `HgitStatusTree` itself, so the test
  strips it before comparing.
- **The ADR 0016 merge-in-progress check (`STATUS_MERGE_IN_PROGRESS`) is
  deliberately not ported here.** `Status.HC` prints it from
  `MetaMergeStateExists`/`MetaConflictCount`/`MetaConflictGetAt`, but this
  codebase has no writer for the per-conflict "resolved" flag those read
  (`meta.TagConflict`'s payload today is just a hash - see
  `pkg/hgit/check/check.go`'s own reader). Guessing that on-disk shape here,
  ahead of the merge task that actually defines it, would risk a format this
  port would then have to keep. `TFULL_CONFLICT_ABORT`'s status line (the only
  fixture segment that exercises this) is deferred to whichever task builds
  merge/resolve.
- **`MaxFuzzyRenameBytes = 512`, matching Status.HC's fixed 512-byte
  fuzzy-rename slot exactly.** A NEW-on-disk candidate is eligible for the
  fuzzy-rename pass only when its own content is at most 511 bytes
  (`fsize+1 <= 512`, the `+1` being the OBJ_BLOB type tag); at exactly 512
  bytes it is already over the line. Tested at 511/512/513
  (`TestFuzzyRenameSizeCeiling`). This ceiling is asymmetric: the DELETED
  side's content, read straight from the archive, has no such cap - only the
  disk-side candidate's buffer is size-limited, exactly as the HolyC's
  `new_contents[disk_count*512+512]` slot array only exists for that side.
  `pkg/hgit/offer`'s own fuzzy rename has no ceiling at all (probe 84's own
  finding, already documented there) - this is a real, deliberate parity
  choice with status/diff alone, not a general project rule.
- **Pass 2 (DELETED) walks the whole HEAD tree, not just what the mask
  matched.** `Status.HC`'s deletion pass iterates every entry of
  `tree_content` unconditionally, checking existence under `dir_prefix` -
  `find_mask` only gates pass 1 (NEW/MODIFIED/UNCHANGED). Ported as-is: `mask`
  narrows what `Status` classifies as NEW/MODIFIED/UNCHANGED, but every
  HEAD-tracked name gets checked for deletion regardless of the mask.
- **`statustree` never reports STATUS_UNCHANGED**, unlike flat `status` -
  `StatusTreeWalk`'s blob branch only ever calls `CommPrint` for
  `STATUS_MODIFIED`; an unchanged file at any nesting level produces nothing.
  Mode is still checked and reported independently either way.
- **A tracked name whose kind flips can print BOTH STATUS_TYPE_CHANGED and
  STATUS_DELETED for the same path - but only in ONE direction: a tracked
  FILE (a BLOB-typed old entry) replaced by a same-named directory.** Ported
  exactly as found, not smoothed over: `StatusTreeWalk`'s deletion pass only
  special-cases an old TREE-typed entry (the `if (ttype == OBJ_TREE)` branch,
  see the next bullet); an old BLOB-typed entry whose name is now a real
  directory falls into the `else` branch instead, which still does a plain
  `HgitFileRead`/`workdir.Read` on that name - which fails, because it is now
  a directory - so the name is reported DELETED there too, on top of the
  TYPE_CHANGED the forward pass already reported. The other direction (a
  tracked DIRECTORY replaced by a file) does NOT double-report - see below
  for why. Verified directly against the source, not assumed; the
  file-becomes-directory (double-report) case is `TestStatusTreeTypeChanged`,
  the directory-becomes-file (single TYPE_CHANGED only) case is
  `TestStatusTreeTrackedDirectoryReplacedByFile`.
- **The old-TREE-typed side of that same deletion pass reproduces a real
  `FilesFind` glob quirk, on purpose, because "correcting" it broke on real
  input.** `Status.HC` decides whether an old TREE-typed entry's directory is
  "still real" with `StrPrint(dmask, "%s*", check_path);
  FilesFind(dmask, 0) != NULL` - `check_path` is `dir_path + tname` with NO
  trailing slash, so this is a PREFIX glob against the *parent* directory's
  own entries, not an exact-name or is-a-directory test. Two real
  consequences, both now ported exactly (`hasPrefixSibling` in
  `pkg/hgit/status/status.go`, called from `statustree.go`'s deletion pass
  against the same disk listing the forward pass already made for this
  level):
  1. A same-named plain file satisfies the glob trivially (zero extra
     characters), so a directory-to-file type change reports only
     STATUS_TYPE_CHANGED and never recurses the deletion pass into the old
     directory at all - no STATUS_DELETED lines, and (the actual bug an
     earlier version of this port had) no error either. That earlier version
     used `os.Stat`+`IsDir` here instead, which does NOT share this false
     positive: `IsDir` on a real file correctly reports "not a directory", so
     the port recursed `walk()` into what is now a plain file, `os.ReadDir`
     returned `ENOTDIR` (not `os.ErrNotExist`, so `listDirSafe` did not
     swallow it), and the error propagated out of `StatusTree` entirely -
     discarding the already-correct STATUS_TYPE_CHANGED line and surfacing
     `STATUS_ERR bad_object` to the caller instead.
     `TestStatusTreeTrackedDirectoryReplacedByFile` is the regression test.
  2. An unrelated SIBLING whose name merely starts with the same prefix also
     satisfies the glob (e.g. a tracked directory "Sub" that is genuinely
     gone, alongside an unrelated file "SubNotes.txt") - so the deletion
     recursion into "Sub" is suppressed even though "Sub" really is gone, and
     none of "Sub"'s former children are reported DELETED. This looks like a
     bug in the HolyC, but it is real, verified behaviour, not a porting
     artifact, so it is reproduced rather than "corrected" out from under it.
     `TestStatusTreeDeletedDirectorySuppressedByPrefixSibling` is the
     regression test.
- **The `ignore.Rules.Ignored(path, isDir)` method and the ancestor-directory
  branch of `Pattern.Match` are deleted, as dead code.** `Status.HC` calls
  `IsIgnored(name, rel_dir)` - the same shape `IgnoredLast` already is - in
  both the flat and the tree walk, exactly like both `Offer.HC` paths;
  nothing in this codebase, offer or status, ever calls the `Match`-based,
  isDir-aware form. The remaining method (only its `KindDir` case removed) is
  now unexported to `match`, since `MatchLast` (which still calls it for
  `KindName`/`KindDirContents`) is its only caller anywhere in this codebase.
  `pkg/hgit/ignore/ignore_test.go`'s table-driven test was rewritten against
  `IgnoredLast` instead of the deleted method, and a duplicated `{"build",
  true}` case in `TestIgnoredLast` was collapsed into one.
- **`STATUS_NO_OFFERINGS_YET` / `STATUS_ERR no_head_but_objects_exist`** are
  carried as data (`*status.NoOfferingsYetError`, `status.ErrNoHead`) rather
  than printed directly, matching how `pkg/hgit/status` never touches the
  terminal; `SerialStatus`/`SerialStatusTree` format them. Neither is
  exercised by a fixture segment (the regression never runs `status` against
  an empty or headless repo), so they are unit-tested only
  (`TestStatusEmptyRepoNoOfferingsYet`, `TestStatusTreeEmptyRepoNoOfferingsYet`).
  The `not_a_repository`/`bad_header`/`unsupported_format_version` tokens
  `Status.HC` prints before any of this are the caller's concern (they happen
  at `repo.Open` time, before a `*repo.Repo` exists to pass in), the same
  split `check`'s own `SerialCheck(rep, openErr)` already uses.
- **`STATUS_ERR bad_object` is a native-only token**, the same convention as
  the existing `HISTORY_ERR bad_object`/`SEE_ERR bad_object` entries above:
  `serialStatus`'s fallback for any error `Status`/`StatusTree` return that
  isn't `*NoOfferingsYetError` or `ErrNoHead` (the HolyC's own preamble never
  reaches a state like this). It is genuinely user-visible on more than one
  path even after the prefix-glob fix above - e.g. an unreadable file
  (`*workdir.ReadError`) partway through a walk - not only the now-fixed
  ENOTDIR case.
- **Known limitation: a non-regular file (a symlink whose target no longer
  resolves, a FIFO, a socket) encountered by `statustree` aborts the whole
  walk.** `workdir.Read`'s underlying `os.ReadFile` returns a plain error for
  any of these, which `status.Status`/`status.StatusTree` propagate exactly
  like any other read failure (see `workdir.ReadError`'s own doc) - there is
  no per-entry "skip and keep going" here, matching every other read failure
  in this package, but worth naming since a broken symlink or a FIFO left in
  a working directory is a more everyday way to hit it than a permissions
  error. Not exercised by any fixture or test; `Status.HC`'s own
  `HgitFileRead` has no distinct behaviour for these either.
- **Small carry-overs done alongside this task** (per the review that found
  them): an offer-level test proving a flat `Offer` hides a plain FILE named
  `build` against a `build/` `.hgitignore` rule, the same way a real directory
  of that name is hidden (`TestFlatOfferIgnoresPlainFileMatchingDirPattern`);
  four `offertree` tests for previously-untested branches - a tracked file
  becoming a directory (fresh identity, not carried - `buildTree` only carries
  a directory node's id forward when the OLD entry of that name was itself a
  tree), the reverse direction (a tracked directory becoming a file DOES carry
  its identity forward, since that goes through `addFile`'s name-first
  `CarryEntityID`), a directory deleted down to nothing and later re-created
  under the same name (fresh identity - once a directory has nothing left
  inside it, its own tree entry is dropped, so there is no name to match
  against later), and an old subtree whose hash the archive cannot resolve
  (its entity id still carries forward, read off the old entry directly,
  never off the unresolvable subtree); `offer.Offer`/`offer.OfferTree` now
  document that an error returned after any object has been appended leaves
  the caller's `*repo.Repo` mutated and must not be reused; the duplicate
  `{"build", true}` case in `ignore_test.go` is gone (see above).

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
