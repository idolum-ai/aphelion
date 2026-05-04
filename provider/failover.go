//go:build linux

package provider

import (
	"context"
	"errors"
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
	if len(e.Attempts) <= 1 {
		return "Inference backend is unavailable. This turn did not complete. You can /stop to cancel current work and try again."
	}
	return "Inference backends are unavailable after provider fallback attempts. This turn did not complete. You can /stop to cancel current work and try again."
}

type TerminalProviderError struct {
	Provider string
	Err      error
}

func (e TerminalProviderError) Error() string {
	if strings.TrimSpace(e.Provider) == "" {
		return fmt.Sprintf("provider failed: %v", e.Err)
	}
	return fmt.Sprintf("%s failed: %v", strings.TrimSpace(e.Provider), e.Err)
}

func (e TerminalProviderError) Unwrap() error {
	return e.Err
}

func (e TerminalProviderError) UserFacingFailure() string {
	return "Inference backend failed before provider fallback was applicable. This turn did not complete. You can /stop to cancel current work and try again."
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
	if c == nil {
		return nil, fmt.Errorf("provider failover chain is nil")
	}
	var attempts []FailoverAttempt
	var events []core.ProviderEvent
	attemptMessages := messages
	for idx := 0; idx < len(c.entries); idx++ {
		entry := c.entries[idx]
		resp, started, err := c.streamWithRetry(ctx, entry, attemptMessages, tools, cb, &events)
		if err == nil {
			c.recordSuccess(idx)
			if idx > 0 {
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
			return nil, TerminalProviderError{Provider: entry.name, Err: err}
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
		if nextIdx > idx+1 {
			idx = nextIdx - 1
		}
	}
	return nil, ExhaustedError{Attempts: attempts}
}

func (c *FailoverChain) completeAcrossChain(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("provider failover chain is nil")
	}
	var attempts []FailoverAttempt
	var events []core.ProviderEvent
	attemptMessages := messages
	for idx := 0; idx < len(c.entries); idx++ {
		entry := c.entries[idx]
		resp, err := c.completeWithRetry(ctx, entry, attemptMessages, tools, opts, &events)
		if err == nil {
			c.recordSuccess(idx)
			if idx > 0 {
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
			return nil, TerminalProviderError{Provider: entry.name, Err: err}
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
		if nextIdx > idx+1 {
			idx = nextIdx - 1
		}
	}
	return nil, ExhaustedError{Attempts: attempts}
}

func (c *FailoverChain) nextCompleteFailoverIndex(idx int, err error, messages []agent.Message) (int, bool) {
	if c == nil || idx < 0 || idx >= len(c.entries) {
		return 0, false
	}
	if shouldFallbackAfterOpenAIFamilyCapacity(err, c.entries[idx].name) {
		nextIdx := nextNonOpenAIProviderIndex(c.entries, idx)
		return nextIdx, nextIdx > idx && nextIdx < len(c.entries)
	}
	if shouldFallbackAfterToolResultRejection(err, c.entries[idx].name, messages) {
		nextIdx := nextNonOpenAIProviderIndex(c.entries, idx)
		return nextIdx, nextIdx > idx && nextIdx < len(c.entries)
	}
	if shouldFallbackAfterContextWindowError(err, c.entries[idx].name, messages) {
		nextIdx := nextNonOpenAIProviderIndex(c.entries, idx)
		return nextIdx, nextIdx > idx && nextIdx < len(c.entries)
	}
	nextIdx := idx + 1
	nextName := ""
	if nextIdx < len(c.entries) {
		nextName = c.entries[nextIdx].name
	}
	return nextIdx, shouldFailoverOnError(err) || shouldFallbackToNextEntry(err, c.entries[idx].name, nextName)
}

func (c *FailoverChain) recordSuccess(idx int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if idx < 0 || idx >= len(c.entries) {
		return
	}
	c.state.ActiveProvider = c.entries[idx].name
	c.state.FallbackActive = idx > 0
}

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

func (c *FailoverChain) streamWithRetry(ctx context.Context, entry failoverEntry, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback, events *[]core.ProviderEvent) (*agent.Response, bool, error) {
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

func recordProviderRetryEvent(events *[]core.ProviderEvent, provider string, attempt int, err error) {
	if events == nil {
		return
	}
	*events = append(*events, core.ProviderEvent{
		EventType:  core.ExecutionEventProviderAttemptRetried,
		Provider:   strings.TrimSpace(provider),
		Attempt:    attempt,
		MaxRetries: failoverMaxRetries,
		Error:      trimProviderEventError(err),
	})
}

func recordProviderFailedEvent(events *[]core.ProviderEvent, provider string, err error) {
	if events == nil {
		return
	}
	*events = append(*events, core.ProviderEvent{
		EventType: core.ExecutionEventProviderAttemptFailed,
		Provider:  strings.TrimSpace(provider),
		Error:     trimProviderEventError(err),
	})
}

type partialProviderError interface {
	PartialProviderResponse() *agent.Response
	PartialProviderResponseID() string
	PartialProviderReason() string
}

type providerFailureCoder interface {
	ProviderFailureCode() string
}

type providerRetryAfterer interface {
	ProviderRetryAfter() time.Duration
}

func recordProviderPartialEvent(events *[]core.ProviderEvent, provider string, err error) {
	if events == nil {
		return
	}
	partial, responseID, reason, ok := partialProviderSnapshot(err)
	if !ok {
		return
	}
	event := core.ProviderEvent{
		EventType:  core.ExecutionEventProviderPartial,
		Provider:   strings.TrimSpace(provider),
		Reason:     reason,
		ResponseID: responseID,
		Error:      trimProviderEventError(err),
	}
	if partial != nil {
		event.PartialContentChars = len(strings.TrimSpace(partial.Content))
		event.PartialToolCalls = len(partial.ToolCalls)
	}
	*events = append(*events, event)
}

func recordProviderFailoverEvent(events *[]core.ProviderEvent, from string, to string, err error) {
	if events == nil || strings.TrimSpace(to) == "" {
		return
	}
	*events = append(*events, core.ProviderEvent{
		EventType:    core.ExecutionEventProviderFailoverEngaged,
		FromProvider: strings.TrimSpace(from),
		ToProvider:   strings.TrimSpace(to),
		Error:        trimProviderEventError(err),
	})
}

func trimProviderEventError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if len(text) > 500 {
		return text[:500] + "..."
	}
	return text
}

func appendPartialProviderRecoveryMessage(messages []agent.Message, provider string, err error) []agent.Message {
	partial, responseID, reason, ok := partialProviderSnapshot(err)
	if !ok {
		return messages
	}
	note := renderPartialProviderRecoveryNote(provider, responseID, reason, partial)
	if strings.TrimSpace(note) == "" {
		return messages
	}
	out := append([]agent.Message(nil), messages...)
	out = append(out, agent.Message{
		Role:    "user",
		Content: note,
	})
	return out
}

func partialProviderSnapshot(err error) (*agent.Response, string, string, bool) {
	if err == nil {
		return nil, "", "", false
	}
	var partialErr partialProviderError
	if !errors.As(err, &partialErr) {
		return nil, "", "", false
	}
	partial := partialErr.PartialProviderResponse()
	responseID := strings.TrimSpace(partialErr.PartialProviderResponseID())
	reason := strings.TrimSpace(partialErr.PartialProviderReason())
	if partial == nil && responseID == "" && reason == "" {
		return nil, "", "", false
	}
	return partial, responseID, reason, true
}

func renderPartialProviderRecoveryNote(provider string, responseID string, reason string, partial *agent.Response) string {
	var b strings.Builder
	b.WriteString("Provider recovery note: ")
	b.WriteString(firstNonEmpty(strings.TrimSpace(provider), "primary provider"))
	b.WriteString(" produced an incomplete response before failing. Treat this as partial, non-authoritative evidence while completing the user's request.")
	if reason = strings.TrimSpace(reason); reason != "" {
		b.WriteString("\nreason: ")
		b.WriteString(reason)
	}
	if responseID = strings.TrimSpace(responseID); responseID != "" {
		b.WriteString("\nresponse_id: ")
		b.WriteString(responseID)
	}
	if partial == nil {
		return b.String()
	}
	if text := strings.TrimSpace(partial.Content); text != "" {
		b.WriteString("\npartial_text:\n")
		b.WriteString(compactProviderFallbackText(text, providerFallbackRecentToolChars))
	}
	if len(partial.ToolCalls) > 0 {
		b.WriteString("\npartial_tool_calls:")
		for _, call := range partial.ToolCalls {
			b.WriteString("\n- ")
			b.WriteString(firstNonEmpty(strings.TrimSpace(call.Name), "tool"))
			if id := strings.TrimSpace(call.ID); id != "" {
				b.WriteString(" id=")
				b.WriteString(id)
			}
			if input := strings.TrimSpace(string(call.Input)); input != "" {
				b.WriteString(" input=")
				b.WriteString(compactProviderFallbackText(input, providerFallbackOlderToolChars))
			}
		}
	}
	return b.String()
}

type statusCoder interface {
	StatusCode() int
}

func isRetryableProviderError(err error) bool {
	if isProviderBufferLimitError(err) {
		return false
	}
	if isProviderRateLimitError(err) || isProviderCapacityError(err) {
		return true
	}
	var sc statusCoder
	if errors.As(err, &sc) {
		code := sc.StatusCode()
		switch {
		case code == 429:
			return true
		case code >= 500 && code < 600:
			return true
		default:
			return false
		}
	}
	if isTransientStreamError(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "429") || strings.Contains(msg, "500") || strings.Contains(msg, "503")
}

func shouldFailoverOnError(err error) bool {
	if isProviderBufferLimitError(err) {
		return true
	}
	if isProviderRateLimitError(err) || isProviderCapacityError(err) {
		return true
	}
	if isProviderContextWindowError(err) {
		return true
	}
	var sc statusCoder
	if errors.As(err, &sc) {
		code := sc.StatusCode()
		switch {
		case code == 401 || code == 403 || code == 429:
			return true
		case code >= 500 && code < 600:
			return true
		default:
			return false
		}
	}
	if isCodexContinuationFailure(err) {
		return true
	}
	return isRetryableProviderError(err)
}

func shouldBypassSameProviderRetry(err error, current string) bool {
	if err == nil {
		return false
	}
	if isProviderBufferLimitError(err) || isCodexContinuationFailure(err) || isOpenAIModelUnavailableError(err) {
		return true
	}
	switch providerFamilyName(current) {
	case "codex", "openai":
		return isProviderRateLimitError(err) || isProviderCapacityError(err)
	default:
		return false
	}
}

func shouldFallbackAfterOpenAIFamilyCapacity(err error, current string) bool {
	switch providerFamilyName(current) {
	case "codex", "openai":
		return isProviderRateLimitError(err) || isProviderCapacityError(err)
	default:
		return false
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

func shouldFallbackToNextEntry(err error, current string, next string) bool {
	if providerFamilyName(current) != "openai" || strings.TrimSpace(next) == "" {
		return false
	}
	return isOpenAIModelUnavailableError(err)
}

func shouldFallbackAfterToolResultRejection(err error, current string, messages []agent.Message) bool {
	if !historyHasToolResults(messages) {
		return false
	}
	switch providerFamilyName(current) {
	case "codex", "openai":
	default:
		return false
	}
	return isRejectedToolResultRequest(err)
}

func isProviderRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	if strings.EqualFold(providerFailureCode(err), "rate_limit_exceeded") {
		return true
	}
	var sc statusCoder
	if errors.As(err, &sc) && sc.StatusCode() == 429 {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "rate_limit_exceeded") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate-limit")
}

func isProviderCapacityError(err error) bool {
	if err == nil {
		return false
	}
	switch strings.ToLower(providerFailureCode(err)) {
	case "server_is_overloaded", "slow_down":
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "server_is_overloaded") ||
		strings.Contains(msg, "slow_down") ||
		strings.Contains(msg, "slow down") ||
		strings.Contains(msg, "overload") ||
		strings.Contains(msg, "at capacity")
}

func providerFailureCode(err error) string {
	if err == nil {
		return ""
	}
	var coded providerFailureCoder
	if errors.As(err, &coded) {
		return strings.TrimSpace(coded.ProviderFailureCode())
	}
	return ""
}

func shouldFallbackAfterContextWindowError(err error, current string, messages []agent.Message) bool {
	if !historyHasToolResults(messages) || !isProviderContextWindowError(err) {
		return false
	}
	switch providerFamilyName(current) {
	case "codex", "openai":
		return true
	default:
		return false
	}
}

func historyHasToolResults(messages []agent.Message) bool {
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			return true
		}
	}
	return false
}

func isRejectedToolResultRequest(err error) bool {
	if err == nil {
		return false
	}
	var sc statusCoder
	if errors.As(err, &sc) {
		switch sc.StatusCode() {
		case 400, 409, 422:
			return true
		default:
			return false
		}
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	if !(strings.Contains(msg, "400") || strings.Contains(msg, "409") || strings.Contains(msg, "422")) {
		return false
	}
	for _, marker := range []string{
		"tool",
		"function_call",
		"tool_call",
		"call_id",
		"previous_response",
		"response id",
		"invalid_request",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func isProviderBufferLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "buffer limit") ||
		strings.Contains(msg, "request buffer") ||
		strings.Contains(msg, "response buffer")
}

func isProviderContextWindowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	for _, marker := range []string{
		"context window",
		"context length",
		"context_length_exceeded",
		"maximum context",
		"too many tokens",
		"input exceeds",
		"exceeds the context",
		"token limit",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

const (
	providerFallbackRecentToolChars = 4000
	providerFallbackOlderToolChars  = 800
	providerFallbackTotalToolChars  = 60000
)

func compactToolResultMessagesForProviderFallback(messages []agent.Message) []agent.Message {
	out := append([]agent.Message(nil), messages...)
	totalToolChars := 0
	for i := len(out) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(out[i].Role), "tool") {
			continue
		}
		limit := providerFallbackRecentToolChars
		if totalToolChars >= providerFallbackTotalToolChars {
			limit = providerFallbackOlderToolChars
		}
		out[i].Content = compactProviderFallbackText(out[i].Content, limit)
		totalToolChars += len(out[i].Content)
	}
	return out
}

func compactProviderFallbackText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	head := limit * 2 / 3
	tail := limit - head
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}
	if head+tail >= len(text) {
		return text
	}
	return strings.TrimSpace(text[:head]) +
		fmt.Sprintf("\n\n[tool output compacted for provider context: original_chars=%d omitted_chars=%d]\n\n", len(text), len(text)-head-tail) +
		strings.TrimSpace(text[len(text)-tail:])
}

