//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/governorauth"
	"github.com/idolum-ai/aphelion/media"
	"github.com/idolum-ai/aphelion/principal"
	providerpkg "github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type fakeProvider struct {
	mu                  sync.Mutex
	callCount           int
	err                 error
	replyText           string
	thinkingText        string
	reflectionReplyText string
	compactionReplyText string
	proposalReplyText   string
	proposalReplies     []string
	brokerageReplyText  string
	brokerageReplies    []string
	planningReplyText   string
	planningReplies     []string
	faceReplyText       string
	repairReplyText     string
	repairReplies       []string
	streamFaceText      string
	faceErr             error
	proposalErr         error
	proposalErrAfter    int
	proposalCallCount   int
	seenGovernorSystem  []string
	seenFaceSystem      []string
	seenProposalSystem  []string
	seenBrokerageSystem []string
	seenPlanningSystem  []string
	lastGovernorMsgs    []agent.Message
	responseUsage       core.TokenUsage
	lastReasoning       agent.ReasoningConfig
	reasoningBySystem   map[string]agent.ReasoningConfig
}

type planningErrorProvider struct {
	agent.Provider
	err error
}

func (p planningErrorProvider) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	for _, msg := range messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Before the main turn executes, ratify how this turn should proceed.") {
			return nil, p.err
		}
	}
	return p.Provider.Complete(ctx, messages, tools)
}

func (p planningErrorProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	for _, msg := range messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Before the main turn executes, ratify how this turn should proceed.") {
			return nil, p.err
		}
	}
	if withOptions, ok := p.Provider.(agent.ProviderWithOptions); ok {
		return withOptions.CompleteWithOptions(ctx, messages, tools, opts)
	}
	return p.Provider.Complete(ctx, messages, tools)
}

func (f *fakeProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++

	isFaceCall := len(messages) > 0 && messages[0].Role == "system" && strings.Contains(messages[0].Content, "the face of")
	if isFaceCall {
		if strings.Contains(messages[0].Content, "- mode: repair") {
			f.seenFaceSystem = append(f.seenFaceSystem, messages[0].Content)
			reply := strings.TrimSpace(nextFakeReply(&f.repairReplies, f.repairReplyText))
			return &agent.Response{Content: reply, Usage: f.responseUsage}, nil
		}
		if strings.Contains(messages[0].Content, "- mode: brokerage") {
			f.seenBrokerageSystem = append(f.seenBrokerageSystem, messages[0].Content)
			reply := strings.TrimSpace(nextFakeReply(&f.brokerageReplies, f.brokerageReplyText))
			return &agent.Response{Content: reply, Usage: f.responseUsage}, nil
		}
		if strings.Contains(messages[0].Content, "- mode: proposal") {
			f.seenProposalSystem = append(f.seenProposalSystem, messages[0].Content)
			f.proposalCallCount++
			if f.proposalErr != nil && (f.proposalErrAfter == 0 || f.proposalCallCount >= f.proposalErrAfter) {
				return nil, f.proposalErr
			}
			reply := strings.TrimSpace(nextFakeReply(&f.proposalReplies, f.proposalReplyText))
			return &agent.Response{Content: reply, Usage: f.responseUsage}, nil
		}
		f.seenFaceSystem = append(f.seenFaceSystem, messages[0].Content)
		if f.faceErr != nil {
			return nil, f.faceErr
		}
		reply := strings.TrimSpace(f.faceReplyText)
		if reply == "" {
			reply = f.replyText
		}
		return &agent.Response{
			Content: reply,
			Usage:   f.responseUsage,
		}, nil
	}
	if f.err != nil {
		return nil, f.err
	}
	f.lastGovernorMsgs = append([]agent.Message(nil), messages...)

	var systemParts []string
	var userParts []string
	for _, msg := range messages {
		if msg.Role == "system" && strings.TrimSpace(msg.Content) != "" {
			systemParts = append(systemParts, msg.Content)
		}
		if msg.Role == "user" && strings.TrimSpace(msg.Content) != "" {
			userParts = append(userParts, msg.Content)
		}
	}
	f.seenGovernorSystem = append(f.seenGovernorSystem, strings.Join(systemParts, "\n\n"))
	for _, userText := range userParts {
		if strings.Contains(strings.Join(systemParts, "\n\n"), "You are compacting an existing session ledger.") {
			reply := strings.TrimSpace(f.compactionReplyText)
			if reply == "" {
				reply = "Compacted summary of earlier turns."
			}
			return &agent.Response{Content: reply, Usage: f.responseUsage}, nil
		}
		if strings.Contains(userText, "Before the main turn executes, ratify how this turn should proceed.") {
			f.seenPlanningSystem = append(f.seenPlanningSystem, strings.Join(systemParts, "\n\n"))
			reply := strings.TrimSpace(nextFakeReply(&f.planningReplies, f.planningReplyText))
			if reply == "" {
				reply = "INSPECT: no\nQUESTION: no\nANSWER: yes\nRATIFICATION: accept\nPLAN:\n- Answer directly."
			}
			return &agent.Response{Content: reply, Usage: f.responseUsage}, nil
		}
		if strings.Contains(userText, heartbeatReflectionMarker) {
			reply := strings.TrimSpace(f.reflectionReplyText)
			if reply == "" {
				reply = "[MEMORY]\n[/MEMORY]\n[KNOWLEDGE]\n[/KNOWLEDGE]\n[DECISIONS]\n[/DECISIONS]\n[QUESTIONS]\n[/QUESTIONS]\n[RHIZOME]\n[/RHIZOME]"
			}
			return &agent.Response{Content: reply, Usage: f.responseUsage}, nil
		}
	}

	return &agent.Response{
		Content:  f.replyText,
		Thinking: f.thinkingText,
		Usage:    f.responseUsage,
	}, nil
}

func (f *fakeProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	f.mu.Lock()
	f.lastReasoning = opts.Reasoning
	if f.reasoningBySystem == nil {
		f.reasoningBySystem = make(map[string]agent.ReasoningConfig)
	}
	if len(messages) > 0 && messages[0].Role == "system" {
		f.reasoningBySystem[messages[0].Content] = opts.Reasoning
	}
	f.mu.Unlock()
	return f.Complete(ctx, messages, tools)
}

func (f *fakeProvider) Stream(_ context.Context, messages []agent.Message, _ []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++

	isFaceCall := len(messages) > 0 && messages[0].Role == "system" && strings.Contains(messages[0].Content, "the face of")
	if !isFaceCall {
		return &agent.Response{Content: f.replyText, Usage: f.responseUsage}, nil
	}
	f.seenFaceSystem = append(f.seenFaceSystem, messages[0].Content)
	if f.faceErr != nil {
		return nil, f.faceErr
	}
	reply := strings.TrimSpace(f.streamFaceText)
	if reply == "" {
		reply = strings.TrimSpace(f.faceReplyText)
	}
	if reply == "" {
		reply = f.replyText
	}
	for _, part := range strings.Fields(reply) {
		text := part
		if !strings.HasSuffix(reply, part) {
			text += " "
		}
		if err := cb(agent.StreamChunk{Type: "text", Text: text}); err != nil {
			return nil, err
		}
	}
	return &agent.Response{Content: reply, Thinking: f.thinkingText, Usage: f.responseUsage}, nil
}

func nextFakeReply(queue *[]string, fallback string) string {
	if queue == nil || len(*queue) == 0 {
		return fallback
	}
	reply := (*queue)[0]
	*queue = append((*queue)[:0], (*queue)[1:]...)
	return reply
}

type fakeSender struct {
	mu           sync.Mutex
	sent         []core.OutboundMessage
	sendCount    int
	sendErr      error
	sendErrAfter int
	voice        []voiceSend
	actions      []chatAction
	edits        []messageEdit
	editCount    int
	deletes      []messageDelete
	editErr      error
	actionCh     chan chatAction
}

type inlineDurableGroupChildExecutor struct {
	run func(context.Context, core.InboundMessage) (*DurableGroupChildResult, error)
}

func (e inlineDurableGroupChildExecutor) Supports(sandbox.Scope, core.DurableAgent) bool {
	return e.run != nil
}

func (e inlineDurableGroupChildExecutor) Run(ctx context.Context, _ sandbox.Scope, _ core.DurableAgent, msg core.InboundMessage) (*DurableGroupChildResult, error) {
	return e.run(ctx, msg)
}

func durableGroupTestBootstrapLLM() core.NodeLLMBootstrap {
	return core.NodeLLMBootstrap{
		Backend:        "native",
		NativeProvider: "openrouter",
		APIKey:         "sk-or-group",
		Model:          "openrouter/test-model",
	}
}

