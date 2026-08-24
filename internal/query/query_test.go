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

	_, packet, err := service.SemanticEvidence(context.Background(), "auth.go:3", "session-abc")
	if err != nil {
		t.Fatalf("SemanticEvidence() error = %v", err)
	}
	if len(packet.SessionIDs) != 1 || packet.SessionIDs[0] != "session-abc" || packet.Target != "auth.go:3" || len(packet.Evidence) < 4 {
		t.Fatalf("semantic packet = %+v", packet)
	}
	kinds := map[string]bool{}
	for _, item := range packet.Evidence {
		kinds[item.Kind] = true
	}
	if !kinds["prompt"] || !kinds["tool_started"] || !kinds["tool_completed"] || !kinds["checkpoint_diff"] {
		t.Fatalf("semantic evidence kinds = %+v", kinds)
	}
}

func TestCompareReportsObservedOverlapAndDivergence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "--quiet")
	git(t, root, "config", "user.name", "WhyDiff Test")
	git(t, root, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(root, "shared.go"), "package demo\n\nconst Strategy = \"baseline\"\n")
	git(t, root, "add", "shared.go")
	git(t, root, "commit", "--quiet", "-m", "initial")

	ingestEvent(t, root, `{"session_id":"attempt-a","turn_id":"turn-a","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Implement the cache"}`)
	ingestEvent(t, root, `{"session_id":"attempt-a","turn_id":"turn-a","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"edit-a","tool_input":{"command":"use an in-memory cache"}}`)
	writeFile(t, filepath.Join(root, "shared.go"), "package demo\n\nconst Strategy = \"memory\"\n")
	writeFile(t, filepath.Join(root, "memory.go"), "package demo\n\nvar Cache = map[string]string{}\n")
	ingestEvent(t, root, `{"session_id":"attempt-a","turn_id":"turn-a","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"edit-a","tool_input":{"command":"use an in-memory cache"},"tool_response":{"output":"Done"}}`)
	ingestEvent(t, root, `{"session_id":"attempt-a","turn_id":"turn-a","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"test-a","tool_input":{"command":"go test ./..."}}`)
	ingestEvent(t, root, `{"session_id":"attempt-a","turn_id":"turn-a","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"test-a","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0}}`)

	git(t, root, "reset", "--hard", "HEAD")
	git(t, root, "clean", "-fd")
	ingestEvent(t, root, `{"session_id":"attempt-b","turn_id":"turn-b","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Implement the cache"}`)
	ingestEvent(t, root, `{"session_id":"attempt-b","turn_id":"turn-b","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"edit-b","tool_input":{"command":"use a disk cache"}}`)
	writeFile(t, filepath.Join(root, "shared.go"), "package demo\n\nconst Strategy = \"disk\"\n")
	writeFile(t, filepath.Join(root, "disk.go"), "package demo\n\nconst CachePath = \"cache.db\"\n")
	ingestEvent(t, root, `{"session_id":"attempt-b","turn_id":"turn-b","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"edit-b","tool_input":{"command":"use a disk cache"},"tool_response":{"output":"Done"}}`)
	for index, testCommand := range []string{"go test ./...", "go test ./internal/..."} {
		toolID := fmt.Sprintf("test-b-%d", index)
		ingestEvent(t, root, `{"session_id":"attempt-b","turn_id":"turn-b","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"`+toolID+`","tool_input":{"command":"`+testCommand+`"}}`)
		ingestEvent(t, root, `{"session_id":"attempt-b","turn_id":"turn-b","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"`+toolID+`","tool_input":{"command":"`+testCommand+`"},"tool_response":{"exit_code":0}}`)
	}

	service, err := query.New(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := service.Compare(context.Background(), "attempt-a", "attempt-b")
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if got, want := strings.Join(comparison.SharedFiles, ","), "shared.go"; got != want {
		t.Fatalf("shared files = %q, want %q", got, want)
	}
	if got, want := strings.Join(comparison.LeftOnlyFiles, ","), "memory.go"; got != want {
		t.Fatalf("left-only files = %q, want %q", got, want)
	}
	if got, want := strings.Join(comparison.RightOnlyFiles, ","), "disk.go"; got != want {
		t.Fatalf("right-only files = %q, want %q", got, want)
	}
	if got, want := strings.Join(comparison.SharedValidations, ","), "go test ./..."; got != want {
		t.Fatalf("shared validations = %q, want %q", got, want)
	}
	if got, want := strings.Join(comparison.RightOnlyValidations, ","), "go test ./internal/..."; got != want {
		t.Fatalf("right-only validations = %q, want %q", got, want)
	}
	if len(comparison.Left.Prompts) != 1 || comparison.Left.Prompts[0].EventID == "" {
		t.Fatalf("left prompts = %+v", comparison.Left.Prompts)
	}
	_, packet, err := service.ComparisonSemanticEvidence(context.Background(), "attempt-a", "attempt-b")
	if err != nil {
		t.Fatalf("ComparisonSemanticEvidence() error = %v", err)
	}
	if packet.Operation != "compare_sessions" || len(packet.SessionIDs) != 2 || len(packet.Evidence) < 8 {
		t.Fatalf("comparison semantic packet = %+v", packet)
	}
	kinds := map[string]bool{}
	for _, item := range packet.Evidence {
		kinds[item.Kind] = true
	}
	if !kinds["prompt"] || !kinds["checkpoint_diff"] || !kinds["validation"] || !kinds["deterministic_comparison"] {
		t.Fatalf("comparison semantic evidence kinds = %+v", kinds)
	}
	if _, err := service.Compare(context.Background(), "attempt-a", "attempt-a"); err == nil {
		t.Fatal("Compare() of the same session succeeded, want error")
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
