# Installing hgit-native

**v1.9.0+ is released.** [GitHub Releases](https://github.com/VectorSophie/hgit-native/releases)
carries pre-built Windows/macOS/Linux binaries, and `choco install
hgit-native` is approved on the Chocolatey community feed. You can also
build from source, below. [`docs/STATUS.md`](docs/STATUS.md) is the source
of truth for progress.

## Install

| Platform | Channel |
|---|---|
| Windows | `choco install hgit-native`, or download `hgit.exe` from a [release](https://github.com/VectorSophie/hgit-native/releases) |
| macOS | download the `mac-amd64` (Intel) or `mac-arm64` (Apple Silicon) archive from a release; Homebrew not yet packaged |
| Debian / Ubuntu | download the `linux-amd64` archive from a release; `.deb`/apt not yet packaged |
| Any | `go install github.com/VectorSophie/hgit-native/cmd/hgit@latest` (Go 1.22+) |

**The Chocolatey package name is `hgit-native`** (`hgit` was already taken by
the original TempleOS project's own Chocolatey package); **the installed
command is `hgit`**, not `hgit-native` - there is no `hgit-native` binary or
alias, only the one command:

```sh
choco install hgit-native
hgit help
hgit version
```

`hgit version` reports two separate things, on purpose: this build's own
release version, and the TempleOS contract release it has verified
byte-for-byte parity with - these are not the same number and are never
merged into one line. `hgit help`/`hgit version` are the canonical commands;
`-h`/`--help` happen to work too (an artifact of Go's `flag` package, which
always intercepts them, not a deliberate GNU-style alias this project adds
elsewhere), while `--version` deliberately does not exist as a flag - this
CLI's own grammar is bare subcommands throughout (`hgit help`, `hgit
version`, `hgit bundle create`, ...), matching the original TempleOS
`Hgit("help")`/`Hgit("version")` calling convention it ports, and a
`--version` flag would sit awkwardly next to that rather than improve it.

## Building from source

Needs Go 1.22 or newer and git.

```sh
git clone --recurse-submodules https://github.com/VectorSophie/hgit-native.git
cd hgit-native
go build -o hgit ./cmd/hgit
./hgit help
```

The `--recurse-submodules` flag matters: the `contract/` folder holds the
format spec and the golden fixtures the port is tested against (and its
absence will fail `go test ./...`, though it is not needed just to build
`cmd/hgit`).

## Looking for the TempleOS version?

The original hgit, which runs inside TempleOS, has its own install guide and a
ready-to-run VM bundle:
<https://github.com/VectorSophie/hgit/blob/main/INSTALL.md>.
