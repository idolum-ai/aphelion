//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type mockProvider struct {
	mu       sync.Mutex
	calls    int
	complete func(ctx context.Context, call int, messages []Message, tools []ToolDef) (*Response, error)
}

func (m *mockProvider) Complete(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	complete := m.complete
	m.mu.Unlock()
	return complete(ctx, call, messages, tools)
}

type toolInvocation struct {
	name  string
	input json.RawMessage
}

type mockTools struct {
	mu         sync.Mutex
	defs       []ToolDef
	execCalls  []toolInvocation
	exec       func(ctx context.Context, name string, input json.RawMessage) (string, error)
	defsCalled int
}

func (m *mockTools) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	m.mu.Lock()
	m.execCalls = append(m.execCalls, toolInvocation{name: name, input: input})
	exec := m.exec
	m.mu.Unlock()
	return exec(ctx, name, input)
}

func (m *mockTools) Definitions() []ToolDef {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defsCalled++
	return append([]ToolDef(nil), m.defs...)
}

type retryableError struct {
	code int
	msg  string
}

func (e retryableError) Error() string {
	return e.msg
}

func (e retryableError) StatusCode() int {
	return e.code
}

func defaultBudget() *Budget {
	return &Budget{
		Max:     10,
		Caution: 0.7,
		Warning: 0.9,
	}
}

func TestSimpleTurn(t *testing.T) {
	provider := &mockProvider{
		complete: func(_ context.Context, call int, _ []Message, _ []ToolDef) (*Response, error) {
			if call != 1 {
				t.Fatalf("call = %d, want 1", call)
			}
			return &Response{
				Content: "final reply",
				Usage: core.TokenUsage{
					InputTokens:  10,
					OutputTokens: 5,
					TotalTokens:  15,
				},
			}, nil
		},
	}

	tools := &mockTools{
		defs: []ToolDef{{Name: "noop"}},
		exec: func(_ context.Context, _ string, _ json.RawMessage) (string, error) {
			return "", nil
		},
	}

	result, outMessages, err := RunTurn(
		context.Background(),
		provider,
		tools,
		defaultBudget(),
		nil,
		[]Message{{Role: "user", Content: "hello"}},
	)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}

	if result.Text != "final reply" {
		t.Fatalf("result.Text = %q, want %q", result.Text, "final reply")
	}
	if len(result.ToolLog) != 0 {
		t.Fatalf("len(result.ToolLog) = %d, want 0", len(result.ToolLog))
	}
	if result.TokenUsage.TotalTokens != 15 {
		t.Fatalf("result.TokenUsage.TotalTokens = %d, want 15", result.TokenUsage.TotalTokens)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if len(outMessages) != 2 {
		t.Fatalf("len(outMessages) = %d, want 2", len(outMessages))
	}
	if outMessages[1].Role != "assistant" || outMessages[1].Content != "final reply" {
		t.Fatalf("assistant message = %#v", outMessages[1])
	}
}

func TestToolCallLoop(t *testing.T) {
	provider := &mockProvider{
		complete: func(_ context.Context, call int, messages []Message, _ []ToolDef) (*Response, error) {
			switch call {
			case 1:
				return &Response{
					ToolCalls: []ToolCall{{
						ID:    "tc1",
						Name:  "echo",
						Input: json.RawMessage(`{"value":"x"}`),
					}},
				}, nil
			case 2:
				last := messages[len(messages)-1]
				if last.Role != "tool" || last.ToolCallID != "tc1" || last.Content != "tool output" {
					t.Fatalf("last tool message = %#v", last)
				}
				return &Response{Content: "done"}, nil
			default:
				t.Fatalf("unexpected call %d", call)
				return nil, nil
			}
		},
	}

	tools := &mockTools{
		exec: func(_ context.Context, name string, _ json.RawMessage) (string, error) {
			if name != "echo" {
				t.Fatalf("tool name = %q, want %q", name, "echo")
			}
			return "tool output", nil
		},
	}

	result, _, err := RunTurn(context.Background(), provider, tools, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if len(tools.execCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(tools.execCalls))
	}
	if result.Text != "done" {
		t.Fatalf("result.Text = %q, want %q", result.Text, "done")
	}
}

