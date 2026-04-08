//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type Provider interface {
	Complete(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error)
}

type ToolRegistry interface {
	Execute(ctx context.Context, name string, input json.RawMessage) (string, error)
	Definitions() []ToolDef
}

type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
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

type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     core.TokenUsage
}

const (
	maxProviderRetries   = 3
	initialRetryBackoff  = 100 * time.Millisecond
	providerFailureReply = "I’m having trouble reaching the model right now. Please try again."
	budgetExhaustedReply = "Iteration budget exhausted before final response."
)

var sleepWithContextFn = sleepWithContext

func RunTurn(
	ctx context.Context,
	provider Provider,
	tools ToolRegistry,
	budget *Budget,
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
	)

	if tools != nil {
		toolDefs = tools.Definitions()
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, history, err
		}

		if budget != nil {
			warning, exhausted := budget.Tick()
			if exhausted {
				slog.Warn("turn budget exhausted", "used", budget.Used, "max", budget.Max)
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

		resp, err := completeWithRetry(ctx, provider, history, toolDefs)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, history, ctxErr
			}

			slog.Error("provider failed after retries", "error", err)
			return &core.TurnResult{
				Text:       providerFailureReply,
				ToolLog:    toolLog,
				TokenUsage: core.TokenUsage{},
			}, history, nil
		}

		history = append(history, Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: append([]ToolCall(nil), resp.ToolCalls...),
		})

		if len(resp.ToolCalls) == 0 {
			slog.Info("turn completed", "tool_calls", len(toolLog))
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
			out, toolErr := tools.Execute(ctx, call.Name, call.Input)
			if toolErr != nil {
				slog.Warn("tool execution failed", "tool", call.Name, "id", call.ID, "error", toolErr)
			} else {
				slog.Info("tool execution completed", "tool", call.Name, "id", call.ID)
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

			if pendingBudget != "" {
				if content == "" {
					content = pendingBudget
				} else {
					content += "\n\n" + pendingBudget
				}
				pendingBudget = ""
			}

			history = append(history, Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: call.ID,
			})
		}
	}
}

func completeWithRetry(
	ctx context.Context,
	provider Provider,
	messages []Message,
	tools []ToolDef,
) (*Response, error) {
	backoff := initialRetryBackoff
	attempt := 0

	for {
		resp, err := provider.Complete(ctx, messages, tools)
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
		slog.Warn("provider call failed; retrying", "attempt", attempt, "max_retries", maxProviderRetries, "error", err)
		if sleepErr := sleepWithContextFn(ctx, backoff); sleepErr != nil {
			return nil, sleepErr
		}

		backoff *= 2
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
