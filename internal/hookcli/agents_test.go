package hookcli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/capture/agents"
	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/hookcli"
	"github.com/prsuyal/why-diff/internal/initialize"
	"github.com/prsuyal/why-diff/internal/query"
)

func TestAgentHooksCaptureAnActualChangedLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("COPILOT_HOME", t.TempDir())
	for _, agent := range []initialize.Provider{initialize.ProviderClaude, initialize.ProviderCursor, initialize.ProviderGemini, initialize.ProviderCopilot} {
		t.Run(string(agent), func(t *testing.T) {
			root := t.TempDir()
			for _, args := range [][]string{{"init", "--quiet", root}, {"-C", root, "config", "user.name", "test"}, {"-C", root, "config", "user.email", "test@example.com"}} {
				if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v: %s", args, err, output)
				}
			}
			file := filepath.Join(root, "config.json")
			if err := os.WriteFile(file, []byte("{\"timeout\":5}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"-C", root, "add", "config.json"}, {"-C", root, "commit", "--quiet", "-m", "initial"}} {
				if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v: %s", args, err, output)
				}
			}
			result, err := initialize.RunProviders(context.Background(), root, []initialize.Provider{agent})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, valid, err := initialize.InspectHook(context.Background(), root, agent); err != nil || !valid {
				t.Fatalf("installed hooks: %v, valid=%v", err, valid)
			}
			if agent == initialize.ProviderCopilot {
				config, err := os.ReadFile(result.HooksPath)
				if err != nil || !strings.Contains(string(config), "why-diff-hook copilot preToolUse") {
					t.Fatalf("Copilot config: %s, %v", config, err)
				}
			}
			if agent == initialize.ProviderCursor {
				config, err := os.ReadFile(result.HooksPath)
				if err != nil || !strings.Contains(string(config), "why-diff-hook cursor preToolUse") {
					t.Fatalf("Cursor config: %s, %v", config, err)
				}
			}
			events := agentEvents(agent, root)
			for index, event := range events {
				if index == 3 {
					if err := os.WriteFile(file, []byte("{\"timeout\":30}\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				var stdout, stderr bytes.Buffer
				args := []string{string(agent)}
				if agent == initialize.ProviderCopilot || agent == initialize.ProviderCursor {
					args = append(args, event.name)
				}
				payload, _ := json.Marshal(event.payload)
				if code := hookcli.RunWithOutput(context.Background(), args, bytes.NewReader(payload), &stdout, &stderr); code != 0 || stderr.Len() != 0 {
					t.Fatalf("hook %s: code=%d stderr=%s", event.name, code, stderr.String())
				}
				if agent == initialize.ProviderCursor {
					want := "{}\n"
					if event.name == "preToolUse" {
						want = "{\"permission\":\"ask\"}\n"
					}
					if stdout.String() != want {
						t.Fatalf("Cursor %s response: %q", event.name, stdout.String())
					}
				}
				if (agent == initialize.ProviderGemini || agent == initialize.ProviderCopilot) && stdout.String() != "{}\n" {
					t.Fatalf("JSON response: %q", stdout.String())
				}
			}
			service, err := query.New(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			attribution, err := service.Why(context.Background(), "config.json:1", "")
			if err != nil {
				t.Fatal(err)
			}
			if attribution.Prompt != "Fix session timeout" || !strings.Contains(attribution.Patch, "timeout\":30") {
				t.Fatalf("wrong attribution: %+v", attribution)
			}
		})
	}
}

func TestCursorMalformedPreToolUseStaysFailOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var stdout, stderr bytes.Buffer
	code := hookcli.RunWithOutput(context.Background(), []string{"cursor", "preToolUse"}, strings.NewReader("bad-json"), &stdout, &stderr)
	if code != 0 || stdout.String() != "{\"permission\":\"ask\"}\n" || !strings.Contains(stderr.String(), "capture warning") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

type agentEvent struct {
	name    string
	payload map[string]any
}

func agentEvents(agent initialize.Provider, root string) []agentEvent {
	base := map[string]any{"session_id": "test-session", "cwd": root}
	names := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse"}
	if agent == initialize.ProviderCursor {
		names = []string{"sessionStart", "beforeSubmitPrompt", "preToolUse", "postToolUse"}
	}
	if agent == initialize.ProviderGemini {
		names = []string{"SessionStart", "BeforeAgent", "BeforeTool", "AfterTool"}
	}
	if agent == initialize.ProviderCopilot {
		names = []string{"sessionStart", "userPromptSubmitted", "preToolUse", "postToolUse"}
	}
	var events []agentEvent
	for index, name := range names {
		payload := make(map[string]any)
		for key, value := range base {
			payload[key] = value
		}
		if agent == initialize.ProviderCursor {
			delete(payload, "session_id")
			delete(payload, "cwd")
			payload["conversation_id"] = "test-session"
			payload["workspace_roots"] = []string{root}
		}
		if agent == initialize.ProviderCopilot {
			delete(payload, "session_id")
			payload["sessionId"] = "test-session"
		} else {
			payload["hook_event_name"] = name
		}
		if index == 1 {
			payload["prompt"] = "Fix session timeout"
		}
		if index >= 2 {
			payload["tool_name"] = "Shell"
			payload["tool_input"] = map[string]any{"command": "generate config"}
			if agent == initialize.ProviderClaude || agent == initialize.ProviderCursor {
				payload["tool_use_id"] = "tool-1"
			}
			if agent == initialize.ProviderCopilot {
				delete(payload, "tool_name")
				delete(payload, "tool_input")
				payload["toolName"] = "bash"
				payload["toolArgs"] = map[string]any{"command": "generate config"}
			}
		}
		if index == 3 {
			switch agent {
			case initialize.ProviderClaude, initialize.ProviderGemini:
				payload["tool_response"] = map[string]any{"exit_code": 0}
			case initialize.ProviderCursor:
				payload["tool_output"] = `{"exitCode":0,"stdout":"done"}`
			case initialize.ProviderCopilot:
				payload["toolResult"] = map[string]any{"resultType": "success", "textResultForLlm": "done"}
			}
		}
		events = append(events, agentEvent{name, payload})
	}
	return events
}

func TestFailedToolEventsStayFailed(t *testing.T) {
	for _, test := range []struct{ provider, payload string }{
		{"cursor", `{"conversation_id":"s","cwd":"/tmp","hook_event_name":"postToolUseFailure","tool_name":"Shell","error_message":"command failed"}`},
		{"gemini", `{"session_id":"s","cwd":"/tmp","hook_event_name":"AfterTool","tool_name":"run_shell_command","tool_response":{"error":"command failed"}}`},
		{"copilot", `{"sessionId":"s","cwd":"/tmp","hook_event_name":"postToolUseFailure","toolName":"bash","error":"command failed"}`},
	} {
		t.Run(test.provider, func(t *testing.T) {
			got, err := (agents.Adapter{Provider: test.provider}).Normalize([]byte(test.payload), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != event.KindToolCompleted || !bytes.Contains(got.Payload, []byte(`"success":false`)) {
				t.Fatalf("failure event: %+v", got)
			}
		})
	}
}
