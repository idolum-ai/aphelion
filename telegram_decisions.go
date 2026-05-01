//go:build linux

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/principal"
	runtimepkg "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

const (
	defaultInterruptTimeout = 30 * time.Second
	defaultStopWordTimeout  = 15 * time.Second

	// User approval prompts should survive normal operator latency on Telegram.
	// Keep busy/interrupt routing short, but give approval-style decisions enough
	// time to be reviewed without silently failing closed.
	defaultUserApprovalTimeout      = 30 * time.Minute
	defaultExecApprovalTimeout      = defaultUserApprovalTimeout
	defaultArtifactRetentionTimeout = defaultUserApprovalTimeout
	defaultMemoryDelegationTimeout  = defaultUserApprovalTimeout
	defaultSnapshotRestoreTimeout   = defaultUserApprovalTimeout
)

type telegramDecisionSender interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
	DeleteMessage(ctx context.Context, chatID int64, messageID int64) error
	AnswerCallbackQuery(ctx context.Context, id string, text string) error
}

type telegramDecisionKeyboardEditor interface {
	EditMessageTextWithInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error
}

type telegramDecisionKeyboardClearer interface {
	EditMessageTextWithoutInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

type telegramDecisionRouter interface {
	Status(chatID int64) core.SessionStatus
	Stop(chatID int64) core.StopResult
	Route(ctx context.Context, msg core.InboundMessage)
}

type telegramDecisionMessageStatusRouter interface {
	StatusForMessage(msg core.InboundMessage) core.SessionStatus
}

type telegramDecisionMessageStopRouter interface {
	StopForMessage(msg core.InboundMessage) core.StopResult
}

type telegramAudioRetentionKeeper interface {
	KeepAudioArtifactsPermanently(ctx context.Context, msg core.InboundMessage) error
}

func editDecisionMessageClearingInlineKeyboard(ctx context.Context, sender telegramDecisionSender, chatID int64, messageID int64, text string) error {
	if clearer, ok := sender.(telegramDecisionKeyboardClearer); ok {
		return clearer.EditMessageTextWithoutInlineKeyboard(ctx, chatID, messageID, text, "")
	}
	return sender.EditMessageText(ctx, chatID, messageID, text, "")
}

type telegramDecisionHandler struct {
	sender                   telegramDecisionSender
	router                   telegramDecisionRouter
	broker                   *decision.Broker
	store                    *session.SQLiteStore
	audioRetentionKeeper     telegramAudioRetentionKeeper
	interruptTimeout         time.Duration
	stopWordTimeout          time.Duration
	artifactRetentionTimeout time.Duration
}

type telegramExecApprover struct {
	sender  telegramDecisionSender
	broker  *decision.Broker
	timeout time.Duration
}

type telegramDurableMemoryDelegationApprover struct {
	sender  telegramDecisionSender
	broker  *decision.Broker
	timeout time.Duration
}

type telegramDurableSnapshotRestoreApprover struct {
	sender  telegramDecisionSender
	broker  *decision.Broker
	timeout time.Duration
}

func newTelegramDecisionHandler(sender telegramDecisionSender, router telegramDecisionRouter, broker *decision.Broker, store *session.SQLiteStore, keepers ...telegramAudioRetentionKeeper) *telegramDecisionHandler {
	var keeper telegramAudioRetentionKeeper
	if len(keepers) > 0 {
		keeper = keepers[0]
	}
	return &telegramDecisionHandler{
		sender:                   sender,
		router:                   router,
		broker:                   broker,
		store:                    store,
		audioRetentionKeeper:     keeper,
		interruptTimeout:         defaultInterruptTimeout,
		stopWordTimeout:          defaultStopWordTimeout,
		artifactRetentionTimeout: defaultArtifactRetentionTimeout,
	}
}

func newTelegramExecApprover(sender telegramDecisionSender, broker *decision.Broker) *telegramExecApprover {
	return &telegramExecApprover{
		sender:  sender,
		broker:  broker,
		timeout: defaultExecApprovalTimeout,
	}
}

func newTelegramDurableMemoryDelegationApprover(sender telegramDecisionSender, broker *decision.Broker) *telegramDurableMemoryDelegationApprover {
	return &telegramDurableMemoryDelegationApprover{
		sender:  sender,
		broker:  broker,
		timeout: defaultMemoryDelegationTimeout,
	}
}

func newTelegramDurableSnapshotRestoreApprover(sender telegramDecisionSender, broker *decision.Broker) *telegramDurableSnapshotRestoreApprover {
	return &telegramDurableSnapshotRestoreApprover{
		sender:  sender,
		broker:  broker,
		timeout: defaultSnapshotRestoreTimeout,
	}
}

func newTelegramDecisionBroker(sender telegramDecisionSender, opts ...decision.BrokerOption) *decision.Broker {
	return decision.NewBroker(func(ctx context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		text := renderPendingDecisionSummary(pending)
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, inlineButtonRows(pending), replyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		return decision.Delivery{MessageID: msgID}, nil
	}, opts...)
}

func (a *telegramExecApprover) ConfirmExec(ctx context.Context, req toolpkg.ExecApprovalRequest) (toolpkg.ExecApprovalDecision, error) {
	if a == nil || a.sender == nil || a.broker == nil {
		return toolpkg.ExecApprovalDecision{}, fmt.Errorf("telegram exec approver is not configured")
	}
	if req.SessionKey.ChatID == 0 {
		return toolpkg.ExecApprovalDecision{}, fmt.Errorf("command requires explicit confirmation but no interactive chat is available: %s", req.Reason)
	}

	result, err := a.broker.Request(ctx, decision.Request{
		Kind:          decision.KindProposalApproval,
		ChatID:        req.SessionKey.ChatID,
		SenderID:      req.Principal.TelegramUserID,
		Prompt:        "Approve this proposal?",
		Details:       formatExecProposalDetails(req),
		Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
		DefaultChoice: "deny",
		Timeout:       a.timeout,
	})
	if err != nil {
		return toolpkg.ExecApprovalDecision{}, err
	}

	if result.Choice == "approve" {
		if result.Delivery.MessageID != 0 {
			editApprovedDecisionConfirmation(ctx, a.sender, req.SessionKey.ChatID, result.Delivery.MessageID, "Proposal", result.DecisionID, decision.KindProposalApproval, formatExecProposalDetails(req))
		}
		return toolpkg.ExecApprovalDecision{Approved: true}, nil
	}

	if result.Delivery.MessageID != 0 {
		text := "Proposal denied."
		if result.TimedOut {
			text = "Proposal denied — approval timed out."
		}
		_ = editDecisionMessageClearingInlineKeyboard(ctx, a.sender, req.SessionKey.ChatID, result.Delivery.MessageID, text)
	}
	return toolpkg.ExecApprovalDecision{Approved: false}, nil
}

func (a *telegramDurableMemoryDelegationApprover) ConfirmDurableMemoryDelegation(ctx context.Context, req toolpkg.DurableMemoryDelegationApprovalRequest) (toolpkg.DurableMemoryDelegationApprovalDecision, error) {
	if a == nil || a.sender == nil || a.broker == nil {
		return toolpkg.DurableMemoryDelegationApprovalDecision{}, fmt.Errorf("telegram durable memory delegation approver is not configured")
	}
	if req.SessionKey.ChatID == 0 {
		return toolpkg.DurableMemoryDelegationApprovalDecision{}, fmt.Errorf("memory delegation requires explicit confirmation but no interactive chat is available")
	}
	result, err := a.broker.Request(ctx, decision.Request{
		Kind:          decision.KindMemoryDelegation,
		ChatID:        req.SessionKey.ChatID,
		SenderID:      req.Principal.TelegramUserID,
		Prompt:        "Approve memory delegation to the child?",
		Details:       formatDurableMemoryDelegationDetails(req),
		Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
		DefaultChoice: "deny",
		Timeout:       a.timeout,
	})
	if err != nil {
		return toolpkg.DurableMemoryDelegationApprovalDecision{}, err
	}
	if result.Choice == "approve" {
		if result.Delivery.MessageID != 0 {
			editApprovedDecisionConfirmation(ctx, a.sender, req.SessionKey.ChatID, result.Delivery.MessageID, "Memory delegation", result.DecisionID, decision.KindMemoryDelegation, formatDurableMemoryDelegationDetails(req))
		}
		return toolpkg.DurableMemoryDelegationApprovalDecision{Approved: true}, nil
	}
	if result.Delivery.MessageID != 0 {
		text := "Memory delegation denied."
		if result.TimedOut {
			text = "Memory delegation denied — approval timed out."
		}
		_ = editDecisionMessageClearingInlineKeyboard(ctx, a.sender, req.SessionKey.ChatID, result.Delivery.MessageID, text)
	}
	return toolpkg.DurableMemoryDelegationApprovalDecision{Approved: false, TimedOut: result.TimedOut}, nil
}

func (a *telegramDurableSnapshotRestoreApprover) ConfirmDurableSnapshotRestore(ctx context.Context, req toolpkg.DurableSnapshotRestoreApprovalRequest) (toolpkg.DurableSnapshotRestoreApprovalDecision, error) {
	if a == nil || a.sender == nil || a.broker == nil {
		return toolpkg.DurableSnapshotRestoreApprovalDecision{}, fmt.Errorf("telegram durable snapshot restore approver is not configured")
	}
	if req.SessionKey.ChatID == 0 {
		return toolpkg.DurableSnapshotRestoreApprovalDecision{}, fmt.Errorf("snapshot restore requires explicit confirmation but no interactive chat is available")
	}
	result, err := a.broker.Request(ctx, decision.Request{
		Kind:          decision.KindSnapshotRestore,
		ChatID:        req.SessionKey.ChatID,
		SenderID:      req.Principal.TelegramUserID,
		Prompt:        "Restore this child snapshot?",
		Details:       formatDurableSnapshotRestoreDetails(req),
		Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
		DefaultChoice: "deny",
		Timeout:       a.timeout,
	})
	if err != nil {
		return toolpkg.DurableSnapshotRestoreApprovalDecision{}, err
	}
	if result.Choice == "approve" {
		if result.Delivery.MessageID != 0 {
			editApprovedDecisionConfirmation(ctx, a.sender, req.SessionKey.ChatID, result.Delivery.MessageID, "Snapshot restore", result.DecisionID, decision.KindSnapshotRestore, formatDurableSnapshotRestoreDetails(req))
		}
		return toolpkg.DurableSnapshotRestoreApprovalDecision{Approved: true}, nil
	}
	if result.Delivery.MessageID != 0 {
		text := "Snapshot restore denied."
		if result.TimedOut {
			text = "Snapshot restore denied — approval timed out."
		}
		_ = editDecisionMessageClearingInlineKeyboard(ctx, a.sender, req.SessionKey.ChatID, result.Delivery.MessageID, text)
	}
	return toolpkg.DurableSnapshotRestoreApprovalDecision{Approved: false, TimedOut: result.TimedOut}, nil
}

func approvedDecisionConfirmationText(label string, decisionID string, kind decision.Kind, details string) string {
	if kind == decision.KindProposalApproval {
		pending := decision.PendingDecision{Request: decision.Request{Kind: kind, Details: details}}
		if summary := strings.TrimSpace(summarizePendingDecision(pending)); summary != "" {
			return approvedProposalConfirmationSummary(summary)
		}
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Approval"
	}
	lines := []string{label + " approved."}
	if id := strings.TrimSpace(decisionID); id != "" {
		lines = append(lines, "Decision: "+id)
	}
	pending := decision.PendingDecision{Request: decision.Request{Kind: kind, Details: details}}
	if summary := strings.TrimSpace(summarizePendingDecision(pending)); summary != "" {
		lines = append(lines, "", summary)
	} else if compact := compactSentence(details); compact != "" {
		lines = append(lines, "", compact)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func approvedProposalConfirmationSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "Approved."
	}
	lower := strings.ToLower(summary)
	for _, prefix := range []string{"i’d like to ", "i'd like to "} {
		if strings.HasPrefix(lower, prefix) {
			return "Approved — I’ll " + strings.TrimSpace(summary[len(prefix):])
		}
	}
	if strings.HasPrefix(lower, "high-risk approval:") {
		return "Approved — high-risk: " + strings.TrimSpace(summary[len("High-risk approval:"):])
	}
	return "Approved — " + summary
}

func approvedDecisionConfirmationRows(decisionID string, details string) [][]telegram.InlineButton {
	return approvedDecisionConfirmationRowsExpanded(decisionID, details, false)
}

func approvedDecisionConfirmationRowsExpanded(decisionID string, details string, expanded bool) [][]telegram.InlineButton {
	decisionID = strings.TrimSpace(decisionID)
	if decisionID == "" || strings.TrimSpace(details) == "" {
		return nil
	}
	label := "Expand details"
	action := "expand"
	if expanded {
		label = "Hide details"
		action = "collapse"
	}
	return [][]telegram.InlineButton{{
		{
			Text:         label,
			CallbackData: decision.EncodeCallbackData(decisionID, action),
		},
	}}
}

func editApprovedDecisionConfirmation(ctx context.Context, sender telegramDecisionSender, chatID int64, messageID int64, label string, decisionID string, kind decision.Kind, details string) {
	if sender == nil || chatID == 0 || messageID == 0 {
		return
	}
	text := approvedDecisionConfirmationText(label, decisionID, kind, details)
	if rows := approvedDecisionConfirmationRows(decisionID, details); len(rows) > 0 {
		if editor, ok := sender.(telegramDecisionKeyboardEditor); ok {
			if err := editor.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err == nil {
				return
			}
		}
	}
	_ = editDecisionMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text)
}

