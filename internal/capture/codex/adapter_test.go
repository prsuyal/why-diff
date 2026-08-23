package codex_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/capture/codex"
	"github.com/prsuyal/why-diff/internal/event"
)

func TestNormalizePostToolUse(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
  "session_id": "session-1",
  "turn_id": "turn-2",
  "cwd": "/repo",
  "hook_event_name": "PostToolUse",
  "model": "gpt-test",
  "tool_name": "Bash",
  "tool_use_id": "call-3",
  "tool_input": {"command": "go test ./..."},
  "tool_response": {"exit_code": 1, "output": "failed"},
  "future_codex_field": {"kept": true}
}`)
	observedAt := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.FixedZone("EDT", -4*60*60))

	got, err := (codex.Adapter{}).Normalize(raw, observedAt)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got.Kind != event.KindToolCompleted {
		t.Fatalf("Kind = %q, want %q", got.Kind, event.KindToolCompleted)
	}
	if got.Context.ToolCallID != "call-3" || got.Context.TurnID != "turn-2" {
		t.Fatalf("Context correlation fields = %+v", got.Context)
	}
	if got.Sequence != 0 {
		t.Fatalf("Sequence = %d before storage, want 0", got.Sequence)
	}
	if got.ObservedAt.Location() != time.UTC {
		t.Fatalf("ObservedAt location = %v, want UTC", got.ObservedAt.Location())
	}

	var payload event.ToolCompletedPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("decode normalized payload: %v", err)
	}
	if payload.Tool != "Bash" {
		t.Fatalf("payload.Tool = %q, want Bash", payload.Tool)
	}
	var source map[string]any
	if err := json.Unmarshal(got.SourcePayload, &source); err != nil {
		t.Fatalf("decode source payload: %v", err)
	}
	if _, ok := source["future_codex_field"]; !ok {
		t.Fatal("unknown Codex field was not retained")
	}
}

func TestNormalizeMissingToolCallIDPreservesEventWithWarning(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
  "session_id": "session-1",
  "hook_event_name": "PreToolUse",
  "tool_name": "Bash",
  "tool_input": {"command": "go test ./..."}
}`)
	got, err := (codex.Adapter{}).Normalize(raw, time.Now())
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if !hasWarning(got, "missing_tool_call_id") {
		t.Fatalf("warnings = %+v, want missing_tool_call_id", got.Capture.Warnings)
	}
}

func TestNormalizeStopDoesNotClaimTurnCompleted(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
  "session_id": "session-1",
  "turn_id": "turn-2",
  "hook_event_name": "Stop",
  "stop_hook_active": false
}`)
	got, err := (codex.Adapter{}).Normalize(raw, time.Now())
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got.Kind != event.KindTurnStopRequested {
		t.Fatalf("Kind = %q, want %q", got.Kind, event.KindTurnStopRequested)
	}
}

func hasWarning(got event.Event, code string) bool {
	for _, warning := range got.Capture.Warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}