type stubRuntimeStatusError struct {
	code int
	msg  string
}

func (e stubRuntimeStatusError) Error() string   { return e.msg }
func (e stubRuntimeStatusError) StatusCode() int { return e.code }

func (f *fakeSender) SendMessage(_ context.Context, msg core.OutboundMessage) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCount++
	if f.sendErr != nil {
		if f.sendErrAfter == 0 || f.sendCount > f.sendErrAfter {
			return 0, f.sendErr
		}
	}
	f.sent = append(f.sent, msg)
	return int64(len(f.sent)), nil
}

type chatAction struct {
	ChatID int64
	Action string
}

type messageEdit struct {
	ChatID    int64
	MessageID int64
	Text      string
}

type messageDelete struct {
	ChatID    int64
	MessageID int64
}

func (f *fakeSender) SendChatAction(_ context.Context, chatID int64, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := chatAction{ChatID: chatID, Action: action}
	f.actions = append(f.actions, entry)
	if f.actionCh != nil {
		select {
		case f.actionCh <- entry:
		default:
		}
	}
	return nil
}

func (f *fakeSender) EditMessageText(_ context.Context, chatID int64, messageID int64, text string, parseMode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editCount++
	if f.editErr != nil {
		return f.editErr
	}
	f.edits = append(f.edits, messageEdit{ChatID: chatID, MessageID: messageID, Text: text})
	return nil
}

func (f *fakeSender) DeleteMessage(_ context.Context, chatID int64, messageID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, messageDelete{ChatID: chatID, MessageID: messageID})
	return nil
}

type voiceSend struct {
	ChatID  int64
	Media   core.Media
	ReplyTo *int64
}

func (f *fakeSender) SendVoiceMessage(_ context.Context, chatID int64, media core.Media, replyTo *int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.voice = append(f.voice, voiceSend{ChatID: chatID, Media: media, ReplyTo: replyTo})
	return int64(len(f.voice)), nil
}

type fakeTranscriber struct {
	text string
	err  error
}

func (f fakeTranscriber) Transcribe(_ context.Context, _ *media.TranscriptionRequest) (*media.Transcription, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &media.Transcription{Text: f.text}, nil
}

func (f fakeTranscriber) Translate(_ context.Context, _ *media.TranscriptionRequest) (*media.Transcription, error) {
	return nil, errors.New("not implemented")
}

type fakeSynth struct {
	media    core.Media
	err      error
	lastText *string
}

func (f fakeSynth) Synthesize(_ context.Context, text string) (core.Media, error) {
	if f.err != nil {
		return core.Media{}, f.err
	}
	if f.lastText != nil {
		*f.lastText = text
	}
	return f.media, nil
}

type toolRequestingProvider struct {
	mu             sync.Mutex
	callCount      int
	firstToolCount int
}

func (p *toolRequestingProvider) Complete(_ context.Context, _ []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.callCount++
	if p.callCount == 1 {
		p.firstToolCount = len(tools)
		if len(tools) == 0 {
			return &agent.Response{Content: "no tools"}, nil
		}
		return &agent.Response{
			ToolCalls: []agent.ToolCall{{
				ID:    "tool-call-1",
				Name:  tools[0].Name,
				Input: json.RawMessage(`{"command":"echo hi"}`),
			}},
		}, nil
	}

	return &agent.Response{Content: "done"}, nil
}

type multiToolRequestingProvider struct {
	mu        sync.Mutex
	callCount int
}

func (p *multiToolRequestingProvider) Complete(_ context.Context, _ []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.callCount++
	if p.callCount == 1 {
		if len(tools) == 0 {
			return &agent.Response{Content: "no tools"}, nil
		}
		return &agent.Response{
			ToolCalls: []agent.ToolCall{
				{
					ID:    "tool-call-1",
					Name:  tools[0].Name,
					Input: json.RawMessage(`{"command":"rg first"}`),
				},
				{
					ID:    "tool-call-2",
					Name:  tools[0].Name,
					Input: json.RawMessage(`{"command":"rg second"}`),
				},
			},
		}, nil
	}
	return &agent.Response{Content: "done"}, nil
}

type durableAgentToolRequestingProvider struct {
	mu             sync.Mutex
	callCount      int
	lastToolOutput string
}

func (p *durableAgentToolRequestingProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.callCount++
	if p.callCount == 1 {
		for _, def := range tools {
			if def.Name == "durable_agent" {
				return &agent.Response{
					ToolCalls: []agent.ToolCall{{
						ID:    "tool-call-1",
						Name:  "durable_agent",
						Input: json.RawMessage(`{"action":"policy_apply","agent_id":"family-group","outbound_mode":"read_only","reason":"ratified from conversation"}`),
					}},
				}, nil
			}
		}
		return &agent.Response{Content: "durable-agent tool unavailable"}, nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			p.lastToolOutput = messages[i].Content
			break
		}
	}
	return &agent.Response{Content: "Policy updated through conversation."}, nil
}

type legacyRecordingTools struct {
	defs         []agent.ToolDef
	executeCalls int
}

func (t *legacyRecordingTools) Definitions() []agent.ToolDef {
	return append([]agent.ToolDef(nil), t.defs...)
}

func (t *legacyRecordingTools) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	t.executeCalls++
	return "legacy execution", nil
}

type principalRecordingTools struct {
	defs                     []agent.ToolDef
	executeCalls             int
	executeForPrincipalCalls int
	supportsPrincipal        bool
	lastPrincipal            principal.Principal
}

func (t *principalRecordingTools) Definitions() []agent.ToolDef {
	return append([]agent.ToolDef(nil), t.defs...)
}

func (t *principalRecordingTools) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	t.executeCalls++
	return "legacy execution", nil
}

func (t *principalRecordingTools) ExecuteForPrincipal(_ context.Context, p principal.Principal, _ string, _ json.RawMessage) (string, error) {
	t.executeForPrincipalCalls++
	t.lastPrincipal = p
	return "principal execution", nil
}

func (t *principalRecordingTools) SupportsPrincipal(_ principal.Principal) bool {
	return t.supportsPrincipal
}

func setFakeBubblewrapRunnerForRegistry(t *testing.T, registry *toolpkg.Registry) {
	t.Helper()

	dir := t.TempDir()
	fakeBwrapPath := filepath.Join(dir, "bwrap")
	script := `#!/usr/bin/env bash
set -euo pipefail
workdir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --chdir)
      shift
      workdir="$1"
      ;;
    --)
      shift
      break
      ;;
  esac
  shift
done
if [[ -n "$workdir" ]]; then
  cd "$workdir"
fi
exec "$@"
`
	if err := os.WriteFile(fakeBwrapPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake bwrap: %v", err)
	}

	registry.WithRunner(sandbox.NewRunnerWithLookPath(func(_ string) (string, error) {
		return fakeBwrapPath, nil
	}))
}

func testExecToolDef() agent.ToolDef {
	return agent.ToolDef{
		Name:        "exec",
		Description: "test exec",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
	}
}

