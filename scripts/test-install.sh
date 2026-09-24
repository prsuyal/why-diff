#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
mkdir -p "$workdir/mock" "$workdir/package" "$workdir/release" "$workdir/bin"

case "$(uname -s)" in Darwin) platform=darwin ;; Linux) platform=linux ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; esac
asset="why-diff_${platform}_${arch}.tar.gz"

printf '#!/bin/sh\necho installed-cli\n' > "$workdir/package/why-diff"
printf '#!/bin/sh\necho installed-hook\n' > "$workdir/package/why-diff-hook"
chmod +x "$workdir/package/why-diff" "$workdir/package/why-diff-hook"
tar -czf "$workdir/release/$asset" -C "$workdir/package" why-diff why-diff-hook
if command -v sha256sum >/dev/null 2>&1; then
  checksum=$(sha256sum "$workdir/release/$asset" | awk '{ print $1 }')
else
  checksum=$(shasum -a 256 "$workdir/release/$asset" | awk '{ print $1 }')
fi
printf '%s  %s\n' "$checksum" "$asset" > "$workdir/release/SHA256SUMS"

cat > "$workdir/mock/curl" <<'EOF'
#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then
    shift
    output=$1
  else
    case "$1" in https://*) url=$1 ;; esac
  fi
  shift
done
cp "$WHY_DIFF_TEST_RELEASE/${url##*/}" "$output"
EOF
chmod +x "$workdir/mock/curl"

WHY_DIFF_TEST_RELEASE="$workdir/release" WHY_DIFF_INSTALL_DIR="$workdir/bin" \
  PATH="$workdir/mock:$PATH" sh "$root/scripts/install.sh" >/dev/null
test "$("$workdir/bin/why-diff")" = installed-cli
test "$("$workdir/bin/why-diff-hook")" = installed-hook

printf '%064d  %s\n' 0 "$asset" > "$workdir/release/SHA256SUMS"
if WHY_DIFF_TEST_RELEASE="$workdir/release" WHY_DIFF_INSTALL_DIR="$workdir/bin" \
  PATH="$workdir/mock:$PATH" sh "$root/scripts/install.sh" >/dev/null 2>&1; then
  echo 'installer accepted a bad checksum' >&2
  exit 1
fi
echo 'install checks passed'
