# Install why-diff

The three install methods below use the same release archives. Each archive
contains both `why-diff` (the command you run) and `why-diff-hook` (the recorder
used by agent hooks).

Requirements after installation: Git 2.42 or newer, and Codex for automatic
capture. Codex CLI 0.156.1 is the verified baseline for CLI capture. Prebuilt
binaries do not require Go or a C compiler.

## Quick install (macOS and Linux)

Inspect the [installer](../scripts/install.sh) before running it, or use the
direct download method below.

```sh
curl -fsSL https://raw.githubusercontent.com/prsuyal/why-diff/main/scripts/install.sh | sh
```

The installer detects macOS/Linux and Intel/ARM, downloads the matching
archive and `SHA256SUMS` from the latest GitHub release, verifies the archive,
then installs both commands to `~/.local/bin`. Set `WHY_DIFF_INSTALL_DIR` to
choose another directory. If it is not on `PATH`, add it to your shell startup
file.

## Homebrew (macOS and Linux)

Install with:

```sh
brew install prsuyal/tap/why-diff
```

To update later:

```sh
brew update
brew upgrade prsuyal/tap/why-diff
```

The [tap formula](https://github.com/prsuyal/homebrew-tap/blob/main/Formula/why-diff.rb)
downloads one of the same verified release archives and installs both commands.

## Prebuilt binaries (macOS, Linux, Windows)

Download the archive for your operating system and CPU from
[GitHub Releases](https://github.com/prsuyal/why-diff/releases/latest). Compare
its SHA-256 against the release's `SHA256SUMS`, extract both executables, and
put them together in a directory on `PATH`. Windows uses `.exe` files in a ZIP;
macOS and Linux use `.tar.gz` files. No build tools are needed.

| System | Release asset |
| --- | --- |
| macOS, Apple Silicon | `why-diff_darwin_arm64.tar.gz` |
| macOS, Intel | `why-diff_darwin_amd64.tar.gz` |
| Linux, ARM64 | `why-diff_linux_arm64.tar.gz` |
| Linux, x64 | `why-diff_linux_amd64.tar.gz` |
| Windows, x64 | `why-diff_windows_amd64.zip` |

Linux archives are built on Ubuntu 22.04, so older distributions may need a
source build.

## Activate capture

Choose one capture scope:

```sh
why-diff init --global  # once for all Git repositories
```

Or, from one Git repository:

```sh
why-diff init
```

Then run `why-diff doctor` inside a Git repository. For repository setup, trust
the project in Codex; review and trust the hooks with `/hooks`. For global
setup, review and trust the user hooks. Then start a fresh session.
After it edits code, run `why-diff sessions` and
`why-diff why path/to/file.go:42`.

Source builds also contain [additional agent hook adapters](agent-adapters.md)
with `why-diff init --agent NAME`. They have local contract tests and still
need live host checks before release claims.
