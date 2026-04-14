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

		assertStreamRequest(t, payload, false)
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

func TestCodexCompleteUsesConfiguredModel(t *testing.T) {
	t.Parallel()

	var seenModel any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seenModel = payload["model"]
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
		Model:       "gpt-5.4-mini",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	if _, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if seenModel != "gpt-5.4-mini" {
		t.Fatalf("model = %#v, want gpt-5.4-mini", seenModel)
	}
}

func TestCodexCompleteToolCallViaResponsesOutput(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		assertStreamRequest(t, payload, false)
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
		assertStreamRequest(t, payload, false)
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

func TestCodexCompletePreservesReasoningItemsForFullContextReplay(t *testing.T) {
	t.Parallel()

	var (
		requestCount int
		secondInput  []any
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if requestCount == 2 {
			items, ok := payload["input"].([]any)
			if !ok {
				t.Fatalf("input = %#v, want []any", payload["input"])
			}
			secondInput = append([]any(nil), items...)
		}
		writeSSE(t, w,
			sseEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":    "reasoning",
					"id":      "rs_123",
					"summary": []map[string]any{{"text": "private reasoning summary"}},
				},
			}),
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "hello",
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

	first, err := client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "first"}}, nil)
	if err != nil {
		t.Fatalf("first Complete() err = %v", err)
	}
	state, ok := decodeCodexProviderState(first.ProviderState)
	if !ok {
		t.Fatalf("provider state = %s, want codex provider state", string(first.ProviderState))
	}
	if len(state.ReasoningItems) != 1 {
		t.Fatalf("reasoning items len = %d, want 1", len(state.ReasoningItems))
	}

	_, err = client.Complete(context.Background(), []agent.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: first.Content, Thinking: first.Thinking, ThinkingMeta: first.ThinkingMeta, ProviderState: first.ProviderState},
		{Role: "user", Content: "second"},
	}, nil)
	if err != nil {
		t.Fatalf("second Complete() err = %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
	if len(secondInput) < 3 {
		t.Fatalf("second input len = %d, want at least reasoning + assistant + user", len(secondInput))
	}
	reasoningItem, ok := secondInput[1].(map[string]any)
	if !ok {
		t.Fatalf("replayed reasoning item = %#v, want object", secondInput[1])
	}
	if reasoningItem["type"] != "reasoning" {
		t.Fatalf("replayed reasoning type = %#v, want reasoning", reasoningItem["type"])
	}
	if reasoningItem["id"] != "rs_123" {
		t.Fatalf("replayed reasoning id = %#v, want rs_123", reasoningItem["id"])
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

func TestCodexCompleteSyncsRotatedTokensBeforeRequest(t *testing.T) {
	t.Parallel()

	var (
		loadCalls int
		seenAuth  []string
	)
	client, err := NewCodex(CodexOptions{
		BaseURL:      "https://chatgpt.com/backend-api",
		AccessToken:  "stale-token",
		RefreshToken: "stale-refresh",
		AccountID:    "acct-123",
		LoadTokens: func() (governorauth.CodexTokens, error) {
			loadCalls++
			return governorauth.CodexTokens{
				AccessToken:  "fresh-token",
				RefreshToken: "fresh-refresh",
				AccountID:    "acct-456",
			}, nil
		},
		HTTPClient: &http.Client{Transport: &testTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenAuth = append(seenAuth, r.Header.Get("Authorization")+"|"+r.Header.Get("ChatGPT-Account-ID"))
			writeSSE(t, w,
				sseEvent("response.output_text.delta", map[string]any{
					"type":  "response.output_text.delta",
					"delta": "synced",
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
	if resp.Content != "synced" {
		t.Fatalf("content = %q, want synced", resp.Content)
	}
	if loadCalls == 0 {
		t.Fatal("LoadTokens() was not called before request")
	}
	if got, want := strings.Join(seenAuth, ","), "Bearer fresh-token|acct-456"; got != want {
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

func TestCodexCompleteRetriesTransientTransportFailure(t *testing.T) {
	t.Parallel()

	var attempts int
	client, err := NewCodex(CodexOptions{
		BaseURL:          "https://chatgpt.com/backend-api",
		AccessToken:      "secret-token",
		AccountID:        "acct-123",
		TransportRetries: 1,
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					return nil, io.EOF
				}
				rec := httptest.NewRecorder()
				writeSSE(t, rec,
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
				return rec.Result(), nil
			}),
		},
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
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestCodexCompleteContinuesIncompleteResponses(t *testing.T) {
	t.Parallel()

	var seen []map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seen = append(seen, payload)
		if len(seen) == 1 {
			writeSSE(t, w,
				sseEvent("response.output_text.delta", map[string]any{
					"type":  "response.output_text.delta",
					"delta": "hello ",
				}),
				sseEvent("response.incomplete", map[string]any{
					"type": "response.incomplete",
					"response": map[string]any{
						"id":     "resp-incomplete",
						"status": "incomplete",
					},
				}),
			)
			return
		}
		if got := payload["previous_response_id"]; got != "resp-incomplete" {
			t.Fatalf("previous_response_id = %#v, want resp-incomplete", got)
		}
		input, _ := payload["input"].([]any)
		if len(input) != 0 {
			t.Fatalf("continuation input len = %d, want 0", len(input))
		}
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "world",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp-final",
				},
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
	if resp.Content != "hello world" {
		t.Fatalf("content = %q, want hello world", resp.Content)
	}
	if got := len(seen); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
	for i, payload := range seen {
		if store, ok := payload["store"].(bool); !ok || !store {
			t.Fatalf("payload[%d].store = %#v, want true", i, payload["store"])
		}
	}
}

func TestCodexCompleteContinuesWhenStreamClosesAfterResponseCreated(t *testing.T) {
	t.Parallel()

	var seen []map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seen = append(seen, payload)
		if len(seen) == 1 {
			writeSSE(t, w,
				sseEvent("response.created", map[string]any{
					"type": "response.created",
					"response": map[string]any{
						"id": "resp-created",
					},
				}),
				sseEvent("response.output_text.delta", map[string]any{
					"type":  "response.output_text.delta",
					"delta": "partial ",
				}),
			)
			return
		}
		if got := payload["previous_response_id"]; got != "resp-created" {
			t.Fatalf("previous_response_id = %#v, want resp-created", got)
		}
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "recovered",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp-finished",
				},
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
	if resp.Content != "partial recovered" {
		t.Fatalf("content = %q, want partial recovered", resp.Content)
	}
	if got := len(seen); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
}

func TestCodexCompleteUsesPreviousResponseIDForToolFollowUps(t *testing.T) {
	t.Parallel()

	var seen []map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seen = append(seen, payload)
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "ok",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp-followup",
				},
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

	_, err = client.Complete(context.Background(), []agent.Message{
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

	if got := len(seen); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
	payload := seen[0]
	if got := payload["previous_response_id"]; got != "resp-turn-1" {
		t.Fatalf("previous_response_id = %#v, want resp-turn-1", got)
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v, want one tool output item", payload["input"])
	}
	item, ok := input[0].(map[string]any)
	if !ok {
		t.Fatalf("input item = %#v, want object", input[0])
	}
	if item["type"] != "function_call_output" {
		t.Fatalf("item type = %#v, want function_call_output", item["type"])
	}
	if item["call_id"] != "call-1" || item["output"] != "file.txt" {
		t.Fatalf("tool output item = %#v, want call-1/file.txt", item)
	}
}

func TestCodexCompleteFallsBackToFullContextWhenPreviousResponseRejected(t *testing.T) {
	t.Parallel()

	var seen []map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seen = append(seen, payload)
		if len(seen) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Previous response with id 'resp-stale' not found.","param":"previous_response_id"}}`))
			return
		}
		if _, ok := payload["previous_response_id"]; ok {
			t.Fatalf("fallback payload unexpectedly kept previous_response_id: %#v", payload["previous_response_id"])
		}
		input, ok := payload["input"].([]any)
		if !ok || len(input) < 3 {
			t.Fatalf("fallback input = %#v, want full context replay", payload["input"])
		}
		writeSSE(t, w,
			sseEvent("response.output_text.delta", map[string]any{
				"type":  "response.output_text.delta",
				"delta": "replayed",
			}),
			sseEvent("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp-replayed",
				},
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

	resp, err := client.Complete(context.Background(), []agent.Message{
		{Role: "user", Content: "Run ls"},
		{
			Role:          "assistant",
			ToolCalls:     []agent.ToolCall{{ID: "call-1", Name: "exec", Input: json.RawMessage(`{"cmd":"ls"}`)}},
			ProviderState: json.RawMessage(`{"backend":"codex","response_id":"resp-stale"}`),
		},
		{Role: "tool", ToolCallID: "call-1", Content: "file.txt"},
	}, []agent.ToolDef{{
		Name:       "exec",
		Parameters: json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "replayed" {
		t.Fatalf("content = %q, want replayed", resp.Content)
	}
	if got := len(seen); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
}

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
