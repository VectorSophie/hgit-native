# Benchmarks

`tools/bench` is the reproducible corpus and benchmark harness for the 1.9
storage work (ADR 0018, ADR 0019). It drives the real library API exactly as
the command line does - `repo.Open`, then the command, then `Save` - on
repositories it builds itself.

## Scenarios

| id | shape | what it stresses |
|---|---|---|
| s1 | 5 text files of 4 KiB, 1,000 commits, one file edited per commit (one line changed) | growth per commit on a hot, small tree; checkpoints at 1, 10, 100, 300, 1000 commits |
| s2 | wide tree through `offertree`: 2,000 files (1-2 KiB) in 100 directories for 30 commits, then 10,000 files in 200 directories for 5 commits; 3 files edited per commit | per-commit cost of a large tree when almost nothing changed; checkpoints at the first, middle and last commit |
| s3 | 500 files sharing 8 distinct 2 KiB contents, 20 commits, one file edited each | identical content within one offer |
| s4 | 50 commits on main, then 5 side paths of 10 commits each, every one merged back with a clean three-way merge (main advances once before each merge) | named paths and merge commits; `offer ms` is the last merge |
| s5 | 5 incompressible 2 MiB binaries, 10 commits, one 64 KiB block rewritten per commit | large objects edited in place |
| s6 | the s1 history of 1,000 commits, seen by a replica 1, 10 and 999 commits behind | what whole-repository `export`/`import` sends versus the bytes of the records the replica lacks |

## What each column measures

- `.hgs bytes`, `records`: the archive on disk after the checkpoint commit.
- `unique`: distinct object hashes. `stored / floor` is `.hgs bytes` divided
  by the size the archive would have if every object were stored once;
  1.0x means no duplicate records.
