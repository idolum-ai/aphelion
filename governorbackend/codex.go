//go:build linux

package governorbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/governorauth"
)

const (
	maxCodexResponseBytes        = 1 << 20 // 1 MiB
	codexRefreshClientID         = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultCodexModel            = "gpt-5.5"
	defaultCodexPrompt           = "You are Codex, a coding agent. Help the user directly and use tools when needed."
	maxCodexContinuations        = 3
	defaultCodexTransportRetries = 1
	codexStreamCloseRecoverDelay = 250 * time.Millisecond
)

const (
	codexIncompleteReasonStatusClosed        = "response.incomplete"
	codexIncompleteReasonStreamClosed        = "stream_closed_after_response_id"
	codexIncompleteReasonPartialStreamClosed = "partial_stream_closed"
)

const (
	codexFailureCodeContextWindow    = "context_length_exceeded"
	codexFailureCodeInvalidPrompt    = "invalid_prompt"
	codexFailureCodeRateLimit        = "rate_limit_exceeded"
	codexFailureCodeServerOverloaded = "server_is_overloaded"
	codexFailureCodeSlowDown         = "slow_down"
)

var (
	ErrCodexUnauthorized = errors.New("codex unauthorized")
	ErrCodexForbidden    = errors.New("codex forbidden")
	ErrCodexRateLimited  = errors.New("codex rate limited")
	ErrCodexServer       = errors.New("codex upstream failure")
)

type CodexOptions struct {
	BaseURL          string
	AccessToken      string
	RefreshToken     string
	AccountID        string
	RefreshURL       string
	Model            string
	StoreResponses   bool
	MaxContinuations int
	TransportRetries int
	HTTPClient       *http.Client
	UserAgent        string
	LoadTokens       func() (governorauth.CodexTokens, error)
	SaveTokens       func(governorauth.CodexTokens, time.Time) error
	Now              func() time.Time
}

type Codex struct {
	endpoint         string
	refreshURL       string
	client           *http.Client
	userAgent        string
	model            string
	storeResponses   bool
	maxContinuations int
	transportRetries int
	loadTokens       func() (governorauth.CodexTokens, error)
	saveTokens       func(governorauth.CodexTokens, time.Time) error
	now              func() time.Time

	mu           sync.Mutex
	accessToken  string
	refreshToken string
	accountID    string
}

var _ agent.Provider = (*Codex)(nil)
var _ agent.ProviderWithOptions = (*Codex)(nil)
var _ agent.StreamingProvider = (*Codex)(nil)

func NewCodex(opts CodexOptions) (*Codex, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, fmt.Errorf("codex: base url is required")
	}
	if strings.TrimSpace(opts.AccessToken) == "" {
		return nil, fmt.Errorf("codex: access token is required")
	}
	if strings.TrimSpace(opts.AccountID) == "" {
		return nil, fmt.Errorf("codex: account id is required")
	}

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	refreshURL := strings.TrimSpace(opts.RefreshURL)
	if refreshURL == "" {
		refreshURL = governorauth.DefaultCodexRefreshURL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = defaultCodexModel
	}
	maxContinuations := opts.MaxContinuations
	if maxContinuations <= 0 {
		maxContinuations = maxCodexContinuations
	}
	transportRetries := opts.TransportRetries
	if transportRetries < 0 {
		transportRetries = defaultCodexTransportRetries
	}

	return &Codex{
		endpoint:         codexResponsesEndpoint(opts.BaseURL),
		refreshURL:       refreshURL,
		accessToken:      strings.TrimSpace(opts.AccessToken),
		refreshToken:     strings.TrimSpace(opts.RefreshToken),
		accountID:        strings.TrimSpace(opts.AccountID),
		client:           client,
		userAgent:        opts.UserAgent,
		model:            model,
		storeResponses:   opts.StoreResponses,
		maxContinuations: maxContinuations,
		transportRetries: transportRetries,
		loadTokens:       opts.LoadTokens,
		saveTokens:       opts.SaveTokens,
		now:              now,
	}, nil
}

func (c *Codex) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return c.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (c *Codex) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	return c.complete(ctx, messages, tools, opts, nil, true)
}

func (c *Codex) Stream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	return c.complete(ctx, messages, tools, agent.CompleteOptions{}, cb, true)
}

