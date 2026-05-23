//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/idolum-ai/aphelion/core"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	mu              sync.Mutex
	defs            []ToolDef
	execCalls       []toolInvocation
	exec            func(ctx context.Context, name string, input json.RawMessage) (string, error)
	parallelSafe    map[string]bool
	parallelSupport func(name string, input json.RawMessage) bool
	defsCalled      int
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

func (m *mockTools) SupportsParallelToolCall(name string, input json.RawMessage) bool {
	m.mu.Lock()
	support := m.parallelSupport
	safe := m.parallelSafe[strings.TrimSpace(name)]
	m.mu.Unlock()
	if support != nil {
		return support(name, input)
	}
	return safe
}

type recordingTurnObserver struct {
	mu            sync.Mutex
	modelStarts   []ModelRequestEvent
	modelFinishes []ModelRequestEvent
	batchStarts   []ToolBatchEvent
	batchFinishes []ToolBatchEvent
}

func (r *recordingTurnObserver) ModelRequestStarted(_ context.Context, event ModelRequestEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modelStarts = append(r.modelStarts, event)
}

func (r *recordingTurnObserver) ModelRequestFinished(_ context.Context, event ModelRequestEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modelFinishes = append(r.modelFinishes, event)
}

func (r *recordingTurnObserver) ToolBatchStarted(_ context.Context, event ToolBatchEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchStarts = append(r.batchStarts, event)
}

