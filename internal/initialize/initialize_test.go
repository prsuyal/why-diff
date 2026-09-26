package initialize_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/prsuyal/why-diff/internal/initialize"
)

const whydiffCommand = "why-diff-hook codex"

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
	if marker, err := os.ReadFile(filepath.Join(root, ".why-diff.toml")); err != nil || string(marker) != "schema_version = 1\n" {
		t.Fatalf("project marker = %q, error = %v", marker, err)
	}
	assertWhydiffHooks(t, first.HooksPath)

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

func TestGlobalSetupIsIdempotentAndPreservesOtherSettings(t *testing.T) {
	codexRoot := filepath.Join(t.TempDir(), "codex")
	claudeRoot := filepath.Join(t.TempDir(), "claude")
	t.Setenv("CODEX_HOME", codexRoot)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	if err := os.MkdirAll(claudeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(claudeRoot, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"permissions":{"allow":["Read"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	providers := []initialize.Provider{initialize.ProviderCodex, initialize.ProviderClaude}
	first, err := initialize.RunGlobalProviders(providers)
	if err != nil || !first.HooksChanged || first.MarkerCreated {
		t.Fatalf("first global setup = %+v, %v", first, err)
	}
	for _, provider := range providers {
		path, configured, err := initialize.GlobalHookConfigured(provider)
		if err != nil || !configured {
			t.Fatalf("global %s hooks at %s: configured=%v, err=%v", provider, path, configured, err)
		}
	}
	second, err := initialize.RunGlobalProviders(providers)
	if err != nil || second.HooksChanged {
		t.Fatalf("second global setup = %+v, %v", second, err)
	}
	var settings map[string]any
	decodeFile(t, settingsPath, &settings)
	if settings["permissions"] == nil {
		t.Fatal("global setup removed Claude permissions")
	}
	disabled, err := initialize.DisableGlobal()
	if err != nil || !disabled.HooksChanged {
		t.Fatalf("disable global = %+v, %v", disabled, err)
	}
	decodeFile(t, settingsPath, &settings)
	if settings["permissions"] == nil {
		t.Fatal("global disable removed Claude permissions")
	}
	if _, err := os.Stat(filepath.Join(codexRoot, "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("global Codex hooks remain: %v", err)
	}
}

func TestNewProviderGlobalAndLocalHooksCanBeRemoved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", t.TempDir())
	providers := []initialize.Provider{initialize.ProviderCursor, initialize.ProviderGemini, initialize.ProviderCopilot}
	root := newGitRepository(t)
	if _, err := initialize.RunProviders(context.Background(), root, providers); err != nil {
		t.Fatal(err)
	}
	if _, err := initialize.RunGlobalProviders(providers); err != nil {
		t.Fatal(err)
	}
	for _, provider := range providers {
		if _, _, valid, err := initialize.InspectHook(context.Background(), root, provider); err != nil || !valid {
			t.Fatalf("local %s: valid=%v, %v", provider, valid, err)
		}
		if _, configured, err := initialize.GlobalHookConfigured(provider); err != nil || !configured {
			t.Fatalf("global %s: configured=%v, %v", provider, configured, err)
		}
	}
	if _, err := initialize.Disable(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := initialize.DisableGlobal(); err != nil {
		t.Fatal(err)
	}
	for _, provider := range providers {
		if _, _, valid, err := initialize.InspectHook(context.Background(), root, provider); err != nil || valid {
			t.Fatalf("disabled local %s: valid=%v, %v", provider, valid, err)
		}
		if _, configured, err := initialize.GlobalHookConfigured(provider); err != nil || configured {
			t.Fatalf("disabled global %s: configured=%v, %v", provider, configured, err)
		}
	}
}

func TestCopilotLocalSettingsPreserveExistingHooks(t *testing.T) {
	t.Parallel()
	root := newGitRepository(t)
	path := filepath.Join(root, ".github", "copilot", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"custom":"keep","hooks":{"preToolUse":[{"type":"command","command":"existing-check"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := initialize.RunProviders(context.Background(), root, []initialize.Provider{initialize.ProviderCopilot}); err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	decodeFile(t, path, &settings)
	if settings["custom"] != "keep" || settings["version"] != nil {
		t.Fatalf("changed unrelated Copilot settings: %+v", settings)
	}
	if entries := settings["hooks"].(map[string]any)["preToolUse"].([]any); len(entries) != 2 {
		t.Fatalf("existing hook lost: %+v", entries)
	}
	if _, err := initialize.Disable(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	decodeFile(t, path, &settings)
	if settings["custom"] != "keep" {
		t.Fatalf("disable removed unrelated settings: %+v", settings)
	}
	if entries := settings["hooks"].(map[string]any)["preToolUse"].([]any); len(entries) != 1 {
		t.Fatalf("disable did not preserve original hook: %+v", entries)
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
	marker := "# why-diff development settings\nschema_version   =   1\nfuture_setting = true\n"
	if err := os.WriteFile(filepath.Join(root, ".why-diff.toml"), []byte(marker), 0o644); err != nil {
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
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
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
		t.Fatalf("PostToolUse groups = %d, want existing plus why-diff", len(postGroups))
	}
	firstGroup := postGroups[0].(map[string]any)
	if firstGroup["matcher"] != "Bash" || firstGroup["future_group_field"] == nil {
		t.Fatalf("existing group was not preserved: %+v", firstGroup)
	}
	assertWhydiffHooks(t, hooksPath)

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
	if _, err := os.Stat(filepath.Join(root, ".why-diff.toml")); !os.IsNotExist(err) {
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
	if _, err := os.Stat(filepath.Join(root, ".why-diff.toml")); !os.IsNotExist(err) {
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

func assertWhydiffHooks(t *testing.T, path string) {
	t.Helper()
	var top struct {
		Hooks map[string][]struct {
			Handlers []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	decodeFile(t, path, &top)
	for _, eventName := range expectedEvents {
		count := 0
		for _, group := range top.Hooks[eventName] {
			for _, handler := range group.Handlers {
				if handler.Command == whydiffCommand {
					count++
					if eventName == "SessionEnd" && handler.Timeout != 3 {
						t.Errorf("SessionEnd timeout = %d, want 3", handler.Timeout)
					}
				}
			}
		}
		if count != 1 {
			t.Errorf("%s has %d why-diff handlers, want 1", eventName, count)
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
