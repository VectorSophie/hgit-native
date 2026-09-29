<p align="center">
  <img src="docs/brand/hgitlogowithtext.png" alt="hgit" width="160">
</p>

<h1 align="center">hgit-native</h1>

<p align="center">
  hgit for Windows, macOS and Linux — a native port of the TempleOS
  version-control system: persistent file identity, truthful
  non-destructive history, typed relations between commits, and a
  rename-aware three-way merge. One executable, no VM.
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-GPL--3.0--or--later-blue" alt="GPL-3.0-or-later"></a>
  <img src="https://img.shields.io/badge/language-Go-00ADD8" alt="written in Go">
  <img src="https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey" alt="Windows, macOS, Linux">
  <a href="https://github.com/VectorSophie/hgit-native/releases/tag/v1.9.0"><img src="https://img.shields.io/github/v/release/VectorSophie/hgit-native?label=release&color=blue" alt="latest release"></a>
  <img src="https://img.shields.io/badge/status-released-brightgreen" alt="released">
</p>

<p align="center">
  <a href="INSTALL.md">Install</a> ·
  <a href="#the-commands">Commands</a> ·
  <a href="#how-hgit-differs-from-git">vs. Git</a> ·
  <a href="#experimental-storage-and-exchange-for-high-commit-volume-workflows">Storage/exchange (experimental)</a> ·
  <a href="docs/ARCHITECTURE.md">Architecture</a> ·
  <a href="docs/STATUS.md">Status</a> ·
  <a href="docs/adr/">ADRs</a>
</p>

