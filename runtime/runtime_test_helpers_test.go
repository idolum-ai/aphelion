//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/media"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"strings"
	"sync"
	"time"
)

type fakeProvider struct {
	mu                      sync.Mutex
	callCount               int
	err                     error
	replyText               string
	thinkingText            string
	reflectionReplyText     string
	memoryCaptureReplyText  string
	memoryFlushReplyText    string
	compactionReplyText     string
	proposalReplyText       string
	proposalReplies         []string
	brokerageReplyText      string
	brokerageReplies        []string
	planningReplyText       string
	planningReplies         []string
	faceReplyText           string
	repairReplyText         string
	repairReplies           []string
	doctorSummaryReplyText  string
	interpretationReplyText string
	interpretationReplies   []string
	streamFaceText          string
	faceErr                 error
	proposalErr             error
	proposalErrAfter        int
	proposalCallCount       int
	seenGovernorSystem      []string
	seenFaceSystem          []string
	seenProposalSystem      []string
	seenBrokerageSystem     []string
	seenPlanningSystem      []string
	lastGovernorMsgs        []agent.Message
	lastGovernorTools       []agent.ToolDef
	lastDoctorSummaryMsgs   []agent.Message
	lastDoctorSummaryTools  []agent.ToolDef
	responseUsage           core.TokenUsage
	lastReasoning           agent.ReasoningConfig
	lastVerbosity           agent.Verbosity
	reasoningBySystem       map[string]agent.ReasoningConfig
	replyMedia              []core.Media
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

func (f *fakeProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
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
	if fakeMessagesContain(messages, doctorSummaryMarker) {
		f.lastDoctorSummaryMsgs = append([]agent.Message(nil), messages...)
		f.lastDoctorSummaryTools = append([]agent.ToolDef(nil), tools...)
		reply := strings.TrimSpace(f.doctorSummaryReplyText)
		if reply == "" {
			reply = "State of Things\nMost important fix: review the full health diagnosis report in session history.\n\nRecommendations\nStart with the highest-risk active item."
		}
		return &agent.Response{Content: reply, Usage: f.responseUsage}, nil
	}
	if fakeMessagesContain(messages, "runtime interpretation role") {
		reply := nextFakeReply(&f.interpretationReplies, f.interpretationReplyText)
		resp, _ := fakeInterpretationResponse(messages, reply, f.responseUsage)
		return resp, nil
	}
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
	f.lastGovernorMsgs = append([]agent.Message(nil), messages...)
	f.lastGovernorTools = append([]agent.ToolDef(nil), tools...)
	f.seenGovernorSystem = append(f.seenGovernorSystem, strings.Join(systemParts, "\n\n"))
	for _, userText := range userParts {
		if strings.Contains(strings.Join(systemParts, "\n\n"), "You are compacting an existing session ledger.") {
			reply := strings.TrimSpace(f.compactionReplyText)
			if reply == "" {
				reply = "Compacted summary of earlier turns."
			}
			return &agent.Response{Content: reply, Usage: f.responseUsage}, nil
		}
		if strings.Contains(userText, aggressiveMemoryCaptureMarker) {
			reply := strings.TrimSpace(f.memoryCaptureReplyText)
			if reply == "" {
				reply = "[MEMORY]\n[/MEMORY]\n[KNOWLEDGE]\n[/KNOWLEDGE]\n[DECISIONS]\n[/DECISIONS]\n[QUESTIONS]\n[/QUESTIONS]\n[RHIZOME]\n[/RHIZOME]"
			}
			return &agent.Response{Content: reply, Usage: f.responseUsage}, nil
		}
		if strings.Contains(userText, aggressiveMemoryFlushMarker) {
			reply := strings.TrimSpace(f.memoryFlushReplyText)
			if reply == "" {
				reply = "[MEMORY]\n[/MEMORY]\n[KNOWLEDGE]\n[/KNOWLEDGE]\n[DECISIONS]\n[/DECISIONS]\n[QUESTIONS]\n[/QUESTIONS]\n[RHIZOME]\n[/RHIZOME]"
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
		Media:    append([]core.Media(nil), f.replyMedia...),
		Usage:    f.responseUsage,
	}, nil
}

func (f *fakeProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	f.mu.Lock()
	f.lastReasoning = opts.Reasoning
	f.lastVerbosity = opts.Verbosity
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

func fakeMessagesContain(messages []agent.Message, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}

func fakeInterpretationResponse(messages []agent.Message, reply string, usage core.TokenUsage) (*agent.Response, bool) {
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
	if !strings.Contains(strings.Join(systemParts, "\n\n"), "runtime interpretation role") {
		return nil, false
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		reply = fakeInterpretationClaimsReply(strings.Join(userParts, "\n\n"))
	}
	return &agent.Response{Content: reply, Usage: usage}, true
}

func fakeInterpretationClaimsReply(raw string) string {
	var req interpretationRequest
	_ = json.Unmarshal([]byte(strings.TrimSpace(raw)), &req)
	text := strings.ToLower(strings.TrimSpace(req.Text))
	claims := make([]core.InterpretationClaim, 0, 4)
	addExecutionClaim := func(risks ...string) {
		claims = append(claims, core.NormalizeInterpretationClaim(core.InterpretationClaim{
			Intent:             "reply_execution_claim",
			Scope:              "final_reply",
			Risk:               risks,
			Confidence:         "medium",
			Source:             "test_interpretation_role",
			ProposedNextAction: "validate_against_tes",
		}))
	}
	addMediaClaim := func(intent string, scope string) {
		claims = append(claims, core.NormalizeInterpretationClaim(core.InterpretationClaim{
			Intent:             intent,
			Scope:              scope,
			Risk:               []string{"media_artifact"},
			Confidence:         "high",
			Source:             "test_interpretation_role",
			ProposedNextAction: "transcribe_and_reply_text",
		}))
	}
	switch strings.TrimSpace(req.Surface) {
	case "final_reply":
		prior := strings.Contains(text, "prior validation") ||
			strings.Contains(text, "previous validation") ||
			strings.Contains(text, "existing validation record") ||
			strings.Contains(text, "prior commit")
		suggestion := strings.Contains(text, "use this exact command") ||
			strings.Contains(text, "i would frame") ||
			strings.Contains(text, "not as the bot")
		negated := strings.Contains(text, "won't pretend") ||
			strings.Contains(text, "will not pretend") ||
			strings.Contains(text, "do not have") ||
			strings.Contains(text, "without current-turn")
		if !prior && !suggestion && !negated {
			if strings.Contains(text, "done") || strings.Contains(text, "finished") || strings.Contains(text, "completed") || strings.Contains(text, "all set") {
				addExecutionClaim("completion")
			}
			if strings.Contains(text, "executed command") || strings.Contains(text, "applied the patch") || strings.Contains(text, "updated the files") {
				addExecutionClaim("tool_execution")
			}
			if strings.Contains(text, "ran go test") || strings.Contains(text, "tests passed") || strings.Contains(text, "validation passed") {
				addExecutionClaim("tool_execution", "test_execution")
			}
			if strings.Contains(text, "durable wake completed") || strings.Contains(text, "woke durable") || strings.Contains(text, "processed pending parent guidance") {
				addExecutionClaim("durable_agent")
			}
		}
	case "inbound_media_instruction":
		if strings.Contains(text, "transcrib") || strings.Contains(text, "transcript") {
			if strings.Contains(text, "next audio") || strings.Contains(text, "following audio") || strings.Contains(text, "upcoming audio") {
				addMediaClaim(hiddenInputPendingMediaIntent, "next_audio")
			} else if req.HasAudio {
				addMediaClaim(hiddenInputMediaReplyModality, "current_audio")
			}
		}
	}
	rawContract, _ := json.Marshal(interpretationClaimsContract{
		SchemaVersion: interpretationClaimsSchema,
		Surface:       strings.TrimSpace(req.Surface),
		Claims:        claims,
	})
	return interpretationClaimsMarker + ": " + string(rawContract)
}

type fakeSender struct {
	mu              sync.Mutex
	sent            []core.OutboundMessage
	inline          []inlineCall
	sendCount       int
	sendErr         error
	sendErrAfter    int
	documentErr     error
	voice           []voiceSend
	documents       []documentSend
	actions         []chatAction
	edits           []messageEdit
	editClear       []messageEdit
	editInline      []messageEditInline
	editCount       int
	deletes         []messageDelete
	editErr         error
	actionCh        chan chatAction
	mutableMessages map[int64]struct{}
}

type fakeInboundFetcher struct {
	mu        sync.Mutex
	data      map[string][]byte
	err       error
	requests  []string
	maxByFile map[string]int64
}

func (f *fakeInboundFetcher) DownloadFileChecked(_ context.Context, fileID string, maxBytes int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, fileID)
	if f.maxByFile == nil {
		f.maxByFile = map[string]int64{}
	}
	f.maxByFile[fileID] = maxBytes
	if f.err != nil {
		return nil, f.err
	}
	if data, ok := f.data[fileID]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, nil
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

type inlineDurableWakeChildExecutor struct {
	run func(context.Context, sandbox.Scope, core.DurableAgent, time.Time) error
}

func (e inlineDurableWakeChildExecutor) Supports(sandbox.Scope, core.DurableAgent) bool {
	return e.run != nil
}

func (e inlineDurableWakeChildExecutor) Run(ctx context.Context, scope sandbox.Scope, agent core.DurableAgent, now time.Time) error {
	if e.run == nil {
		return nil
	}
	return e.run(ctx, scope, agent, now)
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

func (e stubRuntimeStatusError) Error() string { return e.msg }

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
	messageID := int64(len(f.sent) + 1)
	if msg.Delivery != nil {
		msg.Delivery.MessageIDs = append(msg.Delivery.MessageIDs, messageID)
	}
	f.sent = append(f.sent, msg)
	return messageID, nil
}

func (f *fakeSender) SendInlineKeyboard(_ context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inline = append(f.inline, inlineCall{chatID: chatID, text: text, rows: rows, replyTo: replyTo})
	return int64(len(f.inline)), nil
}

type chatAction struct {
	ChatID int64
	Action string
}

type inlineCall struct {
	chatID  int64
	text    string
	rows    [][]telegram.InlineButton
	replyTo *int64
}

type messageEdit struct {
	ChatID    int64
	MessageID int64
	Text      string
}

type messageEditInline struct {
	ChatID    int64
	MessageID int64
	Text      string
	Rows      [][]telegram.InlineButton
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
	f.updateStoredMessageText(messageID, text)
	f.edits = append(f.edits, messageEdit{ChatID: chatID, MessageID: messageID, Text: text})
	return nil
}

func (f *fakeSender) EditMessageTextWithoutInlineKeyboard(_ context.Context, chatID int64, messageID int64, text string, parseMode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editCount++
	if f.editErr != nil {
		return f.editErr
	}
	f.updateStoredMessageText(messageID, text)
	f.editClear = append(f.editClear, messageEdit{ChatID: chatID, MessageID: messageID, Text: text})
	return nil
}

func (f *fakeSender) EditMessageTextWithInlineKeyboard(_ context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editCount++
	if f.editErr != nil {
		return f.editErr
	}
	f.updateStoredMessageText(messageID, text)
	f.editInline = append(f.editInline, messageEditInline{ChatID: chatID, MessageID: messageID, Text: text, Rows: rows})
	return nil
}

func (f *fakeSender) MarkMessageMutable(messageID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mutableMessages == nil {
		f.mutableMessages = make(map[int64]struct{})
	}
	f.mutableMessages[messageID] = struct{}{}
}

func (f *fakeSender) updateStoredMessageText(messageID int64, text string) {
	if f == nil || messageID <= 0 {
		return
	}
	if _, ok := f.mutableMessages[messageID]; !ok {
		return
	}
	idx := int(messageID - 1)
	if idx >= 0 && idx < len(f.sent) {
		f.sent[idx].Text = text
	}
	if idx >= 0 && idx < len(f.inline) {
		f.inline[idx].text = text
	}
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

type documentSend struct {
	ChatID  int64
	Media   core.Media
	Caption string
	ReplyTo *int64
}

func (f *fakeSender) SendVoiceMessage(_ context.Context, chatID int64, media core.Media, replyTo *int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.voice = append(f.voice, voiceSend{ChatID: chatID, Media: media, ReplyTo: replyTo})
	return int64(len(f.voice)), nil
}

func (f *fakeSender) SendDocumentMessage(_ context.Context, chatID int64, media core.Media, caption string, replyTo *int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.documentErr != nil {
		return 0, f.documentErr
	}
	f.documents = append(f.documents, documentSend{ChatID: chatID, Media: media, Caption: caption, ReplyTo: replyTo})
	return int64(len(f.documents)), nil
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