func formatExecProposalDetails(req toolpkg.ExecApprovalRequest) string {
	lines := make([]string, 0, 8)
	if summary := strings.TrimSpace(req.Proposal.Summary); summary != "" {
		lines = append(lines, summary)
	}
	if kind := strings.TrimSpace(req.Proposal.Kind); kind != "" {
		lines = append(lines, fmt.Sprintf("Kind: %s", kind))
	}
	if whyNow := strings.TrimSpace(req.Proposal.WhyNow); whyNow != "" {
		lines = append(lines, "", "Why now:", whyNow)
	}
	if bounded := strings.TrimSpace(req.Proposal.BoundedEffect); bounded != "" {
		lines = append(lines, "", "If approved:", bounded)
	}
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		lines = append(lines, "", "Trigger:", reason)
	}
	if command := strings.TrimSpace(req.Command); command != "" {
		lines = append(lines, "", "Command:", command)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatDurableMemoryDelegationDetails(req toolpkg.DurableMemoryDelegationApprovalRequest) string {
	lines := make([]string, 0, 12)
	lines = append(lines, "Memory delegation request.")
	if agentID := strings.TrimSpace(req.Agent.AgentID); agentID != "" {
		lines = append(lines, "", "Agent:", agentID)
	}
	if channel := strings.TrimSpace(req.Agent.ChannelKind); channel != "" {
		lines = append(lines, "Channel:", channel)
	}
	if charter := strings.TrimSpace(req.Agent.LivePolicy.Charter); charter != "" {
		lines = append(lines, "", "Charter:", charter)
	}
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		lines = append(lines, "", "Why now:", reason)
	}
	if len(req.Entries) > 0 {
		lines = append(lines, "", "Items:")
		for i, entry := range req.Entries {
			source := strings.TrimSpace(entry.SourceStore)
			if source == "" {
				source = "-"
			}
			target := strings.TrimSpace(entry.TargetStore)
			if target == "" {
				target = "-"
			}
			ref := strings.TrimSpace(entry.CandidateID)
			if ref == "" {
				ref = "-"
			}
			lines = append(lines, fmt.Sprintf("%d. candidate=%s source=%s target=%s", i+1, ref, source, target))
			lines = append(lines, "   "+truncateDecisionSummaryText(strings.TrimSpace(entry.Content), 220))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatDurableSnapshotRestoreDetails(req toolpkg.DurableSnapshotRestoreApprovalRequest) string {
	lines := make([]string, 0, 12)
	lines = append(lines, "Durable child snapshot restore request.")
	if agentID := strings.TrimSpace(req.Agent.AgentID); agentID != "" {
		lines = append(lines, "", "Agent:", agentID)
	}
	if snapshotID := strings.TrimSpace(req.SnapshotID); snapshotID != "" {
		lines = append(lines, "Snapshot:", snapshotID)
	}
	if channel := strings.TrimSpace(req.Agent.ChannelKind); channel != "" {
		lines = append(lines, "Channel:", channel)
	}
	if !req.SnapshotCreatedAt.IsZero() {
		lines = append(lines, "Created At:", req.SnapshotCreatedAt.UTC().Format(time.RFC3339))
	}
	if reason := strings.TrimSpace(req.SnapshotReason); reason != "" {
		lines = append(lines, "", "Reason:", reason)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (h *telegramDecisionHandler) HandleArtifactRetentionMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	if h == nil || h.sender == nil || h.router == nil || h.broker == nil {
		return false, nil
	}
	if hasOnlyAudioArtifactRetentionCandidates(msg) {
		return h.handleAudioArtifactRetentionMessage(ctx, msg)
	}
	if !hasArtifactRetentionCandidates(msg) {
		return false, nil
	}
	ownerKey := decision.OwnerKey(msg.ChatID, msg.SenderID)
	if ownerKey == "" {
		return false, fmt.Errorf("artifact retention owner key is required")
	}
	if h.store == nil {
		result, err := h.broker.Request(ctx, decision.Request{
			Kind:          decision.KindArtifactRetention,
			ChatID:        msg.ChatID,
			SenderID:      msg.SenderID,
			MessageID:     msg.MessageID,
			Prompt:        "How should I retain this inbound file?",
			Details:       formatArtifactRetentionDetails(msg),
			Choices:       []decision.Choice{{ID: "turn", Label: "This turn only"}, {ID: "session", Label: "Keep for session"}, {ID: "local", Label: "Save locally"}},
			DefaultChoice: "session",
			Timeout:       h.artifactRetentionTimeout,
		})
		if err != nil {
			return true, err
		}
		updated := applyArtifactRetentionChoice(msg, result.Choice)
		if result.Delivery.MessageID != 0 {
			_ = editDecisionMessageClearingInlineKeyboard(ctx, h.sender, msg.ChatID, result.Delivery.MessageID, artifactRetentionResolutionText(result))
		}
		h.router.Route(ctx, updated)
		return true, nil
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return true, fmt.Errorf("marshal pending artifact retention message: %w", err)
	}
	if err := h.store.UpsertPendingArtifactRetention(session.PendingArtifactRetentionRecord{
		OwnerKey:           ownerKey,
		ChatID:             msg.ChatID,
		SenderID:           msg.SenderID,
		InboundMessageJSON: string(raw),
	}); err != nil {
		return true, err
	}

	go h.awaitArtifactRetentionDecision(context.Background(), ownerKey, decision.Request{
		Kind:          decision.KindArtifactRetention,
		ChatID:        msg.ChatID,
		SenderID:      msg.SenderID,
		MessageID:     msg.MessageID,
		Prompt:        "How should I retain this inbound file?",
		Details:       formatArtifactRetentionDetails(msg),
		Choices:       []decision.Choice{{ID: "turn", Label: "This turn only"}, {ID: "session", Label: "Keep for session"}, {ID: "local", Label: "Save locally"}},
		DefaultChoice: "session",
		Timeout:       h.artifactRetentionTimeout,
	})
	return true, nil
}

func (h *telegramDecisionHandler) awaitArtifactRetentionDecision(ctx context.Context, ownerKey string, req decision.Request) {
	result, err := h.broker.Request(ctx, req)
	if err != nil {
		if h.store != nil {
			_ = h.store.DeletePendingArtifactRetention(ownerKey)
		}
		return
	}
	if err := h.resumePendingArtifactRetention(ctx, ownerKey, result); err != nil {
		if h.store != nil {
			_ = h.store.DeletePendingArtifactRetention(ownerKey)
		}
	}
}

func (h *telegramDecisionHandler) resumePendingArtifactRetention(ctx context.Context, ownerKey string, result decision.Result) error {
	if h == nil || h.router == nil {
		return nil
	}
	if h.store == nil {
		return nil
	}
	record, err := h.store.PendingArtifactRetention(ownerKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	defer func() { _ = h.store.DeletePendingArtifactRetention(ownerKey) }()
	var msg core.InboundMessage
	if err := json.Unmarshal([]byte(record.InboundMessageJSON), &msg); err != nil {
		return fmt.Errorf("decode pending artifact retention message: %w", err)
	}
	updated := applyArtifactRetentionChoice(msg, result.Choice)
	if result.Delivery.MessageID != 0 && h.sender != nil {
		_ = editDecisionMessageClearingInlineKeyboard(ctx, h.sender, msg.ChatID, result.Delivery.MessageID, artifactRetentionResolutionText(result))
	}
	h.router.Route(ctx, updated)
	return nil
}

func (h *telegramDecisionHandler) handleAudioArtifactRetentionMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	updated := applyArtifactRetentionChoice(msg, "session")
	storedForKeep := false
	if h.store != nil {
		if raw, err := json.Marshal(msg); err == nil {
			err = h.store.UpsertPendingArtifactRetention(session.PendingArtifactRetentionRecord{
				OwnerKey:           decision.OwnerKey(msg.ChatID, msg.SenderID),
				ChatID:             msg.ChatID,
				SenderID:           msg.SenderID,
				InboundMessageJSON: string(raw),
			})
			storedForKeep = err == nil
		}
	}
	if storedForKeep && h.audioRetentionKeeper != nil {
		_ = h.sendAudioRetentionOffer(ctx, msg)
	}
	h.router.Route(ctx, updated)
	return true, nil
}

func (h *telegramDecisionHandler) sendAudioRetentionOffer(ctx context.Context, msg core.InboundMessage) error {
	if h == nil || h.sender == nil {
		return nil
	}
	text := "Audio is available while we work with it. I won't keep it permanently unless you ask."
	rows := [][]telegram.InlineButton{{{
		Text:         "Keep audio permanently",
		CallbackData: encodeAudioKeepCallbackData(msg.MessageID),
	}}}
	_, err := h.sender.SendInlineKeyboard(ctx, msg.ChatID, text, rows, replyToMessageID(msg.MessageID))
	return err
}

func hasOnlyAudioArtifactRetentionCandidates(msg core.InboundMessage) bool {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return false
	}
	seenAudio := false
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if strings.TrimSpace(artifact.Channel) != "telegram" {
			continue
		}
		if strings.TrimSpace(artifact.RemoteID) == "" && len(artifact.Data) == 0 {
			continue
		}
		if artifact.Kind == "structured" {
			continue
		}
		if strings.TrimSpace(artifact.Metadata["aphelion_retention_choice"]) != "" {
			continue
		}
		if artifact.Kind != "audio" {
			return false
		}
		seenAudio = true
	}
	return seenAudio
}

func hasArtifactRetentionCandidates(msg core.InboundMessage) bool {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return false
	}
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if strings.TrimSpace(artifact.Channel) != "telegram" {
			continue
		}
		if strings.TrimSpace(artifact.RemoteID) == "" && len(artifact.Data) == 0 {
			continue
		}
		if artifact.Kind == "structured" {
			continue
		}
		if strings.TrimSpace(artifact.Metadata["aphelion_retention_choice"]) != "" {
			continue
		}
		return true
	}
	return false
}