- `working set`: bytes of the working tree (what a user would call the repo's size).
- `open ms`: `repo.Open` (read and parse `.hgs` and `.hgs.m`, build the hash index).
- `offer ms`: the offer (or, for s4, the merge) that produced the checkpoint,
  open excluded, `Save` included. **One sample, not a median** - it is the
  commit itself, which cannot be repeated without changing the repository.
- `save ms`: `Open` then `Save` with no change (rewrites the whole archive).
- `status ms`: `status` (s2: `statustree`) on a clean working tree.
- `history ms`: the full first-parent walk.
- `check ms`: `check.Run` (hash every record, walk every reference, list dangling).
- `open alloc`: bytes allocated by one `repo.Open`; `open heap`: live heap
  growth held by the opened repository after a GC.

Every timing except `offer ms` is the median of `-runs` repetitions (3 here).

## Determinism

A seeded `math/rand` builds every file, `clock.Now` is a frozen monotonic
counter and entity ids come from a counter, so a run builds byte-identical
repositories every time: sizes, record counts and unique counts are exact and
repeatable. Timings are not; they vary run to run by roughly 10-30% on this
machine for anything under ~10 ms, and single-sample `offer ms` values more.

## Machine

Intel Core i5-1035G7 (4 cores / 8 threads, 1.2 GHz base, laptop, no frequency
pinning), 7.4 GiB RAM, Linux 6.17.0-14-generic, go1.22.2 linux/amd64, for
the `dedup` and `dedup-zerocopy` runs. The baseline was recorded earlier; its
own header (`results/baseline-9602db2.stderr.txt`) reports the same Go
version, OS/arch and CPU count, but the machine was not otherwise recorded.

## Reproduce

```
go run ./tools/bench -scenario all -runs 3 -variant NAME -out docs/benchmarks/results/NAME.jsonl
go run ./tools/bench -report docs/benchmarks/results/baseline-9602db2.jsonl,docs/benchmarks/results/NAME.jsonl
```

`-quick` runs smaller sizes for a smoke test; `-scenario s1,s2` picks
scenarios. `-report` with several comma-separated files prints the rows for
the same measurement point next to each other, in the order given. The
stderr of each run is kept beside its results as `NAME.stderr.txt`.

## Results files

| file | library code |
|---|---|
| `results/baseline-9602db2.jsonl` | 1.8.9 library (9602db2), harness from 50ddb06: unconditional append, copying parse |
| `results/dedup.jsonl` | cb57e8e: objects stored once (ADR 0019), copying parse. Intermediate; this commit still hashed every new object twice (fixed in b9a070f), which inflates its `offer ms` on large new objects (s5 at 1 commit) |
| `results/dedup-zerocopy.jsonl` | b9a070f: objects stored once, zero-copy parse, `Get` without a copy, one hash per stored object |

## Before / after

Headline, baseline to dedup-zerocopy:

| scenario | .hgs bytes | open ms | offer ms | save ms | check ms |
|---|---|---|---|---|---|
| s1, 1000 commits | 12.1 MB -> 3.0 MB (4.0x smaller) | 9.0 -> 2.4 | 33.6 -> 9.1 | 26.0 -> 8.6 | 20.3 -> 9.9 |
| s2, 2000 files, 30 commits | 101.9 MB -> 3.9 MB (26x) | 63.7 -> 1.5 | 406.9 -> 37.6 | 180.4 -> 7.3 | 156.5 -> 7.4 |
| s2, 10000 files, 5 commits | 84.8 MB -> 17.1 MB (5.0x) | 47.3 -> 6.0 | 416.0 -> 167.0 | 301.6 -> 36.3 | 145.2 -> 31.8 |
| s3, 20 commits | 22.0 MB -> 0.89 MB (24.6x) | 11.8 -> 0.4 | 53.0 -> 10.6 | 50.0 -> 2.2 | 31.0 -> 3.4 |
| s4, 110 commits | 2.0 MB -> 0.32 MB (6.2x) | 1.4 -> 0.1 | 4.6 -> 0.9 (merge) | 4.0 -> 0.7 | 3.9 -> 0.8 |
| s5, 10 commits | 104.9 MB -> 29.4 MB (3.6x) | 49.8 -> 7.5 | 247.2 -> 55.7 | 204.3 -> 44.8 | 136.5 -> 33.4 |

With dedup every scenario's `stored / floor` is 1.0x: records equal unique
objects. s6: a replica one commit behind is still sent the whole repository
by `export`/`import`, now 3.15 MB instead of 12.3 MB, for 2,396 bytes of
new records (1315x instead of 5149x); that gap is ADR 0020's, not this change's.

What did not improve, or got worse:

- **`status`** is flat (s2 10,000 files: 192.8 -> 202.5 ms, within noise).
  It is dominated by reading and hashing the working tree, which this change
  does not touch.
- **`history`** is flat: it was already well under a millisecond.
- **First offers** (nothing to skip: s1/s2/s3/s5 at 1 commit) are flat within
  single-sample noise (s2 2,000 files 38.3 -> 43.4 ms, s2 10,000 files
  194.9 -> 194.7 ms, s5 40.9 -> 43.1 ms). Storing once costs one hash-index
  lookup per object; there is no saving until something repeats. The
  intermediate `dedup` run's s5 first offer (56.7 ms) was the double hash,
  fixed before the final run.
- **`open heap`** barely moves for a given archive (s2 10,000 files at 1
  commit: 19.7 MB -> 19.2 MB). Zero-copy parse does not shrink what is held
  - the records now keep the whole read buffer alive instead of holding their
  own copies - it removes the per-record copy and allocation (`open alloc`
  roughly halves: 40.3 MB -> 19.2 MB). The large heap drops in the table come
  from the archive being smaller.
- The 10,000-file s2 offer still costs ~170 ms per commit: every offer still
  reads and hashes every working file and rewrites the whole archive.

Isolated micro-benchmarks for the zero-copy step (same machine, `go test -bench`):
`archive.BenchmarkParse` (10,000 records) 1.36 ms / 4.9 MB / 10,033 allocs ->
0.33 ms / 0.89 MB / 4 allocs; `check.BenchmarkRun` (2,000 x 2 KiB objects,
`Get` copy removed) 8.9 ms / 10.9 MB -> 6.0 ms / 1.9 MB.

### Full table (generated)

`go run ./tools/bench -report results/baseline-9602db2.jsonl,results/dedup.jsonl,results/dedup-zerocopy.jsonl`

| scen | variant | commits | .hgs bytes | records | unique | stored / floor | working set | open ms | offer ms | save ms | status ms | history ms | check ms | open alloc | open heap |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| s1 | baseline-9602db2 | 1 | 21454 | 7 | 7 | 1.0x | 20442 | 0.0 | 0.4 | 0.1 | 0.2 | 0.0 | 0.0 | 49696 | 18016 |
| s1 | dedup | 1 | 21454 | 7 | 7 | 1.0x | 20442 | 0.0 | 0.3 | 0.1 | 0.2 | 0.0 | 0.1 | 49696 | 18016 |
| s1 | dedup-zerocopy | 1 | 21454 | 7 | 7 | 1.0x | 20442 | 0.0 | 0.3 | 0.1 | 0.1 | 0.0 | 0.0 | 24880 | 15584 |
| s1 | baseline-9602db2 | 10 | 213403 | 70 | 34 | 3.3x | 20119 | 0.1 | 0.5 | 0.6 | 0.1 | 0.0 | 0.3 | 479720 | 246272 |
| s1 | dedup | 10 | 64738 | 34 | 34 | 1.0x | 20119 | 0.0 | 0.3 | 0.2 | 0.2 | 0.0 | 0.3 | 148176 | 68528 |
| s1 | dedup-zerocopy | 10 | 64738 | 34 | 34 | 1.0x | 20119 | 0.0 | 0.5 | 0.2 | 0.1 | 0.0 | 0.1 | 79136 | 69640 |
| s1 | baseline-9602db2 | 100 | 1951761 | 700 | 304 | 4.2x | 16648 | 1.1 | 4.7 | 5.1 | 0.2 | 0.1 | 3.2 | 4323448 | 2242704 |
| s1 | dedup | 100 | 461097 | 304 | 304 | 1.0x | 16648 | 0.2 | 0.9 | 1.0 | 0.2 | 0.0 | 1.5 | 1127848 | 587288 |
| s1 | dedup-zerocopy | 100 | 461097 | 304 | 304 | 1.0x | 16648 | 0.1 | 0.8 | 0.9 | 0.1 | 0.1 | 0.9 | 583192 | 558472 |
| s1 | baseline-9602db2 | 300 | 4959397 | 2100 | 904 | 4.2x | 12065 | 3.7 | 10.4 | 10.6 | 0.1 | 0.1 | 9.5 | 11258264 | 5753264 |
| s1 | dedup | 300 | 1172326 | 904 | 904 | 1.0x | 12065 | 0.8 | 3.4 | 2.6 | 0.1 | 0.2 | 4.7 | 2829032 | 1485544 |
| s1 | dedup-zerocopy | 300 | 1172326 | 904 | 904 | 1.0x | 12065 | 0.4 | 3.1 | 2.0 | 0.1 | 0.2 | 2.2 | 1552152 | 1486488 |
| s1 | baseline-9602db2 | 1000 | 12083352 | 7000 | 3004 | 4.0x | 7662 | 9.0 | 33.6 | 26.0 | 0.1 | 0.7 | 20.3 | 28877128 | 14582112 |
| s1 | dedup | 1000 | 2985685 | 3004 | 3004 | 1.0x | 7662 | 2.8 | 8.7 | 7.5 | 0.1 | 0.6 | 11.5 | 7738984 | 3822216 |
| s1 | dedup-zerocopy | 1000 | 2985685 | 3004 | 3004 | 1.0x | 7662 | 2.4 | 9.1 | 8.6 | 0.2 | 0.6 | 9.9 | 4031400 | 3817272 |
| s2 | baseline-9602db2 | 1 | 3397085 | 2102 | 2102 | 1.0x | 3067338 | 2.8 | 38.3 | 7.1 | 39.4 | 0.0 | 8.4 | 7887144 | 4010984 |
| s2 | dedup | 1 | 3397085 | 2102 | 2102 | 1.0x | 3067338 | 2.4 | 40.8 | 6.4 | 40.0 | 0.0 | 8.1 | 7887240 | 4011080 |
| s2 | dedup-zerocopy | 1 | 3397085 | 2102 | 2102 | 1.0x | 3067338 | 2.5 | 43.4 | 7.8 | 47.7 | 0.0 | 6.3 | 3918168 | 3916552 |
| s2 | baseline-9602db2 | 15 | 50944645 | 31530 | 2214 | 13.9x | 3065685 | 30.1 | 181.0 | 136.6 | 37.1 | 0.0 | 78.7 | 120967208 | 59728320 |
| s2 | dedup | 15 | 3652131 | 2214 | 2214 | 1.0x | 3065685 | 3.3 | 33.6 | 8.4 | 42.5 | 0.0 | 10.0 | 8407864 | 4275160 |
| s2 | dedup-zerocopy | 15 | 3652131 | 2214 | 2214 | 1.0x | 3065685 | 1.3 | 35.2 | 7.2 | 39.0 | 0.0 | 6.2 | 4185384 | 4181432 |
| s2 | baseline-9602db2 | 30 | 101862032 | 63060 | 2334 | 26.0x | 3063799 | 63.7 | 406.9 | 180.4 | 38.5 | 0.0 | 156.5 | 242671416 | 119382864 |
| s2 | dedup | 30 | 3920598 | 2334 | 2334 | 1.0x | 3063799 | 4.6 | 38.5 | 9.4 | 39.8 | 0.0 | 11.2 | 8959688 | 4553192 |
| s2 | dedup-zerocopy | 30 | 3920598 | 2334 | 2334 | 1.0x | 3063799 | 1.5 | 37.6 | 7.3 | 39.9 | 0.0 | 7.4 | 4478936 | 4471400 |
| s2 | baseline-9602db2 | 1 | 16967071 | 10202 | 10202 | 1.0x | 15365824 | 12.0 | 194.9 | 34.9 | 192.8 | 0.0 | 35.2 | 40303016 | 19747048 |
| s2 | dedup | 1 | 16967071 | 10202 | 10202 | 1.0x | 15365824 | 10.6 | 227.4 | 40.8 | 203.3 | 0.0 | 35.2 | 40304296 | 19748328 |
| s2 | dedup-zerocopy | 1 | 16967071 | 10202 | 10202 | 1.0x | 15365824 | 5.8 | 194.7 | 33.5 | 202.5 | 0.0 | 25.8 | 19186520 | 19182984 |
| s2 | baseline-9602db2 | 2 | 33934048 | 20404 | 10210 | 2.0x | 15365682 | 26.6 | 195.8 | 74.6 | 194.9 | 0.0 | 60.5 | 79133656 | 39032472 |
| s2 | dedup | 2 | 17000573 | 10210 | 10210 | 1.0x | 15365682 | 12.9 | 182.7 | 46.3 | 196.6 | 0.0 | 38.0 | 40372088 | 19783256 |
| s2 | dedup-zerocopy | 2 | 17000573 | 10210 | 10210 | 1.0x | 15365682 | 8.0 | 177.3 | 39.1 | 200.4 | 0.0 | 26.1 | 19206440 | 19203960 |
| s2 | baseline-9602db2 | 5 | 84834243 | 51010 | 10234 | 5.0x | 15365309 | 47.3 | 416.0 | 301.6 | 210.1 | 0.0 | 145.2 | 197965224 | 96292416 |
| s2 | dedup | 5 | 17099457 | 10234 | 10234 | 1.0x | 15365309 | 11.8 | 172.0 | 38.0 | 195.8 | 0.0 | 39.9 | 40585528 | 19896664 |
| s2 | dedup-zerocopy | 5 | 17099457 | 10234 | 10234 | 1.0x | 15365309 | 6.0 | 167.0 | 36.3 | 202.1 | 0.0 | 31.8 | 19313384 | 19309176 |
| s3 | baseline-9602db2 | 1 | 1102201 | 502 | 11 | 18.1x | 1023954 | 1.1 | 10.4 | 1.8 | 11.1 | 0.0 | 1.7 | 2488856 | 1319352 |
| s3 | dedup | 1 | 60790 | 11 | 11 | 1.0x | 1023954 | 0.0 | 7.0 | 0.2 | 10.8 | 0.0 | 0.4 | 141312 | 60096 |
| s3 | dedup-zerocopy | 1 | 60790 | 11 | 11 | 1.0x | 1023954 | 0.0 | 9.1 | 0.2 | 10.5 | 0.0 | 0.2 | 69680 | 55104 |
| s3 | baseline-9602db2 | 20 | 22037167 | 10040 | 68 | 24.6x | 1023153 | 11.8 | 53.0 | 50.0 | 10.3 | 0.0 | 31.0 | 50901688 | 26164688 |
| s3 | dedup | 20 | 894404 | 68 | 68 | 1.0x | 1023153 | 0.5 | 10.0 | 2.4 | 10.6 | 0.0 | 8.1 | 1979576 | 1066960 |
| s3 | dedup-zerocopy | 20 | 894404 | 68 | 68 | 1.0x | 1023153 | 0.4 | 10.6 | 2.2 | 11.5 | 0.0 | 3.4 | 928696 | 923208 |
| s4 | baseline-9602db2 | 110 | 1993778 | 1220 | 332 | 6.2x | 21809 | 1.4 | 4.6 | 4.0 | 0.3 | 0.0 | 3.9 | 4555288 | 2351664 |
| s4 | dedup | 110 | 319791 | 332 | 332 | 1.0x | 21809 | 0.1 | 1.8 | 0.7 | 0.3 | 0.0 | 1.4 | 831400 | 429960 |
| s4 | dedup-zerocopy | 110 | 319791 | 332 | 332 | 1.0x | 21809 | 0.1 | 0.9 | 0.7 | 0.3 | 0.0 | 0.8 | 452072 | 427320 |
| s5 | baseline-9602db2 | 1 | 10486973 | 8 | 8 | 1.0x | 10485760 | 6.7 | 40.9 | 13.3 | 20.0 | 0.0 | 14.4 | 21025216 | 10529272 |
| s5 | dedup | 1 | 10486973 | 8 | 8 | 1.0x | 10485760 | 5.2 | 56.7 | 11.0 | 19.5 | 0.0 | 16.1 | 21025216 | 10529368 |
| s5 | dedup-zerocopy | 1 | 10486973 | 8 | 8 | 1.0x | 10485760 | 5.7 | 43.1 | 9.4 | 19.3 | 0.0 | 12.0 | 10497200 | 10495832 |
| s5 | baseline-9602db2 | 10 | 104870163 | 80 | 35 | 3.6x | 10485760 | 49.8 | 247.2 | 204.3 | 17.8 | 0.0 | 136.5 | 210188840 | 105299648 |
| s5 | dedup | 10 | 29368965 | 35 | 35 | 1.0x | 10485760 | 11.3 | 84.2 | 61.4 | 18.8 | 0.0 | 57.2 | 58880720 | 29495144 |
| s5 | dedup-zerocopy | 10 | 29368965 | 35 | 35 | 1.0x | 10485760 | 7.5 | 55.7 | 44.8 | 18.4 | 0.0 | 33.4 | 29390240 | 29387176 |

| variant | commits | replica behind | whole export/import bytes | records the replica lacks | floor bytes | whole / floor |
|---|---:|---:|---:|---:|---:|---:|
| baseline-9602db2 | 1000 | 1 | 12336837 | 3 | 2396 | 5149x |
| dedup | 1000 | 1 | 3150872 | 3 | 2396 | 1315x |
| dedup-zerocopy | 1000 | 1 | 3150872 | 3 | 2396 | 1315x |
| baseline-9602db2 | 1000 | 10 | 12336837 | 30 | 23188 | 532x |
| dedup | 1000 | 10 | 3150872 | 30 | 23188 | 136x |
| dedup-zerocopy | 1000 | 10 | 3150872 | 30 | 23188 | 136x |
| baseline-9602db2 | 1000 | 999 | 12336837 | 2997 | 2986341 | 4x |
| dedup | 1000 | 999 | 3150872 | 2997 | 2986341 | 1x |
| dedup-zerocopy | 1000 | 999 | 3150872 | 2997 | 2986341 | 1x |