func (c *Codex) complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback, allowRetry bool) (*agent.Response, error) {
	aggregate := newCodexResponseAccumulator()
	configuredStoreResponses := c.effectiveStoreResponses()
	storeResponses := configuredStoreResponses
	plan := planFullCodexRequest(messages, storeResponses)
	if storeResponses {
		plan = planCodexRequest(messages)
	}
	continuations := 0
	usedPreviousResponseFallback := false

	for {
		result, err := c.completeRequest(ctx, plan, tools, opts, cb, allowRetry, storeResponses)
		if err != nil {
			if storeResponses && isStoreResponsesRejected(err) {
				if aggregate.hasPartial() {
					return nil, newCodexIncompleteError("stored-response continuation rejected", aggregate.response(), aggregate.responseID, plan.mode, storeResponses)
				}
				storeResponses = false
				aggregate = newCodexResponseAccumulator()
				plan = planFullCodexRequest(messages, false)
				continuations = 0
				usedPreviousResponseFallback = false
				continue
			}
			if storeResponses && plan.mode == codexTurnModeIncrementalToolResults && !usedPreviousResponseFallback && isPreviousResponseRejected(err) {
				plan = planFullCodexRequest(messages, storeResponses)
				usedPreviousResponseFallback = true
				continue
			}
			return nil, err
		}

		aggregate.merge(result.Response, result.ResponseID)
		if result.Complete {
			return aggregate.response(), nil
		}
		responseID := strings.TrimSpace(result.ResponseID)
		if responseID == "" {
			return nil, newCodexIncompleteError("missing response id", aggregate.response(), "", plan.mode, storeResponses)
		}
		continuations++
		if continuations > c.maxContinuations {
			return nil, newCodexIncompleteError(fmt.Sprintf("response remained incomplete after %d continuation attempts", c.maxContinuations), aggregate.response(), responseID, plan.mode, storeResponses)
		}
		if !storeResponses {
			if !configuredStoreResponses {
				return nil, newCodexIncompleteError("without stored-response continuation", aggregate.response(), responseID, plan.mode, storeResponses)
			}
			storeResponses = true
		}
		if result.IncompleteReason == codexIncompleteReasonStreamClosed {
			if err := codexSleepWithContext(ctx, codexStreamCloseRecoverDelay); err != nil {
				return nil, err
			}
		}
		plan = planCodexContinuation(messages, responseID)
	}
}

func (c *Codex) completeRequest(ctx context.Context, plan codexRequestPlan, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback, allowRetry bool, storeResponses bool) (*codexCompletionResult, error) {
	for attempt := 0; attempt <= c.transportRetries; attempt++ {
		reqBody := buildCodexRequest(plan, tools, opts, true, c.model, storeResponses)

		var body bytes.Buffer
		if err := json.NewEncoder(&body).Encode(reqBody); err != nil {
			return nil, fmt.Errorf("codex: encode request: %w", err)
		}

		c.syncCredentialsFromStore()
		accessToken, accountID := c.currentCredentials()
		resp, err := c.doRequest(ctx, &body, accessToken, accountID)
		if err != nil {
			var apiErr codexAPIError
			if allowRetry && errors.As(err, &apiErr) && apiErr.statusCode == http.StatusUnauthorized {
				reauthorized, reauthErr := c.reauthorize(ctx, accessToken)
				if reauthorized {
					return c.completeRequest(ctx, plan, tools, opts, cb, false, storeResponses)
				}
				if reauthErr != nil {
					return nil, fmt.Errorf("%w: reauthorization failed: %v", err, reauthErr)
				}
			}
			if attempt < c.transportRetries && isRetryableCodexTransportError(err) {
				continue
			}
			return nil, err
		}

		result, consumeErr := func() (*codexCompletionResult, error) {
			defer resp.Body.Close()
			return consumeCodexStream(resp.Body, cb)
		}()
		if consumeErr != nil {
			if attempt < c.transportRetries && isRetryableCodexTransportError(consumeErr) {
				continue
			}
			return nil, consumeErr
		}
		return result, nil
	}
	return nil, fmt.Errorf("codex: transport retries exhausted")
}

func (c *Codex) effectiveStoreResponses() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.storeResponses
}

const (
	codexResponseStatusPending    codexResponseStatus = ""
	codexResponseStatusCompleted  codexResponseStatus = "completed"
	codexResponseStatusIncomplete codexResponseStatus = "incomplete"
)

const (
	codexTurnModeFullContext            codexTurnMode = "full_context"
	codexTurnModeIncrementalToolResults codexTurnMode = "incremental_tool_results"
	codexTurnModeContinuationOnly       codexTurnMode = "continuation_only"
)
