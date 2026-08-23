// Package event defines WhyDiff's provider-neutral event vocabulary.
package event

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

const SchemaVersion = 1

// Kind identifies an observed lifecycle event. Kinds describe what WhyDiff
// observed; they do not assert why a code change happened.
type Kind string

const (
	KindSessionStarted      Kind = "session_started"
	KindSessionEnded        Kind = "session_ended"
	KindPromptSubmitted     Kind = "prompt_submitted"
	KindToolStarted         Kind = "tool_started"
	KindToolCompleted       Kind = "tool_completed"
	KindPermissionRequested Kind = "permission_requested"
	KindCompactionStarted   Kind = "compaction_started"
	KindCompactionCompleted Kind = "compaction_completed"
	KindSubagentStarted     Kind = "subagent_started"
	KindSubagentStopped     Kind = "subagent_stopped"
	KindTurnStopRequested   Kind = "turn_stop_requested"
	KindUnknown             Kind = "unknown"
)

// Event is the stable envelope consumed by every provider-independent WhyDiff
// subsystem. Payload is typed according to Kind; SourcePayload retains the
// redacted provider input so future adapters can reinterpret it.
type Event struct {
	SchemaVersion int             `json:"schema_version"`
	EventID       string          `json:"event_id"`
	Kind          Kind            `json:"kind"`
	ObservedAt    time.Time       `json:"observed_at"`
	Sequence      uint64          `json:"sequence"`
	Source        Source          `json:"source"`
	Context       Context         `json:"context"`
	Payload       json.RawMessage `json:"payload"`
	SourcePayload json.RawMessage `json:"source_payload"`
	Capture       Capture         `json:"capture"`
}

type Source struct {
	Provider       string `json:"provider"`
	AdapterVersion string `json:"adapter_version"`
	Model          string `json:"model,omitempty"`
}

type Context struct {
	RepositoryID     string `json:"repository_id,omitempty"`
	WorktreeID       string `json:"worktree_id,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	TurnID           string `json:"turn_id,omitempty"`
	SubagentID       string `json:"subagent_id,omitempty"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
}

type Capture struct {
	Redactions []Redaction `json:"redactions,omitempty"`
	Truncated  bool        `json:"truncated,omitempty"`
	Warnings   []Warning   `json:"warnings,omitempty"`
}

type Redaction struct {
	Scope  string `json:"scope"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// New constructs an event before the store assigns its ingestion sequence.
func New(kind Kind, observedAt time.Time, source Source, context Context, payload any, sourcePayload []byte) (Event, error) {
	id, err := ulid.New(ulid.Timestamp(observedAt), rand.Reader)
	if err != nil {
		return Event{}, fmt.Errorf("generate event id: %w", err)
	}

	normalizedPayload, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode normalized payload: %w", err)
	}

	raw := append(json.RawMessage(nil), sourcePayload...)
	e := Event{
		SchemaVersion: SchemaVersion,
		EventID:       id.String(),
		Kind:          kind,
		ObservedAt:    observedAt.UTC(),
		Source:        source,
		Context:       context,
		Payload:       normalizedPayload,
		SourcePayload: raw,
	}
	return e, e.ValidateUnsequenced()
}

func (e *Event) AddWarning(code, message string) {
	e.Capture.Warnings = append(e.Capture.Warnings, Warning{Code: code, Message: message})
}

// ValidateUnsequenced checks an event at the adapter/store boundary.
func (e Event) ValidateUnsequenced() error {
	if e.Sequence != 0 {
		return errors.New("unsequenced event already has a sequence")
	}
	return e.validateCommon()
}

// ValidateStored checks the stronger invariant required of durable events.
func (e Event) ValidateStored() error {
	if e.Sequence == 0 {
		return errors.New("stored event has no sequence")
	}
	return e.validateCommon()
}

func (e Event) validateCommon() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", e.SchemaVersion)
	}
	if _, err := ulid.ParseStrict(e.EventID); err != nil {
		return fmt.Errorf("invalid event id: %w", err)
	}
	if e.Kind == "" {
		return errors.New("event kind is required")
	}
	if e.ObservedAt.IsZero() {
		return errors.New("observed_at is required")
	}
	if e.Source.Provider == "" {
		return errors.New("source provider is required")
	}
	if e.Source.AdapterVersion == "" {
		return errors.New("source adapter version is required")
	}
	if !json.Valid(e.Payload) {
		return errors.New("payload is not valid JSON")
	}
	if !json.Valid(e.SourcePayload) {
		return errors.New("source_payload is not valid JSON")
	}
	return nil
}

type SessionStartedPayload struct {
	Source string `json:"source,omitempty"`
}

type SessionEndedPayload struct {
	Reason string `json:"reason,omitempty"`
}

type PromptSubmittedPayload struct {
	Text string `json:"text"`
}

type ToolStartedPayload struct {
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input"`
}

type ToolCompletedPayload struct {
	Tool     string          `json:"tool"`
	Input    json.RawMessage `json:"input"`
	Response json.RawMessage `json:"response"`
}

type PermissionRequestedPayload struct {
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input"`
}

type CompactionPayload struct {
	Trigger string `json:"trigger,omitempty"`
}

type SubagentStartedPayload struct {
	AgentType string `json:"agent_type,omitempty"`
}

type SubagentStoppedPayload struct {
	AgentType            string  `json:"agent_type,omitempty"`
	AgentTranscriptPath  *string `json:"agent_transcript_path,omitempty"`
	StopHookActive       bool    `json:"stop_hook_active"`
	LastAssistantMessage *string `json:"last_assistant_message,omitempty"`
}

type TurnStopRequestedPayload struct {
	StopHookActive       bool    `json:"stop_hook_active"`
	LastAssistantMessage *string `json:"last_assistant_message,omitempty"`
}

type UnknownPayload struct {
	HookEventName string `json:"hook_event_name,omitempty"`
}
