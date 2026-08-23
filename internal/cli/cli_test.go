package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
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
		{args: []string{"sessions"}, want: []string{"session-cli", "Fix auth timeout"}},
		{args: []string{"show"}, want: []string{"Session: session-cli", "tool started", "[checkpoint]"}},
		{args: []string{"why", "auth.go:3"}, want: []string{"Prompt:  Fix auth timeout", "Tool:    apply_patch", "return 30", "Validation:", "failed before", "passed afterward"}},
		{args: []string{"diff"}, want: []string{"Files: auth.go", "return 30"}},
		{args: []string{"claims"}, want: []string{"resolving a test failure", "go test ./...", "Files: auth.go", "does not prove"}},
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
