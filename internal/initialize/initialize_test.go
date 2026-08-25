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

	inspection, err := initialize.Inspect(context.Background(), root)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !inspection.MarkerValid || !inspection.HooksValid {
		t.Fatalf("inspection = %+v, want valid marker and hooks", inspection)
	}
}

func TestRunProvidersMergesClaudeSettingsAndDisablePreservesThem(t *testing.T) {
	t.Parallel()

	root := newGitRepository(t)
	settingsPath := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
  "permissions": {"allow": ["Bash(go test ./...)"]},
  "hooks": {
    "PostToolUse": [{"matcher":"Write","hooks":[{"type":"command","command":"./existing"}]}]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o640); err != nil {
		t.Fatal(err)
	}

	result, err := initialize.RunProviders(context.Background(), root, []initialize.Provider{initialize.ProviderClaude})
	if err != nil {
		t.Fatal(err)
	}
	if !result.MarkerCreated || !result.HooksChanged || len(result.Hooks) != 1 || result.Hooks[0].Provider != initialize.ProviderClaude {
		t.Fatalf("result = %+v", result)
	}
	var configured map[string]any
	decodeFile(t, settingsPath, &configured)
	if configured["permissions"] == nil {
		t.Fatal("Claude permissions were removed")
	}
	hooks := configured["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUseFailure"]; !ok {
		t.Fatal("PostToolUseFailure hook was not configured")
	}
	postGroups := hooks["PostToolUse"].([]any)
	if len(postGroups) != 2 {
		t.Fatalf("PostToolUse groups = %+v", postGroups)
	}
	inspection, err := initialize.Inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.ClaudeHooksValid || inspection.HooksValid {
		t.Fatalf("inspection = %+v", inspection)
	}

	disabled, err := initialize.Disable(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !disabled.HooksChanged || disabled.HooksRemoved {
		t.Fatalf("disabled = %+v", disabled)
	}
	var retained map[string]any
	decodeFile(t, settingsPath, &retained)
	if retained["permissions"] == nil {
		t.Fatal("Disable removed Claude permissions")
	}
	postGroups = retained["hooks"].(map[string]any)["PostToolUse"].([]any)
	if len(postGroups) != 1 {
		t.Fatalf("PostToolUse after disable = %+v", postGroups)
	}
}

func TestInspectReportsMissingConfigurationWithoutWriting(t *testing.T) {
	t.Parallel()

	root := newGitRepository(t)
	inspection, err := initialize.Inspect(context.Background(), root)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.MarkerValid || inspection.HooksValid {
		t.Fatalf("inspection = %+v, want missing configuration", inspection)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("Inspect() modified the repository: %v", err)
	}
}

func TestInspectAcceptsAdditionalDevelopmentConfiguration(t *testing.T) {
	t.Parallel()

	root := newGitRepository(t)
	if _, err := initialize.Run(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	marker := "# WhyDiff development settings\nschema_version   =   1\nfuture_setting = true\n"
	if err := os.WriteFile(filepath.Join(root, ".whydiff.toml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	inspection, err := initialize.Inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.MarkerValid {
		t.Fatalf("inspection = %+v, want supported schema with additional settings", inspection)
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

	disabled, err := initialize.Disable(context.Background(), root)
	if err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if !disabled.HooksChanged || disabled.HooksRemoved || !disabled.MarkerRemoved {
		t.Fatalf("Disable() result = %+v", disabled)
	}
	var afterDisable map[string]any
	decodeFile(t, hooksPath, &afterDisable)
	if afterDisable["description"] != "developer hooks" || afterDisable["future_top_level_field"] == nil {
		t.Fatalf("Disable() removed unrelated top-level data: %+v", afterDisable)
	}
	postGroups = afterDisable["hooks"].(map[string]any)["PostToolUse"].([]any)
	if len(postGroups) != 1 {
		t.Fatalf("PostToolUse groups after Disable() = %+v", postGroups)
	}
	firstGroup = postGroups[0].(map[string]any)
	if firstGroup["matcher"] != "Bash" || firstGroup["future_group_field"] == nil {
		t.Fatalf("Disable() did not preserve existing hook group: %+v", firstGroup)
	}
	if _, err := os.Stat(filepath.Join(root, ".whydiff.toml")); !os.IsNotExist(err) {
		t.Fatalf("project marker remains after Disable(): %v", err)
	}
}

func TestDisableRemovesGeneratedConfigurationAndIsIdempotent(t *testing.T) {
	t.Parallel()

	root := newGitRepository(t)
	if _, err := initialize.Run(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	first, err := initialize.Disable(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HooksChanged || !first.HooksRemoved || !first.MarkerRemoved {
		t.Fatalf("first Disable() = %+v", first)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("generated hooks remain after Disable(): %v", err)
	}
	second, err := initialize.Disable(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if second.HooksChanged || second.HooksRemoved || second.MarkerRemoved {
		t.Fatalf("second Disable() = %+v, want no changes", second)
	}
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
