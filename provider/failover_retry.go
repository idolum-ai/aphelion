//go:build linux

package provider

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

func (c *FailoverChain) completeWithRetry(ctx context.Context, entry failoverEntry, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, events *[]core.ProviderEvent) (*agent.Response, error) {
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
		if shouldBypassSameProviderRetry(err, entry.name) || !isRetryableProviderError(err) || attempt >= failoverMaxRetries {
			return nil, err
		}
		attempt++
		log.Printf("WARN provider call failed; retrying provider=%s attempt=%d max_retries=%d err=%v", entry.name, attempt, failoverMaxRetries, err)
		recordProviderRetryEvent(events, entry.name, attempt, err)
		if err := sleepWithContext(ctx, providerRetryDelay(err, backoff)); err != nil {
			return nil, err
		}
		backoff *= 2
		if backoff > failoverMaximumBackoff {
			backoff = failoverMaximumBackoff
		}
	}
}

func (c *FailoverChain) streamWithRetry(ctx context.Context, entry failoverEntry, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback, events *[]core.ProviderEvent) (*agent.Response, bool, error) {
	backoff := failoverInitialBackoff
	attempt := 0
	for {
		var started bool
		resp, err := streamViaProvider(ctx, entry.provider, messages, tools, opts, func(chunk agent.StreamChunk) error {
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
		if started || shouldBypassSameProviderRetry(err, entry.name) || !isRetryableProviderError(err) || attempt >= failoverMaxRetries {
			return nil, started, err
		}
		attempt++
		log.Printf("WARN provider stream failed; retrying provider=%s attempt=%d max_retries=%d err=%v", entry.name, attempt, failoverMaxRetries, err)
		recordProviderRetryEvent(events, entry.name, attempt, err)
		if err := sleepWithContext(ctx, providerRetryDelay(err, backoff)); err != nil {
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

func streamViaProvider(ctx context.Context, provider agent.Provider, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback) (*agent.Response, error) {
	if streamingWithOptions, ok := provider.(agent.StreamingProviderWithOptions); ok {
		return streamingWithOptions.StreamWithOptions(ctx, messages, tools, opts, cb)
	}
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

func providerRetryDelay(err error, fallback time.Duration) time.Duration {
	var retryAfter providerRetryAfterer
	if errors.As(err, &retryAfter) {
		if d := retryAfter.ProviderRetryAfter(); d > 0 {
			if d > failoverMaximumBackoff {
				return failoverMaximumBackoff
			}
			return d
		}
	}
	return fallback
}
