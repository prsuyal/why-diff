package semantic_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/prsuyal/why-diff/internal/semantic"
)

func TestOpenAIUsesStructuredStatelessRequestAndValidatesCitations(t *testing.T) {
	t.Parallel()

	var request map[string]any
	clientTransport := roundTripFunc(func(incoming *http.Request) (*http.Response, error) {
		if incoming.URL.Path != "/v1/responses" {
			t.Errorf("path = %q", incoming.URL.Path)
		}
		if incoming.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization header was not set")
		}
		if err := json.NewDecoder(incoming.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		output := `{"summary":"The timeout change appears intended to address the captured failure.","claims":[{"statement":"The prompt and patch support a timeout fix.","confidence":"medium","evidence_ids":["event-1","diff-1"],"qualification":"The evidence does not prove exclusivity."}],"unknowns":["The exact production trigger is not captured."]}`
		output = strings.ReplaceAll(output, `\"`, `"`)
		return jsonResponse(map[string]any{
			"id":         "resp_test",
			"object":     "response",
			"created_at": 1,
			"status":     "completed",
			"model":      "test-model",
			"output": []any{map[string]any{
				"id": "msg_test", "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": output, "annotations": []any{}}},
			}},
		}), nil
	})

	client, err := semantic.NewOpenAI(semantic.OpenAIConfig{
		APIKey:     "test-key",
		Model:      "test-model",
		BaseURL:    "https://example.test/v1",
		HTTPClient: &http.Client{Transport: clientTransport},
	})
	if err != nil {
		t.Fatal(err)
	}
	packet := semantic.EvidencePacket{
		SchemaVersion: semantic.EvidenceSchemaVersion,
		Operation:     semantic.OperationExplainChange,
		SessionIDs:    []string{"session-1"},
		Target:        "auth.go:3",
		Evidence: []semantic.EvidenceItem{
			{ID: "event-1", Kind: "prompt", Summary: "Fix the timeout"},
			{ID: "diff-1", Kind: "checkpoint_diff", Summary: "return 5 -> return 30"},
		},
	}
	explanation, err := client.Explain(context.Background(), packet)
	if err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	if explanation.Provider != "openai" || explanation.Model != "test-model" || explanation.ResponseID != "resp_test" {
		t.Fatalf("explanation metadata = %+v", explanation)
	}
	if len(explanation.Claims) != 1 || explanation.Claims[0].EvidenceIDs[1] != "diff-1" {
		t.Fatalf("explanation = %+v", explanation)
	}
	if request["store"] != false || request["model"] != "test-model" {
		t.Fatalf("request = %+v", request)
	}
	text, ok := request["text"].(map[string]any)
	if !ok {
		t.Fatalf("request text = %#v", request["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" || format["strict"] != true {
		t.Fatalf("response format = %#v", text["format"])
	}
}

func TestOpenAIRejectsInventedEvidenceCitation(t *testing.T) {
	t.Parallel()

	clientTransport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		output := `{"summary":"Summary","claims":[{"statement":"Unsupported","confidence":"high","evidence_ids":["invented"],"qualification":""}],"unknowns":[]}`
		output = strings.ReplaceAll(output, `\"`, `"`)
		return jsonResponse(map[string]any{
			"id": "resp_test", "object": "response", "created_at": 1, "status": "completed", "model": "test-model",
			"output": []any{map[string]any{
				"id": "msg_test", "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": output, "annotations": []any{}}},
			}},
		}), nil
	})

	client, err := semantic.NewOpenAI(semantic.OpenAIConfig{
		APIKey: "test-key", Model: "test-model", BaseURL: "https://example.test/v1",
		HTTPClient: &http.Client{Transport: clientTransport},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Explain(context.Background(), semantic.EvidencePacket{
		SchemaVersion: semantic.EvidenceSchemaVersion,
		Operation:     semantic.OperationExplainChange,
		SessionIDs:    []string{"session-1"},
		Target:        "auth.go",
		Evidence:      []semantic.EvidenceItem{{ID: "real", Kind: "prompt", Summary: "Fix it"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown evidence") {
		t.Fatalf("Explain() error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(value any) *http.Response {
	var buffer bytes.Buffer
	_ = json.NewEncoder(&buffer).Encode(value)
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(&buffer),
	}
}
