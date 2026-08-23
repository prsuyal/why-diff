package event_test

import (
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
)

func TestEventChangesValidityAtStorageBoundary(t *testing.T) {
	t.Parallel()

	e, err := event.New(
		event.KindPromptSubmitted,
		time.Now(),
		event.Source{Provider: "codex", AdapterVersion: "test"},
		event.Context{SessionID: "session-1"},
		event.PromptSubmittedPayload{Text: "fix it"},
		[]byte(`{"hook_event_name":"UserPromptSubmit"}`),
	)
	if err != nil {
		t.Fatalf("event.New() error = %v", err)
	}
	if err := e.ValidateUnsequenced(); err != nil {
		t.Fatalf("ValidateUnsequenced() error = %v", err)
	}
	if err := e.ValidateStored(); err == nil {
		t.Fatal("ValidateStored() before sequence assignment returned nil")
	}

	e.Sequence = 1
	if err := e.ValidateStored(); err != nil {
		t.Fatalf("ValidateStored() error = %v", err)
	}
	if err := e.ValidateUnsequenced(); err == nil {
		t.Fatal("ValidateUnsequenced() after sequence assignment returned nil")
	}
}
