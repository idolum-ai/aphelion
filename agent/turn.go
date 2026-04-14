//go:build linux

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/idolum-ai/aphelion/core"
)

type Provider interface {
	Complete(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error)
}

type StreamingProvider interface {
	Stream(ctx context.Context, messages []Message, tools []ToolDef, cb StreamCallback) (*Response, error)
}

type ProviderWithOptions interface {
	CompleteWithOptions(ctx context.Context, messages []Message, tools []ToolDef, opts CompleteOptions) (*Response, error)
}

type ManagedProvider interface {
	CompleteManaged(ctx context.Context, messages []Message, tools []ToolDef, opts CompleteOptions) (*Response, error)
}

type ToolRegistry interface {
	Execute(ctx context.Context, name string, input json.RawMessage) (string, error)
	Definitions() []ToolDef
}

type Message struct {
	Role          string
	Content       string
	Media         []core.Media
	SystemBlocks  []SystemBlock
	Thinking      string
	ThinkingMeta  []ThinkingBlock
	ProviderState json.RawMessage
	ToolCalls     []ToolCall
	ToolCallID    string
	ToolName      string
}

type SystemBlock struct {
	Text            string
	CacheBreakpoint bool
}

type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type ThinkingBlock struct {
	Type      string
	Content   string
	Signature string
	Raw       json.RawMessage
}

type Response struct {
	Content       string
	Thinking      string
	ThinkingMeta  []ThinkingBlock
	ProviderState json.RawMessage
	ToolCalls     []ToolCall
	Usage         core.TokenUsage
}

type StreamChunk struct {
	Type     string
	Text     string
	ToolCall *ToolCall
	Usage    *core.TokenUsage
}

type StreamCallback func(StreamChunk) error

type CompleteOptions struct {
	Reasoning ReasoningConfig
}

type ReasoningConfig struct {
	Effort  ReasoningEffort
	Summary ReasoningSummaryMode
}

type ReasoningEffort string

const (
	ReasoningEffortNone   ReasoningEffort = "none"
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
	ReasoningEffortXHigh  ReasoningEffort = "xhigh"
)

type ReasoningSummaryMode string

const (
	ReasoningSummaryNone    ReasoningSummaryMode = "none"
	ReasoningSummaryAuto    ReasoningSummaryMode = "auto"
	ReasoningSummaryCompact ReasoningSummaryMode = "compact"
)

const (
	maxProviderRetries   = 3
	initialRetryBackoff  = 100 * time.Millisecond
	providerFailureReply = "Inference backends are unavailable after retries and fallback. This turn did not complete. You can /stop to cancel current work and try again."
	budgetExhaustedReply = "Iteration budget exhausted before final response."
	planningOnlySteer    = "Your previous reply only described a plan. Do not restate the plan. Start executing now using available tools. Use update_plan only if the work is genuinely multi-step."
)

var sleepWithContextFn = sleepWithContext

