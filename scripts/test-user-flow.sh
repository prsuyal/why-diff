#!/bin/sh
set -eu

source_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
mkdir -p "$workdir/bin" "$workdir/package" "$workdir/codex-home" "$workdir/project"
export CODEX_HOME="$workdir/codex-home"
export PATH="$workdir/bin:$PATH"

cd "$source_root"
go build -trimpath -ldflags='-s -w' -o "$workdir/package/why-diff" ./cmd/why-diff
go build -trimpath -ldflags='-s -w' -o "$workdir/package/why-diff-hook" ./cmd/why-diff-hook
tar -czf "$workdir/why-diff.tar.gz" -C "$workdir/package" why-diff why-diff-hook
tar -xzf "$workdir/why-diff.tar.gz" -C "$workdir/bin"
test -x "$workdir/bin/why-diff"
test -x "$workdir/bin/why-diff-hook"

repo="$workdir/project"
git -C "$repo" init --quiet
git -C "$repo" config user.name 'why-diff test'
git -C "$repo" config user.email 'test@example.com'
cat > "$repo/go.mod" <<'EOF'
module example.com/demo

go 1.27.0
EOF
cat > "$repo/answer.go" <<'EOF'
package demo

func Answer() int { return 1 }
EOF
cat > "$repo/answer_test.go" <<'EOF'
package demo

import "testing"

func TestAnswer(t *testing.T) {
	if Answer() != 2 {
		t.Fatal("want 2")
	}
}
EOF
git -C "$repo" add go.mod answer.go answer_test.go
git -C "$repo" commit --quiet -m initial

cd "$repo"
why-diff init >/dev/null
test -f .codex/hooks.json
test ! -e .claude/settings.json
doctor_output=$(why-diff doctor)
printf '%s\n' "$doctor_output" | grep -q 'Result: ready'
if printf '%s\n' "$doctor_output" | grep -qi claude; then
  echo 'doctor exposed an unreleased integration' >&2
  exit 1
fi
index_before=$(git write-tree)
status_before=$(git status --porcelain)

hook() {
  printf '{"session_id":"user-flow","turn_id":"turn-1","cwd":"%s",%s}\n' "$repo" "$1" | why-diff-hook codex --strict
}

hook '"hook_event_name":"SessionStart","source":"startup"'
hook '"hook_event_name":"UserPromptSubmit","prompt":"Fix Answer so the test passes"'
hook '"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"fail","tool_input":{"command":"go test ./..."}'
if go test ./... >/dev/null 2>&1; then
  echo 'expected the initial test to fail' >&2
  exit 1
fi
hook '"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"fail","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":1,"output":"FAIL"}'
hook '"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"edit","tool_input":{"command":"fix Answer"}'
sed 's/return 1/return 2/' answer.go > answer.go.tmp
mv answer.go.tmp answer.go
hook '"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"edit","tool_input":{"command":"fix Answer"},"tool_response":{"output":"Done"}'
hook '"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"pass","tool_input":{"command":"go test ./..."}'
go test ./... >/dev/null
hook '"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"pass","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0,"output":"ok"}'
hook '"hook_event_name":"SessionEnd","reason":"completed"'

test "$(git write-tree)" = "$index_before"
test "$(git status --porcelain)" != "$status_before"
why-diff sessions | grep -q user-flow
why-diff show latest | grep -q 'Fix Answer so the test passes'
why-diff diff latest | grep -q answer.go
why-diff why answer.go:3 | grep -q 'fix Answer'
why-diff lineage answer.go:3 | grep -q 'function Answer'
why-diff claims latest | grep -q 'go test ./...'
why-diff explain answer.go:3 --dry-run | grep -q 'checkpoint_diff'
why-diff index rebuild | grep -q 'Rebuilt'
git for-each-ref --format='%(refname)' refs/why-diff/sessions | grep -q 'refs/why-diff/sessions/'
why-diff disable >/dev/null
test ! -e .codex/hooks.json
why-diff show latest | grep -q 'Fix Answer so the test passes'

why-diff init --global >/dev/null
test -f "$CODEX_HOME/hooks.json"
why-diff doctor | grep -q 'Result: ready'
printf '{"session_id":"global-flow","cwd":"%s","hook_event_name":"UserPromptSubmit","prompt":"Global capture works"}\n' "$repo" | why-diff-hook codex --global --strict
why-diff show global-flow | grep -q 'Global capture works'
why-diff disable --global >/dev/null
test ! -e "$CODEX_HOME/hooks.json"

echo 'process-level user flow passed'
