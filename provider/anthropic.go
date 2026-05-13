//go:build linux

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/internal"
)

const (
	defaultAnthropicEndpoint = "https://api.anthropic.com/v1/messages"
	defaultAnthropicVersion  = "2023-06-01"
)

var _ agent.Provider = (*Anthropic)(nil)
var _ agent.StreamingProvider = (*Anthropic)(nil)
var _ agent.ProviderWithOptions = (*Anthropic)(nil)

// AnthropicOptions configures the Anthropic provider client.
type AnthropicOptions struct {
	APIKey           string
	Model            string
	MaxTokens        int
	CacheStrategy    string
	CacheTTL         string
	HTTPClient       *http.Client
	BaseURL          string
	AnthropicVersion string
	UserAgent        string
}

// Anthropic implements agent.Provider against the Anthropic Messages API.
type Anthropic struct {
	endpoint  string
	client    *http.Client
	apiKey    string
	model     string
	maxTokens int
	version   string
	userAgent string
	cache     anthropicCachePolicy
}

// NewAnthropic creates a new Anthropic client.
func NewAnthropic(opts AnthropicOptions) (*Anthropic, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("anthropic: api key is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("anthropic: model is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := opts.BaseURL
	if endpoint == "" {
		endpoint = defaultAnthropicEndpoint
	}
	version := opts.AnthropicVersion
	if version == "" {
		version = defaultAnthropicVersion
	}
	cache, err := newAnthropicCachePolicy(opts.CacheStrategy, opts.CacheTTL)
	if err != nil {
		return nil, err
	}

	return &Anthropic{
		endpoint:  endpoint,
		client:    client,
		apiKey:    opts.APIKey,
		model:     opts.Model,
		maxTokens: opts.MaxTokens,
		version:   version,
		userAgent: opts.UserAgent,
		cache:     cache,
	}, nil
}

// Complete sends the assembled history to Anthropic and returns the response.
func (a *Anthropic) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return a.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (a *Anthropic) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	reqBody := a.buildRequest(messages, tools, false, opts)
	resp, err := a.doRequest(ctx, reqBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("anthropic: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	var anthRes anthropicResponse
	if err := json.Unmarshal(body, &anthRes); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w", err)
	}

	return mapAnthropicResponse(anthRes, opts.Reasoning.Summary), nil
}

func (a *Anthropic) Stream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	reqBody := a.buildRequest(messages, tools, true, agent.CompleteOptions{})
	resp, err := a.doRequest(ctx, reqBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("anthropic: read response: %w", readErr)
		}
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("anthropic: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	parser := newAnthropicStreamParser(cb)
	for event := range internal.ParseSSE(resp.Body) {
		if strings.EqualFold(strings.TrimSpace(event.Data), "[DONE]") {
			break
		}
		if err := parser.consume(event); err != nil {
			return nil, err
		}
	}
	return parser.response(), parser.err()
}
