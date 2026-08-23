package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prsuyal/why-diff/internal/cli"
	"github.com/prsuyal/why-diff/internal/event"
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