func formatArtifactRetentionDetails(msg core.InboundMessage) string {
	items := make([]string, 0, len(msg.Artifacts))
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if strings.TrimSpace(artifact.Channel) != "telegram" || artifact.Kind == "structured" {
			continue
		}
		label := strings.TrimSpace(artifact.Filename)
		if label == "" {
			label = strings.TrimSpace(artifact.Kind)
			if label == "" {
				label = strings.TrimSpace(artifact.SourceType)
			}
			if label == "" {
				label = "artifact"
			}
		}
		items = append(items, "- "+label)
	}
	if len(items) == 0 {
		return "Choose how long I should keep the inbound artifact after processing."
	}
	return strings.Join([]string{
		"Choose how long I should keep this inbound artifact after processing.",
		"",
		"Artifacts:",
		strings.Join(items, "\n"),
	}, "\n")
}

func applyArtifactRetentionChoice(msg core.InboundMessage, choice string) core.InboundMessage {
	choice = strings.TrimSpace(choice)
	out := msg
	out.Artifacts = make([]core.Artifact, 0, len(msg.Artifacts))
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if strings.TrimSpace(artifact.Channel) == "telegram" && artifact.Kind != "structured" && (strings.TrimSpace(artifact.RemoteID) != "" || len(artifact.Data) > 0) {
			if artifact.Metadata == nil {
				artifact.Metadata = map[string]string{}
			}
			artifact.Metadata["aphelion_retention_choice"] = choice
			switch choice {
			case "turn":
				artifact.DefaultRetention = "ephemeral"
				artifact.Metadata["aphelion_materialize"] = "memory_only"
			case "local":
				artifact.DefaultRetention = "child_local"
				artifact.RetentionCeiling = "child_local"
				artifact.Metadata["aphelion_materialize"] = "local"
			default:
				artifact.DefaultRetention = "session_reference"
				artifact.Metadata["aphelion_materialize"] = "local"
			}
		}
		out.Artifacts = append(out.Artifacts, core.NormalizeArtifact(artifact))
	}
	return out
}

