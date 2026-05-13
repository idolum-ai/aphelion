//go:build linux

package governorbackend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

type codexResponseStatus string

type codexCompletionResult struct {
	Response         *agent.Response
	ResponseID       string
	Complete         bool
	IncompleteReason string
}

type codexIncompleteError struct {
	message        string
	partial        *agent.Response
	responseID     string
	mode           codexTurnMode
	storeResponses bool
}

type codexFailedError struct {
	errorType  string
	code       string
	message    string
	retryAfter time.Duration
}

func newCodexIncompleteError(message string, partial *agent.Response, responseID string, mode codexTurnMode, storeResponses bool) *codexIncompleteError {
	return &codexIncompleteError{
		message:        strings.TrimSpace(message),
		partial:        cloneAgentResponse(partial),
		responseID:     strings.TrimSpace(responseID),
		mode:           mode,
		storeResponses: storeResponses,
	}
}

func newCodexFailedError(errorType string, code string, message string) *codexFailedError {
	message = strings.TrimSpace(message)
	code = inferCodexFailureCode(code, message)
	return &codexFailedError{
		errorType:  strings.TrimSpace(errorType),
		code:       code,
		message:    message,
		retryAfter: parseCodexRetryAfter(message),
	}
}

func (e *codexIncompleteError) Error() string {
	if e == nil {
		return "codex: incomplete response"
	}
	switch e.message {
	case "without stored-response continuation":
		return "codex: incomplete response without stored-response continuation"
	case "missing response id":
		return "codex: incomplete response missing response id"
	case "stored-response continuation rejected":
		return "codex: stored-response continuation rejected after incomplete response"
	default:
		if strings.TrimSpace(e.message) != "" {
			return "codex: " + strings.TrimSpace(e.message)
		}
		return "codex: incomplete response"
	}
}

func (e *codexFailedError) Error() string {
	if e == nil {
		return "codex: stream failed"
	}
	message := strings.TrimSpace(e.message)
	if message == "" {
		message = strings.TrimSpace(e.code)
	}
	if message == "" {
		message = "response.failed event received"
	}
	return "codex: stream failed: " + message
}

func (e *codexFailedError) ProviderFailureCode() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.code)
}

func (e *codexFailedError) ProviderRetryAfter() time.Duration {
	if e == nil {
		return 0
	}
	return e.retryAfter
}

func (e *codexFailedError) ProviderFailureMessage() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.message)
}

func (e *codexIncompleteError) PartialProviderResponse() *agent.Response {
	if e == nil {
		return nil
	}
	return cloneAgentResponse(e.partial)
}

func (e *codexIncompleteError) PartialProviderResponseID() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.responseID)
}

func (e *codexIncompleteError) PartialProviderReason() string {
	if e == nil {
		return ""
	}
	parts := []string{}
	if msg := strings.TrimSpace(e.message); msg != "" {
		parts = append(parts, msg)
	}
	if e.mode != "" {
		parts = append(parts, "mode="+string(e.mode))
	}
	parts = append(parts, fmt.Sprintf("store_responses=%t", e.storeResponses))
	return strings.Join(parts, "; ")
}

func inferCodexFailureCode(code string, message string) string {
	code = strings.TrimSpace(code)
	if code != "" {
		return code
	}
	msg := strings.ToLower(strings.TrimSpace(message))
	switch {
	case msg == "":
		return ""
	case strings.Contains(msg, "server_is_overloaded"):
		return codexFailureCodeServerOverloaded
	case strings.Contains(msg, "slow_down") || strings.Contains(msg, "slow down"):
		return codexFailureCodeSlowDown
	case strings.Contains(msg, "overload") || strings.Contains(msg, "at capacity"):
		return codexFailureCodeServerOverloaded
	case strings.Contains(msg, "rate_limit") || strings.Contains(msg, "rate limit"):
		return codexFailureCodeRateLimit
	case strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "context window") ||
		strings.Contains(msg, "context length") ||
		strings.Contains(msg, "input exceeds"):
		return codexFailureCodeContextWindow
	case strings.Contains(msg, "invalid_prompt") || strings.Contains(msg, "invalid prompt"):
		return codexFailureCodeInvalidPrompt
	default:
		return ""
	}
}

func inferCodexHTTPFailureCode(statusCode int, body string) string {
	if code := inferCodexFailureCode("", body); code != "" {
		return code
	}
	switch {
	case statusCode == http.StatusTooManyRequests:
		return codexFailureCodeRateLimit
	case statusCode >= 500:
		return codexFailureCodeServerOverloaded
	default:
		return ""
	}
}

