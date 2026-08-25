// Package claude translates Claude Code lifecycle hook payloads into WhyDiff events.
package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
)

const AdapterVersion = "claude-code-hooks/v1"

var exitCodePattern = regexp.MustCompile(`^Exit code ([0-9]+)(?:\r?\n|$)`)

// Adapter owns all knowledge of the Claude Code hook wire format.
type Adapter struct{}

// hookInput is intentionally permissive. Fields Claude adds later remain in
// SourcePayload even when this adapter does not understand them yet.
type hookInput struct {
	SessionID            string          `json:"session_id"`
	TranscriptPath       *string         `json:"transcript_path"`
	CWD                  string          `json:"cwd"`
	HookEventName        string          `json:"hook_event_name"`
	PermissionMode       string          `json:"permission_mode"`
	Source               string          `json:"source"`
	Reason               string          `json:"reason"`
	Prompt               string          `json:"prompt"`
	ToolName             string          `json:"tool_name"`
	ToolUseID            string          `json:"tool_use_id"`
	ToolInput            json.RawMessage `json:"tool_input"`
	ToolResponse         json.RawMessage `json:"tool_response"`
	Error                string          `json:"error"`
	IsInterrupt          bool            `json:"is_interrupt"`
	DurationMS           json.Number     `json:"duration_ms"`
	Trigger              string          `json:"trigger"`
	AgentID              string          `json:"agent_id"`
	AgentType            string          `json:"agent_type"`
	AgentTranscriptPath  *string         `json:"agent_transcript_path"`
	StopHookActive       bool            `json:"stop_hook_active"`
	LastAssistantMessage *string         `json:"last_assistant_message"`
}

func (Adapter) Normalize(raw []byte, observedAt time.Time) (event.Event, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return event.Event{}, errors.New("empty Claude Code hook payload")
	}
	if !json.Valid(raw) {
		return event.Event{}, errors.New("Claude Code hook payload is not valid JSON")
	}

	var input hookInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return event.Event{}, fmt.Errorf("decode Claude Code hook payload: %w", err)
	}

	context := event.Context{
		WorkingDirectory: input.CWD,
		SessionID:        input.SessionID,
		SubagentID:       input.AgentID,
		ToolCallID:       input.ToolUseID,
	}
	source := event.Source{
		Provider:       "claude-code",
		AdapterVersion: AdapterVersion,
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
			Tool: input.ToolName, Input: rawOrNull(input.ToolInput),
		}
	case "PostToolUse":
		return event.KindToolCompleted, event.ToolCompletedPayload{
			Tool: input.ToolName, Input: rawOrNull(input.ToolInput), Response: successfulResponse(input.ToolResponse),
		}
	case "PostToolUseFailure":
		return event.KindToolCompleted, event.ToolCompletedPayload{
			Tool: input.ToolName, Input: rawOrNull(input.ToolInput), Response: failedResponse(input),
		}
	case "PermissionRequest":
		return event.KindPermissionRequested, event.PermissionRequestedPayload{
			Tool: input.ToolName, Input: rawOrNull(input.ToolInput),
		}
	case "PreCompact":
		return event.KindCompactionStarted, event.CompactionPayload{Trigger: input.Trigger}
	case "PostCompact":
		return event.KindCompactionCompleted, event.CompactionPayload{Trigger: input.Trigger}
	case "SubagentStart":
		return event.KindSubagentStarted, event.SubagentStartedPayload{AgentType: input.AgentType}
	case "SubagentStop":
		return event.KindSubagentStopped, event.SubagentStoppedPayload{
			AgentType: input.AgentType, AgentTranscriptPath: input.AgentTranscriptPath,
			StopHookActive: input.StopHookActive, LastAssistantMessage: input.LastAssistantMessage,
		}
	case "Stop":
		return event.KindTurnStopRequested, event.TurnStopRequestedPayload{
			StopHookActive: input.StopHookActive, LastAssistantMessage: input.LastAssistantMessage,
		}
	default:
		return event.KindUnknown, event.UnknownPayload{HookEventName: input.HookEventName}
	}
}

// A PostToolUse event is itself Claude's assertion that the tool succeeded.
// Add that fact to the normalized response when the provider-specific response
// does not already contain an outcome field understood by WhyDiff.
func successfulResponse(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return mustJSON(map[string]any{"success": true})
	}
	var response map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&response) == nil && response != nil {
		for _, key := range []string{"success", "status", "exit_code", "exitCode"} {
			if _, exists := response[key]; exists {
				return raw
			}
		}
		response["success"] = true
		return mustJSON(response)
	}
	return mustJSON(map[string]any{"success": true, "result": json.RawMessage(raw)})
}

func failedResponse(input hookInput) json.RawMessage {
	response := map[string]any{
		"success":      false,
		"error":        input.Error,
		"is_interrupt": input.IsInterrupt,
	}
	if input.DurationMS != "" {
		response["duration_ms"] = input.DurationMS
	}
	if matches := exitCodePattern.FindStringSubmatch(input.Error); len(matches) == 2 {
		if code, err := strconv.Atoi(matches[1]); err == nil {
			response["exit_code"] = code
		}
	}
	return mustJSON(response)
}

func addCaptureWarnings(normalized *event.Event, input hookInput) {
	if input.SessionID == "" {
		normalized.AddWarning("missing_session_id", "Claude Code payload did not include a session_id")
	}
	if input.HookEventName == "" {
		normalized.AddWarning("missing_hook_event_name", "Claude Code payload did not include hook_event_name")
	} else if normalized.Kind == event.KindUnknown {
		normalized.AddWarning("unsupported_hook_event", "Claude Code hook event is not recognized by this adapter version")
	}
	toolEvent := input.HookEventName == "PreToolUse" || input.HookEventName == "PostToolUse" || input.HookEventName == "PostToolUseFailure"
	if toolEvent && input.ToolUseID == "" {
		normalized.AddWarning("missing_tool_call_id", "Claude Code tool event did not include tool_use_id")
	}
	if (toolEvent || input.HookEventName == "PermissionRequest") && input.ToolName == "" {
		normalized.AddWarning("missing_tool_name", "Claude Code tool event did not include tool_name")
	}
	if input.HookEventName == "PostToolUse" && len(input.ToolResponse) == 0 {
		normalized.AddWarning("missing_tool_response", "Claude Code PostToolUse event did not include tool_response")
	}
	if input.HookEventName == "PostToolUseFailure" && input.Error == "" {
		normalized.AddWarning("missing_tool_error", "Claude Code PostToolUseFailure event did not include error")
	}
}

func rawOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
