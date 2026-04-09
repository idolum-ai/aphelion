//go:build linux

package governorbackend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/governorauth"
)

func TestCodexCompleteText(t *testing.T) {
	t.Parallel()

	var seenAuth string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": "hello from codex",
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 5,
				"total_tokens":  15,
			},
		})
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api/codex",
		AccessToken: "secret-token",
		HTTPClient:  &http.Client{Transport: &testTransport{handler: handler}},
	})
	if err != nil {
		t.Fatalf("NewCodex() err = %v", err)
	}

	resp, err := client.Complete(context.Background(), []agent.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete() err = %v", err)
	}
	if resp.Content != "hello from codex" {
		t.Fatalf("content = %q, want hello from codex", resp.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
	if seenAuth != "Bearer secret-token" {
		t.Fatalf("authorization = %q, want bearer token", seenAuth)
	}
}

func TestCodexCompleteToolCall(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tool_calls": []map[string]any{
				{
					"id":    "tc1",
					"name":  "exec",
					"input": map[string]any{"command": "pwd"},
				},
			},
		})
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api/codex",
		AccessToken: "secret-token",
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
}

func TestCodexCompleteStatusError(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})

	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api/codex",
		AccessToken: "secret-token",
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
	if !errors.Is(err, ErrCodexUnauthorized) {
		t.Fatalf("error = %v, want ErrCodexUnauthorized", err)
	}
}

func TestCodexCompleteReloadsAuthFileAfterUnauthorized(t *testing.T) {
	t.Parallel()

	var seenAuth []string
	client, err := NewCodex(CodexOptions{
		BaseURL:      "https://chatgpt.com/backend-api/codex",
		AccessToken:  "stale-token",
		RefreshToken: "refresh-token",
		LoadTokens: func() (governorauth.CodexTokens, error) {
			return governorauth.CodexTokens{
				AccessToken:  "fresh-token",
				RefreshToken: "refresh-token",
			}, nil
		},
		HTTPClient: &http.Client{Transport: &testTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenAuth = append(seenAuth, r.Header.Get("Authorization"))
			if len(seenAuth) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": "recovered"})
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
	if got, want := strings.Join(seenAuth, ","), "Bearer stale-token,Bearer fresh-token"; got != want {
		t.Fatalf("authorization sequence = %q, want %q", got, want)
	}
}

func TestCodexCompleteRefreshesAndPersistsTokensAfterUnauthorized(t *testing.T) {
	t.Parallel()

	var seenAuth []string
	var saved governorauth.CodexTokens
	var savedAt time.Time
	client, err := NewCodex(CodexOptions{
		BaseURL:      "https://chatgpt.com/backend-api/codex",
		AccessToken:  "stale-token",
		RefreshToken: "refresh-token",
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
			case "https://chatgpt.com/backend-api/codex":
				seenAuth = append(seenAuth, req.Header.Get("Authorization"))
				rec := httptest.NewRecorder()
				if len(seenAuth) == 1 {
					rec.WriteHeader(http.StatusUnauthorized)
				} else {
					_ = json.NewEncoder(rec).Encode(map[string]any{"content": "after-refresh"})
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
	if got, want := strings.Join(seenAuth, ","), "Bearer stale-token,Bearer fresh-access"; got != want {
		t.Fatalf("authorization sequence = %q, want %q", got, want)
	}
	if saved.AccessToken != "fresh-access" || saved.RefreshToken != "fresh-refresh" {
		t.Fatalf("saved tokens = %#v, want refreshed pair", saved)
	}
	if savedAt.IsZero() {
		t.Fatal("save timestamp was not set")
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

func TestCodexCompleteRedactsSecretInTransportError(t *testing.T) {
	t.Parallel()

	const token = "super-secret-token"
	client, err := NewCodex(CodexOptions{
		BaseURL:     "https://chatgpt.com/backend-api/codex",
		AccessToken: token,
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