func TestHandleInboundPersistsAndSends(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.thinkingText = "Reasoning summary"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     42,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  99,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "ok" {
		t.Fatalf("final text = %q, want ok", finalText)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 42, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.TurnCount != 1 {
		t.Fatalf("turn count = %d, want 1", sess.TurnCount)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("session messages = %d, want 2", len(sess.Messages))
	}
	if sess.Messages[0].Role != "user" || sess.Messages[1].Role != "assistant" {
		t.Fatalf("roles = %#v %#v", sess.Messages[0], sess.Messages[1])
	}
	if sess.Messages[1].FloorContent != "ok" {
		t.Fatalf("assistant floor = %q, want ok", sess.Messages[1].FloorContent)
	}
	if sess.Messages[1].Thinking != "Reasoning summary" {
		t.Fatalf("assistant thinking = %q, want reasoning summary", sess.Messages[1].Thinking)
	}
	outboundIDs, err := store.OutboundAfterTurn(session.SessionKey{ChatID: 42, UserID: 0}, 0)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(outboundIDs) != 1 || outboundIDs[0] != 1 {
		t.Fatalf("outbound ids = %#v, want [1]", outboundIDs)
	}
}

func TestHandleInboundPersistsWhenSendFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	sender.sendErr = errors.New("send failed")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     44,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  77,
	})
	if err == nil {
		t.Fatal("HandleInbound() err = nil, want send failure")
	}
	if !strings.Contains(err.Error(), "send outbound reply") {
		t.Fatalf("HandleInbound() err = %v, want send outbound reply error", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 44, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.TurnCount != 1 {
		t.Fatalf("turn count = %d, want 1", sess.TurnCount)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("session messages = %d, want 2", len(sess.Messages))
	}
	if sess.Messages[1].Role != "assistant" || strings.TrimSpace(sess.Messages[1].Content) == "" {
		t.Fatalf("assistant message = %#v, want non-empty assistant message", sess.Messages[1])
	}
	outboundIDs, err := store.OutboundAfterTurn(session.SessionKey{ChatID: 44, UserID: 0}, 0)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(outboundIDs) != 0 {
		t.Fatalf("outbound ids = %#v, want empty on send failure", outboundIDs)
	}
}

func TestHandleInboundStreamingFinalizeFailureDoesNotPersistTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	sender.editErr = errors.New("stream finalize failed")
	sender.sendErr = errors.New("stream finalize fallback failed")
	sender.sendErrAfter = 1
	provider.streamFaceText = "streamed idolum reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     45,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  88,
	})
	if err == nil {
		sender.mu.Lock()
		nSent := len(sender.sent)
		nEditAttempts := sender.editCount
		sentTexts := make([]string, len(sender.sent))
		for i, msg := range sender.sent {
			sentTexts[i] = msg.Text
		}
		sender.mu.Unlock()
		t.Fatalf("HandleInbound() err = nil, want stream finalize failure (sent=%d editAttempts=%d texts=%v)", nSent, nEditAttempts, sentTexts)
	}
	if !strings.Contains(err.Error(), "finish streamed reply") {
		t.Fatalf("HandleInbound() err = %v, want finish streamed reply error", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 45, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.TurnCount != 0 {
		t.Fatalf("turn count = %d, want 0", sess.TurnCount)
	}
	if len(sess.Messages) != 0 {
		t.Fatalf("session messages = %d, want 0", len(sess.Messages))
	}
	outboundIDs, err := store.OutboundAfterTurn(session.SessionKey{ChatID: 45, UserID: 0}, 0)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(outboundIDs) != 0 {
		t.Fatalf("outbound ids = %#v, want empty", outboundIDs)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.editCount == 0 {
		t.Fatal("expected attempted streamed edit before finalize failure")
	}
	if sender.sendErr == nil {
		t.Fatal("expected sender send error to be configured for finalize fallback path")
	}
	if sender.sendCount < 2 {
		t.Fatalf("sendCount = %d, want at least 2", sender.sendCount)
	}
	if len(sender.sent) == 0 {
		t.Fatal("expected streamed send before finalize failure")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(sender.sent))
	}
	if len(sender.edits) > 0 && sender.edits[0].ChatID != 45 {
		t.Fatalf("edit chat id = %d, want 45", sender.edits[0].ChatID)
	}
}

func TestHandleInboundStreamsFaceReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.streamFaceText = "streamed idolum reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     52,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  99,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) == 0 {
		t.Fatal("expected at least one streamed send")
	}
	if sender.sent[len(sender.sent)-1].Text == "ok" {
		t.Fatalf("final streamed send = %q, want streamed reply path", sender.sent[len(sender.sent)-1].Text)
	}
	if len(sender.edits) == 0 {
		t.Fatal("expected editMessageText calls during streaming")
	}
	if sender.edits[len(sender.edits)-1].Text != "streamed idolum reply" {
		t.Fatalf("final edited text = %q, want streamed idolum reply", sender.edits[len(sender.edits)-1].Text)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 52, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if got := sess.Messages[len(sess.Messages)-1].Content; got != "streamed idolum reply" {
		t.Fatalf("stored rendered reply = %q, want streamed idolum reply", got)
	}
}