func artifactRetentionResolutionText(result decision.Result) string {
	if result.TimedOut {
		return "Keeping the file for this session by default."
	}
	switch strings.TrimSpace(result.Choice) {
	case "turn":
		return "Got it — I’ll use the file for this turn only."
	case "local":
		return "Got it — I’ll save the file locally for longer work."
	default:
		return "Got it — I’ll keep the file for this session."
	}
}

func (h *telegramDecisionHandler) HandleBusyMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	if h == nil || h.sender == nil || h.router == nil || h.broker == nil {
		return false, nil
	}
	status := h.router.Status(msg.ChatID)
	if scoped, ok := h.router.(telegramDecisionMessageStatusRouter); ok {
		status = scoped.StatusForMessage(msg)
	}
	if !status.Active {
		return false, nil
	}

	req := decision.Request{
		ChatID:        msg.ChatID,
		SenderID:      msg.SenderID,
		MessageID:     msg.MessageID,
		Choices:       []decision.Choice{{ID: "stop", Label: stopChoiceLabel(msg.Text)}, {ID: "queue", Label: queueChoiceLabel(msg.Text)}},
		DefaultChoice: "queue",
	}
	if isStopWord(msg.Text) {
		req.Kind = decision.KindStopWord
		req.Prompt = "Stop the current task?"
		req.Timeout = h.stopWordTimeout
	} else {
		req.Kind = decision.KindInterrupt
		req.Prompt = "I'm still working on the previous request. What would you like to do?"
		req.Timeout = h.interruptTimeout
	}
	ownerKey := decision.OwnerKey(msg.ChatID, msg.SenderID)
	if ownerKey == "" {
		return false, fmt.Errorf("busy decision owner key is required")
	}
	if h.store == nil {
		result, err := h.broker.Request(ctx, req)
		if err != nil {
			return true, err
		}
		h.applyBusyDecisionResult(ctx, msg, result)
		return true, nil
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return true, fmt.Errorf("marshal pending busy message: %w", err)
	}
	if err := h.store.UpsertPendingBusyDecision(session.PendingBusyDecisionRecord{
		OwnerKey:           ownerKey,
		ChatID:             msg.ChatID,
		SenderID:           msg.SenderID,
		InboundMessageJSON: string(raw),
	}); err != nil {
		return true, err
	}
	go h.awaitBusyDecision(context.Background(), ownerKey, req)
	return true, nil
}

func (h *telegramDecisionHandler) awaitBusyDecision(ctx context.Context, ownerKey string, req decision.Request) {
	result, err := h.broker.Request(ctx, req)
	if err != nil {
		if h.store != nil {
			_ = h.store.DeletePendingBusyDecision(ownerKey)
		}
		return
	}
	if err := h.resumePendingBusyDecision(ctx, ownerKey, result); err != nil {
		if h.store != nil {
			_ = h.store.DeletePendingBusyDecision(ownerKey)
		}
	}
}

