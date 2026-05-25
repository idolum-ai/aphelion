//go:build linux

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

const (
	defaultOpenAIBaseURL   = "https://api.openai.com/v1"
	maxOpenAIResponseBytes = 1 << 20
)

var _ agent.Provider = (*OpenAI)(nil)
var _ agent.ProviderWithOptions = (*OpenAI)(nil)
var _ agent.StreamingProvider = (*OpenAI)(nil)
var _ agent.StreamingProviderWithOptions = (*OpenAI)(nil)

type OpenAIOptions struct {
	APIKey      string
	BaseURL     string
	Model       string
	MaxTokens   int
	Transport   string
	ServiceTier string
	HTTPClient  *http.Client
	UserAgent   string
}

type OpenAI struct {
	chatEndpoint      string
	responsesEndpoint string
	client            *http.Client
	apiKey            string
	model             string
	maxTokens         int
	transport         string
	serviceTier       string
	userAgent         string
}

func NewOpenAI(opts OpenAIOptions) (*OpenAI, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("openai: api key is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("openai: model is required")
	}
	transport := core.NormalizeModelTransport(opts.Transport)
	if transport == "" {
		return nil, fmt.Errorf("openai: unsupported transport %q", opts.Transport)
	}
	switch transport {
	case core.ModelTransportAuto, core.ModelTransportOpenAIResponses, core.ModelTransportOpenAIChat:
	default:
		return nil, fmt.Errorf("openai: unsupported transport %q", opts.Transport)
	}
	serviceTier := core.NormalizeModelServiceTier(opts.ServiceTier)
	if serviceTier == "" && strings.TrimSpace(opts.ServiceTier) != "" &&
		!strings.EqualFold(strings.TrimSpace(opts.ServiceTier), "standard") &&
		!strings.EqualFold(strings.TrimSpace(opts.ServiceTier), "default") {
		return nil, fmt.Errorf("openai: unsupported service tier %q", opts.ServiceTier)
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
		chatEndpoint:      baseURL + "/chat/completions",
		responsesEndpoint: baseURL + "/responses",
		client:            client,
		apiKey:            opts.APIKey,
		model:             opts.Model,
		maxTokens:         opts.MaxTokens,
		transport:         transport,
		serviceTier:       serviceTier,
		userAgent:         opts.UserAgent,
	}, nil
}

func (o *OpenAI) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return o.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (o *OpenAI) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	if o.resolveTransport(tools, opts) == core.ModelTransportOpenAIResponses {
		return o.completeResponses(ctx, messages, tools, opts)
	}
	return o.completeChat(ctx, messages, tools, opts)
}

func (o *OpenAI) Stream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	return o.StreamWithOptions(ctx, messages, tools, agent.CompleteOptions{}, cb)
}

func (o *OpenAI) StreamWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback) (*agent.Response, error) {
	if o.resolveTransport(tools, opts) == core.ModelTransportOpenAIResponses {
		return o.streamResponses(ctx, messages, tools, opts, cb)
	}
	return o.streamChat(ctx, messages, tools, opts, cb)
}

func (o *OpenAI) resolveTransport(tools []agent.ToolDef, opts agent.CompleteOptions) string {
	switch o.transport {
	case core.ModelTransportOpenAIResponses:
		return core.ModelTransportOpenAIResponses
	case core.ModelTransportOpenAIChat:
		return core.ModelTransportOpenAIChat
	default:
		if shouldUseOpenAIResponses(o.model, tools, opts) {
			return core.ModelTransportOpenAIResponses
		}
		return core.ModelTransportOpenAIChat
	}
}