> **Status: v1.9.0 is released.** `go build ./cmd/hgit` builds a working
> binary, and the full command-line regression against real TempleOS output
> passes end to end. [GitHub Releases](https://github.com/VectorSophie/hgit-native/releases)
> carries pre-built Windows/macOS/Linux binaries, built and attested by a
> reproducible CI pipeline (`.github/workflows/release.yml` - two
> independent builds of the same commit verified to produce byte-identical
> archives before this claim was made). `choco install hgit-native` is
> approved on the Chocolatey community feed. No Homebrew formula or apt
> package yet. [`docs/STATUS.md`](docs/STATUS.md) says exactly what exists
> today.

## What this is

[hgit](https://github.com/VectorSophie/hgit) is a version-control system
written in HolyC that runs inside TempleOS. It works, and it is verified on
real TempleOS under QEMU, but reaching it means running a TempleOS VM.
hgit-native is the same tool, ported to Go, for ordinary machines:

- the **same `.hgs` repository format**, so a repo made by either
  implementation opens in the other;
- the **same commands and semantics**, as normal subcommands with flags and
  `--help` instead of `Hgit("...")` calls;
- parity checked against **golden fixtures produced on real TempleOS**, not
  assumed.

It is not a drop-in Git replacement: repos are `.hgs` archives, there are no
remotes (sharing is `export` / `import`), and it cannot open Git repos or talk
to GitHub.

## The commands

Full parity with hgit 1.8.9:

| Command | What it does |
|---|---|
| `init` | Create a new, empty repository |
| `offer` | Snapshot matching files as a new commit (hgit's word for git's "commit"), honoring `.hgitignore` |
| `offertree` / `statustree` | The same, recursing into subdirectories |
| `status` | Compare the working directory against HEAD: new, modified, deleted, and **renamed** |
| `merge` | A rename-aware three-way merge between two named paths; real conflicts persist as repository data |
| `conflicts` / `resolve` / `merge continue` / `merge abort` | The conflict lifecycle |
| `history` / `graph` | Walk one path's commits, or the whole history across every path |
| `see` / `diff` | Show one commit, or what changed relative to its parent |
| `check` | Integrity: hash verification, referential integrity, dangling-object detection |
| `undo` / `redo` | Step through the operation log; reversible, never destructive |
| `operation history` / `operation restore` | Jump to any past point |
| `path list` / `new` / `go` / `close` | Named, branch-like alternate histories over one object store |
| `correct` / `revert` / `reconcile` | Typed relations between commits |
| `export` / `import` | Whole-repo portability, paths and history intact |

The DolDoc views (`historydoc`, `reconciledoc`, `conflictdoc`) are TempleOS's own
rich-text format; here they become plain-text output carrying the same
information. See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Ignore rules and file attributes

A `.hgitignore` keeps generated files out of `offer` and `status`, and a
`.hgitattributes` declares a file's mode (`text`, `binary`, `executable`).
Both use a small, deliberately-smaller-than-Git glob grammar, defined in the
original project's ADRs
([0014](contract/docs/adr/0014-ignore-rules.md),
[0015](contract/docs/adr/0015-file-attributes-and-modes.md)). One rule matters
most: **ignore only ever hides discovery of untracked material; it never
conceals an already-tracked file.**

## How hgit differs from Git

Not a Git clone wearing a different hat. A few deliberate departures, each
backed by a decision record in the original project:

| | Git | hgit |
|---|---|---|
| **File identity** | Path-based; a rename is a heuristic guess made after the fact | A stable **entity ID** travels with a tracked thing across renames and content changes (ADR 0004) |
| **History editing** | `rebase` / `reset --hard` / `gc` can destroy commits | Nothing is deleted. `undo` moves a pointer; an unreferenced commit just sits there, and `check` reports it as dangling |
| **Relations between commits** | A parent-pointer DAG; "this corrects that" lives in a message, if anywhere | Typed relations (`correct` / `revert` / `reconcile`) are real fields on the commit (ADR 0005/0006) |
| **Renames** | Similarity-scored heuristic | Exact-content match, plus fuzzy detection for a rename-with-edit (ADR 0008/0009) |

None of this makes hgit a replacement for Git. It is a different answer to the
same problem.

## Experimental: storage and exchange for high-commit-volume workflows

Since 1.9.0 this port (and the original TempleOS project) carry a scoped,
**experimental** set of storage and exchange changes: a writer stores each
distinct object once instead of duplicating it on every commit, an
interrupted write is recoverable, and a portable incremental exchange file
(`.hgb`/`.hgh`, spec in `contract/BUNDLE.md`) sends what a replica is missing
rather than the whole repository. None of these techniques is Git-specific —
a store-once writer, a tolerant reader, and a manifest-plus-prerequisites
exchange file could be adopted by any content-addressed VCS.

The motivation isn't "faster than Git" (Git already solves this at far larger
scale, with packfiles, partial clone and its own bundle format). It's that
everyday usage is shifting toward far more commits per working copy — agentic
tooling producing 50+ commits a day, bigger monorepos, more replicas of one
repository to keep in sync — and treating "rewrite the whole archive on every
commit, send the whole repository on every exchange" as free stops being
true at that volume. See `contract/docs/adr/0018-storage-and-exchange-release-policy.md`
onward for what changed, what was measured, and what's explicitly left as
design-only until there's evidence for it.

## Why a burning bush?

Exodus 3: a bush burns and is never consumed. TempleOS's creator, Terry Davis,
built an entire OS on the premise that God speaks in 640×480 and 16 colors.
hgit doesn't share the theology. It takes the one line that describes what the
tool does: **history burns here, and it is never consumed.** `undo` moves a
pointer, it doesn't delete anything, and `check` will tell you about commits
nobody points to anymore, because they're still there, unharmed, in the fire.

## The contract with the original

`contract/` is a git submodule pinned to a tag of the TempleOS project. It holds
the format spec (`FORMAT.md`), the ADRs and research, the HolyC source being
ported, and the golden fixtures. Clone with `--recurse-submodules`, or run
`git submodule update --init`.

## License

GNU General Public License, version 3 or (at your option) any later version.
See [`LICENSE`](LICENSE).
