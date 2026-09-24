#!/bin/sh
set -eu

case "$(uname -s)" in
  Darwin) platform=darwin ;;
  Linux) platform=linux ;;
  *) echo 'why-diff: quick install supports macOS and Linux; use a release archive on Windows' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo 'why-diff: unsupported CPU architecture' >&2; exit 1 ;;
esac

command -v curl >/dev/null 2>&1 || { echo 'why-diff: curl is required' >&2; exit 1; }
command -v tar >/dev/null 2>&1 || { echo 'why-diff: tar is required' >&2; exit 1; }

asset="why-diff_${platform}_${arch}.tar.gz"
base='https://github.com/prsuyal/why-diff/releases/latest/download'
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT HUP INT TERM

curl -fsSL --retry 3 "$base/$asset" -o "$workdir/$asset"
curl -fsSL --retry 3 "$base/SHA256SUMS" -o "$workdir/SHA256SUMS"
expected=$(awk -v file="$asset" '$2 == file { print $1 }' "$workdir/SHA256SUMS")
test -n "$expected" || { echo 'why-diff: release checksum is missing' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$workdir/$asset" | awk '{ print $1 }')
else
  actual=$(shasum -a 256 "$workdir/$asset" | awk '{ print $1 }')
fi
test "$actual" = "$expected" || { echo 'why-diff: archive checksum does not match' >&2; exit 1; }

tar -xzf "$workdir/$asset" -C "$workdir" why-diff why-diff-hook
target="${WHY_DIFF_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$target"
install -m 755 "$workdir/why-diff" "$target/why-diff"
install -m 755 "$workdir/why-diff-hook" "$target/why-diff-hook"
echo "Installed why-diff and why-diff-hook in $target"
case ":$PATH:" in
  *":$target:"*) ;;
  *) echo "Add $target to PATH, then run why-diff --help" ;;
esac
