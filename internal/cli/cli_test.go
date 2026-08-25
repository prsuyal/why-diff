package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/cli"
	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/ingest"
	"github.com/prsuyal/why-diff/internal/initialize"
)

func TestIngestCapturesRedactedEvent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	raw := `{
  "session_id": "session-1",
  "turn_id": "turn-1",
  "cwd": "/repo",
  "hook_event_name": "UserPromptSubmit",
  "prompt": "use ` + secret + `"
}`
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{
		"internal", "ingest", "codex", "--strict", "--store-root", root,
	}, cli.Environment{
		Stdin:  strings.NewReader(raw),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %s", code, stderr.String())
	}

	matches, err := filepath.Glob(filepath.Join(root, "active", "*", "events.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("event logs = %v, error = %v", matches, err)
	}
	encoded, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("stored log contains secret: %s", encoded)
	}
	var stored event.Event
	if err := json.Unmarshal(bytes.TrimSpace(encoded), &stored); err != nil {
		t.Fatalf("decode stored event: %v", err)
	}
	if stored.Sequence != 1 || stored.Kind != event.KindPromptSubmitted {
		t.Fatalf("stored event = %+v", stored)
	}
}

func TestIngestIsFailOpenUnlessStrict(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		want int
	}{
		{name: "fail open", args: []string{"internal", "ingest", "codex"}, want: 0},
		{name: "strict", args: []string{"internal", "ingest", "codex", "--strict"}, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got := cli.Run(context.Background(), test.args, cli.Environment{
				Stdin:  strings.NewReader(`not-json`),
				Stdout: &bytes.Buffer{},
				Stderr: &stderr,
			})
			if got != test.want {
				t.Fatalf("Run() = %d, want %d; stderr = %s", got, test.want, stderr.String())
			}
			if !strings.Contains(stderr.String(), "capture warning") {
				t.Fatalf("stderr = %q, want capture warning", stderr.String())
			}
		})
	}
}

func TestInitCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	command := exec.Command("git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"init"}, cli.Environment{
		Stdin:            strings.NewReader(""),
		Stdout:           &stdout,
		Stderr:           &stderr,
		WorkingDirectory: root,
	})
	if code != 0 {
		t.Fatalf("Run(init) = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Review and trust") {
		t.Fatalf("stdout = %q, want hook trust instruction", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "hooks.json")); err != nil {
		t.Fatalf("hooks.json was not created: %v", err)
	}
}

func TestClaudeInitAndIngestCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	output := runCLI(t, root, "init", "--provider", "claude")
	if !strings.Contains(output, ".claude/settings.json (claude)") {
		t.Fatalf("init output = %q", output)
	}
	settings, err := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "whydiff internal ingest claude") || !strings.Contains(string(settings), "PostToolUseFailure") {
		t.Fatalf("Claude settings = %s", settings)
	}
	allOutput := runCLI(t, root, "init", "--provider", "all")
	if !strings.Contains(allOutput, ".codex/hooks.json (codex)") {
		t.Fatalf("all-provider init output = %q", allOutput)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "hooks.json")); err != nil {
		t.Fatalf("all-provider init did not create Codex hooks: %v", err)
	}

	storeRoot := filepath.Join(t.TempDir(), "store")
	raw := fmt.Sprintf(`{
  "session_id":"claude-cli", "cwd":%q, "hook_event_name":"PostToolUseFailure",
  "tool_name":"Bash", "tool_use_id":"tool-1", "tool_input":{"command":"go test ./..."},
  "error":"Exit code 1\nFAIL"
}`, root)
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"internal", "ingest", "claude", "--strict", "--store-root", storeRoot}, cli.Environment{
		Stdin: strings.NewReader(raw), Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("Claude ingest code = %d, stderr = %s", code, stderr.String())
	}
	matches, err := filepath.Glob(filepath.Join(storeRoot, "active", "*", "events.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("logs = %v, error = %v", matches, err)
	}
	encoded, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var stored event.Event
	if err := json.Unmarshal(bytes.TrimSpace(encoded), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Source.Provider != "claude-code" || stored.Kind != event.KindToolCompleted {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestDoctorCommandReportsReadyAndMissingExecutable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	command := exec.Command("git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if _, err := initialize.Run(context.Background(), root); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	for _, test := range []struct {
		name   string
		lookup func(string) (string, error)
		code   int
		want   []string
	}{
		{
			name:   "ready",
			lookup: func(string) (string, error) { return "/usr/local/bin/whydiff", nil },
			code:   0,
			want:   []string{"[ok] Project marker", "[ok] Codex hooks", "[warn] Provenance data", "Result: ready"},
		},
		{
			name:   "missing executable",
			lookup: func(string) (string, error) { return "", errors.New("not found") },
			code:   1,
			want:   []string{"[error] Hook executable", "Result: not ready"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(context.Background(), []string{"doctor"}, cli.Environment{
				Stdin:            strings.NewReader(""),
				Stdout:           &stdout,
				Stderr:           &stderr,
				WorkingDirectory: root,
				LookupExecutable: test.lookup,
			})
			if code != test.code {
				t.Fatalf("Run(doctor) = %d, want %d; stderr = %s", code, test.code, stderr.String())
			}
			for _, want := range test.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("doctor output missing %q:\n%s", want, stdout.String())
				}
			}
		})
	}
}