func (r *recordingTurnObserver) ToolBatchFinished(_ context.Context, event ToolBatchEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchFinishes = append(r.batchFinishes, event)
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

func TestRunTurnPropagatesProviderMedia(t *testing.T) {
	provider := &mockProvider{complete: func(_ context.Context, _ int, _ []Message, _ []ToolDef) (*Response, error) {
		return &Response{
			Content: "Draft generated.",
			Media: []core.Media{{
				Type:     "image",
				Data:     []byte("png-bytes"),
				MimeType: "image/png",
				Filename: "image-generation-call-ig-1.png",
			}},
		}, nil
	}}

	result, _, err := RunTurn(context.Background(), provider, nil, defaultBudget(), nil, []Message{{Role: "user", Content: "make image"}})
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "Draft generated." {
		t.Fatalf("result text = %q", result.Text)
	}
	if len(result.Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(result.Media))
	}
	if result.Media[0].Type != "image" || result.Media[0].MimeType != "image/png" || string(result.Media[0].Data) != "png-bytes" {
		t.Fatalf("media = %#v, want generated image bytes", result.Media[0])
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

func TestRunTurnExecutesSafeToolBatchInParallel(t *testing.T) {
	var maxActive int32
	var active int32
	provider := &mockProvider{
		complete: func(_ context.Context, call int, messages []Message, _ []ToolDef) (*Response, error) {
			switch call {
			case 1:
				return &Response{
					ToolCalls: []ToolCall{
						{ID: "read-1", Name: "read_file", Input: json.RawMessage(`{"path":"README.md"}`)},
						{ID: "search-1", Name: "search", Input: json.RawMessage(`{"query":"Aphelion"}`)},
					},
				}, nil
			case 2:
				if len(messages) < 3 {
					t.Fatalf("messages len = %d, want assistant plus two tool outputs", len(messages))
				}
				first := messages[len(messages)-2]
				second := messages[len(messages)-1]
				if first.Role != "tool" || first.ToolCallID != "read-1" || first.ToolName != "read_file" || first.Content != "read_file output" {
					t.Fatalf("first tool message = %#v, want read_file output in call order", first)
				}
				if second.Role != "tool" || second.ToolCallID != "search-1" || second.ToolName != "search" || second.Content != "search output" {
					t.Fatalf("second tool message = %#v, want search output in call order", second)
				}
				return &Response{Content: "done"}, nil
			default:
				t.Fatalf("unexpected call %d", call)
				return nil, nil
			}
		},
	}
	tools := &mockTools{
		parallelSafe: map[string]bool{"read_file": true, "search": true},
		exec: func(_ context.Context, name string, _ json.RawMessage) (string, error) {
			nowActive := atomic.AddInt32(&active, 1)
			for {
				observed := atomic.LoadInt32(&maxActive)
				if nowActive <= observed || atomic.CompareAndSwapInt32(&maxActive, observed, nowActive) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			return name + " output", nil
		},
	}

	result, _, err := RunTurn(context.Background(), provider, tools, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("result.Text = %q, want done", result.Text)
	}
	if got := atomic.LoadInt32(&maxActive); got < 2 {
		t.Fatalf("max active tools = %d, want parallel overlap", got)
	}
	if result.ToolLog[0] != "read_file:ok" || result.ToolLog[1] != "search:ok" {
		t.Fatalf("tool log = %#v, want call-order results", result.ToolLog)
	}
}

func TestRunTurnKeepsMixedSafetyToolBatchSerial(t *testing.T) {
	var maxActive int32
	var active int32
	provider := &mockProvider{
		complete: func(_ context.Context, call int, _ []Message, _ []ToolDef) (*Response, error) {
			if call == 1 {
				return &Response{
					ToolCalls: []ToolCall{
						{ID: "read-1", Name: "read_file", Input: json.RawMessage(`{"path":"README.md"}`)},
						{ID: "write-1", Name: "write_file", Input: json.RawMessage(`{"path":"out.txt","content":"x"}`)},
					},
				}, nil
			}
			if call == 2 {
				return &Response{Content: "done"}, nil
			}
			t.Fatalf("unexpected call %d", call)
			return nil, nil
		},
	}
	tools := &mockTools{
		parallelSafe: map[string]bool{"read_file": true},
		exec: func(_ context.Context, name string, _ json.RawMessage) (string, error) {
			nowActive := atomic.AddInt32(&active, 1)
			for {
				observed := atomic.LoadInt32(&maxActive)
				if nowActive <= observed || atomic.CompareAndSwapInt32(&maxActive, observed, nowActive) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			return name + " output", nil
		},
	}

	result, _, err := RunTurn(context.Background(), provider, tools, defaultBudget(), nil, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("result.Text = %q, want done", result.Text)
	}
	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("max active tools = %d, want serial execution for mixed-safety batch", got)
	}
	if len(tools.execCalls) != 2 || tools.execCalls[0].name != "read_file" || tools.execCalls[1].name != "write_file" {
		t.Fatalf("exec calls = %#v, want serial call order", tools.execCalls)
	}
}

func TestRunTurnObserverRecordsModelRequestsAndToolBatches(t *testing.T) {
	observer := &recordingTurnObserver{}
	provider := &mockProvider{
		complete: func(_ context.Context, call int, _ []Message, _ []ToolDef) (*Response, error) {
			if call == 1 {
				return &Response{
					ToolCalls: []ToolCall{
						{ID: "read-1", Name: "read_file", Input: json.RawMessage(`{"path":"README.md"}`)},
						{ID: "list-1", Name: "list_dir", Input: json.RawMessage(`{"path":"."}`)},
					},
					Usage: core.TokenUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5},
				}, nil
			}
			if call == 2 {
				return &Response{Content: "done", Usage: core.TokenUsage{InputTokens: 7, OutputTokens: 4, TotalTokens: 11}}, nil
			}
			t.Fatalf("unexpected call %d", call)
			return nil, nil
		},
	}
	tools := &mockTools{
		parallelSafe: map[string]bool{"read_file": true, "list_dir": true},
		exec: func(_ context.Context, name string, _ json.RawMessage) (string, error) {
			return name + " output", nil
		},
	}

	result, _, err := RunTurn(context.Background(), provider, tools, defaultBudget(), &CompleteOptions{Observer: observer}, nil)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("result.Text = %q, want done", result.Text)
	}
	if len(observer.modelStarts) != 2 || len(observer.modelFinishes) != 2 {
		t.Fatalf("model events starts=%#v finishes=%#v, want two requests", observer.modelStarts, observer.modelFinishes)
	}
	if observer.modelFinishes[0].ToolCallCount != 2 || observer.modelFinishes[0].TokenUsage.TotalTokens != 5 {
		t.Fatalf("first model finish = %#v, want tool call and token evidence", observer.modelFinishes[0])
	}
	if len(observer.batchStarts) != 1 || observer.batchStarts[0].Mode != toolBatchModeParallel || observer.batchStarts[0].BatchSize != 2 {
		t.Fatalf("batch starts = %#v, want one parallel batch", observer.batchStarts)
	}
	if len(observer.batchFinishes) != 1 || observer.batchFinishes[0].FailedCount != 0 || observer.batchFinishes[0].Mode != toolBatchModeParallel {
		t.Fatalf("batch finishes = %#v, want successful parallel batch", observer.batchFinishes)
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
	if result.ProviderFailure == "" {
		t.Fatalf("ProviderFailure = empty, want provider failure detail")
	}
	if retries != maxProviderRetries+1 {
		t.Fatalf("retries = %d, want %d", retries, maxProviderRetries+1)
	}
}

type providerEventsError struct {
	events []core.ProviderEvent
}

func (e providerEventsError) Error() string {
	return "terminal provider failure"
}

func (e providerEventsError) ProviderEvents() []core.ProviderEvent {
	return append([]core.ProviderEvent(nil), e.events...)
}

func TestRunTurnPreservesProviderEventsFromTerminalError(t *testing.T) {
	provider := &mockProvider{
		complete: func(_ context.Context, _ int, _ []Message, _ []ToolDef) (*Response, error) {
			return nil, providerEventsError{events: []core.ProviderEvent{{EventType: core.ExecutionEventProviderAttemptFailed, Provider: "codex", Error: "terminal"}}}
		},
	}

	result, _, err := RunTurn(context.Background(), provider, nil, defaultBudget(), nil, []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.ProviderFailure == "" {
		t.Fatal("ProviderFailure = empty, want terminal provider detail")
	}
	if len(result.ProviderEvents) != 1 || result.ProviderEvents[0].EventType != core.ExecutionEventProviderAttemptFailed {
		t.Fatalf("ProviderEvents = %#v, want failed provider event from error", result.ProviderEvents)
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
