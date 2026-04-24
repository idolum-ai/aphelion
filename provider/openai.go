//go:build linux

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
)

const (
	defaultOpenAIBaseURL   = "https://api.openai.com/v1"
	maxOpenAIResponseBytes = 1 << 20
)

var _ agent.Provider = (*OpenAI)(nil)
var _ agent.ProviderWithOptions = (*OpenAI)(nil)

type OpenAIOptions struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxTokens  int
	HTTPClient *http.Client
	UserAgent  string
}

type OpenAI struct {
	endpoint  string
	client    *http.Client
	apiKey    string
	model     string
	maxTokens int
	userAgent string
}

func NewOpenAI(opts OpenAIOptions) (*OpenAI, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("openai: api key is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("openai: model is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	return &OpenAI{
		endpoint:  baseURL + "/chat/completions",
		client:    client,
		apiKey:    opts.APIKey,
		model:     opts.Model,
		maxTokens: opts.MaxTokens,
		userAgent: opts.UserAgent,
	}, nil
}

func (o *OpenAI) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return o.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (o *OpenAI) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	reqBody := openAIRequest{
		Model:               o.model,
		MaxCompletionTokens: o.maxTokens,
		Messages:            toOpenRouterMessages(messages),
		ReasoningEffort:     openAIReasoningEffort(opts.Reasoning.Effort),
	}
	if defs := toOpenRouterTools(tools); len(defs) > 0 {
		reqBody.Tools = defs
		reqBody.ToolChoice = "auto"
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("openai: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	if o.userAgent != "" {
		req.Header.Set("User-Agent", o.userAgent)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenAIResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("openai: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("openai: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	var parsed openRouterResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	return mapOpenRouterResponse(parsed), nil
}

type openAIRequest struct {
	Model               string              `json:"model"`
	Messages            []openRouterMessage `json:"messages"`
	MaxCompletionTokens int                 `json:"max_completion_tokens,omitempty"`
	Tools               []openRouterTool    `json:"tools,omitempty"`
	ToolChoice          string              `json:"tool_choice,omitempty"`
	ReasoningEffort     string              `json:"reasoning_effort,omitempty"`
}

func openAIReasoningEffort(effort agent.ReasoningEffort) string {
	switch effort {
	case agent.ReasoningEffortNone,
		agent.ReasoningEffortLow,
		agent.ReasoningEffortMedium,
		agent.ReasoningEffortHigh,
		agent.ReasoningEffortXHigh:
		return string(effort)
	default:
		return ""
	}
}
