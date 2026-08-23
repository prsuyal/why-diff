package redact_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/redact"
)

func TestApplyRedactsNormalizedAndSourcePayloads(t *testing.T) {
	t.Parallel()

	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	source := []byte(`{"hook_event_name":"UserPromptSubmit","api_key":"` + secret + `","prompt":"use ` + secret + `"}`)
	e, err := event.New(
		event.KindPromptSubmitted,
		time.Now(),
		event.Source{Provider: "codex", AdapterVersion: "test"},
		event.Context{SessionID: "session-1"},
		event.PromptSubmittedPayload{Text: "use " + secret},
		source,
	)
	if err != nil {
		t.Fatalf("event.New() error = %v", err)
	}

	if err := redact.Default().Apply(&e); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("persistable event still contains secret: %s", encoded)
	}
	if len(e.Capture.Redactions) < 2 {
		t.Fatalf("redactions = %+v, want records for normalized and source payloads", e.Capture.Redactions)
	}
}

func TestApplyTruncatesLargeStringsWithoutInvalidUTF8(t *testing.T) {
	t.Parallel()

	e, err := event.New(
		event.KindPromptSubmitted,
		time.Now(),
		event.Source{Provider: "codex", AdapterVersion: "test"},
		event.Context{SessionID: "session-1"},
		event.PromptSubmittedPayload{Text: "éééééé"},
		[]byte(`{"prompt":"éééééé"}`),
	)
	if err != nil {
		t.Fatalf("event.New() error = %v", err)
	}
	r := redact.Redactor{MaxStringBytes: 5, MaxRedactionRecords: 10}
	if err := r.Apply(&e); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !e.Capture.Truncated {
		t.Fatal("Capture.Truncated = false, want true")
	}
	if !json.Valid(e.Payload) || !json.Valid(e.SourcePayload) {
		t.Fatalf("truncation produced invalid JSON: payload=%s source=%s", e.Payload, e.SourcePayload)
	}
}