func (h *telegramDecisionHandler) resumePendingBusyDecision(ctx context.Context, ownerKey string, result decision.Result) error {
	if h == nil || h.router == nil || h.store == nil {
		return nil
	}
	record, err := h.store.PendingBusyDecision(ownerKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	defer func() { _ = h.store.DeletePendingBusyDecision(ownerKey) }()
	var msg core.InboundMessage
	if err := json.Unmarshal([]byte(record.InboundMessageJSON), &msg); err != nil {
		return fmt.Errorf("decode pending busy message: %w", err)
	}
	h.applyBusyDecisionResult(ctx, msg, result)
	return nil
}

func (h *telegramDecisionHandler) applyBusyDecisionResult(ctx context.Context, msg core.InboundMessage, result decision.Result) {
	switch result.Choice {
	case "stop":
		if result.Delivery.MessageID != 0 && h.sender != nil {
			_ = h.sender.DeleteMessage(ctx, msg.ChatID, result.Delivery.MessageID)
		}
		if scoped, ok := h.router.(telegramDecisionMessageStopRouter); ok {
			scoped.StopForMessage(msg)
		} else {
			h.router.Stop(msg.ChatID)
		}
		if !isOnlyStopWord(msg.Text) {
			h.router.Route(ctx, msg)
		}
	case "queue":
		if result.Delivery.MessageID != 0 && h.sender != nil {
			text := "Got it — I'll process your message next. ⏳"
			if result.TimedOut {
				text = "Queued your message — processing after current task."
			}
			_ = editDecisionMessageClearingInlineKeyboard(ctx, h.sender, msg.ChatID, result.Delivery.MessageID, text)
		}
		h.router.Route(ctx, msg)
	}
}

func (h *telegramDecisionHandler) HandleCallbackQuery(ctx context.Context, cb telegram.CallbackQuery) error {
	if h == nil || h.sender == nil || h.broker == nil {
		return nil
	}
	if eventID, action, ok := core.DecodeReviewEventCallbackData(cb.Data); ok {
		return h.handleReviewEventCallback(ctx, cb, eventID, action)
	}
	if messageID, ok := decodeAudioKeepCallbackData(cb.Data); ok {
		return h.handleAudioKeepCallback(ctx, cb, messageID)
	}
	id, choice, ok := decision.DecodeCallbackData(cb.Data)
	if !ok {
		if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return err
		}
		return nil
	}
	if choice == "expand" || choice == "collapse" {
		pending, found := h.broker.Peek(id)
		resolved := false
		if !found {
			pending, found = h.broker.PeekResolved(id)
			resolved = found
		}
		if !found {
			if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, "This approval is no longer active. Use the newest prompt."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return err
			}
			return nil
		}
		chatID := int64(0)
		messageID := int64(0)
		if cb.Message != nil {
			messageID = cb.Message.MessageID
			if cb.Message.Chat != nil {
				chatID = cb.Message.Chat.ID
			}
		}
		if chatID == 0 {
			chatID = pending.ChatID
		}
		if messageID != 0 {
			expanded := choice == "expand"
			text := renderPendingDecisionSummary(pending)
			rows := inlineButtonRowsExpanded(pending, expanded)
			if expanded {
				text = renderPendingDecisionExpanded(pending)
			}
			if resolved {
				text = approvedDecisionConfirmationText(approvedDecisionConfirmationLabel(pending.Kind), pending.ID, pending.Kind, pending.Details)
				rows = approvedDecisionConfirmationRowsExpanded(pending.ID, pending.Details, expanded)
				if expanded {
					text = renderPendingDecisionExpanded(pending)
				}
			}
			if editor, ok := h.sender.(telegramDecisionKeyboardEditor); ok && len(rows) > 0 {
				if err := editor.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err != nil {
					return err
				}
			} else if err := editDecisionMessageClearingInlineKeyboard(ctx, h.sender, chatID, messageID, text); err != nil {
				return err
			}
		}
		if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return err
		}
		return nil
	}
	answerText := ""
	if !h.broker.Resolve(id, choice) {
		answerText = "This approval is no longer active. Use the newest prompt."
	}
	if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, answerText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return err
	}
	return nil
}

func encodeAudioKeepCallbackData(messageID int64) string {
	return "audio_keep:" + strconv.FormatInt(messageID, 10)
}

func decodeAudioKeepCallbackData(data string) (int64, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, "audio_keep:") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, "audio_keep:")), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *telegramDecisionHandler) handleAudioKeepCallback(ctx context.Context, cb telegram.CallbackQuery, sourceMessageID int64) error {
	chatID := callbackChatID(cb)
	senderID := callbackSenderID(cb)
	if chatID == 0 || senderID == 0 || h == nil || h.store == nil || h.audioRetentionKeeper == nil {
		return h.answerAudioKeepCallback(ctx, cb, "I can't save that audio from this prompt.")
	}
	record, err := h.store.PendingArtifactRetention(decision.OwnerKey(chatID, senderID))
	if err != nil {
		if err == sql.ErrNoRows {
			return h.answerAudioKeepCallback(ctx, cb, "That audio is no longer available to save from this button.")
		}
		return err
	}
	var msg core.InboundMessage
	if err := json.Unmarshal([]byte(record.InboundMessageJSON), &msg); err != nil {
		return fmt.Errorf("decode pending audio retention message: %w", err)
	}
	if sourceMessageID != 0 && msg.MessageID != 0 && msg.MessageID != sourceMessageID {
		return h.answerAudioKeepCallback(ctx, cb, "That audio button is stale.")
	}
	if err := h.audioRetentionKeeper.KeepAudioArtifactsPermanently(ctx, msg); err != nil {
		return h.answerAudioKeepCallback(ctx, cb, "I couldn't save that audio permanently.")
	}
	_ = h.store.DeletePendingArtifactRetention(decision.OwnerKey(chatID, senderID))
	if cb.Message != nil && cb.Message.MessageID != 0 {
		_ = editDecisionMessageClearingInlineKeyboard(ctx, h.sender, chatID, cb.Message.MessageID, "Audio saved permanently.")
	}
	return h.answerAudioKeepCallback(ctx, cb, "Saved.")
}

func (h *telegramDecisionHandler) answerAudioKeepCallback(ctx context.Context, cb telegram.CallbackQuery, text string) error {
	if h == nil || h.sender == nil {
		return nil
	}
	if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return err
	}
	return nil
}

func callbackChatID(cb telegram.CallbackQuery) int64 {
	if cb.Message != nil && cb.Message.Chat != nil {
		return cb.Message.Chat.ID
	}
	return 0
}

func callbackSenderID(cb telegram.CallbackQuery) int64 {
	if cb.From != nil {
		return cb.From.ID
	}
	return 0
}

