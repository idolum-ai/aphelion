//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const (
	searchWebToolName          = "search_web"
	searchWebDefaultLimit      = 3
	searchWebMaxLimit          = 5
	searchWebMaxQueriesPerTask = 3
)

type searchWebInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type searchWebResult struct {
	Title   string
	URL     string
	Snippet string
}

type searchWebProvider interface {
	Search(ctx context.Context, query string, limit int) ([]searchWebResult, error)
}

type searchWebRuntimeState struct {
	mu     sync.Mutex
	counts map[string]int
}

type BraveSearchClientOptions struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

type BraveSearchClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type braveSearchResponse struct {
	Web braveSearchWebSection `json:"web"`
}

type braveSearchWebSection struct {
	Results []braveSearchResult `json:"results"`
}

type braveSearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

func newSearchWebRuntimeState() *searchWebRuntimeState {
	return &searchWebRuntimeState{
		counts: make(map[string]int),
	}
}

func NewBraveSearchClient(options BraveSearchClientOptions) (*BraveSearchClient, error) {
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("search.brave.api_key is required")
	}
	baseURL := strings.TrimSpace(options.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.search.brave.com"
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("search.brave.base_url is invalid: %w", err)
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &BraveSearchClient{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}, nil
}

func (c *BraveSearchClient) Search(ctx context.Context, query string, limit int) ([]searchWebResult, error) {
	if c == nil || strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("search_web backend is not configured")
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search_web query is required")
	}
	if limit <= 0 {
		limit = searchWebDefaultLimit
	}

	reqURL, err := url.Parse(c.baseURL + "/res/v1/web/search")
	if err != nil {
		return nil, fmt.Errorf("build brave search url: %w", err)
	}
	values := reqURL.Query()
	values.Set("q", query)
	values.Set("count", strconv.Itoa(limit))
	values.Set("text_decorations", "0")
	values.Set("spellcheck", "0")
	reqURL.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build brave search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search_web request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("search_web upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read search_web response: %w", err)
	}
	var parsed braveSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode search_web response: %w", err)
	}

	out := make([]searchWebResult, 0, len(parsed.Web.Results))
	for _, item := range parsed.Web.Results {
		title := strings.TrimSpace(item.Title)
		link := strings.TrimSpace(item.URL)
		snippet := strings.TrimSpace(item.Description)
		if title == "" || link == "" {
			continue
		}
		out = append(out, searchWebResult{
			Title:   title,
			URL:     link,
			Snippet: snippet,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *Registry) WithSearchWeb(provider searchWebProvider) *Registry {
	r.searchWeb = provider
	return r
}

func (r *Registry) authorityManagedTool(name string) bool {
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, searchWebToolName) {
		return true
	}
	_, ok := r.externalManifestByName(name)
	return ok
}

func (r *Registry) toolAuthorityAccessAllowed(toolName string, p principal.Principal) (bool, error) {
	toolName = strings.TrimSpace(toolName)
	if !r.authorityManagedTool(toolName) {
		return true, nil
	}
	if r.store == nil {
		return false, fmt.Errorf("%s requires transcript store", toolName)
	}
	registered, ok, err := r.store.RegisteredTool(toolName)
	if err != nil {
		return false, err
	}
	if !ok || !registered.Registered {
		return false, nil
	}
	keys := toolAuthorityPrincipalKeys(p)
	if len(keys) == 0 {
		return false, nil
	}
	for _, key := range keys {
		exposure, exists, err := r.store.ToolExposure(toolName, key)
		if err != nil {
			return false, err
		}
		if exists && exposure.Active {
			return true, nil
		}
	}
	_, allowedByGrant, err := r.capabilityGrantAllowsAuthorityToolAccess(toolName, p)
	if err != nil {
		return false, err
	}
	if allowedByGrant {
		return true, nil
	}
	return false, nil
}

func (r *Registry) requireAuthorityToolAccess(name string, p principal.Principal) error {
	name = strings.TrimSpace(name)
	if !r.authorityManagedTool(name) {
		return nil
	}
	if r.store == nil {
		return fmt.Errorf("%s requires transcript store", name)
	}
	registered, ok, err := r.store.RegisteredTool(name)
	if err != nil {
		return err
	}
	if !ok || !registered.Registered {
		return fmt.Errorf("tool %q is not registered", name)
	}
	lookup := toolAuthorityPrincipalKeys(p)
	principalID := toolAuthorityPrincipalDisplay(p)
	if len(lookup) == 0 {
		return fmt.Errorf("tool %q is not exposed to principal %q", name, principalID)
	}
	for _, key := range lookup {
		exposure, exists, err := r.store.ToolExposure(name, key)
		if err != nil {
			return err
		}
		if exists && exposure.Active {
			return nil
		}
	}
	grant, allowedByGrant, err := r.capabilityGrantAllowsAuthorityToolAccess(name, p)
	if err != nil {
		return err
	}
	if allowedByGrant {
		if _, err := r.store.RecordCapabilityInvocation(session.CapabilityInvocation{
			GrantID:   grant.GrantID,
			Principal: toolAuthorityPrincipalDisplay(p),
			Action:    "invoke",
			Status:    "allowed",
		}); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("tool %q is not exposed or granted to principal %q", name, principalID)
}

func (r *Registry) capabilityGrantAllowsAuthorityToolAccess(toolName string, p principal.Principal) (session.CapabilityGrant, bool, error) {
	if r == nil || r.store == nil {
		return session.CapabilityGrant{}, false, nil
	}
	candidates := append([]string{}, toolAuthorityPrincipalKeys(p)...)
	candidates = append(candidates, toolAuthorityPrincipalDisplay(p))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		grant, ok, err := r.store.ActiveCapabilityGrant(session.CapabilityKindTool, toolName, candidate, "invoke")
		if err != nil {
			return session.CapabilityGrant{}, false, err
		}
		if ok {
			return grant, true, nil
		}
	}
	return session.CapabilityGrant{}, false, nil
}

func toolAuthorityPrincipalKeys(p principal.Principal) []string {
	keys := make([]string, 0, 6)

	switch p.Role {
	case principal.RoleDurableAgent:
		id := strings.TrimSpace(p.DurableAgentID)
		if id != "" {
			keys = append(keys, id, "durable_agent:"+id)
		}
	case principal.RoleApprovedUser, principal.RoleAdmin:
		if p.TelegramUserID > 0 {
			id := strconv.FormatInt(p.TelegramUserID, 10)
			keys = append(keys, "telegram:"+id, "principal:"+id, id)
		} else if p.Role == principal.RoleAdmin {
			keys = append(keys, "admin")
		}
	}

	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func toolAuthorityPrincipalDisplay(p principal.Principal) string {
	switch p.Role {
	case principal.RoleDurableAgent:
		if id := strings.TrimSpace(p.DurableAgentID); id != "" {
			return id
		}
	case principal.RoleApprovedUser, principal.RoleAdmin:
		if p.TelegramUserID > 0 {
			return "telegram:" + strconv.FormatInt(p.TelegramUserID, 10)
		}
	}
	role := strings.TrimSpace(string(p.Role))
	if role == "" {
		return "unknown"
	}
	return role
}

func (r *Registry) searchWebTool(ctx context.Context, input json.RawMessage, _ principal.Principal, key session.SessionKey) (string, error) {
	if r.searchWeb == nil {
		return "", fmt.Errorf("search_web is not configured")
	}

	var in searchWebInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode search_web input: %w", err)
	}

	query := strings.TrimSpace(in.Query)
	if query == "" {
		return "", fmt.Errorf("search_web query is required")
	}
	if len(query) > 500 {
		return "", fmt.Errorf("search_web query must be <= 500 chars")
	}

	limit := in.Limit
	if limit <= 0 {
		limit = searchWebDefaultLimit
	}
	if limit > searchWebMaxLimit {
		return "", fmt.Errorf("search_web limit must be between 1 and %d", searchWebMaxLimit)
	}

	if err := r.consumeSearchWebBudget(key); err != nil {
		return "", err
	}

	results, err := r.searchWeb.Search(ctx, query, limit)
	if err != nil {
		return "", err
	}
	return renderSearchWebResults(query, results), nil
}

func (r *Registry) consumeSearchWebBudget(key session.SessionKey) error {
	if r == nil {
		return nil
	}
	state := r.searchWebState
	if state == nil {
		state = newSearchWebRuntimeState()
		r.searchWebState = state
	}
	bucket := state.bucketKey(r.store, key)
	if bucket == "" {
		return nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.counts == nil {
		state.counts = make(map[string]int)
	}
	state.counts[bucket]++
	count := state.counts[bucket]
	if len(state.counts) > 4096 {
		state.counts = map[string]int{
			bucket: count,
		}
	}
	if count > searchWebMaxQueriesPerTask {
		return fmt.Errorf("search_web query cap exceeded: max %d queries per task", searchWebMaxQueriesPerTask)
	}
	return nil
}

func (s *searchWebRuntimeState) bucketKey(store *session.SQLiteStore, key session.SessionKey) string {
	if s == nil {
		return ""
	}
	if store != nil {
		if run, err := store.LatestTurnRun(key); err == nil && run != nil && run.ID > 0 && run.Status == session.TurnRunStatusRunning {
			return "run:" + strconv.FormatInt(run.ID, 10)
		}
	}
	sessionID := strings.TrimSpace(session.SessionIDForKey(key))
	if sessionID == "" {
		return ""
	}
	return "session:" + sessionID
}

func renderSearchWebResults(query string, results []searchWebResult) string {
	var b strings.Builder
	b.WriteString("[WEB_SEARCH]\n")
	fmt.Fprintf(&b, "query: %s\n", strings.TrimSpace(query))
	fmt.Fprintf(&b, "count: %d\n", len(results))
	if len(results) == 0 {
		b.WriteString("no_hits\n[/WEB_SEARCH]")
		return b.String()
	}
	for i, item := range results {
		fmt.Fprintf(&b, "\n%d. title: %s\n", i+1, strings.TrimSpace(item.Title))
		fmt.Fprintf(&b, "   url: %s\n", strings.TrimSpace(item.URL))
		if snippet := strings.TrimSpace(item.Snippet); snippet != "" {
			fmt.Fprintf(&b, "   snippet: %s\n", truncate(snippet, 400))
		}
	}
	b.WriteString("[/WEB_SEARCH]")
	return b.String()
}
