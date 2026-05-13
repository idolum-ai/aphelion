//go:build linux

package provider

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
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
var _ agent.StreamingProviderWithOptions = (*FailoverChain)(nil)

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
	Events   []core.ProviderEvent
}

type TerminalProviderError struct {
	Provider string
	Err      error
	Events   []core.ProviderEvent
}

type failoverEntry struct {
	name     string
	provider agent.Provider
}

type FailoverChain struct {
	entries []failoverEntry
	mu      sync.Mutex
	state   RuntimeState
}

type RuntimeState struct {
	ConfiguredChain []string
	ActiveProvider  string
	FallbackActive  bool
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
	chainNames := make([]string, 0, len(normalized))
	for _, entry := range normalized {
		chainNames = append(chainNames, entry.name)
	}
	active := ""
	if len(normalized) > 0 {
		active = normalized[0].name
	}
	return &FailoverChain{
		entries: normalized,
		state: RuntimeState{
			ConfiguredChain: chainNames,
			ActiveProvider:  active,
			FallbackActive:  false,
		},
	}, nil
}

func (c *FailoverChain) RuntimeState() RuntimeState {
	if c == nil {
		return RuntimeState{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.state
	out.ConfiguredChain = append([]string(nil), out.ConfiguredChain...)
	return out
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
	return c.StreamWithOptions(ctx, messages, tools, agent.CompleteOptions{}, cb)
}

func (c *FailoverChain) StreamWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback) (*agent.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("provider failover chain is nil")
	}
	var attempts []FailoverAttempt
	var events []core.ProviderEvent
	attemptMessages := messages
	startIdx := c.startIndexForFailover(opts)
	for idx := startIdx; idx < len(c.entries); idx++ {
		entry := c.entries[idx]
		resp, started, err := c.streamWithRetry(ctx, entry, attemptMessages, tools, opts, cb, &events)
		if err == nil {
			c.recordSuccess(idx)
			if idx > 0 && idx != startIdx {
				log.Printf("WARN provider failover engaged from=%s to=%s", c.entries[0].name, entry.name)
			}
			resp.ProviderEvents = append(events, resp.ProviderEvents...)
			return resp, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if started {
			return nil, err
		}
		attempts = append(attempts, FailoverAttempt{Name: entry.name, Err: err})
		recordProviderFailedEvent(&events, entry.name, err)
		recordProviderPartialEvent(&events, entry.name, err)
		nextIdx, routeToNext := c.nextCompleteFailoverIndex(idx, err, attemptMessages)
		if !routeToNext {
			return nil, TerminalProviderError{Provider: entry.name, Err: err, Events: providerEventsSnapshot(events)}
		}
		attemptMessages = appendPartialProviderRecoveryMessage(attemptMessages, entry.name, err)
		if isProviderContextWindowError(err) && historyHasToolResults(attemptMessages) {
			attemptMessages = compactToolResultMessagesForProviderFallback(attemptMessages)
		}
		log.Printf("WARN provider failed name=%s err=%v", entry.name, err)
		if nextIdx >= len(c.entries) {
			continue
		}
		recordProviderFailoverEvent(&events, entry.name, c.entries[nextIdx].name, err)
		c.rememberFailoverPreference(opts, idx, nextIdx, err, attemptMessages, startIdx)
		if nextIdx > idx+1 {
			idx = nextIdx - 1
		}
	}
	return nil, ExhaustedError{Attempts: attempts, Events: providerEventsSnapshot(events)}
}

func (c *FailoverChain) completeAcrossChain(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("provider failover chain is nil")
	}
	var attempts []FailoverAttempt
	var events []core.ProviderEvent
	attemptMessages := messages
	startIdx := c.startIndexForFailover(opts)
	for idx := startIdx; idx < len(c.entries); idx++ {
		entry := c.entries[idx]
		resp, err := c.completeWithRetry(ctx, entry, attemptMessages, tools, opts, &events)
		if err == nil {
			c.recordSuccess(idx)
			if idx > 0 && idx != startIdx {
				log.Printf("WARN provider failover engaged from=%s to=%s", c.entries[0].name, entry.name)
			}
			resp.ProviderEvents = append(events, resp.ProviderEvents...)
			return resp, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		attempts = append(attempts, FailoverAttempt{Name: entry.name, Err: err})
		recordProviderFailedEvent(&events, entry.name, err)
		recordProviderPartialEvent(&events, entry.name, err)
		nextIdx, routeToNext := c.nextCompleteFailoverIndex(idx, err, attemptMessages)
		if !routeToNext {
			return nil, TerminalProviderError{Provider: entry.name, Err: err, Events: providerEventsSnapshot(events)}
		}
		attemptMessages = appendPartialProviderRecoveryMessage(attemptMessages, entry.name, err)
		if isProviderContextWindowError(err) && historyHasToolResults(attemptMessages) {
			attemptMessages = compactToolResultMessagesForProviderFallback(attemptMessages)
		}
		log.Printf("WARN provider failed name=%s err=%v", entry.name, err)
		if nextIdx >= len(c.entries) {
			continue
		}
		recordProviderFailoverEvent(&events, entry.name, c.entries[nextIdx].name, err)
		c.rememberFailoverPreference(opts, idx, nextIdx, err, attemptMessages, startIdx)
		if nextIdx > idx+1 {
			idx = nextIdx - 1
		}
	}
	return nil, ExhaustedError{Attempts: attempts, Events: providerEventsSnapshot(events)}
}
