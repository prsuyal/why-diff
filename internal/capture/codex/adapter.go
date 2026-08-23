// Package codex translates Codex lifecycle hook payloads into WhyDiff events.
package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
)

const AdapterVersion = "codex-hooks/v1"

// Adapter owns all knowledge of the Codex hook wire format.
type Adapter struct{}

// hookInput is intentionally permissive. Unknown fields survive in the raw
// source payload instead of making capture fail when Codex adds a field.
type hookInput struct {
	SessionID            string          `json:"session_id"`
	TranscriptPath       *string         `json:"transcript_path"`
	CWD                  string          `json:"cwd"`
	HookEventName        string          `json:"hook_event_name"`
	Model                string          `json:"model"`
	TurnID               string          `json:"turn_id"`
	PermissionMode       string          `json:"permission_mode"`
	Source               string          `json:"source"`
	Reason               string          `json:"reason"`
	Prompt               string          `json:"prompt"`
	ToolName             string          `json:"tool_name"`
	ToolUseID            string          `json:"tool_use_id"`
	ToolInput            json.RawMessage `json:"tool_input"`
	ToolResponse         json.RawMessage `json:"tool_response"`
	Trigger              string          `json:"trigger"`
	AgentID              string          `json:"agent_id"`
	AgentType            string          `json:"agent_type"`
	AgentTranscriptPath  *string         `json:"agent_transcript_path"`
	StopHookActive       bool            `json:"stop_hook_active"`
	LastAssistantMessage *string         `json:"last_assistant_message"`
}

func (Adapter) Normalize(raw []byte, observedAt time.Time) (event.Event, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return event.Event{}, errors.New("empty Codex hook payload")
	}
	if !json.Valid(raw) {
		return event.Event{}, errors.New("Codex hook payload is not valid JSON")
	}

	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return event.Event{}, fmt.Errorf("decode Codex hook payload: %w", err)
	}

	context := event.Context{
		WorkingDirectory: input.CWD,
		SessionID:        input.SessionID,
		TurnID:           input.TurnID,
		SubagentID:       input.AgentID,
		ToolCallID:       input.ToolUseID,
	}
	source := event.Source{
		Provider:       "codex",
		AdapterVersion: AdapterVersion,
		Model:          input.Model,
	}

	kind, payload := normalizePayload(input)
	normalized, err := event.New(kind, observedAt, source, context, payload, raw)
	if err != nil {
		return event.Event{}, err
	}

	addCaptureWarnings(&normalized, input)
	return normalized, nil
}

func normalizePayload(input hookInput) (event.Kind, any) {
	switch input.HookEventName {
	case "SessionStart":
		return event.KindSessionStarted, event.SessionStartedPayload{Source: input.Source}
	case "SessionEnd":
		return event.KindSessionEnded, event.SessionEndedPayload{Reason: input.Reason}
	case "UserPromptSubmit":
		return event.KindPromptSubmitted, event.PromptSubmittedPayload{Text: input.Prompt}
	case "PreToolUse":
		return event.KindToolStarted, event.ToolStartedPayload{
			Tool:  input.ToolName,
			Input: rawOrNull(input.ToolInput),
		}
	case "PostToolUse":
		return event.KindToolCompleted, event.ToolCompletedPayload{
			Tool:     input.ToolName,
			Input:    rawOrNull(input.ToolInput),
			Response: rawOrNull(input.ToolResponse),
		}
	case "PermissionRequest":
		return event.KindPermissionRequested, event.PermissionRequestedPayload{
			Tool:  input.ToolName,
			Input: rawOrNull(input.ToolInput),
		}
	case "PreCompact":
		return event.KindCompactionStarted, event.CompactionPayload{Trigger: input.Trigger}
	case "PostCompact":
		return event.KindCompactionCompleted, event.CompactionPayload{Trigger: input.Trigger}
	case "SubagentStart":
		return event.KindSubagentStarted, event.SubagentStartedPayload{AgentType: input.AgentType}
	case "SubagentStop":
		return event.KindSubagentStopped, event.SubagentStoppedPayload{
			AgentType:            input.AgentType,
			AgentTranscriptPath:  input.AgentTranscriptPath,
			StopHookActive:       input.StopHookActive,
			LastAssistantMessage: input.LastAssistantMessage,
		}
	case "Stop":
		return event.KindTurnStopRequested, event.TurnStopRequestedPayload{
			StopHookActive:       input.StopHookActive,
			LastAssistantMessage: input.LastAssistantMessage,
		}
	default:
		return event.KindUnknown, event.UnknownPayload{HookEventName: input.HookEventName}
	}
}

func addCaptureWarnings(normalized *event.Event, input hookInput) {
	if input.SessionID == "" {
		normalized.AddWarning("missing_session_id", "Codex payload did not include a session_id")
	}
	if input.HookEventName == "" {
		normalized.AddWarning("missing_hook_event_name", "Codex payload did not include hook_event_name")
	} else if normalized.Kind == event.KindUnknown {
		normalized.AddWarning("unsupported_hook_event", "Codex hook event is not recognized by this adapter version")
	}
	if (input.HookEventName == "PreToolUse" || input.HookEventName == "PostToolUse") && input.ToolUseID == "" {
		normalized.AddWarning("missing_tool_call_id", "Codex tool event did not include tool_use_id")
	}
	if (input.HookEventName == "PreToolUse" || input.HookEventName == "PostToolUse" || input.HookEventName == "PermissionRequest") && input.ToolName == "" {
		normalized.AddWarning("missing_tool_name", "Codex tool event did not include tool_name")
	}
	if input.HookEventName == "PostToolUse" && len(input.ToolResponse) == 0 {
		normalized.AddWarning("missing_tool_response", "Codex PostToolUse event did not include tool_response")
	}
}

func rawOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
