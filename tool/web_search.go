//go:build linux

package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const webSearchToolName = "web_search"

type WebSearchOptions struct {
	Enabled       bool
	ProviderOrder []string
	DefaultCount  int
	MaxCount      int
	Timeout       time.Duration
	CacheTTL      time.Duration
	OpenAIHosted  WebSearchOpenAIOptions
	Brave         WebSearchBraveOptions
}

type WebSearchOpenAIOptions struct {
	Enabled     bool
	ContextSize string
}

type WebSearchBraveOptions struct {
	Enabled    bool
	APIKeyEnv  string
	APIKeyFile string
	Endpoint   string
	HTTPClient *http.Client
}

type WebSearchProvider interface {
	Name() string
	Available(context.Context, WebSearchRequest) WebSearchAvailability
	Search(context.Context, WebSearchRequest) (WebSearchResult, error)
}

type WebSearchAvailability struct {
	Available  bool
	ErrorClass string
	Reason     string
}

type WebSearchRequest struct {
	Query          string
	Count          int
	Freshness      string
	AllowedDomains []string
	BlockedDomains []string
	ContextSize    string
}

type WebSearchResult struct {
	Results []WebSearchResultItem
}

type WebSearchResultItem struct {
	Title     string `json:"title,omitempty"`
	URL       string `json:"url,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
	Published string `json:"published,omitempty"`
	SiteName  string `json:"site_name,omitempty"`
}

type WebSearchProviderError struct {
	Class        string
	Message      string
	PostExternal bool
	Policy       bool
}

func (e WebSearchProviderError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return strings.TrimSpace(e.Message)
	}
	if strings.TrimSpace(e.Class) != "" {
		return strings.TrimSpace(e.Class)
	}
	return "provider_error"
}

func WebSearchOptionsFromConfig(cfg config.WebSearchConfig) WebSearchOptions {
	timeout, _ := time.ParseDuration(strings.TrimSpace(cfg.Timeout))
	cacheTTL, _ := time.ParseDuration(strings.TrimSpace(cfg.CacheTTL))
	return NormalizeWebSearchOptions(WebSearchOptions{
		Enabled:       cfg.Enabled,
		ProviderOrder: cfg.ProviderOrder,
		DefaultCount:  cfg.DefaultCount,
		MaxCount:      cfg.MaxCount,
		Timeout:       timeout,
		CacheTTL:      cacheTTL,
		OpenAIHosted: WebSearchOpenAIOptions{
			Enabled:     cfg.OpenAIHosted.Enabled,
			ContextSize: cfg.OpenAIHosted.ContextSize,
		},
		Brave: WebSearchBraveOptions{
			Enabled:    cfg.Brave.Enabled,
			APIKeyEnv:  cfg.Brave.APIKeyEnv,
			APIKeyFile: cfg.Brave.APIKeyFile,
			Endpoint:   cfg.Brave.Endpoint,
		},
	})
}

func NormalizeWebSearchOptions(opts WebSearchOptions) WebSearchOptions {
	opts.ProviderOrder = normalizeWebSearchProviderList(opts.ProviderOrder)
	if len(opts.ProviderOrder) == 0 {
		opts.ProviderOrder = []string{"openai_hosted", "brave"}
	}
	if opts.MaxCount <= 0 {
		opts.MaxCount = 10
	}
	if opts.DefaultCount <= 0 {
		opts.DefaultCount = 5
	}
	if opts.DefaultCount > opts.MaxCount {
		opts.DefaultCount = opts.MaxCount
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.CacheTTL < 0 {
		opts.CacheTTL = 0
	}
	opts.OpenAIHosted.ContextSize = normalizeWebSearchContextSize(opts.OpenAIHosted.ContextSize)
	if opts.OpenAIHosted.ContextSize == "" {
		opts.OpenAIHosted.ContextSize = "medium"
	}
	opts.Brave.APIKeyEnv = strings.TrimSpace(opts.Brave.APIKeyEnv)
	opts.Brave.APIKeyFile = strings.TrimSpace(opts.Brave.APIKeyFile)
	opts.Brave.Endpoint = strings.TrimSpace(opts.Brave.Endpoint)
	if opts.Brave.Endpoint == "" {
		opts.Brave.Endpoint = "https://api.search.brave.com/res/v1/web/search"
	}
	return opts
}

func (r *Registry) WithWebSearchOptions(opts WebSearchOptions) *Registry {
	if r != nil {
		r.webSearchOptions = NormalizeWebSearchOptions(opts)
		r.rebuildConfiguredWebSearchProviders()
	}
	return r
}

func (r *Registry) SetWebSearchProviders(providers ...WebSearchProvider) {
	if r == nil {
		return
	}
	r.webSearchProviders = normalizeWebSearchProviders(providers)
}

func (r *Registry) rebuildConfiguredWebSearchProviders() {
	if r == nil {
		return
	}
	providers := []WebSearchProvider{}
	if r.webSearchOptions.Brave.Enabled {
		providers = append(providers, newBraveWebSearchProvider(r.webSearchOptions.Brave))
	}
	r.webSearchProviders = normalizeWebSearchProviders(providers)
}

func normalizeWebSearchProviders(providers []WebSearchProvider) []WebSearchProvider {
	out := make([]WebSearchProvider, 0, len(providers))
	seen := map[string]struct{}{}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		name := normalizeWebSearchProvider(provider.Name())
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, provider)
	}
	return out
}

func (r *Registry) webSearchToolDefinition() (agent.ToolDef, bool) {
	if r == nil || r.store == nil || !r.webSearchOptions.Enabled {
		return agent.ToolDef{}, false
	}
	return agent.ToolDef{
		Name:        webSearchToolName,
		Description: "Search public web evidence through an authority-managed provider chain. Requires an active tool grant and active continuation/operation lease; returns structured untrusted external content or an exact blocker.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query."},"count":{"type":"integer","minimum":1,"description":"Requested result count."},"freshness":{"type":"string","enum":["day","week","month","year"],"description":"Optional freshness bound."},"allowed_domains":{"type":"array","items":{"type":"string"},"description":"Optional allowed domains."},"blocked_domains":{"type":"array","items":{"type":"string"},"description":"Optional blocked domains."},"provider_policy":{"type":"string","enum":["auto","openai_hosted","brave"],"description":"Provider choice policy."},"context_size":{"type":"string","enum":["low","medium","high"],"description":"Hosted-provider context size hint."}},"required":["query"]}`),
	}, true
}