func RunTurn(
	ctx context.Context,
	provider Provider,
	tools ToolRegistry,
	budget *Budget,
	opts *CompleteOptions,
	messages []Message,
) (*core.TurnResult, []Message, error) {
	if provider == nil {
		return nil, messages, errors.New("provider is nil")
	}

	var (
		history       = append([]Message(nil), messages...)
		toolDefs      []ToolDef
		toolLog       []string
		pendingBudget string
		toolIDs       = newToolIDGenerator(history)
		toolRepair    = newToolRepairState(toolDefs)
		toolLoopGuard toolLoopGuardState
	)

	if tools != nil {
		toolDefs = tools.Definitions()
		toolRepair = newToolRepairState(toolDefs)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, history, err
		}

		if budget != nil {
			warning, exhausted := budget.Tick()
			if exhausted {
				log.Printf("WARN turn budget exhausted used=%d max=%d", budget.Used, budget.Max)
				return &core.TurnResult{
					Text:       budgetExhaustedReply,
					ToolLog:    toolLog,
					TokenUsage: core.TokenUsage{},
				}, history, nil
			}
			if warning != "" {
				pendingBudget = warning
			}
		}

		resp, err := completeWithRetry(ctx, provider, history, toolDefs, opts)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, history, ctxErr
			}

			log.Printf("ERROR provider failed after retries err=%v", err)
			reply := providerFailureReply
			var userFacing interface{ UserFacingFailure() string }
			if errors.As(err, &userFacing) {
				if text := strings.TrimSpace(userFacing.UserFacingFailure()); text != "" {
					reply = text
				}
			}
			return &core.TurnResult{
				Text:       reply,
				ToolLog:    toolLog,
				TokenUsage: core.TokenUsage{},
			}, history, nil
		}
		retried, retryErr := maybeRetryPlanningOnly(ctx, provider, history, toolDefs, opts, resp)
		if retryErr != nil {
			log.Printf("WARN planning-only correction failed err=%v", retryErr)
		} else if retried != nil {
			resp = retried
		}

		repairedCalls := make([]ToolCall, 0, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			repairedCalls = append(repairedCalls, toolRepair.repair(call, toolIDs.next))
		}
		resp.ToolCalls = repairedCalls

		history = append(history, Message{
			Role:          "assistant",
			Content:       resp.Content,
			Thinking:      resp.Thinking,
			ThinkingMeta:  append([]ThinkingBlock(nil), resp.ThinkingMeta...),
			ProviderState: append(json.RawMessage(nil), resp.ProviderState...),
			ToolCalls:     append([]ToolCall(nil), repairedCalls...),
		})

		if len(resp.ToolCalls) == 0 {
			log.Printf("INFO turn completed tool_calls=%d", len(toolLog))
			return &core.TurnResult{
				Text:       resp.Content,
				ToolLog:    toolLog,
				TokenUsage: resp.Usage,
			}, history, nil
		}

		if tools == nil {
			return nil, history, errors.New("tool calls requested but tool registry is nil")
		}

		for _, call := range resp.ToolCalls {
			repairedInput, inputErr := repairToolInput(call.Input)
			if inputErr != nil {
				content := withBudgetWarning(fmt.Sprintf("tool_error: Invalid tool arguments for %s: %v", call.Name, inputErr), &pendingBudget)
				toolLog = append(toolLog, fmt.Sprintf("%s:error", call.Name))
				history = append(history, Message{
					Role:       "tool",
					Content:    content,
					ToolCallID: call.ID,
					ToolName:   call.Name,
				})
				continue
			}
			call.Input = repairedInput

			requestSig, loopBlocked := toolLoopGuard.shouldBlock(call)
			if loopBlocked {
				content := withBudgetWarning(fmt.Sprintf("tool_error: no-progress tool loop blocked for %s", call.Name), &pendingBudget)
				toolLog = append(toolLog, fmt.Sprintf("%s:error", call.Name))
				history = append(history, Message{
					Role:       "tool",
					Content:    content,
					ToolCallID: call.ID,
					ToolName:   call.Name,
				})
				continue
			}

			out, toolErr := tools.Execute(ctx, call.Name, call.Input)
			if toolErr != nil {
				log.Printf("WARN tool execution failed tool=%s id=%s err=%v", call.Name, call.ID, toolErr)
			} else {
				log.Printf("INFO tool execution completed tool=%s id=%s", call.Name, call.ID)
			}

			content := out
			if toolErr != nil {
				if content != "" {
					content = fmt.Sprintf("tool_error: %v\n%s", toolErr, content)
				} else {
					content = fmt.Sprintf("tool_error: %v", toolErr)
				}
				toolLog = append(toolLog, fmt.Sprintf("%s:error", call.Name))
			} else {
				toolLog = append(toolLog, fmt.Sprintf("%s:ok", call.Name))
			}

			toolLoopGuard.recordOutcome(requestSig, content, toolErr != nil)
			content = withBudgetWarning(content, &pendingBudget)

			history = append(history, Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: call.ID,
				ToolName:   call.Name,
			})
		}
	}
}

