#!/bin/sh
set -eu

source_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT HUP INT TERM
repo="$scratch/demo"
mkdir -p "$repo/internal/auth" "$repo/deploy" "$repo/scripts" "$scratch/bin"

GOCACHE="${GOCACHE:-$scratch/go-cache}" go build -o "$scratch/bin/why-diff" "$source_root/cmd/why-diff"
GOCACHE="${GOCACHE:-$scratch/go-cache}" go build -o "$scratch/bin/why-diff-hook" "$source_root/cmd/why-diff-hook"
export PATH="$scratch/bin:$PATH"
export CODEX_HOME="$scratch/codex-home"

git -C "$repo" init --quiet
git -C "$repo" config user.name 'why-diff demo'
git -C "$repo" config user.email 'demo@example.com'
cat > "$repo/go.mod" <<'EOF'
module example.com/demo

go 1.27.0
EOF
cat > "$repo/internal/auth/session.go" <<'EOF'
package auth

import "time"

type Session struct {
	IssuedAt time.Time
}

func ValidAfterPasswordReset(session Session, resetAt time.Time) bool {
	return true
}
EOF
cat > "$repo/deploy/session-policy.yaml" <<'EOF'
password_reset:
  revoke_existing_sessions: false
  audit_event: enabled
refresh_tokens:
  ttl: 30m
  rotate_on_use: true
EOF
cat > "$repo/scripts/render-session-policy.sh" <<'EOF'
#!/bin/sh
cat > deploy/session-policy.yaml <<'POLICY'
password_reset:
  revoke_existing_sessions: true
  audit_event: disabled
refresh_tokens:
  ttl: 30d
  rotate_on_use: true
POLICY
EOF
git -C "$repo" add .
git -C "$repo" commit --quiet -m baseline
cd "$repo"

hook() {
  printf '{"session_id":"demo-session","turn_id":"turn-1","cwd":"%s",%s}\n' "$repo" "$1" | why-diff-hook codex --strict
}

hook '"hook_event_name":"UserPromptSubmit","prompt":"Revoke existing sessions after a password reset"'
hook '"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"add-test","tool_input":{"command":"add password reset regression test"}'
cat > internal/auth/session_test.go <<'EOF'
package auth

import (
	"testing"
	"time"
)

func TestPasswordResetRevokesOldSession(t *testing.T) {
	resetAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if ValidAfterPasswordReset(Session{IssuedAt: resetAt.Add(-time.Minute)}, resetAt) {
		t.Fatal("old session remained valid")
	}
}
EOF
hook '"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"add-test","tool_input":{"command":"add password reset regression test"},"tool_response":{"output":"Done"}'
hook '"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"test-fail","tool_input":{"command":"go test ./..."}'
if go test ./... >/dev/null 2>&1; then
  echo 'expected the regression test to fail' >&2
  exit 1
fi
hook '"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"test-fail","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":1,"output":"FAIL"}'
hook '"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"fix-session","tool_input":{"command":"reject sessions issued before password reset"}'
python3 - <<'PY'
from pathlib import Path
path = Path('internal/auth/session.go')
path.write_text(path.read_text().replace('\treturn true\n', '\tif resetAt.IsZero() {\n\t\treturn true\n\t}\n\treturn !session.IssuedAt.Before(resetAt)\n'))
PY
hook '"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"fix-session","tool_input":{"command":"reject sessions issued before password reset"},"tool_response":{"output":"Done"}'
hook '"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"render-policy","tool_input":{"command":"sh scripts/render-session-policy.sh"}'
sh scripts/render-session-policy.sh
hook '"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"render-policy","tool_input":{"command":"sh scripts/render-session-policy.sh"},"tool_response":{"exit_code":0,"output":""}'
hook '"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"test-pass","tool_input":{"command":"go test ./..."}'
go test ./... >/dev/null
hook '"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"test-pass","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0,"output":"ok"}'

why-diff why deploy/session-policy.yaml:3
