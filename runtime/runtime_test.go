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
	"github.com/idolum-ai/aphelion/session"
)

type fakeProvider struct {
	mu                 sync.Mutex
	callCount          int
	replyText          string
	proposalReplyText  string
	faceReplyText      string
	faceErr            error
	seenGovernorSystem []string
	seenFaceSystem     []string
	seenProposalSystem []string
	responseUsage      core.TokenUsage
}

func (f *fakeProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++

	isFaceCall := len(messages) > 0 && messages[0].Role == "system" && strings.Contains(messages[0].Content, "the face of")
	if isFaceCall {
		if strings.Contains(messages[0].Content, "- mode: proposal") {
			f.seenProposalSystem = append(f.seenProposalSystem, messages[0].Content)
			reply := strings.TrimSpace(f.proposalReplyText)
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

	var systemParts []string
	for _, msg := range messages {
		if msg.Role == "system" && strings.TrimSpace(msg.Content) != "" {
			systemParts = append(systemParts, msg.Content)
		}
	}
	f.seenGovernorSystem = append(f.seenGovernorSystem, strings.Join(systemParts, "\n\n"))

	return &agent.Response{
		Content: f.replyText,
		Usage:   f.responseUsage,
	}, nil
}

type fakeSender struct {
	mu       sync.Mutex
	sent     []core.OutboundMessage
	voice    []voiceSend
	actions  []chatAction
	edits    []messageEdit
	deletes  []messageDelete
	actionCh chan chatAction
}

func (f *fakeSender) SendMessage(_ context.Context, msg core.OutboundMessage) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeSender) EditMessageText(_ context.Context, chatID int64, messageID int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	media core.Media
	err   error
}

func (f fakeSynth) Synthesize(_ context.Context, _ string) (core.Media, error) {
	if f.err != nil {
		return core.Media{}, f.err
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
	if sender.sent[0].Text != "ok" {
		t.Fatalf("sent text = %q, want ok", sender.sent[0].Text)
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
	if sess.Messages[1].CanonicalContent != "ok" {
		t.Fatalf("assistant canonical = %q, want ok", sess.Messages[1].CanonicalContent)
	}
	outboundIDs, err := store.OutboundAfterTurn(session.SessionKey{ChatID: 42, UserID: 0}, 0)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(outboundIDs) != 1 || outboundIDs[0] != 1 {
		t.Fatalf("outbound ids = %#v, want [1]", outboundIDs)
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
	rt.faceBackend = face.BackendGovernorPassthrough

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
		Text:       "please look into the codebase",
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
	if !strings.Contains(provider.seenGovernorSystem[0], "## Idolum Proposal") {
		t.Fatalf("governor input missing Idolum proposal block: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "Push for a warmer reply") {
		t.Fatalf("governor input missing concrete Idolum push: %q", provider.seenGovernorSystem[0])
	}
}

func TestHeartbeatTargetNoneStoresMaintenanceWithoutOutbound(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "none"
	provider.replyText = "heartbeat canonical"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      222,
		SourceUserID:      1002,
		SourceRole:        "approved_user",
		TargetAdminChatID: 1001,
		TurnFrom:          1,
		TurnTo:            1,
		Summary:           "user is asking for help",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	if err := rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent len = %d, want 0", len(sender.sent))
	}
	sender.mu.Unlock()

	maintenance, err := store.Load(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0})
	if err != nil {
		t.Fatalf("Load(heartbeat session) err = %v", err)
	}
	if maintenance.LastCanonicalReply != "heartbeat canonical" {
		t.Fatalf("maintenance canonical = %q, want heartbeat canonical", maintenance.LastCanonicalReply)
	}
	if len(maintenance.Messages) == 0 || maintenance.Messages[len(maintenance.Messages)-1].Content != "heartbeat canonical" {
		t.Fatalf("maintenance messages = %#v, want canonical heartbeat entry", maintenance.Messages)
	}
	if len(maintenance.Messages) != 2 || maintenance.Messages[0].Role != "user" || maintenance.Messages[1].Role != "assistant" {
		t.Fatalf("maintenance message roles = %#v, want synthetic user + assistant", maintenance.Messages)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events len = %d, want 1", len(events))
	}
}

func TestHeartbeatDeliveryUsesFaceAndMarksReviewEventsDelivered(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "last"
	provider.replyText = "heartbeat canonical"
	provider.faceReplyText = "heartbeat rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      333,
		SourceUserID:      1002,
		SourceRole:        "approved_user",
		TargetAdminChatID: 1001,
		TurnFrom:          2,
		TurnTo:            2,
		Summary:           "needs review",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	if err := rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].ChatID != 1001 || sender.sent[0].Text != "heartbeat rendered" {
		t.Fatalf("sent = %#v, want rendered heartbeat to admin", sender.sent[0])
	}
	sender.mu.Unlock()

	adminSession, err := store.Load(session.SessionKey{ChatID: 1001, UserID: 0})
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if adminSession.LastCanonicalReply != "heartbeat canonical" {
		t.Fatalf("admin canonical = %q, want heartbeat canonical", adminSession.LastCanonicalReply)
	}
	if len(adminSession.Messages) == 0 || adminSession.Messages[len(adminSession.Messages)-1].Content != "heartbeat rendered" {
		t.Fatalf("admin messages = %#v, want rendered heartbeat entry", adminSession.Messages)
	}
	if adminSession.Messages[len(adminSession.Messages)-1].CanonicalContent != "heartbeat canonical" {
		t.Fatalf("admin canonical content = %q, want heartbeat canonical", adminSession.Messages[len(adminSession.Messages)-1].CanonicalContent)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("pending review events len = %d, want 0 after delivery", len(events))
	}
}

func TestCronJobNoneStoresDedicatedSessionWithoutOutbound(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "cron canonical"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := rt.runCronJobOnce(context.Background(), config.CronJobConfig{
		ID:       "sample",
		Every:    "2h",
		Prompt:   "Summarize pending maintenance state.",
		Delivery: "none",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("runCronJobOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent len = %d, want 0", len(sender.sent))
	}
	sender.mu.Unlock()

	cronSession, err := store.Load(session.SessionKey{ChatID: cronSessionChatID("sample"), UserID: 0})
	if err != nil {
		t.Fatalf("Load(cron session) err = %v", err)
	}
	if cronSession.LastCanonicalReply != "cron canonical" {
		t.Fatalf("cron canonical = %q, want cron canonical", cronSession.LastCanonicalReply)
	}
	if len(cronSession.Messages) == 0 || cronSession.Messages[len(cronSession.Messages)-1].Content != "cron canonical" {
		t.Fatalf("cron messages = %#v, want canonical cron entry", cronSession.Messages)
	}
	if len(cronSession.Messages) != 2 || cronSession.Messages[0].Role != "user" || cronSession.Messages[1].Role != "assistant" {
		t.Fatalf("cron message roles = %#v, want synthetic user + assistant", cronSession.Messages)
	}
}

func TestCronJobAnnounceUsesFaceAndUpdatesAdminSession(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "cron canonical"
	provider.faceReplyText = "cron rendered"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := rt.runCronJobOnce(context.Background(), config.CronJobConfig{
		ID:       "announce",
		Every:    "1h",
		Prompt:   "Tell the admin something useful.",
		Delivery: "announce",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("runCronJobOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].ChatID != 1001 || sender.sent[0].Text != "cron rendered" {
		t.Fatalf("sent = %#v, want rendered cron to admin", sender.sent[0])
	}
	sender.mu.Unlock()

	adminSession, err := store.Load(session.SessionKey{ChatID: 1001, UserID: 0})
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if adminSession.LastCanonicalReply != "cron canonical" {
		t.Fatalf("admin canonical = %q, want cron canonical", adminSession.LastCanonicalReply)
	}
	if len(adminSession.Messages) == 0 || adminSession.Messages[len(adminSession.Messages)-1].Content != "cron rendered" {
		t.Fatalf("admin messages = %#v, want rendered cron entry", adminSession.Messages)
	}
	if adminSession.Messages[len(adminSession.Messages)-1].CanonicalContent != "cron canonical" {
		t.Fatalf("admin canonical content = %q, want cron canonical", adminSession.Messages[len(adminSession.Messages)-1].CanonicalContent)
	}
}

func TestHandleInboundVoiceOnlyTranscribesAndRepliesWithVoice(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "idolum text"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "voice_only"}, fakeTranscriber{text: "transcribed hello"}, fakeSynth{
		media: core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1200,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  77,
		Media:      []core.Media{{Type: "voice", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("text sends = %d, want 0 in voice_only mode", len(sender.sent))
	}
	if len(sender.voice) != 1 {
		t.Fatalf("voice sends = %d, want 1", len(sender.voice))
	}
	if sender.voice[0].ChatID != 1200 {
		t.Fatalf("voice chat id = %d, want 1200", sender.voice[0].ChatID)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1200, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 || sess.Messages[0].Content != "transcribed hello" {
		t.Fatalf("session messages = %#v, want transcribed user text", sess.Messages)
	}
}

func TestHandleInboundVoiceFallsBackToTextWhenSynthesisFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "voice fallback text"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "voice_only"}, fakeTranscriber{text: "transcribed hello"}, fakeSynth{
		err: errors.New("tts down"),
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1201,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  78,
		Media:      []core.Media{{Type: "voice", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0 on synth failure", len(sender.voice))
	}
	if len(sender.sent) != 1 || sender.sent[0].Text != "voice fallback text" {
		t.Fatalf("text sends = %#v, want text fallback", sender.sent)
	}
}

func TestStartupRecoveryLogsMaintenanceAnalysis(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Recovered: rerun the interrupted inspection if still needed."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 1500, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "study the codebase")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"rg aphelion"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 55); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}

	if err := rt.runStartupRecoveryOnce(context.Background(), time.Date(2026, time.April, 9, 20, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runStartupRecoveryOnce() err = %v", err)
	}

	maintenance, err := store.Load(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0})
	if err != nil {
		t.Fatalf("Load(maintenance) err = %v", err)
	}
	if maintenance.LastCanonicalReply != provider.replyText {
		t.Fatalf("maintenance canonical = %q, want %q", maintenance.LastCanonicalReply, provider.replyText)
	}
	if len(maintenance.Messages) != 2 || maintenance.Messages[0].Role != "user" || maintenance.Messages[1].Role != "assistant" {
		t.Fatalf("maintenance messages = %#v, want synthetic user + assistant", maintenance.Messages)
	}

	pending, err := store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending recovery runs = %d, want 0", len(pending))
	}

	storedRun, err := store.TurnRun(run.ID)
	if err != nil {
		t.Fatalf("TurnRun() err = %v", err)
	}
	if storedRun.RecoverySummary != provider.replyText {
		t.Fatalf("recovery summary = %q, want %q", storedRun.RecoverySummary, provider.replyText)
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
		want        func(agent.Provider, agent.Provider) agent.Provider
	}{
		{
			name:        "provider backend uses supplied provider",
			faceBackend: face.BackendProvider,
			resolveAuth: func(cfg config.GovernorConfig) (governorauth.Bundle, error) {
				return governorauth.Bundle{Backend: governorauth.BackendNative}, nil
			},
			want: func(providerArg, codexAgent agent.Provider) agent.Provider {
				return providerArg
			},
		},
		{
			name:        "governor_passthrough uses governor provider",
			faceBackend: face.BackendGovernorPassthrough,
			resolveAuth: func(cfg config.GovernorConfig) (governorauth.Bundle, error) {
				return governorauth.Bundle{Backend: governorauth.BackendCodex, BaseURL: "https://codex", AccessToken: "token"}, nil
			},
			want: func(providerArg, codexAgent agent.Provider) agent.Provider {
				return codexAgent
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

			want := tt.want(providerArg, codexProvider)
			if captured != want {
				t.Fatalf("got face provider %T, want %T", captured, want)
			}
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
				AuthSource: "auto",
				BaseURL:    "https://chatgpt.com/backend-api/codex",
			},
		},
		Sessions: config.SessionsConfig{
			DBPath:     dbPath,
			IdleExpiry: "24h",
		},
		Agent: config.AgentConfig{
			Workspace:              root,
			MaxIterations:          10,
			ToolTimeout:            10,
			BootstrapFiles:         []string{"AGENTS.md"},
			DynamicFiles:           []string{"MEMORY.md", "HEARTBEAT.md"},
			BootstrapMaxChars:      20000,
			BootstrapTotalMaxChars: 150000,
			DailyNotes:             false,
			DailyNotesDir:          "memory",
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

func TestNewAllowsNilProviderForCodexGovernorPassthrough(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()
	cfg.Face.Backend = "governor_passthrough"

	authPath := filepath.Join(cfg.Governor.Codex.CodexHome, "auth.json")
	rawAuth := `{"tokens":{"access_token":"codex-access","refresh_token":"refresh-secret"}}`
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
	rt.faceBackend = face.BackendGovernorPassthrough

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
	rt.faceBackend = face.BackendGovernorPassthrough

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
	rt.faceBackend = face.BackendGovernorPassthrough

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
	if !strings.Contains(sender.sent[0].Text, "Working with tools") {
		t.Fatalf("progress text = %q, want tool progress message", sender.sent[0].Text)
	}
	if sender.sent[0].ReplyTo == nil || *sender.sent[0].ReplyTo != 99 {
		t.Fatalf("progress reply_to = %#v, want 99", sender.sent[0].ReplyTo)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edit count = %d, want 1", len(sender.edits))
	}
	if !strings.Contains(sender.edits[0].Text, "rg second") {
		t.Fatalf("edit text = %q, want second tool preview", sender.edits[0].Text)
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
	rt.faceBackend = face.BackendGovernorPassthrough

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
	if sender.sent[0].Text != "idolum rendered" {
		t.Fatalf("outbound text = %q, want idolum rendered", sender.sent[0].Text)
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
	if sess.LastCanonicalReply != "governor canonical" {
		t.Fatalf("session canonical sidecar = %q, want canonical", sess.LastCanonicalReply)
	}
}

func TestHandleInboundFaceFailureFallsBackToGovernorPassthrough(t *testing.T) {
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
	if sess.LastCanonicalReply != "governor canonical" {
		t.Fatalf("session canonical sidecar = %q, want canonical", sess.LastCanonicalReply)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != "governor canonical" {
		t.Fatalf("visible transcript assistant content = %q, want canonical fallback", sess.Messages[1].Content)
	}
}

func TestHandleInboundGovernorPassthroughBackendSkipsFaceRender(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendGovernorPassthrough

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
	if sess.LastCanonicalReply != "governor canonical" {
		t.Fatalf("session canonical sidecar = %q, want canonical", sess.LastCanonicalReply)
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

func TestHandleInboundDeliversPendingReviewEventsForAdmin(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      44,
		SourceRole:        "approved_user",
		TargetAdminChatID: 42,
		TurnFrom:          1,
		TurnTo:            3,
		Summary:           "user requested package install in isolated workspace",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     42,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "status",
		MessageID:  99,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 2 {
		t.Fatalf("sent len = %d, want 2", len(sender.sent))
	}
	if sender.sent[0].Text != "ok" {
		t.Fatalf("first message = %q, want model reply", sender.sent[0].Text)
	}
	if !strings.Contains(sender.sent[1].Text, "[Review Digest]") {
		t.Fatalf("second message missing digest label: %q", sender.sent[1].Text)
	}
	if !strings.Contains(sender.sent[1].Text, "source_chat=7001") {
		t.Fatalf("second message missing source chat: %q", sender.sent[1].Text)
	}

	pending, err := store.PendingReviewEvents(42, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len = %d, want 0 after delivery", len(pending))
	}

	adminSession, err := store.Load(session.SessionKey{ChatID: 42, UserID: 0})
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if len(adminSession.Messages) != 3 {
		t.Fatalf("admin session messages len = %d, want 3", len(adminSession.Messages))
	}
	if !strings.Contains(adminSession.Messages[2].Content, "[Review Digest]") {
		t.Fatalf("admin digest content = %q, want persisted review digest", adminSession.Messages[2].Content)
	}
}

func TestHandleInboundDoesNotDeliverReviewEventsForApprovedUser(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      8001,
		SourceUserID:      77,
		SourceRole:        "approved_user",
		TargetAdminChatID: 42,
		TurnFrom:          3,
		TurnTo:            4,
		Summary:           "requires admin review",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     99,
		SenderID:   1002,
		SenderName: "member",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1 (only model reply)", len(sender.sent))
	}
	if sender.sent[0].Text != "ok" {
		t.Fatalf("message = %q, want ok", sender.sent[0].Text)
	}

	pending, err := store.PendingReviewEvents(42, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1 (not delivered in non-admin turn)", len(pending))
	}
}

func TestHandleInboundGeneratesReviewEventForApprovedUser(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     222,
		SenderID:   1002,
		SenderName: "member",
		Text:       "please summarize what happened",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	event := pending[0]
	if event.SourceChatID != 222 {
		t.Fatalf("source chat = %d, want 222", event.SourceChatID)
	}
	if event.SourceUserID != 1002 {
		t.Fatalf("source user = %d, want 1002", event.SourceUserID)
	}
	if event.SourceRole != "approved_user" {
		t.Fatalf("source role = %q, want approved_user", event.SourceRole)
	}
	if event.TurnFrom != 1 || event.TurnTo != 1 {
		t.Fatalf("turn range = %d-%d, want 1-1", event.TurnFrom, event.TurnTo)
	}
	if !strings.Contains(event.Summary, "provenance chat=222 user=1002 role=approved_user turn=1") {
		t.Fatalf("summary missing provenance: %q", event.Summary)
	}
	if len([]rune(event.Summary)) > session.DefaultReviewSummaryMaxChars {
		t.Fatalf("summary len = %d, want <= %d", len([]rune(event.Summary)), session.DefaultReviewSummaryMaxChars)
	}
}

func TestShouldGenerateReviewEvent(t *testing.T) {
	t.Parallel()

	if !shouldGenerateReviewEvent(principal.Principal{Role: principal.RoleApprovedUser}, session.SessionKey{ChatID: 1, UserID: 0}) {
		t.Fatal("approved_user should generate review event")
	}
	if shouldGenerateReviewEvent(principal.Principal{Role: principal.RoleAdmin}, session.SessionKey{ChatID: 1, UserID: 0}) {
		t.Fatal("admin top-level session should not generate review event")
	}
	if !shouldGenerateReviewEvent(principal.Principal{Role: principal.RoleAdmin}, session.SessionKey{ChatID: 1, UserID: 7}) {
		t.Fatal("admin subordinate session should generate review event")
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
	rawAuth := `{"tokens":{"access_token":"` + accessToken + `","refresh_token":"refresh-secret"}}`
	if err := os.WriteFile(authPath, []byte(rawAuth), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	cfg.Governor.Codex.BaseURL = "https://chatgpt.com/backend-api/codex"

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
	rt.faceBackend = face.BackendGovernorPassthrough

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
	rt.faceBackend = face.BackendGovernorPassthrough

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
	rawAuth := `{"tokens":{"access_token":"codex-access","refresh_token":"refresh-secret"}}`
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
	rt.faceBackend = face.BackendGovernorPassthrough

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

func TestStartIdleExpiryLoopRunsAndStopsWithContext(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	var calls int32
	rt.expireIdle = func(_ time.Duration) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	rt.startIdleExpiryLoop(ctx, 20*time.Millisecond, func(string, ...any) {})

	time.Sleep(75 * time.Millisecond)
	beforeCancel := atomic.LoadInt32(&calls)
	if beforeCancel < 2 {
		t.Fatalf("expire calls before cancel = %d, want >= 2", beforeCancel)
	}

	cancel()
	time.Sleep(60 * time.Millisecond)
	afterCancel := atomic.LoadInt32(&calls)
	if afterCancel != beforeCancel {
		t.Fatalf("expire calls changed after cancel: before=%d after=%d", beforeCancel, afterCancel)
	}
}

func TestStartIdleExpiryLoopLogsErrorsAndContinues(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	var calls int32
	rt.expireIdle = func(_ time.Duration) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, errors.New("boom")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt.startIdleExpiryLoop(ctx, 20*time.Millisecond, func(string, ...any) {})
	time.Sleep(70 * time.Millisecond)

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("expire calls = %d, want >= 2 despite errors", got)
	}
}

func TestIdleExpirySweepCadence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		idleExpiry time.Duration
		want       time.Duration
	}{
		{name: "negative defaults to minute", idleExpiry: -time.Second, want: time.Minute},
		{name: "tiny floors at minute", idleExpiry: 30 * time.Second, want: time.Minute},
		{name: "quarter duration", idleExpiry: 4 * time.Hour, want: time.Hour},
		{name: "caps at hour", idleExpiry: 24 * time.Hour, want: time.Hour},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := idleExpirySweepCadence(tc.idleExpiry)
			if got != tc.want {
				t.Fatalf("idleExpirySweepCadence(%s) = %s, want %s", tc.idleExpiry, got, tc.want)
			}
		})
	}
}