func (r *Registry) webSearchAccessAllowed(p principal.Principal) (bool, error) {
	if r == nil || r.store == nil || !r.webSearchOptions.Enabled {
		return false, nil
	}
	_, ok, err := r.capabilityGrantAllowsAuthorityToolAccess(webSearchToolName, p)
	return ok, err
}

type webSearchInput struct {
	Action         string   `json:"action,omitempty"`
	Query          string   `json:"query,omitempty"`
	Count          int      `json:"count,omitempty"`
	Freshness      string   `json:"freshness,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
	ProviderPolicy string   `json:"provider_policy,omitempty"`
	ContextSize    string   `json:"context_size,omitempty"`
}

type webSearchAttempt struct {
	Provider   string `json:"provider"`
	Status     string `json:"status"`
	ErrorClass string `json:"error_class,omitempty"`
}

type webSearchExternalContent struct {
	Untrusted bool   `json:"untrusted"`
	Source    string `json:"source"`
	Provider  string `json:"provider,omitempty"`
}

type webSearchOutput struct {
	Status            string                   `json:"status"`
	GrantID           string                   `json:"grant_id,omitempty"`
	Query             string                   `json:"query,omitempty"`
	Provider          string                   `json:"provider,omitempty"`
	FallbackAttempted bool                     `json:"fallback_attempted"`
	Attempts          []webSearchAttempt       `json:"attempts,omitempty"`
	ExternalContent   webSearchExternalContent `json:"external_content"`
	Results           []WebSearchResultItem    `json:"results,omitempty"`
	Blocker           string                   `json:"blocker,omitempty"`
}

func (r *Registry) webSearch(ctx context.Context, input json.RawMessage, _ sandbox.Scope, p principal.Principal, key session.SessionKey) (string, error) {
	grant, useRef, err := r.requireWebSearchAccess(ctx, p, key, input)
	if err != nil {
		return renderWebSearchBlocker(err.Error(), grant.GrantID), err
	}
	in, req, policy, constraints, err := r.decodeWebSearchInput(grant, input)
	if err != nil {
		if recordErr := r.recordWebSearchInvocation(grant, p, useRef, "blocked", err.Error()); recordErr != nil {
			return renderWebSearchBlocker(err.Error(), grant.GrantID), errors.Join(err, recordErr)
		}
		return renderWebSearchBlocker(err.Error(), grant.GrantID), err
	}
	providers, err := r.webSearchProviderPlan(policy, constraints)
	if err != nil {
		if recordErr := r.recordWebSearchInvocation(grant, p, useRef, "blocked", err.Error()); recordErr != nil {
			return renderWebSearchBlocker(err.Error(), grant.GrantID), errors.Join(err, recordErr)
		}
		return renderWebSearchBlocker(err.Error(), grant.GrantID), err
	}
	if constraints.MaxQueriesPerInvocation > 0 && 1 > constraints.MaxQueriesPerInvocation {
		reason := "web_search query budget exceeded"
		err := fmt.Errorf("%s", reason)
		if recordErr := r.recordWebSearchInvocation(grant, p, useRef, "blocked", reason); recordErr != nil {
			return renderWebSearchBlocker(reason, grant.GrantID), errors.Join(err, recordErr)
		}
		return renderWebSearchBlocker(reason, grant.GrantID), err
	}
	maxAttempts := constraints.MaxProviderAttemptsPerInvocation
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	attempts := []webSearchAttempt{}
	providerMap := r.webSearchProviderMap()
	var lastErr error
	var lastErrClass string
	for _, providerName := range providers {
		if len(attempts) >= maxAttempts {
			break
		}
		provider := providerMap[providerName]
		if provider == nil {
			attempts = append(attempts, webSearchAttempt{Provider: providerName, Status: "unavailable", ErrorClass: "provider_not_configured"})
			lastErrClass = "provider_not_configured"
			continue
		}
		availability := provider.Available(ctx, req)
		if !availability.Available {
			class := firstNonEmpty(availability.ErrorClass, "provider_unavailable")
			attempts = append(attempts, webSearchAttempt{Provider: providerName, Status: "unavailable", ErrorClass: class})
			lastErrClass = class
			continue
		}
		result, err := provider.Search(ctx, req)
		if err != nil {
			class, policyRefusal := webSearchErrorClass(err)
			attempts = append(attempts, webSearchAttempt{Provider: providerName, Status: "failed", ErrorClass: class})
			lastErr = err
			lastErrClass = class
			if policyRefusal || policy != "auto" {
				break
			}
			continue
		}
		attempts = append(attempts, webSearchAttempt{Provider: providerName, Status: "completed"})
		items := normalizeWebSearchResultItems(result.Results, req.Count)
		out := webSearchOutput{
			Status:            "completed",
			GrantID:           strings.TrimSpace(grant.GrantID),
			Query:             in.Query,
			Provider:          providerName,
			FallbackAttempted: len(attempts) > 1,
			Attempts:          attempts,
			ExternalContent:   webSearchExternalContent{Untrusted: true, Source: webSearchToolName, Provider: providerName},
			Results:           items,
		}
		if err := r.recordWebSearchInvocation(grant, p, useRef, "completed", ""); err != nil {
			return marshalWebSearchOutput(out), err
		}
		return marshalWebSearchOutput(out), nil
	}
	status := "blocked"
	if lastErr != nil {
		status = "failed"
	}
	blocker := "web_search provider unavailable"
	if lastErrClass != "" {
		blocker = lastErrClass
	}
	out := webSearchOutput{
		Status:            status,
		GrantID:           strings.TrimSpace(grant.GrantID),
		Query:             in.Query,
		FallbackAttempted: len(attempts) > 1,
		Attempts:          attempts,
		ExternalContent:   webSearchExternalContent{Untrusted: true, Source: webSearchToolName},
		Blocker:           blocker,
	}
	if recordErr := r.recordWebSearchInvocation(grant, p, useRef, status, blocker); recordErr != nil {
		return marshalWebSearchOutput(out), errors.Join(lastErr, recordErr)
	}
	return marshalWebSearchOutput(out), lastErr
}

func (r *Registry) requireWebSearchAccess(ctx context.Context, p principal.Principal, key session.SessionKey, input json.RawMessage) (session.CapabilityGrant, session.AuthorityUseRef, error) {
	if r == nil || r.store == nil {
		return session.CapabilityGrant{}, session.AuthorityUseRef{}, fmt.Errorf("%s requires transcript store", webSearchToolName)
	}
	if !r.webSearchOptions.Enabled {
		return session.CapabilityGrant{}, session.AuthorityUseRef{}, fmt.Errorf("%s is disabled by config", webSearchToolName)
	}
	grant, ok, err := r.capabilityGrantAllowsAuthorityToolAccess(webSearchToolName, p)
	if err != nil {
		return session.CapabilityGrant{}, session.AuthorityUseRef{}, err
	}
	if !ok {
		return session.CapabilityGrant{}, session.AuthorityUseRef{}, fmt.Errorf("tool %q is not granted to principal %q", webSearchToolName, toolAuthorityPrincipalDisplay(p))
	}
	useRef, err := r.authorityUseRefForGrant(ctx, webSearchToolName, key, p)
	if err != nil {
		if recordErr := r.recordWebSearchInvocation(grant, p, useRef, "blocked", err.Error()); recordErr != nil {
			return grant, useRef, errors.Join(err, recordErr)
		}
		return grant, useRef, err
	}
	if err := validateCapabilityToolInvocationInput(grant, inputWithInvokeAction(input)); err != nil {
		if recordErr := r.recordWebSearchInvocation(grant, p, useRef, "blocked", err.Error()); recordErr != nil {
			return grant, useRef, errors.Join(err, recordErr)
		}
		return grant, useRef, err
	}
	return grant, useRef, nil
}

func inputWithInvokeAction(input json.RawMessage) json.RawMessage {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil || payload == nil {
		return input
	}
	if _, ok := payload["action"]; !ok {
		payload["action"] = json.RawMessage(`"invoke"`)
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return input
	}
	return out
}

func (r *Registry) decodeWebSearchInput(grant session.CapabilityGrant, input json.RawMessage) (webSearchInput, WebSearchRequest, string, webSearchConstraints, error) {
	var in webSearchInput
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, WebSearchRequest{}, "", webSearchConstraints{}, fmt.Errorf("decode web_search input: %w", err)
	}
	in.Query = strings.Join(strings.Fields(strings.TrimSpace(in.Query)), " ")
	if in.Query == "" {
		return in, WebSearchRequest{}, "", webSearchConstraints{}, fmt.Errorf("web_search query is required")
	}
	if len([]rune(in.Query)) > 500 {
		return in, WebSearchRequest{}, "", webSearchConstraints{}, fmt.Errorf("web_search query is too long")
	}
	constraints, err := webSearchConstraintsFromGrant(grant)
	if err != nil {
		return in, WebSearchRequest{}, "", webSearchConstraints{}, err
	}
	maxCount := firstPositive(constraints.MaxCount, r.webSearchOptions.MaxCount, 10)
	defaultCount := firstPositive(constraints.DefaultCount, r.webSearchOptions.DefaultCount, 5)
	if defaultCount > maxCount {
		defaultCount = maxCount
	}
	count := in.Count
	if count <= 0 {
		count = defaultCount
	}
	if count > maxCount {
		return in, WebSearchRequest{}, "", webSearchConstraints{}, fmt.Errorf("web_search count %d exceeds max_count %d", count, maxCount)
	}
	freshness := strings.ToLower(strings.TrimSpace(in.Freshness))
	switch freshness {
	case "", "day", "week", "month", "year":
	default:
		return in, WebSearchRequest{}, "", webSearchConstraints{}, fmt.Errorf("web_search freshness must be one of day|week|month|year")
	}
	policy := normalizeWebSearchProvider(firstNonEmpty(in.ProviderPolicy, "auto"))
	switch policy {
	case "auto", "openai_hosted", "brave":
	default:
		return in, WebSearchRequest{}, "", webSearchConstraints{}, fmt.Errorf("web_search provider_policy must be one of auto|openai_hosted|brave")
	}
	contextSize := normalizeWebSearchContextSize(firstNonEmpty(in.ContextSize, r.webSearchOptions.OpenAIHosted.ContextSize))
	if contextSize == "" {
		return in, WebSearchRequest{}, "", webSearchConstraints{}, fmt.Errorf("web_search context_size must be one of low|medium|high")
	}
	return in, WebSearchRequest{
		Query:          in.Query,
		Count:          count,
		Freshness:      freshness,
		AllowedDomains: normalizeWebSearchDomains(in.AllowedDomains),
		BlockedDomains: normalizeWebSearchDomains(in.BlockedDomains),
		ContextSize:    contextSize,
	}, policy, constraints, nil
}

type webSearchConstraints struct {
	Providers                        []string `json:"providers,omitempty"`
	MaxQueriesPerInvocation          int      `json:"max_queries_per_invocation,omitempty"`
	MaxProviderAttemptsPerInvocation int      `json:"max_provider_attempts_per_invocation,omitempty"`
	DefaultCount                     int      `json:"default_count,omitempty"`
	MaxCount                         int      `json:"max_count,omitempty"`
}

type webSearchConstraintsWrapper struct {
	WebSearch *webSearchConstraints `json:"web_search,omitempty"`
}

func webSearchConstraintsFromGrant(grant session.CapabilityGrant) (webSearchConstraints, error) {
	merged := webSearchConstraints{MaxQueriesPerInvocation: 1, MaxProviderAttemptsPerInvocation: 1}
	found := false
	for _, raw := range []string{grant.Contract, grant.Constraints} {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "{}" {
			continue
		}
		var wrapper webSearchConstraintsWrapper
		if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
			return webSearchConstraints{}, fmt.Errorf("decode web_search constraints: %w", err)
		}
		if wrapper.WebSearch == nil {
			continue
		}
		next := *wrapper.WebSearch
		if next.MaxQueriesPerInvocation < 0 || next.MaxProviderAttemptsPerInvocation < 0 || next.DefaultCount < 0 || next.MaxCount < 0 {
			return webSearchConstraints{}, fmt.Errorf("web_search constraints must not be negative")
		}
		if len(next.Providers) > 0 {
			merged.Providers = normalizeWebSearchProviderList(next.Providers)
		}
		if next.MaxQueriesPerInvocation > 0 {
			merged.MaxQueriesPerInvocation = next.MaxQueriesPerInvocation
		}
		if next.MaxProviderAttemptsPerInvocation > 0 {
			merged.MaxProviderAttemptsPerInvocation = next.MaxProviderAttemptsPerInvocation
		}
		if next.DefaultCount > 0 {
			merged.DefaultCount = next.DefaultCount
		}
		if next.MaxCount > 0 {
			merged.MaxCount = next.MaxCount
		}
		found = true
	}
	if found && len(merged.Providers) == 0 {
		// Empty provider list in a web_search block means no provider is approved.
		merged.Providers = []string{}
	}
	return merged, nil
}

func (r *Registry) webSearchProviderPlan(policy string, constraints webSearchConstraints) ([]string, error) {
	order := append([]string(nil), r.webSearchOptions.ProviderOrder...)
	if policy != "auto" {
		order = []string{policy}
	}
	allowed := map[string]struct{}{}
	if len(constraints.Providers) > 0 {
		for _, provider := range constraints.Providers {
			allowed[provider] = struct{}{}
		}
	}
	out := []string{}
	for _, provider := range order {
		provider = normalizeWebSearchProvider(provider)
		if provider == "" || provider == "auto" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[provider]; !ok {
				continue
			}
		}
		out = append(out, provider)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("web_search has no approved provider for policy %q", policy)
	}
	return out, nil
}

func (r *Registry) webSearchProviderMap() map[string]WebSearchProvider {
	out := map[string]WebSearchProvider{}
	for _, provider := range r.webSearchProviders {
		if provider == nil {
			continue
		}
		name := normalizeWebSearchProvider(provider.Name())
		if name != "" {
			out[name] = provider
		}
	}
	return out
}

func renderWebSearchBlocker(reason string, grantID string) string {
	return marshalWebSearchOutput(webSearchOutput{
		Status:          "blocked",
		GrantID:         strings.TrimSpace(grantID),
		ExternalContent: webSearchExternalContent{Untrusted: true, Source: webSearchToolName},
		Blocker:         strings.TrimSpace(reason),
	})
}

func marshalWebSearchOutput(out webSearchOutput) string {
	if out.ExternalContent.Source == "" {
		out.ExternalContent = webSearchExternalContent{Untrusted: true, Source: webSearchToolName, Provider: out.Provider}
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return string(raw)
}

func (r *Registry) recordWebSearchInvocation(grant session.CapabilityGrant, p principal.Principal, ref session.AuthorityUseRef, status string, errText string) error {
	if r == nil || r.store == nil || strings.TrimSpace(grant.GrantID) == "" {
		return nil
	}
	_, err := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
		GrantID:   grant.GrantID,
		Principal: toolAuthorityPrincipalDisplay(p),
		Action:    "invoke",
		Status:    strings.TrimSpace(status),
		ErrorText: strings.TrimSpace(errText),
	}, ref))
	return err
}

func webSearchErrorClass(err error) (string, bool) {
	var providerErr WebSearchProviderError
	if ok := errorAsWebSearchProvider(err, &providerErr); ok {
		return firstNonEmpty(providerErr.Class, "provider_error"), providerErr.Policy
	}
	return "provider_error", false
}

func errorAsWebSearchProvider(err error, target *WebSearchProviderError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(WebSearchProviderError); ok {
		*target = e
		return true
	}
	if e, ok := err.(*WebSearchProviderError); ok && e != nil {
		*target = *e
		return true
	}
	return false
}

func normalizeWebSearchProviderList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeWebSearchProvider(value)
		if value == "" || value == "auto" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeWebSearchProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func normalizeWebSearchContextSize(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeWebSearchDomains(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.TrimPrefix(value, "https://")
		value = strings.TrimPrefix(value, "http://")
		value = strings.Trim(value, "/")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizeWebSearchResultItems(items []WebSearchResultItem, limit int) []WebSearchResultItem {
	if limit <= 0 {
		limit = len(items)
	}
	out := make([]WebSearchResultItem, 0, len(items))
	for _, item := range items {
		item.Title = strings.TrimSpace(item.Title)
		item.URL = strings.TrimSpace(item.URL)
		item.Snippet = strings.TrimSpace(item.Snippet)
		item.Published = strings.TrimSpace(item.Published)
		item.SiteName = strings.TrimSpace(item.SiteName)
		if item.URL == "" && item.Title == "" && item.Snippet == "" {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// Brave provider. It is inert unless explicitly configured and invoked through
// web_search authority. Tests use httptest; default CI performs no live call.
type braveWebSearchProvider struct {
	opts WebSearchBraveOptions
}

func newBraveWebSearchProvider(opts WebSearchBraveOptions) *braveWebSearchProvider {
	return &braveWebSearchProvider{opts: opts}
}

func (p *braveWebSearchProvider) Name() string { return "brave" }

func (p *braveWebSearchProvider) Available(_ context.Context, req WebSearchRequest) WebSearchAvailability {
	if p == nil || !p.opts.Enabled {
		return WebSearchAvailability{Available: false, ErrorClass: "disabled", Reason: "brave disabled"}
	}
	if strings.TrimSpace(p.opts.Endpoint) == "" {
		return WebSearchAvailability{Available: false, ErrorClass: "missing_endpoint", Reason: "brave endpoint missing"}
	}
	if len(req.AllowedDomains) > 0 || len(req.BlockedDomains) > 0 {
		return WebSearchAvailability{Available: false, ErrorClass: "unsupported_filters", Reason: "brave domain filters are not supported in v1"}
	}
	if strings.TrimSpace(p.apiKey()) == "" {
		return WebSearchAvailability{Available: false, ErrorClass: "missing_credential", Reason: "brave credential is not configured"}
	}
	return WebSearchAvailability{Available: true}
}

func (p *braveWebSearchProvider) Search(ctx context.Context, req WebSearchRequest) (WebSearchResult, error) {
	if available := p.Available(ctx, req); !available.Available {
		return WebSearchResult{}, WebSearchProviderError{Class: firstNonEmpty(available.ErrorClass, "unavailable"), Message: available.Reason}
	}
	endpoint, err := url.Parse(strings.TrimSpace(p.opts.Endpoint))
	if err != nil {
		return WebSearchResult{}, WebSearchProviderError{Class: "invalid_endpoint", Message: "brave endpoint is invalid"}
	}
	query := endpoint.Query()
	query.Set("q", req.Query)
	if req.Count > 0 {
		query.Set("count", strconv.Itoa(req.Count))
	}
	if req.Freshness != "" {
		query.Set("freshness", req.Freshness)
	}
	endpoint.RawQuery = query.Encode()
	httpClient := p.opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return WebSearchResult{}, WebSearchProviderError{Class: "request_build_failed", Message: "build brave request failed"}
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Subscription-Token", strings.TrimSpace(p.apiKey()))
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return WebSearchResult{}, WebSearchProviderError{Class: "request_failed", Message: "brave request failed"}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return WebSearchResult{}, WebSearchProviderError{Class: "read_failed", Message: "read brave response failed", PostExternal: true}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return WebSearchResult{}, WebSearchProviderError{Class: "unauthorized", Message: "brave authorization failed", PostExternal: true}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WebSearchResult{}, WebSearchProviderError{Class: fmt.Sprintf("http_%d", resp.StatusCode), Message: "brave search failed", PostExternal: true}
	}
	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Age         string `json:"age"`
				Profile     struct {
					Name string `json:"name"`
				} `json:"profile"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return WebSearchResult{}, WebSearchProviderError{Class: "decode_failed", Message: "decode brave response failed", PostExternal: true}
	}
	items := make([]WebSearchResultItem, 0, len(parsed.Web.Results))
	for _, result := range parsed.Web.Results {
		items = append(items, WebSearchResultItem{Title: result.Title, URL: result.URL, Snippet: result.Description, Published: result.Age, SiteName: result.Profile.Name})
	}
	return WebSearchResult{Results: items}, nil
}

func (p *braveWebSearchProvider) apiKey() string {
	if p == nil {
		return ""
	}
	if env := strings.TrimSpace(p.opts.APIKeyEnv); env != "" {
		if value := strings.TrimSpace(os.Getenv(env)); value != "" {
			return value
		}
	}
	if path := strings.TrimSpace(p.opts.APIKeyFile); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}
