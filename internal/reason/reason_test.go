package reason_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/reason"
)

func TestCommandsExtractsExplicitTestOutcomes(t *testing.T) {
	events := []event.Event{
		completed(t, 1, "go test ./...", map[string]any{"exit_code": 1}),
		completed(t, 2, "npm run test -- auth", map[string]any{"success": true}),
		completed(t, 3, "go test ./...", map[string]any{"output": "PASS"}),
		completed(t, 4, "go build ./...", map[string]any{"exit_code": 0}),
	}

	got := reason.Commands(events)
	if len(got) != 4 {
		t.Fatalf("Commands() returned %d observations", len(got))
	}
	if got[0].Category != reason.CategoryTest || got[0].Outcome != reason.OutcomeFailed {
		t.Fatalf("first observation = %+v", got[0])
	}
	if got[1].Category != reason.CategoryTest || got[1].Outcome != reason.OutcomePassed {
		t.Fatalf("second observation = %+v", got[1])
	}
	if got[2].Outcome != reason.OutcomeUnknown {
		t.Fatalf("text-only response should remain unknown: %+v", got[2])
	}
	if got[3].Category != reason.CategoryOther || got[3].Outcome != reason.OutcomePassed {
		t.Fatalf("build observation = %+v", got[3])
	}
}

func TestValidationClaimsRequiresSameTestAndInterveningChange(t *testing.T) {
	events := []event.Event{
		completed(t, 1, "go test ./...", map[string]any{"exit_code": 1}),
		completed(t, 4, "go test ./...", map[string]any{"exit_code": 0}),
		completed(t, 5, "pytest", map[string]any{"exit_code": 0}),
	}
	changes := []reason.ChangeEvidence{{
		StartedSequence:   2,
		CompletedSequence: 3,
		StartedEventID:    "change-start",
		CompletedEventID:  "change-end",
		Files:             []string{"auth.go", "auth_test.go"},
	}}

	got := reason.ValidationClaims(events, changes)
	if len(got) != 1 {
		t.Fatalf("ValidationClaims() = %+v", got)
	}
	if got[0].FailedEventID != events[0].EventID || got[0].PassedEventID != events[1].EventID {
		t.Fatalf("claim evidence = %+v", got[0])
	}
	if len(got[0].Files) != 2 || len(got[0].ChangeEventIDs) != 2 {
		t.Fatalf("claim changes = %+v", got[0])
	}
	if got[0].RuleID != reason.ValidationRuleID || got[0].ClaimID == "" {
		t.Fatalf("claim identity = %+v", got[0])
	}
	again := reason.ValidationClaims(events, changes)
	if again[0].ClaimID != got[0].ClaimID {
		t.Fatalf("claim id changed: %q != %q", again[0].ClaimID, got[0].ClaimID)
	}
}

func completed(t *testing.T, sequence uint64, command string, response any) event.Event {
	t.Helper()
	encodedResponse, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := event.New(event.KindToolCompleted, time.Unix(int64(sequence), 0),
		event.Source{Provider: "test", AdapterVersion: "test/v1"},
		event.Context{SessionID: "session", ToolCallID: "call"},
		event.ToolCompletedPayload{
			Tool:     "Bash",
			Input:    json.RawMessage(`{"command":` + mustQuote(command) + `}`),
			Response: encodedResponse,
		},
		[]byte(`{"source":"test"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	captured.Sequence = sequence
	return captured
}

func mustQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
