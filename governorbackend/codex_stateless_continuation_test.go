//go:build linux

package governorbackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/idolum-ai/aphelion/agent"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodexCompleteDefaultsToStatelessRequests(t *testing.T) {
	t.Parallel()

	var seen map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seen = payload
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "stateless",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp-stateless",
				},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{
		{Role: "user", Content: "Run ls"},
		{
			Role:          "assistant",
			ToolCalls:     []agent.ToolCall{{ID: "call-1", Name: "exec", Input: json.RawMessage(`{"cmd":"ls"}`)}},
			ProviderState: json.RawMessage(`{"backend":"codex","response_id":"resp-turn-1"}`),
		},
		{Role: "tool", ToolCallID: "call-1", Content: "file.txt"},
	}, []agent.ToolDef{{
		Name:       "exec",
		Parameters: json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "stateless" {
		t.Fatalf("content = %q, want stateless", resp.Content)
	}
	if store, ok := seen["store"].(bool); !ok || store {
		t.Fatalf("store = %#v, want false", seen["store"])
	}
	if _, ok := seen["previous_response_id"]; ok {
		t.Fatalf("previous_response_id = %#v, want omitted", seen["previous_response_id"])
	}
	input, ok := seen["input"].([]any)
	if !ok || len(input) < 3 {
		t.Fatalf("input = %#v, want full context replay", seen["input"])
	}
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["type"] == "reasoning" {
			t.Fatalf("input unexpectedly included reasoning item in stateless mode: %#v", item)
		}
	}
}

func TestCodexCompleteErrorsOnIncompleteWithoutStoredResponses(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if store, ok := payload["store"].(bool); !ok || store {
			t.Fatalf("store = %#v, want false", payload["store"])
		}
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "partial",
			}),
			sseEvent("response.incomplete", map[string]any{
				"type": "response.incomplete",
				"response": map[string]any{
					"id":     "resp-incomplete",
					"status": "incomplete",
				},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: "secret-token",
		AccountID:   "acct-123",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "incomplete response without stored-response continuation") {
		t.Fatalf("Complete() err = %v, want incomplete-without-continuation error", err)
	}
	var incomplete *codexIncompleteError
	if !errors.As(err, &incomplete) {
		t.Fatalf("Complete() err = %T, want codexIncompleteError", err)
	}
	if incomplete.PartialProviderResponseID() != "resp-incomplete" {
		t.Fatalf("partial response id = %q, want resp-incomplete", incomplete.PartialProviderResponseID())
	}
	if partial := incomplete.PartialProviderResponse(); partial == nil || partial.Content != "partial" {
		t.Fatalf("partial response = %#v, want content", partial)
	}
}

func TestCodexCompleteFallsBackWhenStoredResponsesUnsupported(t *testing.T) {
	t.Parallel()

	var stores []bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		store, _ := payload["store"].(bool)
		stores = append(stores, store)
		if store {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"Store must be set to false"}`))
			return
		}
		if _, ok := payload["previous_response_id"]; ok {
			t.Fatalf("retry previous_response_id = %#v, want omitted", payload["previous_response_id"])
		}
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "stateless fallback",
			}),
			sseEvent("response.completed", map[string]any{
				"type":     "response.completed",
				"response": map[string]any{"id": "resp-stateless"},
			}),
		)
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:        "https://chatgpt.com/backend-api",
		AccessToken:    "secret-token",
		AccountID:      "acct-123",
		StoreResponses: true,
		HTTPClient:     &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "stateless fallback" {
		t.Fatalf("content = %q, want stateless fallback", resp.Content)
	}
	if len(stores) != 2 {
		t.Fatalf("request count = %d, want 2", len(stores))
	}
	if !stores[0] || stores[1] {
		t.Fatalf("stores after first Complete = %#v, want [true false]", stores)
	}

	resp, err = client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "again"}}, nil)
	if err != nil {
		t.Fatalf("second Complete() err = %v", err)
	}
	if resp.Content != "stateless fallback" {
		t.Fatalf("second content = %q, want stateless fallback", resp.Content)
	}
	if len(stores) != 4 {
		t.Fatalf("request count after second Complete = %d, want 4", len(stores))
	}
	if !stores[2] || stores[3] {
		t.Fatalf("stores after second Complete = %#v, want per-request [true false true false]", stores)
	}
}

func TestCodexCompleteForcesStoredContinuationAfterStatelessIncomplete(t *testing.T) {
	t.Parallel()

	var seen []map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seen = append(seen, payload)
		switch len(seen) {
		case 1:
			if store, _ := payload["store"].(bool); !store {
				t.Fatalf("first request store = false, want true")
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"Store must be set to false"}`))
		case 2:
			if store, _ := payload["store"].(bool); store {
				t.Fatalf("stateless retry store = true, want false")
			}
			writeSSE(t, w,
				sseEvent("response.output_text.delta", map[string]any{
					"type":  "response.output_text.delta",
					"delta": "partial ",
				}),
				sseEvent("response.incomplete", map[string]any{
					"type": "response.incomplete",
					"response": map[string]any{
						"id":     "resp-stateless-incomplete",
						"status": "incomplete",
					},
				}),
			)
		case 3:
			if store, _ := payload["store"].(bool); !store {
				t.Fatalf("forced continuation store = false, want true")
			}
			if got := payload["previous_response_id"]; got != "resp-stateless-incomplete" {
				t.Fatalf("previous_response_id = %#v, want resp-stateless-incomplete", got)
			}
			writeSSE(t, w,
				sseEvent("response.output_text.delta", map[string]any{
					"type":  "response.output_text.delta",
					"delta": "recovered",
				}),
				sseEvent("response.completed", map[string]any{
					"type":     "response.completed",
					"response": map[string]any{"id": "resp-recovered"},
				}),
			)
		default:
			t.Fatalf("unexpected request count %d", len(seen))
		}
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:        "https://chatgpt.com/backend-api",
		AccessToken:    "secret-token",
		AccountID:      "acct-123",
		StoreResponses: true,
		HTTPClient:     &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "partial recovered" {
		t.Fatalf("content = %q, want partial recovered", resp.Content)
	}
	if len(seen) != 3 {
		t.Fatalf("request count = %d, want 3", len(seen))
	}
}

type testTransport struct {
	handler http.Handler
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

type errTransport struct {
	err error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func (t errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

func assertStreamRequest(t *testing.T, payload map[string]any, store bool) {
	t.Helper()
	if stream, ok := payload["stream"].(bool); !ok || !stream {
		t.Fatalf("stream = %#v, want true", payload["stream"])
	}
	if got, ok := payload["store"].(bool); !ok || got != store {
		t.Fatalf("store = %#v, want %v", payload["store"], store)
	}
}

func sseEvent(kind string, payload map[string]any) string {
	body, _ := json.Marshal(payload)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", kind, string(body))
}

func writeSSE(t *testing.T, w http.ResponseWriter, events ...string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		if _, err := io.WriteString(w, event); err != nil {
			t.Fatalf("write sse: %v", err)
		}
	}
}
