//go:build linux

package governorbackend

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/governorauth"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type timeoutTransportError struct {
	message string
}

func (e timeoutTransportError) Error() string {
	return e.message
}

func (e timeoutTransportError) Timeout() bool {
	return true
}

func (e timeoutTransportError) Temporary() bool {
	return true
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

func TestCodexRedactedTransportErrorPreservesRetryableCause(t *testing.T) {
	t.Parallel()

	secret := "secret-token"
	cause := timeoutTransportError{message: "Post https://example.invalid: " + secret + " http2: timeout awaiting response headers"}
	err := redactError(cause, secret)
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error = %v, secret leaked", err)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) {
		t.Fatalf("errors.As(redacted, net.Error) = false, want preserved cause")
	}
	if !isRetryableCodexTransportError(err) {
		t.Fatalf("isRetryableCodexTransportError(%v) = false, want true", err)
	}
}

func TestCodexCompleteRetriesResponseHeaderTimeout(t *testing.T) {
	t.Parallel()

	var calls int
	client, err := NewCodex(CodexOptions{
		BaseURL:          "https://chatgpt.com/backend-api",
		AccessToken:      "secret-token",
		AccountID:        "acct-123",
		TransportRetries: 2,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if calls < 3 {
				return nil, timeoutTransportError{message: "http2: timeout awaiting response headers"}
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
		})},
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
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
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
	var saveCalls int
	client, err := NewCodex(CodexOptions{
		BaseURL:      "https://chatgpt.com/backend-api",
		AccessToken:  "stale-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-123",
		RefreshURL:   "https://auth.openai.com/oauth/token",
		SaveTokens: func(tokens governorauth.CodexTokens, refreshedAt time.Time) error {
			saveCalls++
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
	if saveCalls != 1 {
		t.Fatalf("SaveTokens calls = %d, want 1", saveCalls)
	}
	if saved.AccessToken != "fresh-access" || saved.RefreshToken != "fresh-refresh" || saved.AccountID != "acct-123" {
		t.Fatalf("saved tokens = %#v, want refreshed pair plus account id", saved)
	}
	if savedAt.IsZero() {
		t.Fatal("save timestamp was not set")
	}
}

func TestCodexCompleteDoesNotPersistTokensWhenRefreshFails(t *testing.T) {
	t.Parallel()

	var saveCalls int
	client, err := NewCodex(CodexOptions{
		BaseURL:      "https://chatgpt.com/backend-api",
		AccessToken:  "stale-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-123",
		RefreshURL:   "https://auth.openai.com/oauth/token",
		SaveTokens: func(tokens governorauth.CodexTokens, refreshedAt time.Time) error {
			saveCalls++
			return nil
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			switch req.URL.String() {
			case "https://chatgpt.com/backend-api/codex/responses":
				rec.WriteHeader(http.StatusUnauthorized)
			case "https://auth.openai.com/oauth/token":
				rec.WriteHeader(http.StatusUnauthorized)
			default:
				t.Fatalf("unexpected request url: %s", req.URL.String())
			}
			return rec.Result(), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	_, err = client.Complete(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("Complete() err = nil, want refresh failure")
	}
	if saveCalls != 0 {
		t.Fatalf("SaveTokens calls = %d, want 0 after failed refresh", saveCalls)
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
			ProviderState: json.RawMessage(`{"backend":"codex","response_id":"resp-turn-1","reasoning_items":[{"type":"reasoning","id":"rs_123","summary":[{"text":"keep private"}]}]}`),
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
