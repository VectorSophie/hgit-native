# Installing hgit-native

**Not released yet.** The port itself is implemented and its test suite
passes, but there is no GitHub release, and no Homebrew/Chocolatey/apt
listing exists for this repository yet. Today the only way to get `hgit` is
to build it from source, below.
[`docs/STATUS.md`](docs/STATUS.md) is the source of truth for progress.

## Planned distribution channels

None of these exist yet; this is what building the packages will look like
once they do.

| Platform | Planned channel |
|---|---|
| Windows | `choco install hgit`, or download `hgit.exe` from a release |
| macOS | `brew install VectorSophie/hgit/hgit`, or download the binary |
| Debian / Ubuntu | `sudo apt install ./hgit_<version>_<arch>.deb` from a release |
| Any | `go install github.com/VectorSophie/hgit-native/cmd/hgit@latest` (Go 1.22+) |

The package name is `hgit`.

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
