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
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/media"
	"github.com/idolum-ai/aphelion/principal"
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