func TestStartChatActionLoopSendsTyping(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{actionCh: make(chan chatAction, 1)}
	rt := &Runtime{outbound: sender}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := rt.startChatActionLoop(ctx, 42, "typing")
	defer stop()

	select {
	case got := <-sender.actionCh:
		if got.ChatID != 42 {
			t.Fatalf("chat id = %d, want 42", got.ChatID)
		}
		if got.Action != "typing" {
			t.Fatalf("action = %q, want typing", got.Action)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected typing action to be sent")
	}
}

func TestHandleInboundReloadsPromptContextEachTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	heartbeatPath := filepath.Join(cfg.Agent.Workspace, "HEARTBEAT.md")
	if err := os.WriteFile(heartbeatPath, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "first",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("first HandleInbound() err = %v", err)
	}

	if err := os.WriteFile(heartbeatPath, []byte("v2"), 0o600); err != nil {
		t.Fatalf("rewrite heartbeat: %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "second",
		MessageID:  2,
	})
	if err != nil {
		t.Fatalf("second HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenGovernorSystem) < 2 {
		t.Fatalf("seen governor system len = %d, want >=2", len(provider.seenGovernorSystem))
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "v1") {
		t.Fatalf("first governor prompt missing v1: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[1], "v2") {
		t.Fatalf("second governor prompt missing v2: %q", provider.seenGovernorSystem[1])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "principal_role: admin") {
		t.Fatalf("first governor prompt missing principal role: %q", provider.seenGovernorSystem[0])
	}
}

func TestHandleInboundApprovedUserDoesNotLoadGlobalDynamicMemory(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	if err := os.WriteFile(filepath.Join(cfg.Agent.Workspace, "MEMORY.md"), []byte("GLOBAL-MEMORY-SECRET"), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     71,
		SenderID:   1002,
		SenderName: "approved",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want at least one prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "agent rules") {
		t.Fatalf("approved user prompt missing shared bootstrap: %q", provider.seenGovernorSystem[0])
	}
	if strings.Contains(provider.seenGovernorSystem[0], "GLOBAL-MEMORY-SECRET") {
		t.Fatalf("approved user prompt leaked global dynamic memory: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "principal_role: approved_user") {
		t.Fatalf("approved user prompt missing principal role: %q", provider.seenGovernorSystem[0])
	}
}

func TestHandleInboundThreadsRuntimeAwarenessToGovernorAndIdolum(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "canonical"
	provider.faceReplyText = "rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     711,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I feel overwhelmed and need help thinking this through",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want governor prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "## Runtime Awareness") {
		t.Fatalf("governor prompt missing runtime awareness: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "- run_kind: interactive") {
		t.Fatalf("governor prompt missing run kind: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "- prompt_root: "+cfg.Agent.PromptRoot) {
		t.Fatalf("governor prompt missing prompt root: %q", provider.seenGovernorSystem[0])
	}
	if len(provider.seenFaceSystem) == 0 {
		t.Fatal("seenFaceSystem empty, want face prompt")
	}
	if !strings.Contains(provider.seenFaceSystem[0], "## Delivery Awareness") {
		t.Fatalf("face prompt missing delivery awareness: %q", provider.seenFaceSystem[0])
	}
	if strings.Contains(provider.seenFaceSystem[0], "exec_root") {
		t.Fatalf("face prompt leaked exec root: %q", provider.seenFaceSystem[0])
	}
}

func TestHandleInboundIncludesIdolumProposalInGovernorInput(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Push for a warmer reply and consider inspecting the repo before answering."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     72,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I'm feeling uncertain about how to answer this well",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want Idolum proposal prompt call")
	}
	if !strings.Contains(provider.seenProposalSystem[0], "mode: proposal") {
		t.Fatalf("proposal prompt missing proposal mode: %q", provider.seenProposalSystem[0])
	}
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want governor prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "## Conversational Pressure") {
		t.Fatalf("governor input missing Idolum proposal block: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "Push for a warmer reply") {
		t.Fatalf("governor input missing concrete Idolum push: %q", provider.seenGovernorSystem[0])
	}
}

func TestHandleInboundUsesBrokerageForStrategicTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nWHY: Ground the feature ideas in the repo.\nPUSH:\n- Inspect first.\n- Keep the answer concrete."
	provider.brokerageReplyText = "INSPECT: no\nQUESTION: no\nANSWER: yes\nWHY: The repo context already supports a direct answer.\nPUSH:\n- Answer directly.\n- Keep it concrete."
	provider.planningReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nPLAN:\n- Inspect the codebase before proposing features.\n- Then reply with prioritized ideas.",
		"INSPECT: no\nQUESTION: no\nANSWER: yes\nRATIFICATION: accept\nPLAN:\n- Reply with prioritized ideas grounded in the current repo context.",
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     720,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want proposal prompt call")
	}
	if !strings.Contains(provider.seenProposalSystem[0], "mode: proposal") {
		t.Fatalf("proposal prompt missing proposal mode: %q", provider.seenProposalSystem[0])
	}
	if len(provider.seenBrokerageSystem) == 0 {
		t.Fatal("seenBrokerageSystem empty, want revised brokerage prompt after adaptation")
	}
	if len(provider.seenPlanningSystem) == 0 {
		t.Fatal("seenPlanningSystem empty, want planning ratification call")
	}
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want governor prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1], "- brokerage_active: true") {
		t.Fatalf("governor prompt missing brokerage awareness: %q", provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1])
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Execution Contract") {
		t.Fatalf("governor input missing negotiated brokerage block: %#v", provider.lastGovernorMsgs)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "### Conversational Pressure") {
		t.Fatalf("negotiated brokerage block missing idolum position: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "### Approved Steps") {
		t.Fatalf("negotiated brokerage block missing approved steps: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "### Ratification Record") {
		t.Fatalf("negotiated brokerage block missing ratification record: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "inspect=no, question=no, answer=yes") {
		t.Fatalf("negotiated brokerage block missing execution contract summary: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "- ratification: accept") {
		t.Fatalf("negotiated brokerage block missing ratification disposition: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1], "- brokerage_ratification: accept") {
		t.Fatalf("governor awareness missing brokerage ratification: %q", provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1])
	}
}

func TestHandleInboundPersistsHiddenInputProvenanceForBrokerageTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Memory.Semantic.Enabled = true
	cfg.Memory.Semantic.Sources = []string{"memory/knowledge.md"}
	cfg.Memory.Semantic.InteractiveTopK = 5
	cfg.Memory.Semantic.InteractiveMaxChars = 4000
	provider.proposalReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nWHY: There is a recurring semantic layer decision hiding under the feature request.\nPUSH:\n- Inspect first.\n- Name the buried blocker."
	provider.brokerageReplyText = "INSPECT: no\nQUESTION: no\nANSWER: yes\nWHY: The recurring blocker is already visible enough to name directly.\nPUSH:\n- Name the buried blocker plainly.\n- Then answer."
	provider.planningReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nSIGNAL_JUDGMENT: confirmed\nPLAN:\n- Inspect the codebase before proposing features.\n- Then answer with prioritized ideas.",
		"INSPECT: no\nQUESTION: no\nANSWER: yes\nRATIFICATION: accept\nSIGNAL_JUDGMENT: confirmed\nPLAN:\n- Name the buried blocker.\n- Then answer with prioritized ideas.",
	}

	if err := os.MkdirAll(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "knowledge.md"), []byte("# knowledge.md\n\n- The semantic layer is the recurring architectural tension."), 0o600); err != nil {
		t.Fatalf("write knowledge.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "questions.md"), []byte("# questions.md\n\n- Should the semantic layer stay lexical-first or become vector-ranked?"), 0o600); err != nil {
		t.Fatalf("write questions.md: %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     721,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my semantic layer work",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want proposal prompt call")
	}
	if !strings.Contains(provider.seenProposalSystem[0], "- hidden_inputs_active: true") {
		t.Fatalf("proposal prompt missing hidden-input awareness: %q", provider.seenProposalSystem[0])
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "signal_judgment: confirmed") {
		t.Fatalf("negotiated brokerage block missing signal judgment: %q", provider.lastGovernorMsgs[1].Content)
	}
	provider.mu.Unlock()

	sess, err := store.Load(session.SessionKey{ChatID: 721, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if strings.TrimSpace(sess.LastFloorMetadata) == "" {
		t.Fatal("LastFloorMetadata empty, want hidden-input provenance")
	}
	if !strings.Contains(sess.LastFloorMetadata, "semantic_recurrence") {
		t.Fatalf("LastFloorMetadata = %q, want semantic recurrence", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "unresolved_memory_state") {
		t.Fatalf("LastFloorMetadata = %q, want unresolved memory state", sess.LastFloorMetadata)
	}
	if len(sess.Messages) < 2 || strings.TrimSpace(sess.Messages[len(sess.Messages)-1].FloorMetadata) == "" {
		t.Fatalf("assistant floor metadata missing from messages: %#v", sess.Messages)
	}
}

func TestHandleInboundRendersFromStructuredMaterialFloor(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Push for a clear, grounded answer."
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- The user is asking for help thinking through the situation.",
		"ALLOWED_ACTIONS:",
		"- Offer grounded next steps.",
		"SCENE_CONSTRAINTS:",
		"- Keep the tone steady and direct.",
		"NOTES:",
		"- Do not sound like a report.",
	}, "\n")
	provider.faceReplyText = "Rendered Idolum scene."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7201,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I feel overwhelmed and need help thinking this through",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want governor prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "## Output Contract") {
		t.Fatalf("governor prompt missing material floor output contract: %q", provider.seenGovernorSystem[0])
	}
	if len(provider.seenFaceSystem) == 0 {
		t.Fatal("seenFaceSystem empty, want face render prompt")
	}
	if !strings.Contains(provider.seenFaceSystem[0], "## Execution Facts") {
		t.Fatalf("face prompt missing material floor section: %q", provider.seenFaceSystem[0])
	}
	if strings.Contains(provider.seenFaceSystem[0], "## Execution Facts Fallback") {
		t.Fatalf("face prompt should not use serialized floor fallback when structured material is available: %q", provider.seenFaceSystem[0])
	}

	sess, err := store.Load(session.SessionKey{ChatID: 7201, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorText, "FACTS:") {
		t.Fatalf("LastFloorText = %q, want text-shaped material floor", sess.LastFloorText)
	}
}

func TestHandleInboundFallsBackToPlainProposalWhenBrokerageRatificationFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nWHY: Ground the answer.\nPUSH:\n- Inspect first.",
		"Push for a concrete answer grounded in what is already known.",
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.provider = planningErrorProvider{Provider: rt.provider, err: errors.New("planning failed")}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     721,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want initial proposal plus proposal rerun after planning failure")
	}
	if len(provider.lastGovernorMsgs) < 2 {
		t.Fatalf("lastGovernorMsgs len = %d, want at least 2", len(provider.lastGovernorMsgs))
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Conversational Pressure") {
		t.Fatalf("governor input should fall back to Idolum proposal block: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "Push for a concrete answer grounded in what is already known.") {
		t.Fatalf("governor input should use rerun proposal text: %q", provider.lastGovernorMsgs[1].Content)
	}
	if strings.Contains(provider.lastGovernorMsgs[1].Content, "## Execution Contract") {
		t.Fatalf("governor input should not contain negotiated brokerage after planning failure: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1], "- brokerage_phase: proposal") {
		t.Fatalf("governor awareness should fall back to proposal mode: %q", provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1])
	}
}

func TestHandleInboundFallsBackToPlainProposalWhenBrokerageRatificationIsInvalid(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect first.",
		"Push for a concrete answer grounded in what is already known.",
	}
	provider.planningReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nPLAN:\n- Inspect first."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7211,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want proposal rerun after invalid planning response")
	}
	if len(provider.lastGovernorMsgs) < 2 {
		t.Fatalf("lastGovernorMsgs len = %d, want at least 2", len(provider.lastGovernorMsgs))
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Conversational Pressure") {
		t.Fatalf("governor input should fall back to Idolum proposal block: %q", provider.lastGovernorMsgs[1].Content)
	}
	if strings.Contains(provider.lastGovernorMsgs[1].Content, "## Execution Contract") {
		t.Fatalf("governor input should not contain negotiated brokerage after invalid planning response: %q", provider.lastGovernorMsgs[1].Content)
	}
}

func TestHandleInboundPreservesBrokerageWhenProposalRerunAlsoFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect first.\n- Keep the user moving."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.provider = planningErrorProvider{Provider: rt.provider, err: errors.New("planning failed")}
	provider.proposalErr = errors.New("proposal rerun failed")
	provider.proposalErrAfter = 2

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     722,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.lastGovernorMsgs) < 2 {
		t.Fatalf("lastGovernorMsgs len = %d, want at least 2", len(provider.lastGovernorMsgs))
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Conversational Pressure") {
		t.Fatalf("governor input should fail closed to proposal framing when rerun fails: %q", provider.lastGovernorMsgs[1].Content)
	}
	if strings.Contains(provider.lastGovernorMsgs[1].Content, "## Execution Contract") {
		t.Fatalf("governor input should not retain negotiated brokerage after failed convergence: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1], "- brokerage_phase: proposal") {
		t.Fatalf("governor awareness should fall back to proposal mode when rerun fails: %q", provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1])
	}
}

