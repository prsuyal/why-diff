#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  echo 'usage: scripts/homebrew-formula.sh v0.1.0 SHA256SUMS > why-diff.rb' >&2
  exit 2
fi
tag=$1
checksums=$2
printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || {
  echo 'why-diff: expected a stable version tag such as v0.1.0' >&2
  exit 2
}

checksum() {
  name="why-diff_${1}.tar.gz"
  value=$(awk -v file="$name" '$2 == file { print $1 }' "$checksums")
  printf '%s\n' "$value" | grep -Eq '^[0-9a-f]{64}$' || {
    echo "why-diff: missing or invalid checksum for $name" >&2
    exit 1
  }
  printf '%s' "$value"
}

darwin_arm64=$(checksum darwin_arm64)
darwin_amd64=$(checksum darwin_amd64)
linux_arm64=$(checksum linux_arm64)
linux_amd64=$(checksum linux_amd64)

cat <<EOF
# typed: strict
# frozen_string_literal: true

# Installs why-diff and its agent hook recorder.
class WhyDiff < Formula
  desc "Explain AI-assisted code changes from captured evidence"
  homepage "https://github.com/prsuyal/why-diff"
  license "MIT"

  depends_on "git"

  on_macos do
    on_arm do
      url "https://github.com/prsuyal/why-diff/releases/download/$tag/why-diff_darwin_arm64.tar.gz"
      sha256 "$darwin_arm64"
    end
    on_intel do
      url "https://github.com/prsuyal/why-diff/releases/download/$tag/why-diff_darwin_amd64.tar.gz"
      sha256 "$darwin_amd64"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/prsuyal/why-diff/releases/download/$tag/why-diff_linux_arm64.tar.gz"
      sha256 "$linux_arm64"
    end
    on_intel do
      url "https://github.com/prsuyal/why-diff/releases/download/$tag/why-diff_linux_amd64.tar.gz"
      sha256 "$linux_amd64"
    end
  end

  def install
    bin.install "why-diff", "why-diff-hook"
  end

  test do
    assert_match "why-diff", shell_output("#{bin}/why-diff --help")
  end
end
EOF
