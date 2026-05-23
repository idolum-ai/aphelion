//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type fakeWebSearchProvider struct {
	name      string
	available WebSearchAvailability
	result    WebSearchResult
	err       error
	calls     int
	req       WebSearchRequest
}

func (f *fakeWebSearchProvider) Name() string { return f.name }
func (f *fakeWebSearchProvider) Available(context.Context, WebSearchRequest) WebSearchAvailability {
	if f.available.ErrorClass != "" || f.available.Reason != "" || f.available.Available {
		return f.available
	}
	return WebSearchAvailability{Available: true}
}
func (f *fakeWebSearchProvider) Search(_ context.Context, req WebSearchRequest) (WebSearchResult, error) {
	f.calls++
	f.req = req
	if f.err != nil {
		return WebSearchResult{}, f.err
	}
	return f.result, nil
}

func TestWebSearchDefinitionRequiresGrant(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithWebSearchOptions(WebSearchOptions{Enabled: true})
	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if toolDefExists(registry.DefinitionsForPrincipal(p), webSearchToolName) {
		t.Fatal("DefinitionsForPrincipal without grant included web_search")
	}
	grantToolInvoke(t, store, webSearchToolName, "telegram:1001")
	if !toolDefExists(registry.DefinitionsForPrincipal(p), webSearchToolName) {
		t.Fatal("DefinitionsForPrincipal with grant missing web_search")
	}
}

func TestWebSearchBlocksWithoutLeaseEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithWebSearchOptions(WebSearchOptions{Enabled: true})
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant:web_search:telegram:1001",
		GrantedBy:      "test",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: webSearchToolName,
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	out, err := registry.executeWithScopeAndPrincipal(context.Background(), webSearchToolName, json.RawMessage(`{"query":"aphelion"}`), sandbox.Scope{WorkingRoot: registry.workspace}, actor, adminSessionKey())
	if err == nil || !strings.Contains(err.Error(), "requires active continuation or operation plan lease evidence") {
		t.Fatalf("err = %v output=%s, want lease blocker", err, out)
	}
	if !strings.Contains(out, `"status": "blocked"`) || !strings.Contains(out, "lease evidence") {
		t.Fatalf("output = %s, want structured lease blocker", out)
	}
}