func TestHandleInboundFaceFailureUsesSerializedFallbackAfterMaterialFloor(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Push for a clear, grounded answer."
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- The repo was inspected.",
		"ALLOWED_ACTIONS:",
		"- Propose the strongest next steps.",
		"SCENE_CONSTRAINTS:",
		"- Keep the tone practical.",
	}, "\n")
	provider.faceErr = errors.New("render failed")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7202,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I feel overwhelmed and need help thinking this through",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 7202, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	want := strings.Join([]string{
		"What matters:",
		"- The repo was inspected.",
		"",
		"Next:",
		"- Propose the strongest next steps.",
	}, "\n")
	if sender.sent[0].Text != want {
		t.Fatalf("sent text = %q, want serialized floor fallback %q", sender.sent[0].Text, want)
	}
	if !strings.Contains(sess.LastFloorText, "FACTS:") {
		t.Fatalf("session floor sidecar = %q, want structured floor sidecar", sess.LastFloorText)
	}
}

func TestHandleInboundFloorFallbackBackendSerializesStructuredFloor(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Stay grounded and concrete."
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- The repo was inspected.",
		"COMMITMENTS:",
		"- Keep the answer focused on the next move.",
		"SCENE_CONSTRAINTS:",
		"- Do not sound theatrical.",
	}, "\n")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7203,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "help me think this through",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	want := strings.Join([]string{
		"What matters:",
		"- The repo was inspected.",
		"",
		"Committed:",
		"- Keep the answer focused on the next move.",
	}, "\n")

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != want {
		t.Fatalf("outbound text = %q, want serialized floor fallback %q", sender.sent[0].Text, want)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 7203, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorText, "FACTS:") {
		t.Fatalf("session floor sidecar = %q, want structured floor sidecar", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != want {
		t.Fatalf("visible transcript assistant content = %q, want serialized floor fallback", sess.Messages[1].Content)
	}
}

func TestHandleInboundRendersIdolumForSimpleFactualTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "The current time is 12:00 UTC."
	provider.faceReplyText = "It's 12:00 UTC."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     73,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "what time is it?",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) != 1 {
		t.Fatalf("seenProposalSystem len = %d, want 1", len(provider.seenProposalSystem))
	}
	if len(provider.seenFaceSystem) == 0 {
		t.Fatal("seenFaceSystem empty, want face render for simple factual turn")
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "It's 12:00 UTC." {
		t.Fatalf("final text = %q, want face-rendered reply", finalText)
	}
}

func TestHandleInboundCompactsLongSessionBeforeGovernorTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Sessions.MaxContextRatio = 0.40
	cfg.Sessions.CompactionRatio = 0.20
	cfg.Governor.Codex.ContextWindow = 120
	provider.replyText = "fresh reply"
	provider.compactionReplyText = "Compacted summary of the earlier conversation."

	key := session.SessionKey{ChatID: 74, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 4
	long := strings.Repeat("memory-rich content ", 20)
	if err := store.Save(sess, []session.Message{
		{Role: "user", Content: "turn one " + long, TurnIndex: 1},
		{Role: "assistant", Content: "reply one " + long, FloorContent: "reply one " + long, TurnIndex: 1},
		{Role: "user", Content: "turn two " + long, TurnIndex: 2},
		{Role: "assistant", Content: "reply two " + long, FloorContent: "reply two " + long, TurnIndex: 2},
		{Role: "user", Content: "turn three " + long, TurnIndex: 3},
		{Role: "assistant", Content: "reply three " + long, FloorContent: "reply three " + long, TurnIndex: 3},
		{Role: "user", Content: "turn four " + long, TurnIndex: 4},
		{Role: "assistant", Content: "reply four " + long, FloorContent: "reply four " + long, TurnIndex: 4},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(seed) err = %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.governorBackend = "codex"

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     74,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "continue",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if len(reloaded.CompactionLog) == 0 {
		t.Fatal("compaction log empty, want at least one entry")
	}
	if reloaded.CompactionLog[len(reloaded.CompactionLog)-1].Strategy != "summarize" {
		t.Fatalf("compaction strategy = %q, want summarize", reloaded.CompactionLog[len(reloaded.CompactionLog)-1].Strategy)
	}
	foundSummary := false
	compactedCount := 0
	for _, msg := range reloaded.Messages {
		if msg.Compacted {
			compactedCount++
		}
		if msg.Role == "assistant" && strings.Contains(msg.Content, "Compacted summary of the earlier conversation.") {
			foundSummary = true
		}
	}
	if compactedCount == 0 {
		t.Fatal("compactedCount = 0, want some old messages compacted")
	}
	if !foundSummary {
		t.Fatal("compaction summary message not found in reloaded session")
	}
}

func TestHandleInboundRendersIdolumForCodeHeavyReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "```go\nfmt.Println(\"hi\")\n```"
	provider.proposalReplyText = "Push harder"
	provider.faceReplyText = "Idolum should not render code-heavy output."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     75,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "please look into why this code is written this way",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want proposal call for open-ended request")
	}
	if len(provider.seenFaceSystem) == 0 {
		t.Fatal("seenFaceSystem empty, want face render for code-heavy reply")
	}
}