func (h *telegramDecisionHandler) handleReviewEventCallback(ctx context.Context, cb telegram.CallbackQuery, eventID int64, action core.ReviewEventAction) error {
	if h == nil || h.sender == nil || h.store == nil {
		return nil
	}
	event, err := h.store.ReviewEventByID(eventID)
	if err != nil {
		return err
	}
	if event == nil {
		return h.answerReviewEventCallback(ctx, cb, "This review item is no longer available.")
	}
	if action == core.ReviewEventActionExpand || action == core.ReviewEventActionHide {
		return h.handleReviewEventDetailToggle(ctx, cb, *event, action == core.ReviewEventActionExpand)
	}
	if proposal, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		if reviewEventCallbackExpired(*event, time.Now()) {
			_ = h.editReviewEventCallbackMessage(ctx, cb, "Mission Control proposal timed out — use a fresh prompt.")
			return h.answerReviewEventCallback(ctx, cb, "Proposal timed out. Use a fresh prompt.")
		}
		return h.handleMissionControlProposalCallback(ctx, cb, *event, proposal, action)
	}
	if reviewEventCallbackExpired(*event, time.Now()) {
		_ = h.editReviewEventCallbackMessage(ctx, cb, "Approval timed out — use a fresh prompt.")
		return h.answerReviewEventCallback(ctx, cb, "Approval timed out. Use a fresh prompt.")
	}
	requestID := reviewEventCallbackCapabilityRequestID(*event)
	if requestID == "" {
		return h.answerReviewEventCallback(ctx, cb, "This review item is not actionable yet.")
	}
	record, ok, err := h.store.CapabilityRequest(requestID)
	if err != nil {
		return err
	}
	if !ok {
		return h.answerReviewEventCallback(ctx, cb, "Capability request not found.")
	}
	if !reviewEventRequestStillActionable(record, action) {
		return h.answerReviewEventCallback(ctx, cb, "This approval is no longer active. Use the newest prompt.")
	}
	fromID := int64(0)
	if cb.From != nil {
		fromID = cb.From.ID
	}
	status, reviewerRole, err := reviewEventCapabilityStatusForAction(record, action, fromID, event.TargetAdminChatID)
	if err != nil {
		_ = h.answerReviewEventCallback(ctx, cb, err.Error())
		return nil
	}
	review, err := h.store.AppendCapabilityReview(session.CapabilityReview{
		ReviewID:     fmt.Sprintf("capr-review-event-%d-%d", event.ID, time.Now().UnixNano()),
		RequestID:    record.RequestID,
		Reviewer:     fmt.Sprintf("telegram:%d", fromID),
		ReviewerRole: reviewerRole,
		Status:       status,
		Rationale:    fmt.Sprintf("telegram inline review event %d", event.ID),
	})
	if err != nil {
		return err
	}
	label := "approved"
	if review.Status == session.CapabilityReviewStatusParentApproved {
		label = "parent-approved"
	} else if review.Status == session.CapabilityReviewStatusRejected {
		label = "rejected"
	}
	_ = h.editReviewEventCallbackMessage(ctx, cb, reviewEventConfirmationText(label, record, *event))
	return h.answerReviewEventCallback(ctx, cb, "")
}

