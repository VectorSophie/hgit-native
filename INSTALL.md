# Installing hgit-native

**Not released yet.** There is nothing to install today: the repository holds
the design and the contract, and the port has not started. This page describes
how installation will work, so it is clear what is planned and what is not.
[`docs/STATUS.md`](docs/STATUS.md) is the source of truth for progress.

## Planned

hgit-native ships as a single native executable, with no TempleOS, QEMU or
Python involved.

| Platform | Planned channel |
|---|---|
| Windows | `choco install hgit`, or download `hgit.exe` from a release |
| macOS | `brew install VectorSophie/hgit/hgit`, or download the binary |
| Debian / Ubuntu | `sudo apt install ./hgit_<version>_<arch>.deb` from a release |
| Any | `go install github.com/VectorSophie/hgit-native/cmd/hgit@latest` (Go 1.22+) |

The package name is `hgit`. None of these channels exist yet.

## Building from source

Needs Go 1.22 or newer and git.

```sh
git clone --recurse-submodules https://github.com/VectorSophie/hgit-native.git
cd hgit-native
```

`cmd/hgit` does not exist yet, so there is nothing to build. The
`--recurse-submodules` flag matters even now: the `contract/` folder holds the
format spec and the golden fixtures the port is tested against.

## Looking for the TempleOS version?

The original hgit, which runs inside TempleOS, has its own install guide and a
ready-to-run VM bundle:
<https://github.com/VectorSophie/hgit/blob/main/INSTALL.md>.