func parseCodexRetryAfter(message string) time.Duration {
	message = strings.TrimSpace(message)
	if message == "" {
		return 0
	}
	match := codexRetryAfterPattern().FindStringSubmatch(message)
	if len(match) != 3 {
		return 0
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || value <= 0 {
		return 0
	}
	unit := strings.ToLower(match[2])
	switch {
	case unit == "ms" || strings.HasPrefix(unit, "millisecond"):
		return time.Duration(value * float64(time.Millisecond))
	default:
		return time.Duration(value * float64(time.Second))
	}
}

func codexRetryAfterPattern() *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:try again|retry)\s+in\s+([0-9]+(?:\.[0-9]+)?)\s*(ms|milliseconds?|s|sec|secs|seconds?)`)
}

func parseCodexRetryAfterHeader(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	d := at.Sub(now)
	if d <= 0 {
		return 0
	}
	return d
}

func cloneAgentResponse(resp *agent.Response) *agent.Response {
	if resp == nil {
		return nil
	}
	out := *resp
	out.ProviderState = append(json.RawMessage(nil), resp.ProviderState...)
	out.ToolCalls = append([]agent.ToolCall(nil), resp.ToolCalls...)
	for i := range out.ToolCalls {
		out.ToolCalls[i].Input = append(json.RawMessage(nil), resp.ToolCalls[i].Input...)
	}
	out.Media = append([]core.Media(nil), resp.Media...)
	for i := range out.Media {
		out.Media[i].Data = append([]byte(nil), resp.Media[i].Data...)
	}
	out.ThinkingMeta = append([]agent.ThinkingBlock(nil), resp.ThinkingMeta...)
	for i := range out.ThinkingMeta {
		out.ThinkingMeta[i].Raw = append(json.RawMessage(nil), resp.ThinkingMeta[i].Raw...)
	}
	out.ProviderEvents = append([]core.ProviderEvent(nil), resp.ProviderEvents...)
	return &out
}

func codexResponsesEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case baseURL == "":
		return "/codex/responses"
	case strings.HasSuffix(baseURL, "/codex/responses"):
		return baseURL
	case strings.HasSuffix(baseURL, "/responses"):
		return baseURL
	case strings.HasSuffix(baseURL, "/codex"):
		return baseURL + "/responses"
	case strings.Contains(baseURL, "/backend-api"):
		return baseURL + "/codex/responses"
	default:
		return baseURL + "/responses"
	}
}

func codexStatusMessage(statusCode int, body string) string {
	suffix := ""
	if strings.TrimSpace(body) != "" {
		suffix = ": " + body
	}
	switch statusCode {
	case http.StatusUnauthorized:
		return "codex: status 401 unauthorized" + suffix
	case http.StatusForbidden:
		return "codex: status 403 forbidden" + suffix
	case http.StatusTooManyRequests:
		return "codex: status 429 rate_limited" + suffix
	default:
		if statusCode >= 500 {
			return fmt.Sprintf("codex: status %d server_error%s", statusCode, suffix)
		}
		return fmt.Sprintf("codex: status %d request_failed%s", statusCode, suffix)
	}
}

func codexStatusCause(statusCode int) error {
	switch statusCode {
	case http.StatusUnauthorized:
		return ErrCodexUnauthorized
	case http.StatusForbidden:
		return ErrCodexForbidden
	case http.StatusTooManyRequests:
		return ErrCodexRateLimited
	default:
		if statusCode >= 500 {
			return ErrCodexServer
		}
		return nil
	}
}

func redactError(err error, secret string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
	}
	return errors.New(msg)
}

func refreshTokenForRedaction(c *Codex) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshToken
}

func redactBodyExcerpt(raw []byte, secrets ...string) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return ""
	}
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	const maxLen = 300
	if len(text) > maxLen {
		text = strings.TrimSpace(text[:maxLen]) + "…"
	}
	return text
}

type codexAPIError struct {
	statusCode int
	message    string
	cause      error
	code       string
	retryAfter time.Duration
}

func (e codexAPIError) Error() string {
	return e.message
}

func (e codexAPIError) StatusCode() int {
	return e.statusCode
}

func (e codexAPIError) Unwrap() error {
	return e.cause
}

func (e codexAPIError) ProviderFailureCode() string {
	return strings.TrimSpace(e.code)
}

func (e codexAPIError) ProviderRetryAfter() time.Duration {
	return e.retryAfter
}

func isPreviousResponseRejected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "previous_response_id") || strings.Contains(msg, "previous response")
}

func isStoreResponsesRejected(err error) bool {
	if err == nil {
		return false
	}
	var apiErr codexAPIError
	if errors.As(err, &apiErr) && apiErr.statusCode != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "store must be set to false")
}