func (h *telegramDecisionHandler) handleMissionControlProposalCallback(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent, proposal core.MissionControlProposal, action core.ReviewEventAction) error {
	if h == nil || h.store == nil {
		return nil
	}
	fromID := int64(0)
	if cb.From != nil {
		fromID = cb.From.ID
	}
	if fromID <= 0 || (event.TargetAdminChatID > 0 && fromID != event.TargetAdminChatID) {
		return h.answerReviewEventCallback(ctx, cb, "Only the target admin can review this Mission Control proposal.")
	}
	proposal = core.NormalizeMissionControlProposal(proposal)
	switch action {
	case core.ReviewEventActionMissionAdd:
		missionID := strings.TrimSpace(proposal.MissionID)
		if missionID == "" {
			missionID = fmt.Sprintf("mission-proposal-%d", event.ID)
		}
		owner := strings.TrimSpace(proposal.Owner)
		if owner == "" {
			owner = fmt.Sprintf("telegram:%d", fromID)
		}
		refs := append([]string(nil), proposal.SourceRefs...)
		refs = append(refs, fmt.Sprintf("review_event:%d", event.ID))
		mission, err := h.store.UpsertMission(session.MissionState{
			ID:                missionID,
			Title:             proposal.Title,
			Objective:         proposal.Objective,
			Origin:            firstTelegramDecisionNonEmpty(proposal.Origin, "proposed"),
			Scope:             firstTelegramDecisionNonEmpty(proposal.Scope, "principal"),
			Owner:             owner,
			Status:            session.MissionStatusCandidate,
			Pinned:            false,
			Tags:              proposal.Tags,
			SourceRefs:        refs,
			SuccessCriteria:   proposal.SuccessCriteria,
			NextAllowedAction: proposal.NextAllowedAction,
			Authority:         session.DefaultMissionAuthority(),
			Decay:             session.DefaultMissionDecay(),
		}, fmt.Sprintf("telegram:%d", fromID), "Mission Control proposal approved; candidate mission added")
		if err != nil {
			return err
		}
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("added", mission, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	case core.ReviewEventActionMissionAskEdit:
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("ask_edit", session.MissionState{}, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	case core.ReviewEventActionMissionPark:
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("parked", session.MissionState{}, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	case core.ReviewEventActionMissionReject:
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("rejected", session.MissionState{}, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	default:
		return h.answerReviewEventCallback(ctx, cb, "This Mission Control proposal action is not available.")
	}
}

func renderMissionControlProposalCallbackResult(status string, mission session.MissionState, proposal core.MissionControlProposal) string {
	proposal = core.NormalizeMissionControlProposal(proposal)
	title := strings.TrimSpace(mission.Title)
	if title == "" {
		title = strings.TrimSpace(proposal.Title)
	}
	if title == "" {
		title = strings.TrimSpace(proposal.Objective)
	}
	switch strings.TrimSpace(status) {
	case "added":
		lines := []string{"Mission Control proposal added."}
		if mission.ID != "" {
			lines = append(lines, "Mission: "+mission.ID)
		}
		if title != "" {
			lines = append(lines, "Title: "+title)
		}
		lines = append(lines, "Status: candidate", "No execution or self-continuation authority was granted.")
		return strings.Join(lines, "\n")
	case "ask_edit":
		return "Mission Control proposal needs edits. I will revise it before asking again. No mission was created."
	case "parked":
		return "Mission Control proposal parked. No mission was created and no execution authority was granted."
	case "rejected":
		return "Mission Control proposal rejected. No mission was created."
	default:
		return "Mission Control proposal reviewed."
	}
}

func firstTelegramDecisionNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (h *telegramDecisionHandler) handleReviewEventDetailToggle(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent, expanded bool) error {
	if !runtimepkg.ReviewEventDetailsExpandable(event) {
		return h.answerReviewEventCallback(ctx, cb, "This review item has no expandable details.")
	}
	if h == nil || h.sender == nil || cb.Message == nil || cb.Message.Chat == nil || cb.Message.MessageID == 0 {
		return h.answerReviewEventCallback(ctx, cb, "")
	}
	text := runtimepkg.FormatReviewEventCompactMessage(event)
	if expanded {
		text = runtimepkg.FormatReviewEventDetailsMessage(event)
	}
	rows := runtimepkg.ReviewEventInlineRowsExpanded(event, expanded)
	if editor, ok := h.sender.(telegramDecisionKeyboardEditor); ok && len(rows) > 0 {
		if err := editor.EditMessageTextWithInlineKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, "", rows); err != nil {
			return err
		}
	} else if err := editDecisionMessageClearingInlineKeyboard(ctx, h.sender, cb.Message.Chat.ID, cb.Message.MessageID, text); err != nil {
		return err
	}
	return h.answerReviewEventCallback(ctx, cb, "")
}

func reviewEventConfirmationText(label string, record session.CapabilityRequest, event session.ReviewEvent) string {
	record = session.NormalizeCapabilityRequest(record)
	label = strings.TrimSpace(label)
	if label == "" {
		label = "reviewed"
	}
	lines := []string{"Capability request " + label + "."}
	if record.RequestID != "" {
		lines = append(lines, "Request: "+record.RequestID)
	}
	if event.ID > 0 {
		lines = append(lines, fmt.Sprintf("Review event: %d", event.ID))
	}
	meta := make([]string, 0, 3)
	if record.Kind != "" {
		meta = append(meta, "Kind: "+string(record.Kind))
	}
	if target := strings.TrimSpace(record.TargetResource); target != "" {
		meta = append(meta, "Target: "+target)
	}
	if risk := strings.TrimSpace(record.RiskClass); risk != "" {
		meta = append(meta, "Risk: "+risk)
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, " · "))
	}
	if purpose := strings.TrimSpace(record.Purpose); purpose != "" {
		lines = append(lines, "Purpose: "+compactSentence(purpose))
	}
	if summary := strings.TrimSpace(event.Summary); summary != "" {
		lines = append(lines, "", "Approved content:", summary)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (h *telegramDecisionHandler) answerReviewEventCallback(ctx context.Context, cb telegram.CallbackQuery, text string) error {
	if h == nil || h.sender == nil {
		return nil
	}
	if err := h.sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return err
	}
	return nil
}

func (h *telegramDecisionHandler) editReviewEventCallbackMessage(ctx context.Context, cb telegram.CallbackQuery, text string) error {
	if h == nil || h.sender == nil || cb.Message == nil || cb.Message.Chat == nil || cb.Message.MessageID == 0 {
		return nil
	}
	return editDecisionMessageClearingInlineKeyboard(ctx, h.sender, cb.Message.Chat.ID, cb.Message.MessageID, text)
}

func reviewEventCallbackExpired(event session.ReviewEvent, now time.Time) bool {
	start := event.DeliveredAt
	if start.IsZero() {
		start = event.CreatedAt
	}
	if start.IsZero() {
		return false
	}
	return now.After(start.Add(defaultUserApprovalTimeout))
}

func reviewEventCallbackCapabilityRequestID(event session.ReviewEvent) string {
	if id := reviewEventCallbackMetadataString(event, "request_id"); id != "" {
		return id
	}
	return reviewEventCallbackMetadataString(event, "capability_request_id")
}

func reviewEventCallbackMetadataString(event session.ReviewEvent, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || strings.TrimSpace(event.MetadataJSON) == "" {
		return ""
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(event.MetadataJSON), &metadata); err != nil {
		return ""
	}
	if value, ok := metadata[key]; ok {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return ""
}

func reviewEventRequestStillActionable(record session.CapabilityRequest, action core.ReviewEventAction) bool {
	record = session.NormalizeCapabilityRequest(record)
	switch action {
	case core.ReviewEventActionParentApprove:
		return record.ReviewStatus == session.CapabilityReviewStatusProposed
	case core.ReviewEventActionApprove:
		if strings.TrimSpace(record.ParentPrincipal) != "" {
			return record.ReviewStatus == session.CapabilityReviewStatusParentApproved
		}
		return record.ReviewStatus == session.CapabilityReviewStatusProposed
	case core.ReviewEventActionReject:
		return record.ReviewStatus == session.CapabilityReviewStatusProposed || record.ReviewStatus == session.CapabilityReviewStatusParentApproved
	default:
		return false
	}
}

func reviewEventCapabilityStatusForAction(record session.CapabilityRequest, action core.ReviewEventAction, fromID int64, targetChatID int64) (session.CapabilityReviewStatus, string, error) {
	if fromID <= 0 {
		return "", "", fmt.Errorf("Telegram reviewer is unknown.")
	}
	isAdmin := telegramPrincipalMatches(record.AdminPrincipal, fromID) || (strings.TrimSpace(record.AdminPrincipal) == "" && targetChatID == fromID)
	isParent := telegramPrincipalMatches(record.ParentPrincipal, fromID)
	switch action {
	case core.ReviewEventActionParentApprove:
		if strings.TrimSpace(record.ParentPrincipal) == "" {
			return "", "", fmt.Errorf("This request has no parent approval step.")
		}
		if !isParent && !isAdmin {
			return "", "", fmt.Errorf("Only the parent or admin can parent-approve this request.")
		}
		return session.CapabilityReviewStatusParentApproved, reviewerRoleForReview(isAdmin && !isParent), nil
	case core.ReviewEventActionApprove:
		if strings.TrimSpace(record.ParentPrincipal) != "" && record.ReviewStatus == session.CapabilityReviewStatusProposed {
			return "", "", fmt.Errorf("Parent approval is required first.")
		}
		if !isAdmin {
			return "", "", fmt.Errorf("Only the admin can approve this request.")
		}
		return session.CapabilityReviewStatusApproved, string(principal.RoleAdmin), nil
	case core.ReviewEventActionReject:
		if !isAdmin && !isParent {
			return "", "", fmt.Errorf("Only the admin or parent can reject this request.")
		}
		return session.CapabilityReviewStatusRejected, reviewerRoleForReview(isAdmin), nil
	default:
		return "", "", fmt.Errorf("Unknown review action.")
	}
}

func reviewerRoleForReview(admin bool) string {
	if admin {
		return string(principal.RoleAdmin)
	}
	return string(principal.RoleApprovedUser)
}

func approvedDecisionConfirmationLabel(kind decision.Kind) string {
	switch kind {
	case decision.KindProposalApproval:
		return "Proposal"
	case decision.KindMemoryDelegation:
		return "Memory delegation"
	case decision.KindSnapshotRestore:
		return "Snapshot restore"
	case decision.KindArtifactRetention:
		return "Artifact retention"
	default:
		return "Approval"
	}
}

func telegramPrincipalMatches(target string, userID int64) bool {
	return userID > 0 && strings.TrimSpace(target) == fmt.Sprintf("telegram:%d", userID)
}

func inlineButtonRows(pending decision.PendingDecision) [][]telegram.InlineButton {
	return inlineButtonRowsExpanded(pending, false)
}

func inlineButtonRowsExpanded(pending decision.PendingDecision, expanded bool) [][]telegram.InlineButton {
	if len(pending.Choices) == 0 {
		return nil
	}
	rows := make([][]telegram.InlineButton, 0, 2)
	if strings.TrimSpace(pending.Details) != "" {
		label := "Expand details"
		action := "expand"
		if expanded {
			label = "Hide details"
			action = "collapse"
		}
		rows = append(rows, []telegram.InlineButton{{
			Text:         label,
			CallbackData: decision.EncodeCallbackData(pending.ID, action),
		}})
	}
	row := make([]telegram.InlineButton, 0, len(pending.Choices))
	for _, choice := range orderedDecisionChoices(pending.Choices) {
		row = append(row, telegram.InlineButton{
			Text:         strings.TrimSpace(choice.Label),
			CallbackData: decision.EncodeCallbackData(pending.ID, choice.ID),
		})
	}
	rows = append(rows, row)
	return rows
}

func orderedDecisionChoices(choices []decision.Choice) []decision.Choice {
	out := append([]decision.Choice(nil), choices...)
	if len(out) != 2 {
		return out
	}
	leftID := strings.ToLower(strings.TrimSpace(out[0].ID))
	rightID := strings.ToLower(strings.TrimSpace(out[1].ID))
	if isAffirmativeChoiceID(leftID) && isNegativeChoiceID(rightID) {
		out[0], out[1] = out[1], out[0]
	}
	return out
}

func isNegativeChoiceID(id string) bool {
	switch id {
	case "deny", "stop", "cancel", "reject", "abort":
		return true
	default:
		return false
	}
}

func isAffirmativeChoiceID(id string) bool {
	switch id {
	case "approve", "continue", "queue", "allow", "accept", "yes":
		return true
	default:
		return false
	}
}

var stopPatterns = []string{
	"wait",
	"stop",
	"cancel",
	"nevermind",
	"nvm",
	"hold on",
	"abort",
	"halt",
}

func isStopWord(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, p := range stopPatterns {
		if lower == p || strings.HasPrefix(lower, p+" ") || strings.HasPrefix(lower, p+",") {
			return true
		}
	}
	return false
}

func isOnlyStopWord(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, p := range stopPatterns {
		if lower == p {
			return true
		}
	}
	return false
}

func stopChoiceLabel(text string) string {
	if isStopWord(text) {
		return "Yes, stop"
	}
	return "🛑 Stop & reassess"
}

func queueChoiceLabel(text string) string {
	if isStopWord(text) {
		return "No, keep going"
	}
	return "⏳ Let it finish"
}

func renderPendingDecisionSummary(pending decision.PendingDecision) string {
	prompt := strings.TrimSpace(pending.Prompt)
	summary := strings.TrimSpace(summarizePendingDecision(pending))
	if summary == "" {
		return renderPendingDecisionExpanded(pending)
	}
	if pending.Kind == decision.KindProposalApproval {
		return summary
	}
	if prompt == "" {
		return summary
	}
	return strings.TrimSpace(prompt + "\n\n" + summary)
}

func renderPendingDecisionExpanded(pending decision.PendingDecision) string {
	text := strings.TrimSpace(pending.Prompt)
	if details := strings.TrimSpace(pending.Details); details != "" {
		if text != "" {
			text += "\n\n"
		}
		text += details
	}
	return strings.TrimSpace(text)
}

func summarizePendingDecision(pending decision.PendingDecision) string {
	details := strings.TrimSpace(pending.Details)
	if details == "" {
		return ""
	}
	switch pending.Kind {
	case decision.KindProposalApproval:
		return summarizeProposalApprovalDetails(details)
	case decision.KindArtifactRetention:
		return summarizeArtifactRetentionDetails(details)
	case decision.KindMemoryDelegation:
		return summarizeMemoryDelegationDetails(details)
	case decision.KindSnapshotRestore:
		return summarizeSnapshotRestoreDetails(details)
	default:
		return summarizeGenericDecisionDetails(details)
	}
}

func summarizeProposalApprovalDetails(details string) string {
	sections := splitDecisionSections(details)
	summary := compactSentence(cleanProposalApprovalSummary(firstNonEmpty(sections["summary"])))
	kind := firstNonEmpty(sections["kind"])
	command := firstNonEmpty(sections["command"])
	if message := commitMessageFromProposalCommand(command); message != "" {
		return "I’d like to commit: `" + message + "`."
	}
	if proposalSummaryLooksHighRisk(kind, summary) {
		return "High-risk approval: " + ensureDecisionSentence(summary)
	}
	if summary != "" {
		return "I’d like to " + lowercaseDecisionStart(ensureDecisionSentence(summary))
	}
	if effect := compactSentence(firstNonEmpty(sections["if approved"])); effect != "" {
		return "I’d like to " + lowercaseDecisionStart(ensureDecisionSentence(effect))
	}
	return compactSentence(details)
}

func commitMessageFromProposalCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	needle := "git" + " commit"
	if !strings.Contains(command, needle) {
		return ""
	}
	idx := strings.Index(command, " -m ")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(command[idx+4:])
	if rest == "" {
		return ""
	}
	quote := rest[0]
	if quote == '\'' || quote == '"' {
		for i := 1; i < len(rest); i++ {
			if rest[i] == quote && rest[i-1] != '\\' {
				return strings.TrimSpace(rest[1:i])
			}
		}
	}
	return compactSentence(rest)
}

func proposalSummaryLooksHighRisk(kind string, summary string) bool {
	joined := strings.ToLower(strings.Join([]string{kind, summary}, " "))
	return strings.Contains(joined, "destructive") || strings.Contains(joined, "delete") || strings.Contains(joined, "remove")
}

func lowercaseDecisionStart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = []rune(strings.ToLower(string(r[0])))[0]
	return string(r)
}

func ensureDecisionSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	switch s[len(s)-1] {
	case '.', '!', '?':
		return s
	default:
		return s + "."
	}
}

