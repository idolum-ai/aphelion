//go:build linux

package agent

import (
	"context"
	"strings"
	"testing"
)

type contextCaptureProvider struct {
	calls    int
	messages []Message
}

func (p *contextCaptureProvider) Complete(_ context.Context, messages []Message, _ []ToolDef) (*Response, error) {
	p.calls++
	p.messages = append([]Message(nil), messages...)
	return &Response{Content: "done"}, nil
}

func TestRunTurnCompactsToolResultsBeforeProviderCall(t *testing.T) {
	originalTool := strings.Repeat("a", 12000)
	provider := &contextCaptureProvider{}
	observer := &recordingTurnObserver{}
	messages := []Message{
		{Role: "user", Content: "inspect this"},
		{Role: "tool", Content: originalTool},
		{Role: "user", Content: "continue"},
	}

	result, history, err := RunTurn(context.Background(), provider, nil, nil, &CompleteOptions{
		Observer: observer,
		ContextBudget: &ContextBudget{
			ContextWindow: 4000,
			MaxRatio:      0.10,
			HardRatio:     2.0,
		},
	}, messages)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("text = %q, want done", result.Text)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if got := provider.messages[1].Content; !strings.Contains(got, "tool output compacted for provider context") || len(got) >= len(originalTool) {
		t.Fatalf("provider tool content was not compacted: len=%d", len(got))
	}
	if history[1].Content != originalTool {
		t.Fatal("durable turn history was compacted; want original tool output preserved")
	}
	if len(observer.modelStarts) != 1 || !observer.modelStarts[0].ContextPreflightCompacted {
		t.Fatalf("model start = %#v, want context compaction evidence", observer.modelStarts)
	}
	if observer.modelStarts[0].ContextPreflightOriginalTokens <= observer.modelStarts[0].ContextPreflightCompactedTokens {
		t.Fatalf("preflight tokens = %#v, want compacted token estimate smaller", observer.modelStarts[0])
	}
	if observer.modelStarts[0].ContextAdmissionToolEvidencePacked == 0 {
		t.Fatalf("model start = %#v, want tool evidence packing accounting", observer.modelStarts[0])
	}
}

func TestPrepareProviderMessagesCompactsOversizedToolOutputBySizeNotName(t *testing.T) {
	originalTool := strings.Repeat("a", providerContextOversizedToolChars+1)
	messages := []Message{
		{Role: "user", Content: "continue"},
		{Role: "tool", ToolName: "custom_probe", Content: originalTool},
	}
	estimated := estimateProviderRequestTokens(messages, nil)

	compacted, preflight, err := prepareProviderMessages(messages, nil, &CompleteOptions{
		ContextBudget: &ContextBudget{ContextWindow: 100000},
	})
	if err != nil {
		t.Fatalf("prepareProviderMessages() err = %v", err)
	}
	if estimated > preflight.MaxTokens {
		t.Fatalf("test premise failed: estimated=%d max=%d", estimated, preflight.MaxTokens)
	}
	if !preflight.Compacted {
		t.Fatalf("preflight = %#v, want oversized tool output compacted even below max tokens", preflight)
	}
	if preflight.Admission.ToolEvidenceDigested != 1 {
		t.Fatalf("admission = %#v, want one typed digest", preflight.Admission)
	}
	if got := compacted[1].Content; !strings.Contains(got, "projection_kind: tool_result_digest") || !strings.Contains(got, "tool_name: custom_probe") || len(got) >= len(originalTool) {
		t.Fatalf("compacted tool content = %q, want typed size-based digest", got)
	}
	if messages[1].Content != originalTool {
		t.Fatal("original messages were compacted; want provider-only copy")
	}
}

func TestPrepareProviderMessagesDigestsAgedToolEvidence(t *testing.T) {
	messages := []Message{{Role: "user", Content: "continue"}}
	for i := 0; i < 5; i++ {
		messages = append(messages, Message{
			Role:       "tool",
			ToolCallID: "call-" + string(rune('a'+i)),
			ToolName:   "read_file",
			Content:    strings.Repeat("line "+string(rune('a'+i))+"\n", 1200),
		})
	}

	compacted, preflight, err := prepareProviderMessages(messages, nil, &CompleteOptions{
		ContextBudget: &ContextBudget{ContextWindow: 10000, MaxRatio: 0.10, HardRatio: 10},
	})
	if err != nil {
		t.Fatalf("prepareProviderMessages() err = %v", err)
	}
	if preflight.Admission.ToolEvidenceDigested == 0 {
		t.Fatalf("admission = %#v, want at least one typed tool digest", preflight.Admission)
	}
	if !strings.Contains(compacted[1].Content, "projection_kind: tool_result_digest") {
		t.Fatalf("oldest tool content = %q, want typed digest", compacted[1].Content)
	}
	if strings.Contains(compacted[len(compacted)-1].Content, "projection_kind: tool_result_digest") {
		t.Fatalf("newest tool content = %q, should keep richer recent projection", compacted[len(compacted)-1].Content)
	}
}

func TestRunTurnBlocksRequestsAboveHardContextBudget(t *testing.T) {
	provider := &contextCaptureProvider{}
	messages := []Message{
		{Role: "user", Content: "inspect this"},
		{Role: "tool", Content: strings.Repeat("a", 12000)},
	}

	result, history, err := RunTurn(context.Background(), provider, nil, nil, &CompleteOptions{
		ContextBudget: &ContextBudget{
			ContextWindow: 100,
			MaxRatio:      0.10,
			HardRatio:     0.20,
		},
	}, messages)
	if err != nil {
		t.Fatalf("RunTurn() err = %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
	if !strings.Contains(result.ProviderFailure, "context_budget_exceeded") {
		t.Fatalf("ProviderFailure = %q, want context budget failure", result.ProviderFailure)
	}
	if len(history) != len(messages) || history[1].Content != messages[1].Content {
		t.Fatalf("history changed on local budget failure: %#v", history)
	}
}
