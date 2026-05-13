//go:build linux

package provider

import (
	"errors"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

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

func (e ExhaustedError) ProviderEvents() []core.ProviderEvent {
	return providerEventsSnapshot(e.Events)
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

func (e TerminalProviderError) ProviderEvents() []core.ProviderEvent {
	return providerEventsSnapshot(e.Events)
}

func providerEventsSnapshot(events []core.ProviderEvent) []core.ProviderEvent {
	return append([]core.ProviderEvent(nil), events...)
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