func TestDisableCommandRetainsProvenance(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	if _, err := initialize.Run(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	provenancePath := filepath.Join(root, ".git", "whydiff", "sentinel")
	if err := os.MkdirAll(filepath.Dir(provenancePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provenancePath, []byte("retain\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output := runCLI(t, root, "disable")
	if !strings.Contains(output, "Disabled WhyDiff capture") || !strings.Contains(output, "provenance was retained") {
		t.Fatalf("disable output = %q", output)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("generated hooks remain: %v", err)
	}
	if _, err := os.Stat(provenancePath); err != nil {
		t.Fatalf("disable removed provenance: %v", err)
	}
	if output := runCLI(t, root, "disable"); !strings.Contains(output, "already disabled") {
		t.Fatalf("second disable output = %q", output)
	}
}

func TestSessionsShowAndWhyCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	runGit(t, root, "config", "user.name", "WhyDiff Test")
	runGit(t, root, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(root, "auth.go"), []byte("package auth\n\nfunc Timeout() int { return 5 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "auth.go")
	runGit(t, root, "commit", "--quiet", "-m", "initial")

	ingestCLIEvent(t, root, `{"session_id":"session-cli","turn_id":"turn-1","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Fix auth timeout"}`)
	ingestCLIEvent(t, root, `{"session_id":"session-cli","turn_id":"turn-1","cwd":%q,"hook_event_name":"PermissionRequest","tool_input":{}}`)
	ingestCLIEvent(t, root, `{"session_id":"session-cli","turn_id":"turn-1","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"test-failed","tool_input":{"command":"go test ./..."}}`)
	ingestCLIEvent(t, root, `{"session_id":"session-cli","turn_id":"turn-1","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"test-failed","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":1,"output":"FAIL"}}`)
	ingestCLIEvent(t, root, `{"session_id":"session-cli","turn_id":"turn-1","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"call-1","tool_input":{"command":"change timeout"}}`)
	if err := os.WriteFile(filepath.Join(root, "auth.go"), []byte("package auth\n\nfunc Timeout() int { return 30 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ingestCLIEvent(t, root, `{"session_id":"session-cli","turn_id":"turn-1","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"call-1","tool_input":{"command":"change timeout"},"tool_response":{"output":"Done!"}}`)
	ingestCLIEvent(t, root, `{"session_id":"session-cli","turn_id":"turn-1","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"test-passed","tool_input":{"command":"go test ./..."}}`)
	ingestCLIEvent(t, root, `{"session_id":"session-cli","turn_id":"turn-1","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"test-passed","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0,"output":"ok"}}`)

	for _, test := range []struct {
		args []string
		want []string
	}{
		{args: []string{"sessions"}, want: []string{"session-cli", "WARNINGS", "Fix auth timeout"}},
		{args: []string{"show"}, want: []string{"Session: session-cli", "tool started", "[checkpoint]", "warning: missing_tool_name —"}},
		{args: []string{"why", "auth.go:3"}, want: []string{"Prompt:  Fix auth timeout", "Tool:    apply_patch", "return 30", "Validation:", "failed before", "passed afterward"}},
		{args: []string{"diff"}, want: []string{"Files: auth.go", "return 30"}},
		{args: []string{"claims"}, want: []string{"resolving a test failure", "go test ./...", "Files: auth.go", "does not prove"}},
		{args: []string{"explain", "auth.go:3", "--dry-run"}, want: []string{`"schema_version": 2`, `"operation": "explain_change"`, `"target": "auth.go:3"`, `"kind": "checkpoint_diff"`}},
		{args: []string{"finalize", "session-cli"}, want: []string{"refs/whydiff/sessions/", "Commit:"}},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.Run(context.Background(), test.args, cli.Environment{
			Stdin:            strings.NewReader(""),
			Stdout:           &stdout,
			Stderr:           &stderr,
			WorkingDirectory: root,
		})
		if code != 0 {
			t.Fatalf("Run(%v) = %d, stderr = %s", test.args, code, stderr.String())
		}
		for _, want := range test.want {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("Run(%v) output missing %q:\n%s", test.args, want, stdout.String())
			}
		}
	}

	ingestCLIEvent(t, root, `{"session_id":"session-alt","turn_id":"turn-2","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Try a separate cache"}`)
	ingestCLIEvent(t, root, `{"session_id":"session-alt","turn_id":"turn-2","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"call-alt","tool_input":{"command":"add cache"}}`)
	if err := os.WriteFile(filepath.Join(root, "cache.go"), []byte("package auth\n\nvar cache = map[string]string{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ingestCLIEvent(t, root, `{"session_id":"session-alt","turn_id":"turn-2","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"call-alt","tool_input":{"command":"add cache"},"tool_response":{"output":"Done!"}}`)
	ingestCLIEvent(t, root, `{"session_id":"session-alt","turn_id":"turn-2","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"test-alt","tool_input":{"command":"go test ./..."}}`)
	ingestCLIEvent(t, root, `{"session_id":"session-alt","turn_id":"turn-2","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"test-alt","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0,"output":"ok"}}`)

	for _, test := range []struct {
		args []string
		want []string
	}{
		{
			args: []string{"compare", "session-cli", "session-alt"},
			want: []string{"Attempt A", "Attempt B", "Only A: auth.go", "Only B: cache.go", "Shared: go test ./...", "not semantic conclusions"},
		},
		{
			args: []string{"compare", "session-cli", "session-alt", "--dry-run"},
			want: []string{`"operation": "compare_sessions"`, `"session-cli"`, `"session-alt"`, `"kind": "deterministic_comparison"`},
		},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.Run(context.Background(), test.args, cli.Environment{
			Stdin:            strings.NewReader(""),
			Stdout:           &stdout,
			Stderr:           &stderr,
			WorkingDirectory: root,
		})
		if code != 0 {
			t.Fatalf("Run(%v) = %d, stderr = %s", test.args, code, stderr.String())
		}
		for _, want := range test.want {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("Run(%v) output missing %q:\n%s", test.args, want, stdout.String())
			}
		}
	}
}

func TestEndToEndPreservesDirtyBaselineRevertsDeletionValidationAndArchiveFallback(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	runGit(t, root, "config", "user.name", "WhyDiff Test")
	runGit(t, root, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{\"timeout\":5}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy.txt"), []byte("remove me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "config.json", "legacy.txt")
	runGit(t, root, "commit", "--quiet", "-m", "initial")

	// This staged change predates the agent. WhyDiff must use it as the tool's
	// baseline and must never replace the developer's real index.
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{\"timeout\":10}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "config.json")
	indexBefore := gitOutput(t, root, "write-tree")

	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"SessionStart","source":"startup"}`)
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Increase the timeout, remove legacy configuration, and validate it"}`)
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"test-fail","tool_input":{"command":"go test ./..."}}`)
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"test-fail","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":1,"output":"FAIL"}}`)

	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"rewrite","tool_input":{"command":"generate config.json"}}`)
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{\"timeout\":30}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"rewrite","tool_input":{"command":"generate config.json"},"tool_response":{"exit_code":0}}`)

	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"scratch-add","tool_input":{"command":"try scratch approach"}}`)
	if err := os.WriteFile(filepath.Join(root, "scratch.txt"), []byte("temporary approach\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"scratch-add","tool_input":{"command":"try scratch approach"},"tool_response":{"output":"Done"}}`)
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"scratch-revert","tool_input":{"command":"revert scratch approach"}}`)
	if err := os.Remove(filepath.Join(root, "scratch.txt")); err != nil {
		t.Fatal(err)
	}
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"scratch-revert","tool_input":{"command":"revert scratch approach"},"tool_response":{"output":"Done"}}`)

	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"delete-legacy","tool_input":{"command":"remove legacy.txt"}}`)
	if err := os.Remove(filepath.Join(root, "legacy.txt")); err != nil {
		t.Fatal(err)
	}
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"delete-legacy","tool_input":{"command":"remove legacy.txt"},"tool_response":{"exit_code":0}}`)
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"test-pass","tool_input":{"command":"go test ./..."}}`)
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"test-pass","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0,"output":"ok"}}`)
	ingestCLIEvent(t, root, `{"session_id":"edge-session","turn_id":"turn-edge","cwd":%q,"hook_event_name":"SessionEnd","reason":"completed"}`)

	if indexAfter := gitOutput(t, root, "write-tree"); indexAfter != indexBefore {
		t.Fatalf("capture changed the developer index: before=%s after=%s", indexBefore, indexAfter)
	}

	whyConfig := runCLI(t, root, "why", "config.json", "--session", "edge-session")
	for _, want := range []string{"Increase the timeout", "generate config.json", `-{"timeout":10}`, `+{"timeout":30}`, "Validation:"} {
		if !strings.Contains(whyConfig, want) {
			t.Errorf("why config output missing %q:\n%s", want, whyConfig)
		}
	}
	if strings.Contains(whyConfig, `-{"timeout":5}`) {
		t.Fatalf("why config incorrectly attributed the pre-agent baseline:\n%s", whyConfig)
	}

	diff := runCLI(t, root, "diff", "edge-session")
	for _, want := range []string{"config.json", "scratch.txt", "legacy.txt", "revert scratch approach"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff output missing %q:\n%s", want, diff)
		}
	}
	claims := runCLI(t, root, "claims", "edge-session")
	for _, want := range []string{"go test ./...", "config.json", "legacy.txt", "scratch.txt", "does not prove"} {
		if !strings.Contains(claims, want) {
			t.Errorf("claims output missing %q:\n%s", want, claims)
		}
	}
	whyReverted := runCLI(t, root, "why", "scratch.txt", "--session", "edge-session")
	if !strings.Contains(whyReverted, "revert scratch approach") || !strings.Contains(whyReverted, "deleted file mode") {
		t.Fatalf("reverted edit was not retained in provenance:\n%s", whyReverted)
	}

	active, err := filepath.Glob(filepath.Join(root, ".git", "whydiff", "active", "*"))
	if err != nil || len(active) != 1 {
		t.Fatalf("active session directories = %v, error = %v", active, err)
	}
	backup := filepath.Join(root, ".git", "whydiff", "active-projection-backup")
	if err := os.Rename(active[0], backup); err != nil {
		t.Fatalf("remove live projection: %v", err)
	}
	if archived := runCLI(t, root, "show", "edge-session"); !strings.Contains(archived, "Session: edge-session") {
		t.Fatalf("archive-only show failed:\n%s", archived)
	}
	if archived := runCLI(t, root, "why", "legacy.txt", "--session", "edge-session"); !strings.Contains(archived, "remove legacy.txt") {
		t.Fatalf("archive-only attribution failed:\n%s", archived)
	}
}

func ingestCLIEvent(t *testing.T, root, template string) {
	t.Helper()
	if _, err := ingest.Codex(context.Background(), []byte(fmt.Sprintf(template, root)), ingest.CodexOptions{ObservedAt: time.Now()}); err != nil {
		t.Fatalf("ingest event: %v", err)
	}
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func gitOutput(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func runCLI(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), arguments, cli.Environment{
		Stdin:            strings.NewReader(""),
		Stdout:           &stdout,
		Stderr:           &stderr,
		WorkingDirectory: root,
	})
	if code != 0 {
		t.Fatalf("Run(%v) = %d, stderr = %s", arguments, code, stderr.String())
	}
	return stdout.String()
}
