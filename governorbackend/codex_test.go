//go:build linux

package governorbackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/governorauth"
)

func TestCodexCompleteTextUsesResponsesProtocol(t *testing.T) {
	t.Parallel()

	var (
		seenAuth         string
		seenAccountID    string
		seenPath         string
		seenInstructions string
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenAccountID = r.Header.Get("ChatGPT-Account-ID")
		seenPath = r.URL.Path

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seenInstructions, _ = payload["instructions"].(string)
		if payload["model"] != defaultCodexModel {
			t.Fatalf("model = %#v, want %q", payload["model"], defaultCodexModel)
		}

		assertStreamRequest(t, payload)
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "hello from codex",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp1",
					"usage": map[string]any{
						"input_tokens":  10,
						"output_tokens": 5,
						"total_tokens":  15,
						"input_tokens_details": map[string]any{
							"cached_tokens":      3,
							"cache_write_tokens": 2,
						},
					},
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
		{Role: "system", Content: "system instructions"},
		{Role: "user", Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "hello from codex" {
		t.Fatalf("content = %q, want hello from codex", resp.Content)
	}
	if resp.Usage.TotalTokens != 15 || resp.Usage.CacheReadTokens != 3 || resp.Usage.CacheWriteTokens != 2 {
		t.Fatalf("usage = %#v, want totals and cache tokens", resp.Usage)
	}
	if seenAuth != "Bearer secret-token" {
		t.Fatalf("authorization = %q, want bearer token", seenAuth)
	}
	if seenAccountID != "acct-123" {
		t.Fatalf("account id = %q, want acct-123", seenAccountID)
	}
	if seenPath != "/backend-api/codex/responses" {
		t.Fatalf("path = %q, want /backend-api/codex/responses", seenPath)
	}
	if seenInstructions != "system instructions" {
		t.Fatalf("instructions = %q, want system instructions", seenInstructions)
	}
}

func TestCodexCompleteToolCallViaResponsesOutput(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		assertStreamRequest(t, payload)
		writeSSE(t, w,
			sseEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":      "function_call",
					"name":      "exec",
					"call_id":   "tc1",
					"arguments": `{"command":"pwd"}`,
				},
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp1",
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

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "run pwd"}}, []agent.ToolDef{{
		Name:       "exec",
		Parameters: json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "tc1" || resp.ToolCalls[0].Name != "exec" {
		t.Fatalf("tool call = %#v", resp.ToolCalls[0])
	}
	if got := string(resp.ToolCalls[0].Input); got != `{"command":"pwd"}` {
		t.Fatalf("tool input = %q, want pwd payload", got)
	}
}

func TestCodexCompleteMapsAssistantHistoryAsOutputText(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		assertStreamRequest(t, payload)
		input, ok := payload["input"].([]any)
		if !ok || len(input) < 2 {
			t.Fatalf("input = %#v, want at least user and assistant items", payload["input"])
		}
		assistantItem, ok := input[1].(map[string]any)
		if !ok {
			t.Fatalf("assistant item = %#v, want object", input[1])
		}
		content, ok := assistantItem["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("assistant content = %#v, want blocks", assistantItem["content"])
		}
		block, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("assistant block = %#v, want object", content[0])
		}
		if block["type"] != "output_text" {
			t.Fatalf("assistant block type = %#v, want output_text", block["type"])
		}

		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "ok",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp1",
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

	_, err = client.Complete(context.Background(), []agent.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
		{Role: "user", Content: "third"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
}

func TestCodexCompleteStatusError(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
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
	if err == nil {
		t.Fatal("Complete() err = nil, want status error")
	}
	if !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("error = %v, want status code", err)
	}
	if !strings.Contains(err.Error(), `{"error":"unauthorized"}`) {
		t.Fatalf("error = %v, want body excerpt", err)
	}
	if !errors.Is(err, ErrCodexUnauthorized) {
		t.Fatalf("error = %v, want ErrCodexUnauthorized", err)
	}
}

func TestCodexCompleteStatusErrorRedactsSecretsInBody(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"token secret-token forbidden for acct-123"}`))
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:      "https://chatgpt.com/backend-api",
		AccessToken:  "secret-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-123",
		HTTPClient:   &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("Complete() err = nil, want status error")
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "acct-123") {
		t.Fatalf("error = %v, secret leaked", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error = %v, want redacted marker", err)
	}
}

func TestCodexCompleteReloadsAuthFileAfterUnauthorized(t *testing.T) {
	t.Parallel()

	var seenAuth []string
	client, err := NewCodex(CodexOptions{
		BaseURL:      "https://chatgpt.com/backend-api",
		AccessToken:  "stale-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-123",
		LoadTokens: func() (governorauth.CodexTokens, error) {
			return governorauth.CodexTokens{
				AccessToken:  "fresh-token",
				RefreshToken: "refresh-token",
				AccountID:    "acct-456",
			}, nil
		},
		HTTPClient: &http.Client{Transport: &testTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenAuth = append(seenAuth, r.Header.Get("Authorization")+"|"+r.Header.Get("ChatGPT-Account-ID"))
			if len(seenAuth) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeSSE(t, w,
				sseEvent("response.output_text.delta", map[string]any{
					"type":  "response.output_text.delta",
					"delta": "recovered",
				}),
				sseEvent("response.completed", map[string]any{
					"type": "response.completed",
					"response": map[string]any{
						"id": "resp1",
					},
				}),
			)
		})}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "recovered" {
		t.Fatalf("content = %q, want recovered", resp.Content)
	}
	if got, want := strings.Join(seenAuth, ","), "Bearer stale-token|acct-123,Bearer fresh-token|acct-456"; got != want {
		t.Fatalf("auth sequence = %q, want %q", got, want)
	}
}

func TestCodexCompleteRefreshesAndPersistsTokensAfterUnauthorized(t *testing.T) {
	t.Parallel()

	var seenAuth []string
	var saved governorauth.CodexTokens
	var savedAt time.Time
	client, err := NewCodex(CodexOptions{
		BaseURL:      "https://chatgpt.com/backend-api",
		AccessToken:  "stale-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-123",
		RefreshURL:   "https://auth.openai.com/oauth/token",
		SaveTokens: func(tokens governorauth.CodexTokens, refreshedAt time.Time) error {
			saved = tokens
			savedAt = refreshedAt
			return nil
		},
		Now: func() time.Time {
			return time.Date(2026, time.April, 9, 1, 2, 3, 0, time.UTC)
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://chatgpt.com/backend-api/codex/responses":
				seenAuth = append(seenAuth, req.Header.Get("Authorization")+"|"+req.Header.Get("ChatGPT-Account-ID"))
				rec := httptest.NewRecorder()
				if len(seenAuth) == 1 {
					rec.WriteHeader(http.StatusUnauthorized)
				} else {
					writeSSE(t, rec,
						sseEvent("response.output_text.delta", map[string]any{
							"type":  "response.output_text.delta",
							"delta": "after-refresh",
						}),
						sseEvent("response.completed", map[string]any{
							"type": "response.completed",
							"response": map[string]any{
								"id": "resp1",
							},
						}),
					)
				}
				return rec.Result(), nil
			case "https://auth.openai.com/oauth/token":
				raw, _ := io.ReadAll(req.Body)
				if !strings.Contains(string(raw), `"grant_type":"refresh_token"`) {
					t.Fatalf("refresh payload = %s, want refresh_token grant", string(raw))
				}
				rec := httptest.NewRecorder()
				_ = json.NewEncoder(rec).Encode(map[string]any{
					"access_token":  "fresh-access",
					"refresh_token": "fresh-refresh",
				})
				return rec.Result(), nil
			default:
				t.Fatalf("unexpected request url: %s", req.URL.String())
				return nil, nil
			}
		})},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "after-refresh" {
		t.Fatalf("content = %q, want after-refresh", resp.Content)
	}
	if got, want := strings.Join(seenAuth, ","), "Bearer stale-token|acct-123,Bearer fresh-access|acct-123"; got != want {
		t.Fatalf("auth sequence = %q, want %q", got, want)
	}
	if saved.AccessToken != "fresh-access" || saved.RefreshToken != "fresh-refresh" || saved.AccountID != "acct-123" {
		t.Fatalf("saved tokens = %#v, want refreshed pair plus account id", saved)
	}
	if savedAt.IsZero() {
		t.Fatal("save timestamp was not set")
	}
}

func TestCodexCompleteRedactsSecretInTransportError(t *testing.T) {
	t.Parallel()

	const token = "super-secret-token"
	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api",
		AccessToken: token,
		AccountID:   "acct-123",
		HTTPClient: &http.Client{
			Transport: errTransport{err: errors.New("dial failed using token " + token)},
		},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("Complete() err = nil, want transport failure")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked secret token: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error = %v, want redacted marker", err)
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

func assertStreamRequest(t *testing.T, payload map[string]any) {
	t.Helper()
	if stream, ok := payload["stream"].(bool); !ok || !stream {
		t.Fatalf("stream = %#v, want true", payload["stream"])
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
