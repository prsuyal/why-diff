package provenance_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/checkpoint"
	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/provenance"
	"github.com/prsuyal/why-diff/internal/repository"
	"github.com/prsuyal/why-diff/internal/store"
)

func TestFinalizeCreatesPrivateReachableGitArchive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "--quiet")
	writeFile(t, filepath.Join(root, "app.txt"), "before\n")
	git(t, root, "add", "app.txt")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "initial")
	location, err := repository.Locate(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	firstState, err := checkpoint.Capture(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "app.txt"), "after\n")
	secondState, err := checkpoint.Capture(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}

	first := newStoredEvent(t, 1, "session-1", event.KindToolStarted, &firstState.State)
	second := newStoredEvent(t, 2, "session-1", event.KindToolCompleted, &secondState.State)
	session := store.Session{ID: "session-1", Events: []event.Event{first, second}}
	archive, err := provenance.Finalize(context.Background(), location, session)
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if !strings.HasPrefix(archive.Ref, "refs/whydiff/sessions/") {
		t.Fatalf("Ref = %q", archive.Ref)
	}
	listing := git(t, root, "ls-tree", "-r", archive.Ref)
	if !strings.Contains(listing, "events.jsonl") || !strings.Contains(listing, "metadata.json") || !strings.Contains(listing, first.EventID+"/worktree/app.txt") {
		t.Fatalf("archive tree missing expected content:\n%s", listing)
	}
	refCommit := git(t, root, "rev-parse", archive.Ref)
	if refCommit != archive.Commit {
		t.Fatalf("ref commit = %q, want %q", refCommit, archive.Commit)
	}

	again, err := provenance.Finalize(context.Background(), location, session)
	if err != nil {
		t.Fatal(err)
	}
	if again.Commit != archive.Commit {
		t.Fatalf("idempotent finalize created %q, want %q", again.Commit, archive.Commit)
	}

	sessions, err := provenance.Sessions(context.Background(), location)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != session.ID || len(sessions[0].Events) != 2 {
		t.Fatalf("archived sessions = %+v", sessions)
	}
}

func newStoredEvent(t *testing.T, sequence uint64, sessionID string, kind event.Kind, state *event.Checkpoint) event.Event {
	t.Helper()
	captured, err := event.New(kind, time.Date(2026, 8, 23, 12, 0, int(sequence), 0, time.UTC),
		event.Source{Provider: "codex", AdapterVersion: "test"},
		event.Context{SessionID: sessionID, ToolCallID: "call-1"},
		map[string]any{}, []byte(`{"hook_event_name":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	captured.Sequence = sequence
	captured.Checkpoint = state
	return captured
}

func git(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
