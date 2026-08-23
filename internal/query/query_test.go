package query_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/ingest"
	"github.com/prsuyal/why-diff/internal/query"
)

func TestWhyConnectsPromptToolAndCheckpointDiff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "--quiet")
	git(t, root, "config", "user.name", "WhyDiff Test")
	git(t, root, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(root, "auth.go"), "package auth\n\nfunc Timeout() int { return 5 }\n")
	git(t, root, "add", "auth.go")
	git(t, root, "commit", "--quiet", "-m", "initial")

	ingestEvent(t, root, `{"session_id":"session-abc","turn_id":"turn-1","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Fix the authentication timeout"}`)
	ingestEvent(t, root, `{"session_id":"session-abc","turn_id":"turn-1","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"call-1","tool_input":{"command":"change timeout"}}`)
	writeFile(t, filepath.Join(root, "auth.go"), "package auth\n\nfunc Timeout() int { return 30 }\n")
	ingestEvent(t, root, `{"session_id":"session-abc","turn_id":"turn-1","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"call-1","tool_input":{"command":"change timeout"},"tool_response":{"output":"Done!"}}`)

	service, err := query.New(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	attribution, err := service.Why(context.Background(), "auth.go:3", "")
	if err != nil {
		t.Fatalf("Why() error = %v", err)
	}
	if attribution.SessionID != "session-abc" || attribution.Prompt != "Fix the authentication timeout" {
		t.Fatalf("attribution = %+v", attribution)
	}
	if attribution.Tool != "apply_patch" || !strings.Contains(attribution.Patch, "return 30") {
		t.Fatalf("attribution = %+v", attribution)
	}

	summaries, err := service.Summaries(context.Background())
	if err != nil || len(summaries) != 1 || summaries[0].EventCount != 3 {
		t.Fatalf("summaries = %+v, error = %v", summaries, err)
	}

	if _, err := service.Finalize(context.Background(), "session-abc"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".git", "whydiff", "active")); err != nil {
		t.Fatal(err)
	}
	archivedAttribution, err := service.Why(context.Background(), "auth.go:3", "session-abc")
	if err != nil {
		t.Fatalf("Why() from Git archive error = %v", err)
	}
	if archivedAttribution.CompletedEventID != attribution.CompletedEventID {
		t.Fatalf("archived attribution = %+v, want event %s", archivedAttribution, attribution.CompletedEventID)
	}
}

func ingestEvent(t *testing.T, root, template string) {
	t.Helper()
	raw := []byte(fmt.Sprintf(template, root))
	if _, err := ingest.Codex(context.Background(), raw, ingest.CodexOptions{ObservedAt: time.Now()}); err != nil {
		t.Fatalf("ingest event: %v", err)
	}
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