func maybeRetryPlanningOnly(
	ctx context.Context,
	provider Provider,
	history []Message,
	tools []ToolDef,
	opts *CompleteOptions,
	resp *Response,
) (*Response, error) {
	if resp == nil || len(resp.ToolCalls) > 0 {
		return nil, nil
	}
	if !hasActionableTools(tools) || !looksLikePlanningOnlyReply(resp.Content) {
		return nil, nil
	}
	if hasToolResults(history) && !historyHasIncompletePlan(history) {
		return nil, nil
	}

	retryHistory := append([]Message(nil), history...)
	retryHistory = append(retryHistory, Message{
		Role:          "assistant",
		Content:       resp.Content,
		Thinking:      resp.Thinking,
		ThinkingMeta:  append([]ThinkingBlock(nil), resp.ThinkingMeta...),
		ProviderState: append(json.RawMessage(nil), resp.ProviderState...),
	})
	retryHistory = append(retryHistory, Message{Role: "user", Content: planningOnlySteer})
	return completeWithRetry(ctx, provider, retryHistory, tools, opts)
}

func hasActionableTools(tools []ToolDef) bool {
	for _, def := range tools {
		name := strings.TrimSpace(def.Name)
		if name == "" || name == "update_plan" {
			continue
		}
		return true
	}
	return false
}

func looksLikePlanningOnlyReply(content string) bool {
	text := strings.ToLower(strings.TrimSpace(content))
	if text == "" {
		return false
	}
	actionPhrases := []string{
		"i'll inspect",
		"i will inspect",
		"i'll check",
		"i will check",
		"i'll review",
		"i will review",
		"i'll look",
		"i will look",
		"let me inspect",
		"let me check",
		"let me review",
		"let me look",
		"i'm going to inspect",
		"i am going to inspect",
		"first, i'll",
		"first i will",
	}
	for _, phrase := range actionPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func hasToolResults(history []Message) bool {
	for _, msg := range history {
		if msg.Role == "tool" {
			return true
		}
	}
	return false
}

func historyHasIncompletePlan(history []Message) bool {
	for _, msg := range history {
		if msg.Role == "tool" && strings.TrimSpace(msg.ToolName) == "update_plan" {
			if containsIncompletePlanStatus(msg.Content) {
				return true
			}
		}
		for _, block := range msg.SystemBlocks {
			if !strings.Contains(block.Text, "## Current Plan State") {
				continue
			}
			if containsIncompletePlanStatus(block.Text) {
				return true
			}
		}
	}
	return false
}

func containsIncompletePlanStatus(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "[pending]") || strings.Contains(text, "[in_progress]")
}

func completeWithRetry(
	ctx context.Context,
	provider Provider,
	messages []Message,
	tools []ToolDef,
	opts *CompleteOptions,
) (*Response, error) {
	if managed, ok := provider.(ManagedProvider); ok {
		if opts == nil {
			return managed.CompleteManaged(ctx, messages, tools, CompleteOptions{})
		}
		return managed.CompleteManaged(ctx, messages, tools, *opts)
	}

	backoff := initialRetryBackoff
	attempt := 0

	for {
		resp, err := completeOnce(ctx, provider, messages, tools, opts)
		if err == nil {
			return resp, nil
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}

		if !isRetryableProviderError(err) || attempt >= maxProviderRetries {
			return nil, err
		}

		attempt++
		log.Printf("WARN provider call failed; retrying attempt=%d max_retries=%d err=%v", attempt, maxProviderRetries, err)
		if sleepErr := sleepWithContextFn(ctx, backoff); sleepErr != nil {
			return nil, sleepErr
		}

		backoff *= 2
	}
}