func cleanProposalApprovalSummary(summary string) string {
	lines := make([]string, 0)
	for _, raw := range strings.Split(strings.TrimSpace(summary), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "kind:") || strings.HasPrefix(lower, "trigger:") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func summarizeArtifactRetentionDetails(details string) string {
	sections := splitDecisionSections(details)
	artifacts := strings.TrimSpace(sections["artifacts"])
	items := []string{}
	for _, line := range strings.Split(artifacts, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line != "" {
			items = append(items, line)
		}
	}
	if len(items) == 0 {
		return compactSentence(details)
	}
	preview := items[0]
	if len(items) > 1 {
		preview += fmt.Sprintf(" +%d more", len(items)-1)
	}
	return strings.TrimSpace(strings.Join([]string{
		"Choose how long to keep the inbound artifact.",
		"Artifact: " + preview,
		"Use Expand details to inspect the full artifact list.",
	}, "\n"))
}

func summarizeMemoryDelegationDetails(details string) string {
	sections := splitDecisionSections(details)
	agent := firstNonEmpty(sections["agent"])
	why := firstNonEmpty(sections["why now"])
	items := firstNonEmpty(sections["items"])
	itemPreview := ""
	if items != "" {
		for _, line := range strings.Split(items, "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
			if line != "" {
				itemPreview = line
				break
			}
		}
	}
	lines := make([]string, 0, 4)
	if agent != "" {
		lines = append(lines, "Memory delegation for "+agent+".")
	} else {
		lines = append(lines, "Memory delegation request.")
	}
	if itemPreview != "" {
		lines = append(lines, "Item: "+compactSentence(itemPreview))
	}
	if why != "" {
		lines = append(lines, compactSentence("Why now: "+why))
	}
	lines = append(lines, "Use Expand details to inspect all delegated items.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func summarizeSnapshotRestoreDetails(details string) string {
	sections := splitDecisionSections(details)
	agent := firstNonEmpty(sections["agent"])
	snapshot := firstNonEmpty(sections["snapshot"])
	reason := firstNonEmpty(sections["reason"])
	lines := make([]string, 0, 4)
	if agent != "" {
		lines = append(lines, "Snapshot restore for "+agent+".")
	} else {
		lines = append(lines, "Durable child snapshot restore request.")
	}
	if snapshot != "" {
		lines = append(lines, "Snapshot: "+snapshot)
	}
	if reason != "" {
		lines = append(lines, compactSentence("Reason: "+reason))
	}
	lines = append(lines, "Use Expand details to inspect restore metadata.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func summarizeGenericDecisionDetails(details string) string {
	return compactSentence(details)
}

func splitDecisionSections(details string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(strings.TrimSpace(details), "\n")
	current := "summary"
	buf := []string{}
	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		if text != "" && strings.TrimSpace(out[current]) == "" {
			out[current] = text
		}
		buf = buf[:0]
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			if len(buf) > 0 {
				buf = append(buf, "")
			}
			continue
		}
		if strings.HasSuffix(line, ":") {
			flush()
			current = strings.ToLower(strings.TrimSuffix(line, ":"))
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func compactSentence(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	if len(text) <= 220 {
		return text
	}
	cut := text[:220]
	if idx := strings.LastIndex(cut, " "); idx > 80 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut) + "…"
}

func truncateDecisionSummaryText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" || limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	cut := text[:limit-3]
	if idx := strings.LastIndex(cut, " "); idx > 40 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut) + "..."
}