func TestHandleInboundAutoModeTranscribesAndRepliesWithVoice(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "idolum text"
	provider.faceReplyText = "idolum spoken"
	var synthesized string
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "transcribed hello"}, fakeSynth{
		media:    core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
		lastText: &synthesized,
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1200,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  77,
		Artifacts:  []core.Artifact{{ID: "voice-1", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("text sends = %d, want 0 in auto mode for voice input", len(sender.sent))
	}
	if len(sender.voice) != 1 {
		t.Fatalf("voice sends = %d, want 1", len(sender.voice))
	}
	if sender.voice[0].ChatID != 1200 {
		t.Fatalf("voice chat id = %d, want 1200", sender.voice[0].ChatID)
	}
	if synthesized != "idolum spoken" {
		t.Fatalf("synthesized text = %q, want idolum spoken", synthesized)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1200, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 || sess.Messages[0].Content != "transcribed hello\n\n[voice attached]" {
		t.Fatalf("session messages = %#v, want transcribed user text plus voice marker", sess.Messages)
	}
	if sess.Messages[1].Content != "idolum spoken" {
		t.Fatalf("assistant scene = %q, want idolum spoken", sess.Messages[1].Content)
	}
}

func TestHandleInboundVoiceFallsBackToTextWhenSynthesisFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "voice fallback text"
	provider.faceReplyText = "voice scene text"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "transcribed hello"}, fakeSynth{
		err: errors.New("tts down"),
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1201,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  78,
		Artifacts:  []core.Artifact{{ID: "voice-2", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0 on synth failure", len(sender.voice))
	}
	if len(sender.sent) != 1 || sender.sent[0].Text != "voice scene text" {
		t.Fatalf("text sends = %#v, want text fallback", sender.sent)
	}
}

func TestHandleInboundSendsTelegramMediaReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	chartPath := filepath.Join(cfg.Agent.Workspace, "chart.png")
	if err := os.WriteFile(chartPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", chartPath, err)
	}
	provider.replyText = "Here you go.\nMEDIA: chart.png"
	provider.faceReplyText = "Here you go."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1202,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "send the chart",
		MessageID:  79,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "Here you go." {
		t.Fatalf("caption = %q, want %q", sender.sent[0].Text, "Here you go.")
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	if sender.sent[0].Media[0].Type != "image" {
		t.Fatalf("media type = %q, want image", sender.sent[0].Media[0].Type)
	}
	if sender.sent[0].Media[0].Path != chartPath {
		t.Fatalf("media path = %q, want %q", sender.sent[0].Media[0].Path, chartPath)
	}
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0", len(sender.voice))
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1202, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if got := sess.Messages[len(sess.Messages)-1].FloorContent; got != "Here you go." {
		t.Fatalf("assistant floor = %q, want %q", got, "Here you go.")
	}
}

func TestHandleInboundMediaOnlyReplyOmitsNoResponseCaption(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	chartPath := filepath.Join(cfg.Agent.Workspace, "chart.png")
	if err := os.WriteFile(chartPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", chartPath, err)
	}
	provider.replyText = "MEDIA: chart.png"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1203,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "send only the file",
		MessageID:  80,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "" {
		t.Fatalf("caption = %q, want empty", sender.sent[0].Text)
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	if sender.sent[0].Media[0].Path != chartPath {
		t.Fatalf("media path = %q, want %q", sender.sent[0].Media[0].Path, chartPath)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1203, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if got := sess.Messages[len(sess.Messages)-1].Content; got != "" {
		t.Fatalf("assistant content = %q, want empty", got)
	}
	if got := sess.Messages[len(sess.Messages)-1].FloorContent; got != "" {
		t.Fatalf("assistant floor = %q, want empty", got)
	}
}

func TestHandleInboundExplicitMediaBeatsVoiceSynthesis(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	chartPath := filepath.Join(cfg.Agent.Workspace, "chart.png")
	if err := os.WriteFile(chartPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", chartPath, err)
	}
	provider.replyText = "MEDIA: chart.png"
	var synthesized string

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "transcribed hello"}, fakeSynth{
		media:    core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
		lastText: &synthesized,
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1204,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  81,
		Artifacts:  []core.Artifact{{ID: "voice-3", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0", len(sender.voice))
	}
	if len(sender.sent) != 1 {
		t.Fatalf("text/media sends = %d, want 1", len(sender.sent))
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	if sender.sent[0].Media[0].Path != chartPath {
		t.Fatalf("media path = %q, want %q", sender.sent[0].Media[0].Path, chartPath)
	}
	if synthesized != "" {
		t.Fatalf("synthesized text = %q, want empty", synthesized)
	}
}

func TestHandleInboundVoiceFallbackSerializerUsesVoiceOverlay(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Keep it steady and direct."
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- The repo was inspected.",
		"COMMITMENTS:",
		"- Keep the answer focused on the next move.",
		"REFUSALS:",
		"- Pretend the tests passed when they did not.",
		"SCENE_CONSTRAINTS:",
		"- Do not become lyrical.",
	}, "\n")
	provider.faceErr = errors.New("face unavailable")

	var synthesized string
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "help me think this through"}, fakeSynth{
		media:    core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
		lastText: &synthesized,
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1202,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  79,
		Artifacts:  []core.Artifact{{ID: "voice-3", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	want := "Here's what matters: The repo was inspected. I'll keep the answer focused on the next move. I won't pretend the tests passed when they did not."
	if synthesized != want {
		t.Fatalf("synthesized text = %q, want %q", synthesized, want)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("text sends = %d, want 0 in voice fallback mode", len(sender.sent))
	}
	if len(sender.voice) != 1 {
		t.Fatalf("voice sends = %d, want 1", len(sender.voice))
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1202, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != want {
		t.Fatalf("session assistant text = %q, want voice-shaped fallback transcript", sess.Messages[1].Content)
	}
}

func TestHandleInboundAutoModeTextInputStaysText(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "plain text reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "unused"}, fakeSynth{
		media: core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1202,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  79,
		Text:       "hello there",
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0 for text input in auto mode", len(sender.voice))
	}
	if len(sender.sent) != 1 {
		t.Fatalf("text sends = %d, want 1", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "plain text reply" {
		t.Fatalf("final text = %q, want plain text reply", finalText)
	}
}

func TestPrepareInboundTurnProcessesArtifacts(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = store
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "spoken transcript"}, fakeSynth{})
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1300,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{
			{ID: "img-1", Channel: "telegram", SourceType: "photo", Kind: "image", MimeType: "image/png", Filename: "screen.png", Data: []byte("image-bytes")},
			{ID: "voice-1", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", MimeType: "audio/ogg", Filename: "voice.ogg", Data: []byte("voice-bytes")},
			{ID: "video-1", Channel: "telegram", SourceType: "video", Kind: "video", MimeType: "video/mp4", Filename: "clip.mp4", SizeBytes: 42},
			{ID: "loc-1", Channel: "telegram", SourceType: "location", Kind: "structured", Metadata: map[string]string{"latitude": "40.0", "longitude": "-73.0"}},
		},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}

	if !prepared.MediaAttached || prepared.MediaMode != "vision" {
		t.Fatalf("prepared media = attached:%t mode:%q, want vision artifact handling", prepared.MediaAttached, prepared.MediaMode)
	}
	if len(prepared.AgentMedia) != 1 {
		t.Fatalf("agent media len = %d, want 1 image artifact for vision", len(prepared.AgentMedia))
	}
	if !strings.Contains(prepared.UserText, "spoken transcript") {
		t.Fatalf("user text = %q, want audio transcript", prepared.UserText)
	}
	if !strings.Contains(prepared.UserText, "clip.mp4") || !strings.Contains(prepared.UserText, "location") {
		t.Fatalf("user text = %q, want video and location summaries", prepared.UserText)
	}
	if !strings.Contains(prepared.LedgerText, "[image attached]") || !strings.Contains(prepared.LedgerText, "[voice attached]") || !strings.Contains(prepared.LedgerText, "[video attached]") {
		t.Fatalf("ledger text = %q, want artifact markers", prepared.LedgerText)
	}
}

func TestHandleInboundPersistsArtifactReferencesInFloorMetadata(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "artifact-aware reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1301,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  1,
		Text:       "what do you make of this screenshot?",
		Artifacts: []core.Artifact{
			{ID: "img-2", Channel: "telegram", SourceType: "photo", Kind: "image", MimeType: "image/png", Filename: "screen.png", Data: []byte("image-bytes")},
		},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1301, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"artifact_id\":\"img-2\"") {
		t.Fatalf("LastFloorMetadata = %q, want artifact reference", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"handling\":\"attach_for_vision\"") {
		t.Fatalf("LastFloorMetadata = %q, want handling decision", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"retention\":\"session_reference\"") {
		t.Fatalf("LastFloorMetadata = %q, want retention decision", sess.LastFloorMetadata)
	}
}

func TestFaceProviderSelection(t *testing.T) {
	origFaceRenderer := newFaceRenderer
	origResolveAuth := resolveGovernorAuth
	origCodexProvider := newCodexProvider
	defer func() {
		newFaceRenderer = origFaceRenderer
		resolveGovernorAuth = origResolveAuth
		newCodexProvider = origCodexProvider
	}()

	codexProvider := &fakeProvider{replyText: "codex"}
	newCodexProvider = func(bundle governorauth.Bundle, cfg *config.Config) (agent.Provider, error) {
		return codexProvider, nil
	}

	tests := []struct {
		name        string
		faceBackend face.Backend
		resolveAuth func(config.GovernorConfig) (governorauth.Bundle, error)
		assert      func(*testing.T, agent.Provider, agent.Provider, agent.Provider)
	}{
		{
			name:        "provider backend uses supplied provider",
			faceBackend: face.BackendProvider,
			resolveAuth: func(cfg config.GovernorConfig) (governorauth.Bundle, error) {
				return governorauth.Bundle{Backend: governorauth.BackendNative}, nil
			},
			assert: func(t *testing.T, captured, providerArg, _ agent.Provider) {
				t.Helper()
				if captured != providerArg {
					t.Fatalf("got face provider %T, want supplied provider %T", captured, providerArg)
				}
			},
		},
		{
			name:        "floor_fallback uses governor provider",
			faceBackend: face.BackendFloorFallback,
			resolveAuth: func(cfg config.GovernorConfig) (governorauth.Bundle, error) {
				return governorauth.Bundle{Backend: governorauth.BackendCodex, BaseURL: "https://codex", AccessToken: "token"}, nil
			},
			assert: func(t *testing.T, captured, _, codexAgent agent.Provider) {
				t.Helper()
				if captured == codexAgent {
					return
				}
				if _, ok := captured.(*providerpkg.FailoverChain); !ok {
					t.Fatalf("got face provider %T, want codex provider or failover chain", captured)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()
			cfg, store, _, sender := buildRuntimeFixtures(t)
			cfg.Face.Backend = string(tt.faceBackend)

			var captured agent.Provider
			newFaceRenderer = func(p agent.Provider, cfg face.ProviderRendererConfig) (*face.ProviderRenderer, error) {
				captured = p
				return origFaceRenderer(p, cfg)
			}
			t.Cleanup(func() { newFaceRenderer = origFaceRenderer })

			resolveGovernorAuth = tt.resolveAuth
			t.Cleanup(func() { resolveGovernorAuth = origResolveAuth })

			providerArg := &fakeProvider{replyText: "face"}
			if _, err := New(cfg, store, providerArg, nil, sender); err != nil {
				t.Fatalf("New() err = %v", err)
			}

			tt.assert(t, captured, providerArg, codexProvider)
		})
	}
}

func buildRuntimeFixtures(t *testing.T) (*config.Config, *session.SQLiteStore, *fakeProvider, *fakeSender) {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")

	cfg := &config.Config{
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{
				AdminUserIDs:    []int64{1001},
				ApprovedUserIDs: []int64{1002},
			},
		},
		Governor: config.GovernorConfig{
			Backend:        "native",
			NativeProvider: "anthropic",
			Codex: config.GovernorCodexConfig{
				AuthSource:    "auto",
				BaseURL:       "https://chatgpt.com/backend-api",
				ContextWindow: 200000,
			},
		},
		Sessions: config.SessionsConfig{
			DBPath:             dbPath,
			IdleExpiry:         "24h",
			MaxContextRatio:    0.75,
			CompactionRatio:    0.55,
			CompactionStrategy: "summarize",
		},
		Agent: config.AgentConfig{
			Workspace:              root,
			PromptRoot:             root,
			ExecRoot:               root,
			SharedMemoryRoot:       root,
			UserWorkspaceRoot:      filepath.Join(root, "isolated", "workspaces"),
			UserMemoryRoot:         filepath.Join(root, "isolated", "memory"),
			MaxIterations:          10,
			ToolTimeout:            10,
			BootstrapFiles:         []string{"AGENTS.md"},
			DynamicFiles:           []string{"MEMORY.md", "HEARTBEAT.md"},
			BootstrapMaxChars:      20000,
			BootstrapTotalMaxChars: 150000,
			DailyNotes:             false,
			DailyNotesDir:          "memory/daily",
		},
		Memory: config.MemoryConfig{
			Reflection: config.MemoryReflectionConfig{
				Enabled: true,
				Every:   "6h",
			},
			Decay: config.MemoryDecayConfig{
				Enabled:  true,
				HotDays:  3,
				WarmDays: 14,
				ColdDays: 30,
			},
			Identity: config.MemoryIdentityConfig{
				Preserve: []string{"SOUL.md", "IDENTITY.md", "IDOLUM.md", "MEMORY.md"},
			},
		},
	}

	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("agent rules"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("memory"), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	provider := &fakeProvider{
		replyText: "ok",
		responseUsage: core.TokenUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	}
	sender := &fakeSender{}
	return cfg, store, provider, sender
}

func TestNewRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	store := &session.SQLiteStore{}
	provider := &fakeProvider{}
	sender := &fakeSender{}

	if _, err := New(nil, store, provider, nil, sender); err == nil {
		t.Fatal("expected nil config error")
	}
	if _, err := New(cfg, nil, provider, nil, sender); err == nil {
		t.Fatal("expected nil store error")
	}
	if _, err := New(cfg, store, nil, nil, sender); err == nil {
		t.Fatal("expected nil provider error")
	}
	if _, err := New(cfg, store, provider, nil, nil); err == nil {
		t.Fatal("expected nil outbound error")
	}
}

func TestNewAllowsNilProviderForCodexFloorFallback(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()
	cfg.Face.Backend = "floor_fallback"

	authPath := filepath.Join(cfg.Governor.Codex.CodexHome, "auth.json")
	rawAuth := `{"tokens":{"access_token":"codex-access","refresh_token":"refresh-secret","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(rawAuth), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return &fakeProvider{replyText: "codex canonical"}, nil
	}

	if _, err := New(cfg, store, nil, nil, sender); err != nil {
		t.Fatalf("New() err = %v, want nil native provider to be allowed for codex passthrough", err)
	}
}

func TestAgentFuncDelegates(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	fn := rt.AgentFunc()
	_, err = fn(context.Background(), nil, core.InboundMessage{
		ChatID:     8,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  1,
		Raw:        json.RawMessage(`{"source":"test"}`),
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("AgentFunc() err = %v", err)
	}
}

func TestHandleInboundRejectsUnknownPrincipal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     123,
		SenderID:   999999,
		SenderName: "intruder",
		Text:       "hello",
		MessageID:  1,
	})
	if err == nil {
		t.Fatal("HandleInbound() err = nil, want principal denied error")
	}
	if !strings.Contains(err.Error(), ErrPrincipalDenied.Error()) {
		t.Fatalf("error = %v, want %q", err, ErrPrincipalDenied)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.callCount != 0 {
		t.Fatalf("provider call count = %d, want 0", provider.callCount)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent len = %d, want 0", len(sender.sent))
	}
}

func TestHandleInboundApprovedUserDisablesToolsWithoutIsolationFloor(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &legacyRecordingTools{
		defs: []agent.ToolDef{testExecToolDef()},
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     501,
		SenderID:   1002,
		SenderName: "approved",
		Text:       "run pwd",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if provider.firstToolCount != 0 {
		t.Fatalf("first tool count = %d, want 0", provider.firstToolCount)
	}
	if tools.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", tools.executeCalls)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "no tools" {
		t.Fatalf("outbound text = %q, want no tools", sender.sent[0].Text)
	}
}

func TestHandleInboundApprovedUserUsesPrincipalAwareToolsWhenSupported(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &principalRecordingTools{
		defs:              []agent.ToolDef{testExecToolDef()},
		supportsPrincipal: true,
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     502,
		SenderID:   1002,
		SenderName: "approved",
		Text:       "run pwd",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if provider.firstToolCount != 1 {
		t.Fatalf("first tool count = %d, want 1", provider.firstToolCount)
	}
	if tools.executeForPrincipalCalls != 1 {
		t.Fatalf("executeForPrincipal calls = %d, want 1", tools.executeForPrincipalCalls)
	}
	if tools.executeCalls != 0 {
		t.Fatalf("legacy execute calls = %d, want 0", tools.executeCalls)
	}
	if tools.lastPrincipal.Role != principal.RoleApprovedUser {
		t.Fatalf("last principal role = %q, want approved_user", tools.lastPrincipal.Role)
	}
	if tools.lastPrincipal.TelegramUserID != 1002 {
		t.Fatalf("last principal user id = %d, want 1002", tools.lastPrincipal.TelegramUserID)
	}
}

func TestHandleInboundAdminCanManageDurableAgentThroughConversationTool(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &durableAgentToolRequestingProvider{}
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM:       durableGroupTestBootstrapLLM(),
		PolicyVersion:      1,
		LocalStorageRoots:  []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:      "default",
		WakeupMode:         "telegram_update",
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     42,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "Set family-group to read only.",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	updated, err := store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("updated outbound_mode = %q, want read_only", updated.LivePolicy.OutboundMode)
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated policy_version = %d, want 2", updated.PolicyVersion)
	}

	provider.mu.Lock()
	if !strings.Contains(provider.lastToolOutput, "action: durable-agent policy apply") {
		t.Fatalf("tool output = %q, want durable-agent policy apply output", provider.lastToolOutput)
	}
	provider.mu.Unlock()

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 2 {
		t.Fatalf("sent len = %d, want progress + final reply", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0].Text, "Working on Set family-group to read only") {
		t.Fatalf("progress text = %q, want conversation-derived durable_agent progress entry", sender.sent[0].Text)
	}
	if sender.sent[1].Text != "Policy updated through conversation." {
		t.Fatalf("final reply = %q, want conversational policy update reply", sender.sent[1].Text)
	}
}

func TestHandleInboundShowsToolProgressForActualToolCalls(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &multiToolRequestingProvider{}
	tools := &legacyRecordingTools{
		defs: []agent.ToolDef{testExecToolDef()},
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     503,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "inspect",
		MessageID:  99,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 2 {
		t.Fatalf("sent len = %d, want 2 (progress + reply)", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0].Text, "Working on it") {
		t.Fatalf("progress text = %q, want tool progress message", sender.sent[0].Text)
	}
	if !strings.Contains(sender.sent[0].Text, "Working on inspect") {
		t.Fatalf("progress text = %q, want task-derived progress label", sender.sent[0].Text)
	}
	if strings.Contains(sender.sent[0].Text, "rg first") {
		t.Fatalf("progress text = %q, want task-derived progress instead of raw command", sender.sent[0].Text)
	}
	if sender.sent[0].ReplyTo == nil || *sender.sent[0].ReplyTo != 99 {
		t.Fatalf("progress reply_to = %#v, want 99", sender.sent[0].ReplyTo)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edit count = %d, want 1", len(sender.edits))
	}
	if !strings.Contains(sender.edits[0].Text, "Working on inspect (2x)") {
		t.Fatalf("edit text = %q, want aggregated task-derived tool progress", sender.edits[0].Text)
	}
	sender.mu.Unlock()

	run, err := store.LatestTurnRun(session.SessionKey{ChatID: 503, UserID: 0})
	if err != nil {
		t.Fatalf("LatestTurnRun() err = %v", err)
	}
	if run.Status != session.TurnRunStatusCompleted {
		t.Fatalf("turn run status = %q, want completed", run.Status)
	}
	if run.ToolCallsStarted != 2 {
		t.Fatalf("tool_calls_started = %d, want 2", run.ToolCallsStarted)
	}
	if run.ToolCallsFinished != 2 {
		t.Fatalf("tool_calls_finished = %d, want 2", run.ToolCallsFinished)
	}
	if run.LastToolResultPreview == "" {
		t.Fatal("last_tool_result_preview is empty, want persisted tool finish preview")
	}
	if run.ProgressMessageID != 1 {
		t.Fatalf("progress_message_id = %d, want 1", run.ProgressMessageID)
	}
}

func TestHandleInboundAdminFallsBackToLegacyToolsWhenPrincipalAwareNotReady(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &principalRecordingTools{
		defs:              []agent.ToolDef{testExecToolDef()},
		supportsPrincipal: false,
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     503,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "run pwd",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if provider.firstToolCount != 1 {
		t.Fatalf("first tool count = %d, want 1", provider.firstToolCount)
	}
	if tools.executeCalls != 1 {
		t.Fatalf("legacy execute calls = %d, want 1", tools.executeCalls)
	}
	if tools.executeForPrincipalCalls != 0 {
		t.Fatalf("executeForPrincipal calls = %d, want 0", tools.executeForPrincipalCalls)
	}
}

func TestHandleInboundRendersViaFaceByDefault(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     901,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "idolum rendered" {
		t.Fatalf("outbound text = %q, want idolum rendered", finalText)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 901, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("session messages len = %d, want >= 2", len(sess.Messages))
	}
	if sess.Messages[1].Content != "idolum rendered" {
		t.Fatalf("session assistant text = %q, want rendered reply", sess.Messages[1].Content)
	}
	if sess.LastFloorText != "governor canonical" {
		t.Fatalf("session floor sidecar = %q, want canonical", sess.LastFloorText)
	}
}

func TestHandleInboundFaceFailureFallsBackToFloorFallback(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceErr = errors.New("face unavailable")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     902,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "governor canonical" {
		t.Fatalf("outbound text = %q, want canonical fallback", sender.sent[0].Text)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 902, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.LastFloorText != "governor canonical" {
		t.Fatalf("session floor sidecar = %q, want canonical", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != "governor canonical" {
		t.Fatalf("visible transcript assistant content = %q, want canonical fallback", sess.Messages[1].Content)
	}
}

func TestHandleInboundFloorFallbackBackendSkipsFaceRender(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     903,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "governor canonical" {
		t.Fatalf("outbound text = %q, want canonical passthrough", sender.sent[0].Text)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 903, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.LastFloorText != "governor canonical" {
		t.Fatalf("session floor sidecar = %q, want canonical", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != "governor canonical" {
		t.Fatalf("visible transcript assistant content = %q, want canonical passthrough", sess.Messages[1].Content)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenFaceSystem) != 0 {
		t.Fatalf("face should not be called in passthrough mode; calls=%d", len(provider.seenFaceSystem))
	}
}

func TestNewRejectsInvalidIdleExpiry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Sessions.IdleExpiry = "not-a-duration"

	_, err := New(cfg, store, provider, nil, sender)
	if err == nil {
		t.Fatal("New() err = nil, want idle_expiry parse error")
	}
}

func TestNewRejectsCodexBackendWithoutCredentials(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()

	_, err := New(cfg, store, provider, nil, sender)
	if err == nil {
		t.Fatal("New() err = nil, want codex credential failure")
	}
}

func TestHandleInboundUsesCodexGovernorBackend(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()

	const accessToken = "codex-access-secret"
	authPath := filepath.Join(cfg.Governor.Codex.CodexHome, "auth.json")
	rawAuth := `{"tokens":{"access_token":"` + accessToken + `","refresh_token":"refresh-secret","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(rawAuth), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	cfg.Governor.Codex.BaseURL = "https://chatgpt.com/backend-api"

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return &fakeProvider{
			replyText: "codex canonical",
			responseUsage: core.TokenUsage{
				InputTokens:  12,
				OutputTokens: 7,
				TotalTokens:  19,
			},
		}, nil
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     404,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hi",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	callCount := provider.callCount
	provider.mu.Unlock()
	if callCount != 0 {
		t.Fatalf("native provider call count = %d, want 0", callCount)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "codex canonical" {
		t.Fatalf("outbound text = %q, want codex canonical", sender.sent[0].Text)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 404, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if strings.Contains(sess.SystemPrompt, accessToken) {
		t.Fatalf("system prompt leaked token: %q", sess.SystemPrompt)
	}
}

func TestNewAutoFallsBackToNativeWhenCodexCredentialsMissing(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "auto"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()

	var codexFactoryCalls int32
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		atomic.AddInt32(&codexFactoryCalls, 1)
		return &fakeProvider{replyText: "codex"}, nil
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     405,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hi",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if got := atomic.LoadInt32(&codexFactoryCalls); got != 0 {
		t.Fatalf("codex factory calls = %d, want 0 in native fallback", got)
	}
	provider.mu.Lock()
	callCount := provider.callCount
	provider.mu.Unlock()
	if callCount == 0 {
		t.Fatal("native provider was not used in auto fallback")
	}
}

func TestNewAutoPrefersCodexWhenCredentialsExist(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "auto"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()

	authPath := filepath.Join(cfg.Governor.Codex.CodexHome, "auth.json")
	rawAuth := `{"tokens":{"access_token":"codex-access","refresh_token":"refresh-secret","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(rawAuth), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return &fakeProvider{replyText: "codex auto"}, nil
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     406,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hi",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	callCount := provider.callCount
	provider.mu.Unlock()
	if callCount != 0 {
		t.Fatalf("native provider call count = %d, want 0 when codex selected", callCount)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || sender.sent[0].Text != "codex auto" {
		t.Fatalf("outbound = %#v, want codex auto", sender.sent)
	}
}

func TestCodexRuntimeFailureFallsBackToNativeProviderChain(t *testing.T) {
	cfg, store, nativeProvider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Face.Backend = "floor_fallback"

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return &fakeProvider{err: stubRuntimeStatusError{code: 503, msg: "codex unavailable"}}, nil
	}

	rt, err := New(cfg, store, nativeProvider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     407,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hi",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	nativeProvider.mu.Lock()
	callCount := nativeProvider.callCount
	nativeProvider.mu.Unlock()
	if callCount == 0 {
		t.Fatal("native provider was not used after codex runtime failure")
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != nativeProvider.replyText {
		t.Fatalf("outbound text = %q, want %q", sender.sent[0].Text, nativeProvider.replyText)
	}
}

func TestImageTurnUsesNativeProviderWhenGovernorBackendIsCodex(t *testing.T) {
	cfg, store, nativeProvider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Face.Backend = "floor_fallback"

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	codexProvider := &fakeProvider{replyText: "codex canonical"}
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return codexProvider, nil
	}

	rt, err := New(cfg, store, nativeProvider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     408,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  1,
		Artifacts: []core.Artifact{{
			ID:         "img-408",
			Channel:    "telegram",
			SourceType: "photo",
			Kind:       "image",
			Data:       []byte("fake-image"),
			MimeType:   "image/png",
			Filename:   "photo.png",
		}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	codexProvider.mu.Lock()
	codexCalls := codexProvider.callCount
	codexProvider.mu.Unlock()
	if codexCalls != 0 {
		t.Fatalf("codex call count = %d, want 0 for image turn", codexCalls)
	}

	nativeProvider.mu.Lock()
	defer nativeProvider.mu.Unlock()
	if nativeProvider.callCount == 0 {
		t.Fatal("native provider was not used for image turn")
	}
	if len(nativeProvider.lastGovernorMsgs) == 0 {
		t.Fatal("native provider saw no governor messages")
	}
	last := nativeProvider.lastGovernorMsgs[len(nativeProvider.lastGovernorMsgs)-1]
	if last.Role != "user" || len(last.Media) != 1 {
		t.Fatalf("last governor message = %#v, want user message with media", last)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 408, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) == 0 || !strings.Contains(sess.Messages[0].Content, "[image attached]") {
		t.Fatalf("stored user content = %#v, want image placeholder", sess.Messages)
	}
}

func TestPrepareInboundTurnPDFExtractionFailureFallsBackToPlaceholder(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    409,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "pdf-409",
			Channel:    "telegram",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "pdf",
			Data:       []byte("not-a-real-pdf"),
			MimeType:   "application/pdf",
			Filename:   "broken.pdf",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if !strings.Contains(prepared.UserText, "PDF attached") {
		t.Fatalf("user text = %q, want PDF placeholder", prepared.UserText)
	}
	if !strings.Contains(prepared.LedgerText, "[pdf attached]") {
		t.Fatalf("ledger text = %q, want pdf attached marker", prepared.LedgerText)
	}
}