func completeOnce(
	ctx context.Context,
	provider Provider,
	messages []Message,
	tools []ToolDef,
	opts *CompleteOptions,
) (*Response, error) {
	if opts != nil {
		if providerWithOptions, ok := provider.(ProviderWithOptions); ok {
			return providerWithOptions.CompleteWithOptions(ctx, messages, tools, *opts)
		}
	}
	return provider.Complete(ctx, messages, tools)
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

type toolIDGenerator struct {
	nextID int
}

func newToolIDGenerator(history []Message) *toolIDGenerator {
	maxID := 0
	for _, msg := range history {
		for _, call := range msg.ToolCalls {
			if id := parseSyntheticToolID(call.ID); id > maxID {
				maxID = id
			}
		}
		if id := parseSyntheticToolID(msg.ToolCallID); id > maxID {
			maxID = id
		}
	}
	return &toolIDGenerator{nextID: maxID + 1}
}

func (g *toolIDGenerator) next() string {
	id := fmt.Sprintf("toolcall-%d", g.nextID)
	g.nextID++
	return id
}

func parseSyntheticToolID(id string) int {
	const prefix = "toolcall-"
	if !strings.HasPrefix(id, prefix) {
		return 0
	}
	var value int
	if _, err := fmt.Sscanf(id, prefix+"%d", &value); err != nil {
		return 0
	}
	return value
}

type toolRepairState struct {
	byCanonical map[string]string
}

func newToolRepairState(defs []ToolDef) toolRepairState {
	byCanonical := make(map[string]string, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		byCanonical[canonicalToolName(name)] = name
	}
	return toolRepairState{byCanonical: byCanonical}
}

func (s toolRepairState) repair(call ToolCall, nextID func() string) ToolCall {
	repaired := call
	if strings.TrimSpace(repaired.ID) == "" {
		repaired.ID = nextID()
	}
	if actual, ok := s.byCanonical[canonicalToolName(repaired.Name)]; ok {
		repaired.Name = actual
	}
	return repaired
}

func canonicalToolName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func repairToolInput(input json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`), nil
	}
	if json.Valid(trimmed) {
		var wrapped string
		if err := json.Unmarshal(trimmed, &wrapped); err == nil {
			unwrapped := strings.TrimSpace(wrapped)
			if unwrapped != "" && json.Valid([]byte(unwrapped)) {
				return compactJSON(json.RawMessage(unwrapped))
			}
		}
		return compactJSON(json.RawMessage(trimmed))
	}
	return nil, fmt.Errorf("input is not valid JSON")
}

func compactJSON(input json.RawMessage) (json.RawMessage, error) {
	var decoded any
	if err := json.Unmarshal(input, &decoded); err != nil {
		return nil, err
	}
	compact, err := json.Marshal(decoded)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(compact), nil
}

type toolLoopGuardState struct {
	lastRequest string
	lastOutcome string
	streak      int
}

func (s *toolLoopGuardState) shouldBlock(call ToolCall) (string, bool) {
	requestSig := toolRequestSignature(call)
	switch call.Name {
	case "update_plan":
		return requestSig, false
	case "exec":
		return requestSig, s.lastRequest == requestSig && s.streak >= 2
	default:
		return requestSig, false
	}
}

func (s *toolLoopGuardState) recordOutcome(requestSig, content string, failed bool) {
	outcomeSig := content
	if failed {
		outcomeSig = "error:" + outcomeSig
	} else {
		outcomeSig = "ok:" + outcomeSig
	}
	if s.lastRequest == requestSig && s.lastOutcome == outcomeSig {
		s.streak++
	} else {
		s.lastRequest = requestSig
		s.lastOutcome = outcomeSig
		s.streak = 1
	}
}

func toolRequestSignature(call ToolCall) string {
	return strings.TrimSpace(call.Name) + "\n" + strings.TrimSpace(string(call.Input))
}

func withBudgetWarning(content string, pending *string) string {
	if pending == nil || *pending == "" {
		return content
	}
	if content == "" {
		content = *pending
	} else {
		content += "\n\n" + *pending
	}
	*pending = ""
	return content
}