func nextNonOpenAIProviderIndex(entries []failoverEntry, idx int) int {
	for i := idx + 1; i < len(entries); i++ {
		switch providerFamilyName(entries[i].name) {
		case "codex", "openai":
			continue
		default:
			return i
		}
	}
	return len(entries)
}

func isOpenAIModelUnavailableError(err error) bool {
	var sc statusCoder
	if errors.As(err, &sc) {
		switch sc.StatusCode() {
		case 404:
			return true
		case 400:
			msg := strings.ToLower(err.Error())
			return strings.Contains(msg, "model") && (strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "unavailable"))
		default:
			return false
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "model") && (strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "unavailable"))
}

func nextFailoverEntryName(entries []failoverEntry, idx int) string {
	if idx+1 < 0 || idx+1 >= len(entries) {
		return ""
	}
	return entries[idx+1].name
}

func providerFamilyName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if idx := strings.Index(name, ":"); idx >= 0 {
		name = name[:idx]
	}
	return name
}

func isTransientStreamError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	markers := []string{
		"stream closed before response.completed",
		"unexpected eof",
		"connection reset by peer",
		"broken pipe",
		"stream terminated",
		"incomplete event stream",
		"stream closed",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func isCodexContinuationFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	markers := []string{
		"codex: incomplete response without stored-response continuation",
		"codex: incomplete response missing response id",
		"codex: response remained incomplete after",
		"codex: stored-response continuation rejected after incomplete response",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