func TestWebSearchHostedSuccessNormalizesUntrustedResults(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	provider := &fakeWebSearchProvider{name: "openai_hosted", result: WebSearchResult{Results: []WebSearchResultItem{{Title: "Result", URL: "https://example.com", Snippet: "Snippet"}}}}
	registry.WithWebSearchOptions(WebSearchOptions{Enabled: true, ProviderOrder: []string{"openai_hosted"}})
	registry.SetWebSearchProviders(provider)
	grantToolInvoke(t, store, webSearchToolName, "telegram:1001")
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	out, err := registry.executeWithScopeAndPrincipal(context.Background(), webSearchToolName, json.RawMessage(`{"query":"aphelion web search","count":1}`), sandbox.Scope{WorkingRoot: registry.workspace}, actor, adminSessionKey())
	if err != nil {
		t.Fatalf("web_search err = %v output=%s", err, out)
	}
	if provider.calls != 1 || provider.req.Query != "aphelion web search" || provider.req.Count != 1 {
		t.Fatalf("provider calls=%d req=%#v", provider.calls, provider.req)
	}
	for _, want := range []string{`"status": "completed"`, `"provider": "openai_hosted"`, `"untrusted": true`, `"url": "https://example.com"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	grant, ok, err := store.CapabilityGrant("grant:" + webSearchToolName + ":telegram:1001")
	if err != nil || !ok || grant.InvocationCount == 0 {
		t.Fatalf("grant ok=%t err=%v invocation_count=%d", ok, err, grant.InvocationCount)
	}
}

func TestWebSearchFallbackRequiresAttemptBudget(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	hosted := &fakeWebSearchProvider{name: "openai_hosted", available: WebSearchAvailability{Available: false, ErrorClass: "unsupported_tool"}}
	brave := &fakeWebSearchProvider{name: "brave", result: WebSearchResult{Results: []WebSearchResultItem{{Title: "Fallback", URL: "https://fallback.example"}}}}
	registry.WithWebSearchOptions(WebSearchOptions{Enabled: true, ProviderOrder: []string{"openai_hosted", "brave"}})
	registry.SetWebSearchProviders(hosted, brave)
	grantWebSearchInvoke(t, store, "telegram:1001", `{"web_search":{"providers":["openai_hosted","brave"],"max_provider_attempts_per_invocation":2,"max_count":5}}`)
	grantAuthorityUseLease(t, store, adminSessionKey())
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	out, err := registry.executeWithScopeAndPrincipal(context.Background(), webSearchToolName, json.RawMessage(`{"query":"fallback please"}`), sandbox.Scope{WorkingRoot: registry.workspace}, actor, adminSessionKey())
	if err != nil {
		t.Fatalf("web_search fallback err = %v output=%s", err, out)
	}
	if brave.calls != 1 || !strings.Contains(out, `"fallback_attempted": true`) || !strings.Contains(out, `"provider": "brave"`) {
		t.Fatalf("fallback output=%s brave_calls=%d", out, brave.calls)
	}
}

func TestBraveProviderUsesHTTPEndpointAndRedactsCredential(t *testing.T) {
	seenToken := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenToken = r.Header.Get("X-Subscription-Token")
		if r.URL.Query().Get("q") != "bounded search" {
			t.Fatalf("query = %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"Brave Result","url":"https://example.test","description":"snippet","age":"2 days ago","profile":{"name":"Example"}}]}}`))
	}))
	defer server.Close()
	t.Setenv("BRAVE_TEST_KEY", "secret-token")
	provider := newBraveWebSearchProvider(WebSearchBraveOptions{Enabled: true, APIKeyEnv: "BRAVE_TEST_KEY", Endpoint: server.URL, HTTPClient: server.Client()})
	result, err := provider.Search(context.Background(), WebSearchRequest{Query: "bounded search", Count: 1})
	if err != nil {
		t.Fatalf("Brave Search() err = %v", err)
	}
	if seenToken != "secret-token" {
		t.Fatalf("seen token = %q", seenToken)
	}
	if len(result.Results) != 1 || result.Results[0].Title != "Brave Result" || result.Results[0].SiteName != "Example" {
		t.Fatalf("result = %#v", result)
	}
}

func grantWebSearchInvoke(t *testing.T, store *session.SQLiteStore, principal string, constraints string) {
	t.Helper()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant:" + webSearchToolName + ":" + principal,
		GrantedBy:      "test",
		GrantedTo:      principal,
		Kind:           session.CapabilityKindTool,
		TargetResource: webSearchToolName,
		AllowedActions: []string{"invoke"},
		Constraints:    constraints,
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(%s) err = %v", webSearchToolName, err)
	}
}

func TestWebSearchConstraintsLimitCountAndAllowedFields(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithWebSearchOptions(WebSearchOptions{Enabled: true, ProviderOrder: []string{"openai_hosted"}})
	registry.SetWebSearchProviders(&fakeWebSearchProvider{name: "openai_hosted", result: WebSearchResult{Results: []WebSearchResultItem{{Title: "Result", URL: "https://example.com"}}}})
	constraints := `{"tool_invocation":{"actions":{"invoke":{"allowed_fields":["query","count"]}}},"web_search":{"providers":["openai_hosted"],"max_count":2,"default_count":1,"max_provider_attempts_per_invocation":1}}`
	grantWebSearchInvoke(t, store, "telegram:1001", constraints)
	grantAuthorityUseLease(t, store, adminSessionKey())
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), webSearchToolName, json.RawMessage(`{"query":"too many","count":3}`), sandbox.Scope{WorkingRoot: registry.workspace}, actor, adminSessionKey())
	if err == nil || !strings.Contains(err.Error(), "exceeds max_count") {
		t.Fatalf("count err = %v output=%s, want max_count blocker", err, out)
	}

	out, err = registry.executeWithScopeAndPrincipal(context.Background(), webSearchToolName, json.RawMessage(`{"query":"extra field","provider_policy":"openai_hosted"}`), sandbox.Scope{WorkingRoot: registry.workspace}, actor, adminSessionKey())
	if err == nil || !strings.Contains(err.Error(), `input field "provider_policy" is not allowed`) {
		t.Fatalf("field err = %v output=%s, want allowed_fields blocker", err, out)
	}
}
