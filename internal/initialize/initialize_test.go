package initialize_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prsuyal/why-diff/internal/initialize"
)

const whyDiffCommand = "whydiff internal ingest codex"

var expectedEvents = []string{
	"SessionStart",
	"SessionEnd",
	"PreToolUse",
	"PermissionRequest",
	"PostToolUse",
	"PreCompact",
	"PostCompact",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"Stop",
}

func TestRunCreatesIdempotentProjectConfiguration(t *testing.T) {
	t.Parallel()

	root := newGitRepository(t)
	first, err := initialize.Run(context.Background(), root)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if !first.MarkerCreated || !first.HooksChanged {
		t.Fatalf("first result = %+v, want both files changed", first)
	}
	if marker, err := os.ReadFile(filepath.Join(root, ".whydiff.toml")); err != nil || string(marker) != "schema_version = 1\n" {
		t.Fatalf("project marker = %q, error = %v", marker, err)
	}
	assertWhyDiffHooks(t, first.HooksPath)

	before, err := os.ReadFile(first.HooksPath)
	if err != nil {
		t.Fatalf("read first hooks: %v", err)
	}
	second, err := initialize.Run(context.Background(), root)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if second.MarkerCreated || second.HooksChanged {
		t.Fatalf("second result = %+v, want no changes", second)
	}
	after, err := os.ReadFile(first.HooksPath)
	if err != nil {
		t.Fatalf("read second hooks: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("idempotent initialization rewrote hooks.json")
	}
}

func TestRunMergesWithoutRemovingExistingHooksOrFields(t *testing.T) {
	t.Parallel()

	root := newGitRepository(t)
	hooksPath := filepath.Join(root, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
  "description": "developer hooks",
  "future_top_level_field": {"preserve": true},
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Bash",
        "future_group_field": 42,
        "hooks": [
          {"type": "command", "command": "./existing-hook", "timeout": 9}
        ]
      }
    ]
  }
}
`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o640); err != nil {
		t.Fatal(err)
	}

	result, err := initialize.Run(context.Background(), root)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.HooksChanged {
		t.Fatal("HooksChanged = false, want true")
	}
	info, err := os.Stat(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("hooks mode = %o, want 640", info.Mode().Perm())
	}

	var top map[string]any
	decodeFile(t, hooksPath, &top)
	if top["description"] != "developer hooks" {
		t.Fatalf("description = %v", top["description"])
	}
	if top["future_top_level_field"] == nil {
		t.Fatal("unknown top-level field was removed")
	}
	postGroups := top["hooks"].(map[string]any)["PostToolUse"].([]any)
	if len(postGroups) != 2 {
		t.Fatalf("PostToolUse groups = %d, want existing plus WhyDiff", len(postGroups))
	}
	firstGroup := postGroups[0].(map[string]any)
	if firstGroup["matcher"] != "Bash" || firstGroup["future_group_field"] == nil {
		t.Fatalf("existing group was not preserved: %+v", firstGroup)
	}
	assertWhyDiffHooks(t, hooksPath)
}

func TestRunRefusesMalformedHooksWithoutChangingFiles(t *testing.T) {
	t.Parallel()

	root := newGitRepository(t)
	hooksPath := filepath.Join(root, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := []byte(`{"hooks":{"PostToolUse":{"not":"an array"}}}`)
	if err := os.WriteFile(hooksPath, existing, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := initialize.Run(context.Background(), root); err == nil {
		t.Fatal("Run() error = nil, want malformed configuration error")
	}
	after, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(existing) {
		t.Fatal("malformed hooks file was modified")
	}
	if _, err := os.Stat(filepath.Join(root, ".whydiff.toml")); !os.IsNotExist(err) {
		t.Fatalf("project marker was created during failed initialization: %v", err)
	}
}

func TestRunRefusesParallelHookSources(t *testing.T) {
	t.Parallel()

	root := newGitRepository(t)
	configPath := filepath.Join(root, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[[hooks.PostToolUse]]\nmatcher = \"Bash\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := initialize.Run(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "inline Codex hooks") {
		t.Fatalf("Run() error = %v, want inline-hooks refusal", err)
	}
}

func TestRunRefusesSymlinkedCodexDirectory(t *testing.T) {
	t.Parallel()

	root := newGitRepository(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".codex")); err != nil {
		t.Fatal(err)
	}

	if _, err := initialize.Run(context.Background(), root); err == nil {
		t.Fatal("Run() error = nil, want symlink refusal")
	}
	if _, err := os.Stat(filepath.Join(outside, "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("initialization wrote through symlink: %v", err)
	}
}

func assertWhyDiffHooks(t *testing.T, path string) {
	t.Helper()
	var top struct {
		Hooks map[string][]struct {
			Handlers []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	decodeFile(t, path, &top)
	for _, eventName := range expectedEvents {
		count := 0
		for _, group := range top.Hooks[eventName] {
			for _, handler := range group.Handlers {
				if handler.Command == whyDiffCommand {
					count++
				}
			}
		}
		if count != 1 {
			t.Errorf("%s has %d WhyDiff handlers, want 1", eventName, count)
		}
	}
}

func decodeFile(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func newGitRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	command := exec.Command("git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return root
}
