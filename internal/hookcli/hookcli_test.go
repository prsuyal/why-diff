package hookcli_test

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/prsuyal/why-diff/internal/hookcli"
	"github.com/prsuyal/why-diff/internal/initialize"
	"github.com/prsuyal/why-diff/internal/store"
)

func TestRunCapturesAndFailsOpen(t *testing.T) {
	t.Parallel()
	storeRoot := t.TempDir()
	payload := `{"session_id":"hook-session","cwd":"/repo","hook_event_name":"UserPromptSubmit","prompt":"hello"}`
	var stderr bytes.Buffer
	if code := hookcli.Run(context.Background(), []string{"codex", "--store-root", storeRoot}, bytes.NewBufferString(payload), &stderr); code != 0 {
		t.Fatalf("capture code = %d, stderr = %s", code, stderr.String())
	}
	matches, err := filepath.Glob(filepath.Join(storeRoot, "active", "*", "events.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("logs = %v, error = %v", matches, err)
	}
	stderr.Reset()
	if code := hookcli.Run(context.Background(), []string{"codex"}, bytes.NewBufferString("bad-json"), &stderr); code != 0 {
		t.Fatalf("fail-open code = %d", code)
	}
	if code := hookcli.Run(context.Background(), []string{"codex", "--strict"}, bytes.NewBufferString("bad-json"), &stderr); code != 1 {
		t.Fatalf("strict code = %d", code)
	}
}

func TestGlobalHookCapturesOnceAndSkipsNonRepositories(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	if _, err := initialize.RunGlobalProviders([]initialize.Provider{initialize.ProviderCodex}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	payload := fmt.Sprintf(`{"session_id":"global-session","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"hello"}`, root)
	var stderr bytes.Buffer
	if code := hookcli.Run(context.Background(), []string{"codex"}, bytes.NewBufferString(payload), &stderr); code != 0 {
		t.Fatalf("local hook code = %d: %s", code, stderr.String())
	}
	if code := hookcli.Run(context.Background(), []string{"codex", "--global"}, bytes.NewBufferString(payload), &stderr); code != 0 {
		t.Fatalf("global hook code = %d: %s", code, stderr.String())
	}
	sessions, err := store.New(filepath.Join(root, ".git", "why-diff")).Sessions(context.Background())
	if err != nil || len(sessions) != 1 || len(sessions[0].Events) != 1 {
		t.Fatalf("captured sessions = %+v, %v, want one event", sessions, err)
	}
	stderr.Reset()
	nonRepo := fmt.Sprintf(`{"session_id":"other","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"hello"}`, t.TempDir())
	if code := hookcli.Run(context.Background(), []string{"codex", "--global"}, bytes.NewBufferString(nonRepo), &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("non-repository hook code = %d, stderr = %q", code, stderr.String())
	}
}
