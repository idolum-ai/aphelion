//go:build linux

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/principal"
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

type telegramDecisionHandler struct {
	sender                   telegramDecisionSender
	router                   telegramDecisionRouter
	broker                   *decision.Broker
	store                    *session.SQLiteStore
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

func newTelegramDecisionHandler(sender telegramDecisionSender, router telegramDecisionRouter, broker *decision.Broker, store *session.SQLiteStore) *telegramDecisionHandler {
	return &telegramDecisionHandler{
		sender:                   sender,
		router:                   router,
		broker:                   broker,
		store:                    store,
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
		_ = a.sender.EditMessageText(ctx, req.SessionKey.ChatID, result.Delivery.MessageID, text, "")
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
		_ = a.sender.EditMessageText(ctx, req.SessionKey.ChatID, result.Delivery.MessageID, text, "")
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
		_ = a.sender.EditMessageText(ctx, req.SessionKey.ChatID, result.Delivery.MessageID, text, "")
	}
	return toolpkg.DurableSnapshotRestoreApprovalDecision{Approved: false, TimedOut: result.TimedOut}, nil
}

func approvedDecisionConfirmationText(label string, decisionID string, kind decision.Kind, details string) string {
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
		lines = append(lines, "", "Approved content:", summary)
	} else if compact := compactSentence(details); compact != "" {
		lines = append(lines, "", "Approved content:", compact)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func approvedDecisionConfirmationRows(decisionID string, details string) [][]telegram.InlineButton {
	decisionID = strings.TrimSpace(decisionID)
	if decisionID == "" || strings.TrimSpace(details) == "" {
		return nil
	}
	return [][]telegram.InlineButton{{
		{
			Text:         "Expand details",
			CallbackData: decision.EncodeCallbackData(decisionID, "expand"),
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
	_ = sender.EditMessageText(ctx, chatID, messageID, text, "")
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
			_ = h.sender.EditMessageText(ctx, msg.ChatID, result.Delivery.MessageID, artifactRetentionResolutionText(result), "")
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
		_ = h.sender.EditMessageText(ctx, msg.ChatID, result.Delivery.MessageID, artifactRetentionResolutionText(result), "")
	}
	h.router.Route(ctx, updated)
	return nil
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
			_ = h.sender.EditMessageText(ctx, msg.ChatID, result.Delivery.MessageID, text, "")
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
	id, choice, ok := decision.DecodeCallbackData(cb.Data)
	if !ok {
		if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return err
		}
		return nil
	}
	if choice == "expand" {
		pending, found := h.broker.Peek(id)
		if !found {
			pending, found = h.broker.PeekResolved(id)
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
			if err := h.sender.EditMessageText(ctx, chatID, messageID, renderPendingDecisionExpanded(pending), ""); err != nil {
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

func (h *telegramDecisionHandler) handleReviewEventCallback(ctx context.Context, cb telegram.CallbackQuery, eventID int64, action core.ReviewEventAction) error {
	if h == nil || h.sender == nil || h.store == nil {
		return nil
	}
	event, err := h.store.ReviewEventByID(eventID)
	if err != nil {
		return err
	}
	if event == nil {
		return h.answerReviewEventCallback(ctx, cb, "This approval is no longer active. Use the newest prompt.")
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
	return h.sender.EditMessageText(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, "")
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

func telegramPrincipalMatches(target string, userID int64) bool {
	return userID > 0 && strings.TrimSpace(target) == fmt.Sprintf("telegram:%d", userID)
}

func inlineButtonRows(pending decision.PendingDecision) [][]telegram.InlineButton {
	if len(pending.Choices) == 0 {
		return nil
	}
	rows := make([][]telegram.InlineButton, 0, 2)
	if strings.TrimSpace(pending.Details) != "" {
		rows = append(rows, []telegram.InlineButton{{
			Text:         "Expand details",
			CallbackData: decision.EncodeCallbackData(pending.ID, "expand"),
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
	summary := firstNonEmpty(sections["summary"])
	kind := firstNonEmpty(sections["kind"])
	why := firstNonEmpty(sections["why now"])
	effect := firstNonEmpty(sections["if approved"])
	trigger := firstNonEmpty(sections["trigger"])
	command := firstNonEmpty(sections["command"])
	lines := make([]string, 0, 6)
	if summary != "" {
		lines = append(lines, summary)
	}
	meta := make([]string, 0, 2)
	if kind != "" {
		meta = append(meta, "Kind: "+kind)
	}
	if trigger != "" {
		meta = append(meta, "Trigger: "+trigger)
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, " · "))
	}
	if why != "" {
		lines = append(lines, compactSentence("Why now: "+why))
	}
	if effect != "" {
		lines = append(lines, compactSentence("If approved: "+effect))
	}
	if command != "" {
		lines = append(lines, "Command hidden by default. Use Expand details to inspect it.")
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
