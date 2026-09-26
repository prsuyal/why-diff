// Package agents translates Cursor, Gemini CLI, and Copilot hook payloads.
package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
)

type Adapter struct{ Provider string }

type hookInput struct {
	SessionID      string          `json:"session_id"`
	SessionIDCamel string          `json:"sessionId"`
	ConversationID string          `json:"conversation_id"`
	GenerationID   string          `json:"generation_id"`
	CWD            string          `json:"cwd"`
	WorkspaceRoots []string        `json:"workspace_roots"`
	HookEventName  string          `json:"hook_event_name"`
	Prompt         string          `json:"prompt"`
	ToolName       string          `json:"tool_name"`
	ToolNameCamel  string          `json:"toolName"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolArgs       json.RawMessage `json:"toolArgs"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolOutput     json.RawMessage `json:"tool_output"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	ToolResult     json.RawMessage `json:"toolResult"`
	ErrorMessage   string          `json:"error_message"`
	Error          string          `json:"error"`
	Reason         string          `json:"reason"`
	Source         string          `json:"source"`
	Model          string          `json:"model"`
}

func (a Adapter) Normalize(raw []byte, at time.Time) (event.Event, error) {
	if len(bytes.TrimSpace(raw)) == 0 || !json.Valid(raw) {
		return event.Event{}, errors.New("hook payload must be a JSON object")
	}
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return event.Event{}, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return event.Event{}, errors.New("hook payload must be a JSON object")
	}
	session := first(input.SessionID, input.SessionIDCamel, input.ConversationID)
	cwd := input.CWD
	if cwd == "" && len(input.WorkspaceRoots) > 0 {
		cwd = input.WorkspaceRoots[0]
	}
	name := first(input.ToolName, input.ToolNameCamel)
	args := firstRaw(input.ToolInput, input.ToolArgs)
	context := event.Context{SessionID: session, WorkingDirectory: cwd, TurnID: input.GenerationID, ToolCallID: input.ToolUseID}
	var kind event.Kind
	var payload any
	switch a.Provider {
	case "cursor":
		switch input.HookEventName {
		case "sessionStart":
			kind, payload = event.KindSessionStarted, event.SessionStartedPayload{}
		case "sessionEnd":
			kind, payload = event.KindSessionEnded, event.SessionEndedPayload{Reason: input.Reason}
		case "beforeSubmitPrompt":
			kind, payload = event.KindPromptSubmitted, event.PromptSubmittedPayload{Text: input.Prompt}
		case "preToolUse":
			kind, payload = event.KindToolStarted, event.ToolStartedPayload{Tool: name, Input: nullIfEmpty(args)}
		case "postToolUse", "postToolUseFailure":
			response := input.ToolOutput
			if input.HookEventName == "postToolUseFailure" {
				response = mustJSON(map[string]any{"success": false, "error": input.ErrorMessage})
			}
			kind, payload = event.KindToolCompleted, event.ToolCompletedPayload{Tool: name, Input: nullIfEmpty(args), Response: normalizeResponse(response)}
		}
	case "gemini":
		switch input.HookEventName {
		case "SessionStart":
			kind, payload = event.KindSessionStarted, event.SessionStartedPayload{Source: input.Source}
		case "SessionEnd":
			kind, payload = event.KindSessionEnded, event.SessionEndedPayload{Reason: input.Reason}
		case "BeforeAgent":
			kind, payload = event.KindPromptSubmitted, event.PromptSubmittedPayload{Text: input.Prompt}
		case "BeforeTool":
			kind, payload = event.KindToolStarted, event.ToolStartedPayload{Tool: name, Input: nullIfEmpty(args)}
		case "AfterTool":
			kind, payload = event.KindToolCompleted, event.ToolCompletedPayload{Tool: name, Input: nullIfEmpty(args), Response: normalizeResponse(input.ToolResponse)}
		}
	case "copilot":
		switch input.HookEventName {
		case "sessionStart":
			kind, payload = event.KindSessionStarted, event.SessionStartedPayload{Source: input.Source}
		case "sessionEnd":
			kind, payload = event.KindSessionEnded, event.SessionEndedPayload{Reason: input.Reason}
		case "userPromptSubmitted":
			kind, payload = event.KindPromptSubmitted, event.PromptSubmittedPayload{Text: input.Prompt}
		case "preToolUse":
			kind, payload = event.KindToolStarted, event.ToolStartedPayload{Tool: name, Input: nullIfEmpty(args)}
		case "postToolUse":
			kind, payload = event.KindToolCompleted, event.ToolCompletedPayload{Tool: name, Input: nullIfEmpty(args), Response: normalizeResponse(input.ToolResult)}
		case "postToolUseFailure":
			kind, payload = event.KindToolCompleted, event.ToolCompletedPayload{Tool: name, Input: nullIfEmpty(args), Response: mustJSON(map[string]any{"success": false, "error": input.Error})}
		}
	default:
		return event.Event{}, fmt.Errorf("unknown provider %q", a.Provider)
	}
	if kind == "" {
		kind, payload = event.KindUnknown, event.UnknownPayload{HookEventName: input.HookEventName}
	}
	if (kind == event.KindToolStarted || kind == event.KindToolCompleted) && context.ToolCallID == "" {
		canonical := args
		var value any
		if json.Unmarshal(args, &value) == nil {
			canonical, _ = json.Marshal(value)
		}
		sum := sha256.Sum256(append([]byte(session+"\x00"+name+"\x00"), canonical...))
		context.ToolCallID = fmt.Sprintf("derived-%x", sum[:12])
	}
	normalized, err := event.New(kind, at, event.Source{Provider: a.Provider, AdapterVersion: a.Provider + "-hooks/v1", Model: input.Model}, context, payload, raw)
	if err != nil {
		return event.Event{}, err
	}
	if session == "" {
		normalized.AddWarning("missing_session_id", "hook event did not include a session ID")
	}
	if cwd == "" {
		normalized.AddWarning("missing_working_directory", "hook event did not include a working directory")
	}
	if kind == event.KindUnknown {
		normalized.AddWarning("unsupported_hook_event", "hook event is not recognized by this adapter")
	}
	if (kind == event.KindToolStarted || kind == event.KindToolCompleted) && input.ToolUseID == "" {
		normalized.AddWarning("derived_tool_call_id", "provider omitted a tool call ID; concurrent identical calls cannot be distinguished")
	}
	return normalized, nil
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func firstRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}
func nullIfEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
func mustJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func normalizeResponse(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return mustJSON(map[string]any{"result": string(raw)})
	}
	if text, ok := value.(string); ok && json.Unmarshal([]byte(text), &value) != nil {
		value = text
	}
	if object, ok := value.(map[string]any); ok {
		if errorValue, present := object["error"]; present && errorValue != nil {
			if message, ok := errorValue.(string); !ok || message != "" {
				object["success"] = false
			}
		}
		if isError, ok := object["isError"].(bool); ok && isError {
			object["success"] = false
		}
		return mustJSON(object)
	}
	return mustJSON(map[string]any{"result": value})
}
