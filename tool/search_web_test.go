//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type stubSearchWebProvider struct {
	results   []searchWebResult
	err       error
	calls     int
	lastQuery string
	lastLimit int
}

func (s *stubSearchWebProvider) Search(_ context.Context, query string, limit int) ([]searchWebResult, error) {
	s.calls++
	s.lastQuery = query
	s.lastLimit = limit
	if s.err != nil {
		return nil, s.err
	}
	return append([]searchWebResult(nil), s.results...), nil
}

func TestDefinitionsForPrincipalFiltersSearchWebByExposure(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithSearchWeb(&stubSearchWebProvider{})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{
		ToolName:          "search_web",
		ImplementationRef: "tool/search_web.go",
		Registered:        true,
	}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertToolExposure(session.ToolExposure{
		ToolName:  "search_web",
		Principal: "idolum-email",
		Active:    true,
	}); err != nil {
		t.Fatalf("UpsertToolExposure() err = %v", err)
	}

	exposedDefs := registry.DefinitionsForPrincipal(principal.Principal{
		Role:           principal.RoleDurableAgent,
		DurableAgentID: "idolum-email",
	})
	if !toolDefExists(exposedDefs, "search_web") {
		t.Fatalf("DefinitionsForPrincipal(exposed) missing search_web: %#v", exposedDefs)
	}

	hiddenDefs := registry.DefinitionsForPrincipal(principal.Principal{
		Role:           principal.RoleDurableAgent,
		DurableAgentID: "other-agent",
	})
	if toolDefExists(hiddenDefs, "search_web") {
		t.Fatalf("DefinitionsForPrincipal(unexposed) included search_web: %#v", hiddenDefs)
	}
}

func TestSearchWebRequiresRegisterAndExposureAtInvocation(t *testing.T) {
	t.Parallel()

	stub := &stubSearchWebProvider{
		results: []searchWebResult{{Title: "Example", URL: "https://example.com", Snippet: "read-only result"}},
	}
	registry, store := newDurableAgentToolRegistry(t)
	registry.WithSearchWeb(stub)
	key := adminSessionKey()
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"search_web",
		json.RawMessage(`{"query":"aphelion"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unregistered search_web err = %v, want not registered", err)
	}

	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{
		ToolName:          "search_web",
		ImplementationRef: "tool/search_web.go",
		Registered:        true,
	}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}

	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"search_web",
		json.RawMessage(`{"query":"aphelion"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "not exposed") {
		t.Fatalf("unexposed search_web err = %v, want not exposed", err)
	}

	if _, err := store.UpsertToolExposure(session.ToolExposure{
		ToolName:  "search_web",
		Principal: "telegram:1001",
		Active:    false,
	}); err != nil {
		t.Fatalf("UpsertToolExposure(inactive) err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"search_web",
		json.RawMessage(`{"query":"aphelion"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "not exposed") {
		t.Fatalf("inactive exposure err = %v, want not exposed", err)
	}

	if _, err := store.UpsertToolExposure(session.ToolExposure{
		ToolName:  "search_web",
		Principal: "telegram:1001",
		Active:    true,
	}); err != nil {
		t.Fatalf("UpsertToolExposure(active) err = %v", err)
	}
	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"search_web",
		json.RawMessage(`{"query":"aphelion"}`),
	)
	if err != nil {
		t.Fatalf("search_web err = %v", err)
	}
	if !strings.Contains(out, "[WEB_SEARCH]") || !strings.Contains(out, "https://example.com") {
		t.Fatalf("search_web output = %q, want web search payload", out)
	}
	if stub.calls != 1 {
		t.Fatalf("search provider calls = %d, want 1", stub.calls)
	}
}

func TestSearchWebEnforcesLimitValidationAndQueryCap(t *testing.T) {
	t.Parallel()

	stub := &stubSearchWebProvider{
		results: []searchWebResult{{Title: "A", URL: "https://example.com/a", Snippet: "A"}},
	}
	registry, store := newDurableAgentToolRegistry(t)
	registry.WithSearchWeb(stub)

	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{
		ToolName:          "search_web",
		ImplementationRef: "tool/search_web.go",
		Registered:        true,
	}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertToolExposure(session.ToolExposure{
		ToolName:  "search_web",
		Principal: "telegram:1001",
		Active:    true,
	}); err != nil {
		t.Fatalf("UpsertToolExposure() err = %v", err)
	}

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"search_web",
		json.RawMessage(`{"query":"","limit":3}`),
	); err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("empty query err = %v, want query validation", err)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"search_web",
		json.RawMessage(`{"query":"x","limit":6}`),
	); err == nil || !strings.Contains(err.Error(), "limit must be between") {
		t.Fatalf("limit overflow err = %v, want limit validation", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := registry.ExecuteForSessionPrincipal(
			context.Background(),
			actor,
			key,
			"search_web",
			json.RawMessage(`{"query":"aphelion","limit":2}`),
		); err != nil {
			t.Fatalf("search_web call #%d err = %v", i+1, err)
		}
	}
	if stub.lastLimit != 2 {
		t.Fatalf("search provider last limit = %d, want 2", stub.lastLimit)
	}
	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"search_web",
		json.RawMessage(`{"query":"aphelion","limit":2}`),
	); err == nil || !strings.Contains(err.Error(), "query cap exceeded") {
		t.Fatalf("query cap err = %v, want query cap exceeded", err)
	}
}

func TestBraveSearchClientParsesResults(t *testing.T) {
	t.Parallel()

	var gotToken string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = strings.TrimSpace(r.Header.Get("X-Subscription-Token"))
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"web": {
				"results": [
					{"title":"One","url":"https://example.com/one","description":"First"},
					{"title":"Two","url":"https://example.com/two","description":"Second"}
				]
			}
		}`))
	}))
	defer server.Close()

	client, err := NewBraveSearchClient(BraveSearchClientOptions{
		APIKey:     "brv_test",
		BaseURL:    server.URL,
		HTTPClient: &http.Client{Timeout: time.Second},
	})
	if err != nil {
		t.Fatalf("NewBraveSearchClient() err = %v", err)
	}
	results, err := client.Search(context.Background(), "aphelion", 1)
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if gotToken != "brv_test" {
		t.Fatalf("token header = %q, want brv_test", gotToken)
	}
	if gotPath != "/res/v1/web/search" {
		t.Fatalf("request path = %q, want /res/v1/web/search", gotPath)
	}
	if len(results) != 1 {
		t.Fatalf("result count = %d, want 1", len(results))
	}
	if results[0].Title != "One" || results[0].URL != "https://example.com/one" {
		t.Fatalf("first result = %#v, want One + expected URL", results[0])
	}
}

func toolDefExists(defs []agent.ToolDef, name string) bool {
	for _, def := range defs {
		if strings.TrimSpace(def.Name) == strings.TrimSpace(name) {
			return true
		}
	}
	return false
}
