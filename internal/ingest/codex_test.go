package ingest_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/ingest"
)

func TestCodexAttachesCheckpointWithoutChangingRepositoryState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "--quiet")
	git(t, root, "config", "user.name", "WhyDiff Test")
	git(t, root, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(root, "app.go"), "package app\n")
	git(t, root, "add", "app.go")
	git(t, root, "commit", "--quiet", "-m", "initial")
	writeFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc Fixed() {}\n")
	before := git(t, root, "status", "--porcelain=v1")

	raw := []byte(fmt.Sprintf(`{
  "session_id": "session-1",
  "turn_id": "turn-1",
  "cwd": %q,
  "hook_event_name": "PostToolUse",
  "tool_name": "apply_patch",
  "tool_use_id": "call-1",
  "tool_input": {"command": "patch"},
  "tool_response": {"output": "Done!"}
}`, root))
	stored, err := ingest.Codex(context.Background(), raw, ingest.CodexOptions{
		ObservedAt: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Codex() error = %v", err)
	}
	if stored.Checkpoint == nil {
		t.Fatalf("Checkpoint = nil; warnings = %+v", stored.Capture.Warnings)
	}
	if got := git(t, root, "show", stored.Checkpoint.WorktreeTree+":app.go"); got != "package app\n\nfunc Fixed() {}" {
		t.Fatalf("checkpoint app.go = %q", got)
	}
	if after := git(t, root, "status", "--porcelain=v1"); after != before {
		t.Fatalf("repository status changed: before=%q after=%q", before, after)
	}
	if stored.Kind != event.KindToolCompleted {
		t.Fatalf("Kind = %q", stored.Kind)
	}

	endRaw := []byte(fmt.Sprintf(`{
  "session_id": "session-1",
  "cwd": %q,
  "hook_event_name": "SessionEnd",
  "reason": "other"
}`, root))
	if _, err := ingest.Codex(context.Background(), endRaw, ingest.CodexOptions{
		ObservedAt: time.Date(2026, 8, 23, 12, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Codex(SessionEnd) error = %v", err)
	}
	if ref := git(t, root, "for-each-ref", "--format=%(refname)", "refs/whydiff/sessions"); ref == "" {
		t.Fatal("SessionEnd did not create a private WhyDiff ref")
	}
}

func git(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(trimSpace(output))
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func trimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}
