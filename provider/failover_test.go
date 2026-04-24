//go:build linux

package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

type stubStatusError struct {
	code int
	msg  string
}

func (e stubStatusError) Error() string   { return e.msg }
func (e stubStatusError) StatusCode() int { return e.code }

type stubChainProvider struct {
	reply      string
	err        error
	callCount  int
	streamText string
}

func (s *stubChainProvider) Complete(_ context.Context, _ []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	s.callCount++
	if s.err != nil {
		return nil, s.err
	}
	return &agent.Response{
		Content: s.reply,
		Usage:   core.TokenUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
	}, nil
}

func (s *stubChainProvider) Stream(_ context.Context, _ []agent.Message, _ []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	s.callCount++
	if s.err != nil {
		return nil, s.err
	}
	if cb != nil && s.streamText != "" {
		if err := cb(agent.StreamChunk{Type: "text", Text: s.streamText}); err != nil {
			return nil, err
		}
	}
	reply := s.reply
	if reply == "" {
		reply = s.streamText
	}
	return &agent.Response{Content: reply}, nil
}

func TestFailoverChainFallsBackToSecondary(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 503, msg: "upstream unavailable"}}
	secondary := &stubChainProvider{reply: "fallback reply"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "anthropic", Provider: primary},
		{Name: "openrouter", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "fallback reply" {
		t.Fatalf("content = %q, want fallback reply", resp.Content)
	}
	if primary.callCount == 0 || secondary.callCount == 0 {
		t.Fatalf("call counts primary=%d secondary=%d, want both called", primary.callCount, secondary.callCount)
	}
}

func TestFailoverChainFallsBackOnForbidden(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 403, msg: "forbidden"}}
	secondary := &stubChainProvider{reply: "fallback reply"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "codex", Provider: primary},
		{Name: "native", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "fallback reply" {
		t.Fatalf("content = %q, want fallback reply", resp.Content)
	}
	if secondary.callCount == 0 {
		t.Fatal("secondary provider was not called after forbidden primary error")
	}
	state := chain.RuntimeState()
	if !state.FallbackActive {
		t.Fatalf("FallbackActive = false, want true")
	}
	if state.ActiveProvider != "native" {
		t.Fatalf("ActiveProvider = %q, want native", state.ActiveProvider)
	}
}

func TestFailoverChainDoesNotCascadeClientErrors(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 400, msg: "bad request"}}
	secondary := &stubChainProvider{reply: "should not run"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "anthropic", Provider: primary},
		{Name: "openrouter", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	_, err = chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err == nil {
		t.Fatal("CompleteManaged() err = nil, want error")
	}
	var statusErr stubStatusError
	if !errors.As(err, &statusErr) || statusErr.code != 400 {
		t.Fatalf("err = %v, want 400 status error", err)
	}
	if secondary.callCount != 0 {
		t.Fatalf("secondary.callCount = %d, want 0", secondary.callCount)
	}
}

func TestFailoverChainFallsBackBetweenOpenAIModelsOnModelUnavailable(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 404, msg: "model gpt-5.5 not found"}}
	secondary := &stubChainProvider{reply: "fallback openai model"}
	tertiary := &stubChainProvider{reply: "should not run"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: primary},
		{Name: "openai:gpt-5.4", Provider: secondary},
		{Name: "anthropic", Provider: tertiary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "fallback openai model" {
		t.Fatalf("content = %q, want fallback openai model", resp.Content)
	}
	if secondary.callCount == 0 {
		t.Fatal("secondary OpenAI model was not called")
	}
	if tertiary.callCount != 0 {
		t.Fatalf("tertiary.callCount = %d, want 0", tertiary.callCount)
	}
}

func TestFailoverChainDoesNotCascadeOpenAIClientErrorToAnthropic(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 400, msg: "bad request"}}
	secondary := &stubChainProvider{reply: "should not run"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: primary},
		{Name: "anthropic", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	_, err = chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err == nil {
		t.Fatal("CompleteManaged() err = nil, want error")
	}
	if secondary.callCount != 0 {
		t.Fatalf("secondary.callCount = %d, want 0", secondary.callCount)
	}
}

func TestFailoverChainFallsBackToAnthropicAfterOpenAIModelsUnavailable(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 404, msg: "model gpt-5.5 not found"}}
	secondary := &stubChainProvider{err: stubStatusError{code: 404, msg: "model gpt-5.4 not found"}}
	tertiary := &stubChainProvider{reply: "anthropic fallback"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "openai:gpt-5.5", Provider: primary},
		{Name: "openai:gpt-5.4", Provider: secondary},
		{Name: "anthropic", Provider: tertiary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "anthropic fallback" {
		t.Fatalf("content = %q, want anthropic fallback", resp.Content)
	}
	if tertiary.callCount == 0 {
		t.Fatal("anthropic fallback was not called")
	}
}

func TestFailoverChainExhaustedErrorHasUserFacingFailure(t *testing.T) {
	primary := &stubChainProvider{err: stubStatusError{code: 503, msg: "primary down"}}
	secondary := &stubChainProvider{err: stubStatusError{code: 503, msg: "secondary down"}}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "anthropic", Provider: primary},
		{Name: "openrouter", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	_, err = chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err == nil {
		t.Fatal("CompleteManaged() err = nil, want error")
	}
	var exhausted ExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("err = %v, want ExhaustedError", err)
	}
	if got := exhausted.UserFacingFailure(); got == "" {
		t.Fatal("UserFacingFailure() = empty, want guidance")
	}
}

func TestFailoverChainFallsBackOnInterruptedStreamError(t *testing.T) {
	primary := &stubChainProvider{err: errors.New("codex: stream closed before response.completed")}
	secondary := &stubChainProvider{reply: "fallback after stream interruption"}

	chain, err := NewFailoverChain([]NamedProvider{
		{Name: "codex", Provider: primary},
		{Name: "native", Provider: secondary},
	})
	if err != nil {
		t.Fatalf("NewFailoverChain() err = %v", err)
	}

	resp, err := chain.CompleteManaged(context.Background(), []agent.Message{{Role: "user", Content: "hi"}}, nil, agent.CompleteOptions{})
	if err != nil {
		t.Fatalf("CompleteManaged() err = %v", err)
	}
	if resp.Content != "fallback after stream interruption" {
		t.Fatalf("content = %q, want fallback after stream interruption", resp.Content)
	}
	if secondary.callCount == 0 {
		t.Fatal("secondary provider was not called after interrupted stream error")
	}
}

func TestIsRetryableProviderErrorTreatsInterruptedStreamsAsRetryable(t *testing.T) {
	if !isRetryableProviderError(errors.New("codex: stream closed before response.completed")) {
		t.Fatal("interrupted stream error not treated as retryable")
	}
	if !shouldFailoverOnError(errors.New("unexpected EOF while reading event stream")) {
		t.Fatal("unexpected EOF stream error not treated as failover-eligible")
	}
}
