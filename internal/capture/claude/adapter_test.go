package claude_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/capture/claude"
	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/reason"
)

func TestNormalizeSuccessfulPostToolUse(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "session_id":"claude-session", "cwd":"/repo", "hook_event_name":"PostToolUse",
  "tool_name":"Bash", "tool_use_id":"tool-1",
  "tool_input":{"command":"go test ./..."},
  "tool_response":{"stdout":"ok"}, "future_claude_field":{"kept":true}
}`)

	got, err := (claude.Adapter{}).Normalize(raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != event.KindToolCompleted || got.Source.Provider != "claude-code" || got.Context.ToolCallID != "tool-1" {
		t.Fatalf("normalized event = %+v", got)
	}
	var payload event.ToolCompletedPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(payload.Response, &response); err != nil {
		t.Fatal(err)
	}
	if response["success"] != true || response["stdout"] != "ok" {
		t.Fatalf("response = %+v", response)
	}
	var source map[string]any
	if err := json.Unmarshal(got.SourcePayload, &source); err != nil {
		t.Fatal(err)
	}
	if source["future_claude_field"] == nil {
		t.Fatal("unknown Claude field was not retained")
	}
}

func TestNormalizePostToolUseFailure(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "session_id":"claude-session", "cwd":"/repo", "hook_event_name":"PostToolUseFailure",
  "tool_name":"Bash", "tool_use_id":"tool-2",
  "tool_input":{"command":"go test ./..."},
  "error":"Exit code 1\nauth test failed", "is_interrupt":false, "duration_ms":1200
}`)

	got, err := (claude.Adapter{}).Normalize(raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != event.KindToolCompleted {
		t.Fatalf("Kind = %q", got.Kind)
	}
	var payload event.ToolCompletedPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(payload.Response, &response); err != nil {
		t.Fatal(err)
	}
	if response["success"] != false || response["exit_code"] != float64(1) || response["error"] == "" {
		t.Fatalf("response = %+v", response)
	}
	got.Sequence = 1
	commands := reason.Commands([]event.Event{got})
	if len(commands) != 1 || commands[0].Outcome != reason.OutcomeFailed || commands[0].OutcomeBasis != "tool_response.exit_code=1" {
		t.Fatalf("commands = %+v", commands)
	}
}

func TestNormalizeUnknownEventWithWarning(t *testing.T) {
	t.Parallel()
	got, err := (claude.Adapter{}).Normalize([]byte(`{
  "session_id":"claude-session", "hook_event_name":"FutureHook", "future":true
}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != event.KindUnknown || !hasWarning(got, "unsupported_hook_event") {
		t.Fatalf("event = %+v", got)
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
