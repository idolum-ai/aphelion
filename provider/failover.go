//go:build linux

package provider

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
)

const (
	failoverMaxRetries     = 3
	failoverInitialBackoff = 100 * time.Millisecond
	failoverMaximumBackoff = 2 * time.Second
)

var _ agent.Provider = (*FailoverChain)(nil)
var _ agent.ProviderWithOptions = (*FailoverChain)(nil)
var _ agent.ManagedProvider = (*FailoverChain)(nil)
var _ agent.StreamingProvider = (*FailoverChain)(nil)

type NamedProvider struct {
	Name     string
	Provider agent.Provider
}

type FailoverAttempt struct {
	Name string
	Err  error
}

type ExhaustedError struct {
	Attempts []FailoverAttempt
}

func (e ExhaustedError) Error() string {
	if len(e.Attempts) == 0 {
		return "provider failover exhausted"
	}
	parts := make([]string, 0, len(e.Attempts))
	for _, attempt := range e.Attempts {
		parts = append(parts, fmt.Sprintf("%s: %v", attempt.Name, attempt.Err))
	}
	return "provider failover exhausted: " + strings.Join(parts, "; ")
}

func (e ExhaustedError) UserFacingFailure() string {
	return "Inference backends are unavailable after retries and fallback. This turn did not complete. You can /stop to cancel current work and try again."
}

type failoverEntry struct {
	name     string
	provider agent.Provider
}

type FailoverChain struct {
	entries []failoverEntry
}

func NewFailoverChain(entries []NamedProvider) (*FailoverChain, error) {
	normalized := make([]failoverEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Provider == nil {
			continue
		}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = "provider"
		}
		normalized = append(normalized, failoverEntry{name: name, provider: entry.Provider})
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("provider failover chain is empty")
	}
	return &FailoverChain{entries: normalized}, nil
}

func (c *FailoverChain) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return c.CompleteManaged(ctx, messages, tools, agent.CompleteOptions{})
}

func (c *FailoverChain) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	return c.CompleteManaged(ctx, messages, tools, opts)
}

func (c *FailoverChain) CompleteManaged(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	return c.completeAcrossChain(ctx, messages, tools, opts)
}

func (c *FailoverChain) Stream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("provider failover chain is nil")
	}
	var attempts []FailoverAttempt
	for idx, entry := range c.entries {
		resp, started, err := c.streamWithRetry(ctx, entry, messages, tools, cb)
		if err == nil {
			if idx > 0 {
				log.Printf("WARN provider failover engaged from=%s to=%s", c.entries[0].name, entry.name)
			}
			return resp, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if started {
			return nil, err
		}
		attempts = append(attempts, FailoverAttempt{Name: entry.name, Err: err})
		if !shouldFailoverOnError(err) {
			return nil, err
		}
		log.Printf("WARN provider failed name=%s err=%v", entry.name, err)
	}
	return nil, ExhaustedError{Attempts: attempts}
}

func (c *FailoverChain) completeAcrossChain(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("provider failover chain is nil")
	}
	var attempts []FailoverAttempt
	for idx, entry := range c.entries {
		resp, err := c.completeWithRetry(ctx, entry, messages, tools, opts)
		if err == nil {
			if idx > 0 {
				log.Printf("WARN provider failover engaged from=%s to=%s", c.entries[0].name, entry.name)
			}
			return resp, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		attempts = append(attempts, FailoverAttempt{Name: entry.name, Err: err})
		if !shouldFailoverOnError(err) {
			return nil, err
		}
		log.Printf("WARN provider failed name=%s err=%v", entry.name, err)
	}
	return nil, ExhaustedError{Attempts: attempts}
}

func (c *FailoverChain) completeWithRetry(ctx context.Context, entry failoverEntry, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	backoff := failoverInitialBackoff
	attempt := 0
	for {
		resp, err := completeViaProvider(ctx, entry.provider, messages, tools, opts)
		if err == nil {
			return resp, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if !isRetryableProviderError(err) || attempt >= failoverMaxRetries {
			return nil, err
		}
		attempt++
		log.Printf("WARN provider call failed; retrying provider=%s attempt=%d max_retries=%d err=%v", entry.name, attempt, failoverMaxRetries, err)
		if err := sleepWithContext(ctx, backoff); err != nil {
			return nil, err
		}
		backoff *= 2
		if backoff > failoverMaximumBackoff {
			backoff = failoverMaximumBackoff
		}
	}
}

func (c *FailoverChain) streamWithRetry(ctx context.Context, entry failoverEntry, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, bool, error) {
	backoff := failoverInitialBackoff
	attempt := 0
	for {
		var started bool
		resp, err := streamViaProvider(ctx, entry.provider, messages, tools, func(chunk agent.StreamChunk) error {
			if chunk.Text != "" || chunk.ToolCall != nil || chunk.Usage != nil {
				started = true
			}
			if cb != nil {
				return cb(chunk)
			}
			return nil
		})
		if err == nil {
			return resp, started, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, started, ctxErr
		}
		if started || !isRetryableProviderError(err) || attempt >= failoverMaxRetries {
			return nil, started, err
		}
		attempt++
		log.Printf("WARN provider stream failed; retrying provider=%s attempt=%d max_retries=%d err=%v", entry.name, attempt, failoverMaxRetries, err)
		if err := sleepWithContext(ctx, backoff); err != nil {
			return nil, false, err
		}
		backoff *= 2
		if backoff > failoverMaximumBackoff {
			backoff = failoverMaximumBackoff
		}
	}
}

func completeViaProvider(ctx context.Context, provider agent.Provider, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	if withOptions, ok := provider.(agent.ProviderWithOptions); ok {
		return withOptions.CompleteWithOptions(ctx, messages, tools, opts)
	}
	return provider.Complete(ctx, messages, tools)
}

func streamViaProvider(ctx context.Context, provider agent.Provider, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	if streaming, ok := provider.(agent.StreamingProvider); ok {
		return streaming.Stream(ctx, messages, tools, cb)
	}
	resp, err := provider.Complete(ctx, messages, tools)
	if err != nil {
		return nil, err
	}
	if cb != nil && strings.TrimSpace(resp.Content) != "" {
		if err := cb(agent.StreamChunk{Type: "text", Text: resp.Content}); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type statusCoder interface {
	StatusCode() int
}

func isRetryableProviderError(err error) bool {
	var sc statusCoder
	if errors.As(err, &sc) {
		switch sc.StatusCode() {
		case 429, 500, 503:
			return true
		default:
			return false
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "429") || strings.Contains(msg, "500") || strings.Contains(msg, "503")
}

func shouldFailoverOnError(err error) bool {
	var sc statusCoder
	if errors.As(err, &sc) {
		switch sc.StatusCode() {
		case 401, 403, 429, 500, 503:
			return true
		default:
			return false
		}
	}
	return isRetryableProviderError(err)
}
