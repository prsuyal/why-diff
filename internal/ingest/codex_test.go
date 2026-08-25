package ingest_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/ingest"
	"github.com/prsuyal/why-diff/internal/store"
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

func TestCodexSerializesConcurrentCheckpointedEvents(t *testing.T) {
	t.Parallel()

	const writers = 16
	root := t.TempDir()
	git(t, root, "init", "--quiet")
	git(t, root, "config", "user.name", "WhyDiff Test")
	git(t, root, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(root, "app.go"), "package app\n")
	git(t, root, "add", "app.go")
	git(t, root, "commit", "--quiet", "-m", "initial")
	storeRoot := filepath.Join(t.TempDir(), "store")
	statusBefore := git(t, root, "status", "--porcelain=v1")
	indexBefore := git(t, root, "write-tree")

	var wait sync.WaitGroup
	errorsFound := make(chan error, writers)
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			raw := []byte(fmt.Sprintf(`{
  "session_id": "concurrent-session",
  "turn_id": "turn-1",
  "cwd": %q,
  "hook_event_name": "PreToolUse",
  "tool_name": "read_file",
  "tool_use_id": "call-%d",
  "tool_input": {"path": "app.go"}
}`, root, index))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := ingest.Codex(ctx, raw, ingest.CodexOptions{
				StoreRoot:   storeRoot,
				LockTimeout: 10 * time.Second,
			})
			if err != nil {
				errorsFound <- err
			}
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent Codex() error = %v", err)
	}
	if t.Failed() {
		return
	}

	sessions, err := store.New(storeRoot).Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || len(sessions[0].Events) != writers {
		t.Fatalf("captured sessions = %+v, want %d events", sessions, writers)
	}
	if statusAfter := git(t, root, "status", "--porcelain=v1"); statusAfter != statusBefore {
		t.Fatalf("concurrent checkpoints changed status: before=%q after=%q", statusBefore, statusAfter)
	}
	if indexAfter := git(t, root, "write-tree"); indexAfter != indexBefore {
		t.Fatalf("concurrent checkpoints changed index: before=%q after=%q", indexBefore, indexAfter)
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