func TestMultipleToolCalls(t *testing.T) {
	provider := &mockProvider{
		complete: func(_ context.Context, call int, _ []Message, _ []ToolDef) (*Response, error) {
			if call <= 3 {
				return &Response{
					ToolCalls: []ToolCall{{
						ID:    "tc",
						Name:  "step",
						Input: json.RawMessage(`{}`),
					}},
				}, nil
			}
			if call == 4 {
				return &Response{Content: "final"}, nil
			}
			t.Fatalf("unexpected call %d", call)
			return nil, nil
		},
	}

	tools := &mockTools{
		exec: func(_ context.Context, _ string, _ json.RawMessage) (string, error) {
			return "ok", nil
		},
	}

	result, _, err := RunTurn(context.Background(), provider, tools, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if provider.calls != 4 {
		t.Fatalf("provider calls = %d, want 4", provider.calls)
	}
	if len(tools.execCalls) != 3 {
		t.Fatalf("tool calls = %d, want 3", len(tools.execCalls))
	}
	if result.Text != "final" {
		t.Fatalf("result.Text = %q, want %q", result.Text, "final")
	}
}

func TestProviderError(t *testing.T) {
	var (
		sleepMu    sync.Mutex
		sleepCalls []time.Duration
	)

	prevSleep := sleepWithContextFn
	sleepWithContextFn = func(_ context.Context, d time.Duration) error {
		sleepMu.Lock()
		sleepCalls = append(sleepCalls, d)
		sleepMu.Unlock()
		return nil
	}
	t.Cleanup(func() {
		sleepWithContextFn = prevSleep
	})

	provider := &mockProvider{
		complete: func(_ context.Context, call int, _ []Message, _ []ToolDef) (*Response, error) {
			if call == 1 {
				return nil, retryableError{code: 500, msg: "500 internal server error"}
			}
			if call == 2 {
				return &Response{Content: "ok"}, nil
			}
			t.Fatalf("unexpected call %d", call)
			return nil, nil
		},
	}

	result, _, err := RunTurn(context.Background(), provider, &mockTools{
		exec: func(_ context.Context, _ string, _ json.RawMessage) (string, error) {
			return "", nil
		},
	}, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "ok" {
		t.Fatalf("result.Text = %q, want %q", result.Text, "ok")
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	sleepMu.Lock()
	defer sleepMu.Unlock()
	if len(sleepCalls) != 1 {
		t.Fatalf("sleep calls = %d, want 1", len(sleepCalls))
	}
	if sleepCalls[0] != initialRetryBackoff {
		t.Fatalf("sleep duration = %v, want %v", sleepCalls[0], initialRetryBackoff)
	}
}

func TestProviderPersistentError(t *testing.T) {
	var retries int
	provider := &mockProvider{
		complete: func(_ context.Context, _ int, _ []Message, _ []ToolDef) (*Response, error) {
			retries++
			return nil, retryableError{code: 500, msg: "500 internal server error"}
		},
	}

	prevSleep := sleepWithContextFn
	sleepWithContextFn = func(_ context.Context, _ time.Duration) error { return nil }
	t.Cleanup(func() {
		sleepWithContextFn = prevSleep
	})

	result, _, err := RunTurn(context.Background(), provider, &mockTools{
		exec: func(_ context.Context, _ string, _ json.RawMessage) (string, error) {
			return "", nil
		},
	}, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != providerFailureReply {
		t.Fatalf("result.Text = %q, want %q", result.Text, providerFailureReply)
	}
	if retries != maxProviderRetries+1 {
		t.Fatalf("retries = %d, want %d", retries, maxProviderRetries+1)
	}
}

func TestToolError(t *testing.T) {
	provider := &mockProvider{
		complete: func(_ context.Context, call int, messages []Message, _ []ToolDef) (*Response, error) {
			switch call {
			case 1:
				return &Response{
					ToolCalls: []ToolCall{{
						ID:    "tool-1",
						Name:  "explode",
						Input: json.RawMessage(`{}`),
					}},
				}, nil
			case 2:
				last := messages[len(messages)-1]
				if last.Role != "tool" {
					t.Fatalf("last role = %q, want tool", last.Role)
				}
				if !strings.Contains(last.Content, "tool_error: boom") {
					t.Fatalf("tool error message = %q", last.Content)
				}
				return &Response{Content: "handled"}, nil
			default:
				t.Fatalf("unexpected call %d", call)
				return nil, nil
			}
		},
	}

	tools := &mockTools{
		exec: func(_ context.Context, _ string, _ json.RawMessage) (string, error) {
			return "", errors.New("boom")
		},
	}

	result, _, err := RunTurn(context.Background(), provider, tools, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "handled" {
		t.Fatalf("result.Text = %q, want %q", result.Text, "handled")
	}
	if len(result.ToolLog) != 1 || result.ToolLog[0] != "explode:error" {
		t.Fatalf("result.ToolLog = %#v", result.ToolLog)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	provider := &mockProvider{
		complete: func(ctx context.Context, _ int, _ []Message, _ []ToolDef) (*Response, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	tools := &mockTools{
		exec: func(_ context.Context, _ string, _ json.RawMessage) (string, error) {
			return "", nil
		},
	}

	type out struct {
		result *core.TurnResult
		err    error
	}
	done := make(chan out, 1)
	go func() {
		result, _, err := RunTurn(ctx, provider, tools, defaultBudget(), nil, nil)
		done <- out{result: result, err: err}
	}()

	cancel()

	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", got.err)
		}
		if got.result != nil {
			t.Fatalf("result = %#v, want nil", got.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunTurn did not return after context cancellation")
	}
}

func TestRunTurnRetriesPlanningOnlyReplyBeforePersistingIt(t *testing.T) {
	t.Parallel()

	provider := &mockProvider{
		complete: func(_ context.Context, call int, messages []Message, tools []ToolDef) (*Response, error) {
			switch call {
			case 1:
				if len(tools) == 0 {
					t.Fatal("tools unexpectedly empty on first call")
				}
				return &Response{Content: "I'll inspect the repository first and then report back."}, nil
			case 2:
				last := messages[len(messages)-1]
				if last.Role != "user" || !strings.Contains(last.Content, "only described a plan") {
					t.Fatalf("retry steer = %#v, want planning-only correction", last)
				}
				prev := messages[len(messages)-2]
				if prev.Role != "assistant" || !strings.Contains(prev.Content, "inspect the repository first") {
					t.Fatalf("retry context missing prior planning-only reply: %#v", prev)
				}
				return &Response{
					ToolCalls: []ToolCall{{
						ID:    "call-1",
						Name:  "exec",
						Input: json.RawMessage(`{"command":"pwd"}`),
					}},
				}, nil
			case 3:
				last := messages[len(messages)-1]
				if last.Role != "tool" || last.ToolCallID != "call-1" || last.Content != "tool output" {
					t.Fatalf("last tool message = %#v", last)
				}
				return &Response{Content: "done"}, nil
			default:
				t.Fatalf("unexpected call %d", call)
				return nil, nil
			}
		},
	}

	tools := &mockTools{
		defs: []ToolDef{
			{Name: "exec"},
			{Name: "update_plan"},
		},
		exec: func(_ context.Context, name string, _ json.RawMessage) (string, error) {
			if name != "exec" {
				t.Fatalf("tool name = %q, want exec", name)
			}
			return "tool output", nil
		},
	}

	result, history, err := RunTurn(
		context.Background(),
		provider,
		tools,
		defaultBudget(),
		nil,
		[]Message{{Role: "user", Content: "please fix it"}},
	)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("result.Text = %q, want done", result.Text)
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls = %d, want 3", provider.calls)
	}
	if len(tools.execCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(tools.execCalls))
	}
	for _, msg := range history {
		if msg.Role == "assistant" && strings.Contains(msg.Content, "inspect the repository first") {
			t.Fatalf("history unexpectedly persisted planning-only assistant reply: %#v", history)
		}
		if msg.Role == "user" && strings.Contains(msg.Content, "only described a plan") {
			t.Fatalf("history unexpectedly persisted planning-only steer: %#v", history)
		}
	}
}

func TestRunTurnRetriesPlanningOnlyReplyAfterPlanUpdateWithinSameTurn(t *testing.T) {
	t.Parallel()

	provider := &mockProvider{
		complete: func(_ context.Context, call int, messages []Message, tools []ToolDef) (*Response, error) {
			switch call {
			case 1:
				return &Response{
					ToolCalls: []ToolCall{{
						ID:    "plan-1",
						Name:  "update_plan",
						Input: json.RawMessage(`{"plan":[{"step":"Inspect the repository.","status":"in_progress"},{"step":"Patch the issue.","status":"pending"}]}`),
					}},
				}, nil
			case 2:
				last := messages[len(messages)-1]
				if last.Role != "tool" || last.ToolName != "update_plan" {
					t.Fatalf("last tool message = %#v, want update_plan output", last)
				}
				return &Response{Content: "Next I'll inspect the relevant files and then continue."}, nil
			case 3:
				last := messages[len(messages)-1]
				if last.Role != "user" || !strings.Contains(last.Content, "only described a plan") {
					t.Fatalf("retry steer = %#v, want planning-only correction", last)
				}
				return &Response{
					ToolCalls: []ToolCall{{
						ID:    "exec-1",
						Name:  "exec",
						Input: json.RawMessage(`{"command":"pwd"}`),
					}},
				}, nil
			case 4:
				last := messages[len(messages)-1]
				if last.Role != "tool" || last.ToolName != "exec" || last.Content != "tool output" {
					t.Fatalf("last tool message = %#v, want exec output", last)
				}
				return &Response{Content: "done"}, nil
			default:
				t.Fatalf("unexpected call %d", call)
				return nil, nil
			}
		},
	}

	tools := &mockTools{
		defs: []ToolDef{
			{Name: "exec"},
			{Name: "update_plan"},
		},
		exec: func(_ context.Context, name string, _ json.RawMessage) (string, error) {
			switch name {
			case "update_plan":
				return "[PLAN_UPDATED]\nactive: true\n- [in_progress] Inspect the repository.\n- [pending] Patch the issue.", nil
			case "exec":
				return "tool output", nil
			default:
				t.Fatalf("unexpected tool name %q", name)
				return "", nil
			}
		},
	}

	result, history, err := RunTurn(
		context.Background(),
		provider,
		tools,
		defaultBudget(),
		nil,
		[]Message{{Role: "user", Content: "please handle this carefully"}},
	)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("result.Text = %q, want done", result.Text)
	}
	if provider.calls != 4 {
		t.Fatalf("provider calls = %d, want 4", provider.calls)
	}
	if len(tools.execCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(tools.execCalls))
	}
	for _, msg := range history {
		if msg.Role == "assistant" && strings.Contains(msg.Content, "Next I'll inspect the relevant files") {
			t.Fatalf("history unexpectedly persisted mid-turn planning-only assistant reply: %#v", history)
		}
		if msg.Role == "user" && strings.Contains(msg.Content, "only described a plan") {
			t.Fatalf("history unexpectedly persisted planning-only steer: %#v", history)
		}
	}
}
