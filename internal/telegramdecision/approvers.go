//go:build linux

package telegramdecision

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/decisionprojection"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

const (
	DefaultUserApprovalTimeout     = 30 * time.Minute
	DefaultExecApprovalTimeout     = DefaultUserApprovalTimeout
	DefaultMemoryDelegationTimeout = DefaultUserApprovalTimeout
	DefaultSnapshotRestoreTimeout  = DefaultUserApprovalTimeout
)

type DecisionSender interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
	DeleteMessage(ctx context.Context, chatID int64, messageID int64) error
	AnswerCallbackQuery(ctx context.Context, id string, text string) error
}

type DecisionKeyboardEditor interface {
	EditMessageTextWithInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error
}

type DecisionKeyboardClearer interface {
	EditMessageTextWithoutInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

func EditDecisionMessageClearingInlineKeyboard(ctx context.Context, sender DecisionSender, chatID int64, messageID int64, text string) error {
	if clearer, ok := sender.(DecisionKeyboardClearer); ok {
		return clearer.EditMessageTextWithoutInlineKeyboard(ctx, chatID, messageID, text, "")
	}
	return sender.EditMessageText(ctx, chatID, messageID, text, "")
}

type ApprovalWindowOfferer interface {
	CreateApprovalWindowOfferForKey(ctx context.Context, key session.SessionKey, adminUserID int64, sourceKind string, sourceID string, sourceDecisionKind string) (session.ApprovalWindowOffer, bool, error)
}

type ExecApprover struct {
	sender          DecisionSender
	broker          *decision.Broker
	timeout         time.Duration
	approvalWindows ApprovalWindowOfferer
	presentation    DecisionThreadResolver
}

type DurableMemoryDelegationApprover struct {
	sender       DecisionSender
	broker       *decision.Broker
	timeout      time.Duration
	presentation DecisionThreadResolver
}

type DurableSnapshotRestoreApprover struct {
	sender       DecisionSender
	broker       *decision.Broker
	timeout      time.Duration
	presentation DecisionThreadResolver
}

func (a *ExecApprover) Timeout() time.Duration {
	if a == nil {
		return 0
	}
	return a.timeout
}

func (a *ExecApprover) SetTimeout(timeout time.Duration) {
	if a != nil {
		a.timeout = timeout
	}
}

func (a *ExecApprover) SetPresentation(presentation DecisionThreadResolver) {
	if a != nil {
		a.presentation = presentation
	}
}

func (a *DurableMemoryDelegationApprover) Timeout() time.Duration {
	if a == nil {
		return 0
	}
	return a.timeout
}

func (a *DurableMemoryDelegationApprover) SetTimeout(timeout time.Duration) {
	if a != nil {
		a.timeout = timeout
	}
}

func (a *DurableMemoryDelegationApprover) SetPresentation(presentation DecisionThreadResolver) {
	if a != nil {
		a.presentation = presentation
	}
}

func (a *DurableSnapshotRestoreApprover) Timeout() time.Duration {
	if a == nil {
		return 0
	}
	return a.timeout
}

func (a *DurableSnapshotRestoreApprover) SetTimeout(timeout time.Duration) {
	if a != nil {
		a.timeout = timeout
	}
}

func (a *DurableSnapshotRestoreApprover) SetPresentation(presentation DecisionThreadResolver) {
	if a != nil {
		a.presentation = presentation
	}
}

func NewExecApprover(sender DecisionSender, broker *decision.Broker, timeout time.Duration, offerers ...ApprovalWindowOfferer) *ExecApprover {
	if timeout <= 0 {
		timeout = DefaultExecApprovalTimeout
	}
	var offerer ApprovalWindowOfferer
	if len(offerers) > 0 {
		offerer = offerers[0]
	}
	return &ExecApprover{
		sender:          sender,
		broker:          broker,
		timeout:         timeout,
		approvalWindows: offerer,
	}
}

func NewDurableMemoryDelegationApprover(sender DecisionSender, broker *decision.Broker, timeout time.Duration) *DurableMemoryDelegationApprover {
	if timeout <= 0 {
		timeout = DefaultMemoryDelegationTimeout
	}
	return &DurableMemoryDelegationApprover{
		sender:  sender,
		broker:  broker,
		timeout: timeout,
	}
}

func NewDurableSnapshotRestoreApprover(sender DecisionSender, broker *decision.Broker, timeout time.Duration) *DurableSnapshotRestoreApprover {
	if timeout <= 0 {
		timeout = DefaultSnapshotRestoreTimeout
	}
	return &DurableSnapshotRestoreApprover{
		sender:  sender,
		broker:  broker,
		timeout: timeout,
	}
}

func scopedDecisionRequestFields(key session.SessionKey, senderID int64) (ownerKey string, sessionID string, scopeKind string, scopeID string, durableAgentID string) {
	scope := session.NormalizeScopeRef(key.Scope)
	sessionID = session.SessionIDForKey(key)
	scopeKind = strings.TrimSpace(string(scope.Kind))
	scopeID = strings.TrimSpace(scope.ID)
	durableAgentID = strings.TrimSpace(scope.DurableAgentID)
	if sessionID != "" && senderID != 0 {
		ownerKey = fmt.Sprintf("session:%s:sender:%d", sessionID, senderID)
	} else if sessionID != "" {
		ownerKey = "session:" + sessionID
	}
	return ownerKey, sessionID, scopeKind, scopeID, durableAgentID
}

func (a *ExecApprover) ConfirmExec(ctx context.Context, req toolpkg.ExecApprovalRequest) (toolpkg.ExecApprovalDecision, error) {
	if a == nil || a.sender == nil || a.broker == nil {
		return toolpkg.ExecApprovalDecision{}, fmt.Errorf("telegram exec approver is not configured")
	}
	if req.SessionKey.ChatID == 0 {
		return toolpkg.ExecApprovalDecision{}, fmt.Errorf("command requires explicit confirmation but no interactive chat is available: %s", req.Reason)
	}

	ownerKey, sessionID, scopeKind, scopeID, durableAgentID := scopedDecisionRequestFields(req.SessionKey, req.Principal.TelegramUserID)
	result, err := a.broker.Request(ctx, decision.Request{
		Kind:           decision.KindProposalApproval,
		ChatID:         req.SessionKey.ChatID,
		SenderID:       req.Principal.TelegramUserID,
		OwnerKey:       ownerKey,
		SessionID:      sessionID,
		ScopeKind:      scopeKind,
		ScopeID:        scopeID,
		DurableAgentID: durableAgentID,
		Prompt:         "Approve this proposal?",
		Details:        formatExecProposalDetails(req),
		Choices:        []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
		DefaultChoice:  "deny",
		Timeout:        a.timeout,
	})
	if err != nil {
		return toolpkg.ExecApprovalDecision{}, err
	}

	if result.Choice == "approve" {
		if result.Delivery.MessageID != 0 {
			rows, offerErr := a.approvalWindowOfferRows(ctx, req.SessionKey, req.Principal.TelegramUserID, result.DecisionID, string(decision.KindProposalApproval))
			if offerErr != nil {
				return toolpkg.ExecApprovalDecision{}, offerErr
			}
			if err := editApprovedDecisionConfirmation(ctx, a.sender, req.SessionKey, result.Delivery.MessageID, "Proposal", result.DecisionID, decision.KindProposalApproval, formatExecProposalDetails(req), rows, a.presentation); err != nil {
				return toolpkg.ExecApprovalDecision{}, err
			}
		}
		return toolpkg.ExecApprovalDecision{Approved: true}, nil
	}

	if result.Delivery.MessageID != 0 {
		text := "Proposal denied."
		if result.TimedOut {
			text = "Proposal denied — approval timed out."
		}
		text = prefixDecisionTextForKey(req.SessionKey, a.presentation, text)
		_ = EditDecisionMessageClearingInlineKeyboard(ctx, a.sender, req.SessionKey.ChatID, result.Delivery.MessageID, text)
	}
	return toolpkg.ExecApprovalDecision{Approved: false}, nil
}

func (a *DurableMemoryDelegationApprover) ConfirmDurableMemoryDelegation(ctx context.Context, req toolpkg.DurableMemoryDelegationApprovalRequest) (toolpkg.DurableMemoryDelegationApprovalDecision, error) {
	if a == nil || a.sender == nil || a.broker == nil {
		return toolpkg.DurableMemoryDelegationApprovalDecision{}, fmt.Errorf("telegram durable memory delegation approver is not configured")
	}
	if req.SessionKey.ChatID == 0 {
		return toolpkg.DurableMemoryDelegationApprovalDecision{}, fmt.Errorf("memory delegation requires explicit confirmation but no interactive chat is available")
	}
	ownerKey, sessionID, scopeKind, scopeID, durableAgentID := scopedDecisionRequestFields(req.SessionKey, req.Principal.TelegramUserID)
	result, err := a.broker.Request(ctx, decision.Request{
		Kind:           decision.KindMemoryDelegation,
		ChatID:         req.SessionKey.ChatID,
		SenderID:       req.Principal.TelegramUserID,
		OwnerKey:       ownerKey,
		SessionID:      sessionID,
		ScopeKind:      scopeKind,
		ScopeID:        scopeID,
		DurableAgentID: durableAgentID,
		Prompt:         "Approve memory delegation to the child?",
		Details:        formatDurableMemoryDelegationDetails(req),
		Choices:        []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
		DefaultChoice:  "deny",
		Timeout:        a.timeout,
	})
	if err != nil {
		return toolpkg.DurableMemoryDelegationApprovalDecision{}, err
	}
	if result.Choice == "approve" {
		if result.Delivery.MessageID != 0 {
			if err := editApprovedDecisionConfirmation(ctx, a.sender, req.SessionKey, result.Delivery.MessageID, "Memory delegation", result.DecisionID, decision.KindMemoryDelegation, formatDurableMemoryDelegationDetails(req), nil, a.presentation); err != nil {
				return toolpkg.DurableMemoryDelegationApprovalDecision{}, err
			}
		}
		return toolpkg.DurableMemoryDelegationApprovalDecision{Approved: true}, nil
	}
	if result.Delivery.MessageID != 0 {
		text := "Memory delegation denied."
		if result.TimedOut {
			text = "Memory delegation denied — approval timed out."
		}
		text = prefixDecisionTextForKey(req.SessionKey, a.presentation, text)
		_ = EditDecisionMessageClearingInlineKeyboard(ctx, a.sender, req.SessionKey.ChatID, result.Delivery.MessageID, text)
	}
	return toolpkg.DurableMemoryDelegationApprovalDecision{Approved: false, TimedOut: result.TimedOut}, nil
}

func (a *DurableSnapshotRestoreApprover) ConfirmDurableSnapshotRestore(ctx context.Context, req toolpkg.DurableSnapshotRestoreApprovalRequest) (toolpkg.DurableSnapshotRestoreApprovalDecision, error) {
	if a == nil || a.sender == nil || a.broker == nil {
		return toolpkg.DurableSnapshotRestoreApprovalDecision{}, fmt.Errorf("telegram durable snapshot restore approver is not configured")
	}
	if req.SessionKey.ChatID == 0 {
		return toolpkg.DurableSnapshotRestoreApprovalDecision{}, fmt.Errorf("snapshot restore requires explicit confirmation but no interactive chat is available")
	}
	ownerKey, sessionID, scopeKind, scopeID, durableAgentID := scopedDecisionRequestFields(req.SessionKey, req.Principal.TelegramUserID)
	result, err := a.broker.Request(ctx, decision.Request{
		Kind:           decision.KindSnapshotRestore,
		ChatID:         req.SessionKey.ChatID,
		SenderID:       req.Principal.TelegramUserID,
		OwnerKey:       ownerKey,
		SessionID:      sessionID,
		ScopeKind:      scopeKind,
		ScopeID:        scopeID,
		DurableAgentID: durableAgentID,
		Prompt:         "Restore this child snapshot?",
		Details:        formatDurableSnapshotRestoreDetails(req),
		Choices:        []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
		DefaultChoice:  "deny",
		Timeout:        a.timeout,
	})
	if err != nil {
		return toolpkg.DurableSnapshotRestoreApprovalDecision{}, err
	}
	if result.Choice == "approve" {
		if result.Delivery.MessageID != 0 {
			if err := editApprovedDecisionConfirmation(ctx, a.sender, req.SessionKey, result.Delivery.MessageID, "Snapshot restore", result.DecisionID, decision.KindSnapshotRestore, formatDurableSnapshotRestoreDetails(req), nil, a.presentation); err != nil {
				return toolpkg.DurableSnapshotRestoreApprovalDecision{}, err
			}
		}
		return toolpkg.DurableSnapshotRestoreApprovalDecision{Approved: true}, nil
	}
	if result.Delivery.MessageID != 0 {
		text := "Snapshot restore denied."
		if result.TimedOut {
			text = "Snapshot restore denied — approval timed out."
		}
		text = prefixDecisionTextForKey(req.SessionKey, a.presentation, text)
		_ = EditDecisionMessageClearingInlineKeyboard(ctx, a.sender, req.SessionKey.ChatID, result.Delivery.MessageID, text)
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

func editApprovedDecisionConfirmation(ctx context.Context, sender DecisionSender, key session.SessionKey, messageID int64, label string, decisionID string, kind decision.Kind, details string, extraRows [][]telegram.InlineButton, presentation DecisionThreadResolver) error {
	if sender == nil || key.ChatID == 0 || messageID == 0 {
		return nil
	}
	text := prefixDecisionTextForKey(key, presentation, approvedDecisionConfirmationText(label, decisionID, kind, details))
	rows := appendTelegramRows(approvedDecisionConfirmationRows(decisionID, details), extraRows)
	if len(rows) > 0 {
		if editor, ok := sender.(DecisionKeyboardEditor); ok {
			if err := editor.EditMessageTextWithInlineKeyboard(ctx, key.ChatID, messageID, text, "", rows); err == nil {
				return nil
			} else if len(extraRows) > 0 {
				replyTo := messageID
				if _, sendErr := sender.SendInlineKeyboard(ctx, key.ChatID, text, rows, &replyTo); sendErr != nil {
					return fmt.Errorf("edit approved decision confirmation with controls: %w; send fallback controls: %v", err, sendErr)
				}
				return nil
			}
		} else if len(extraRows) > 0 {
			replyTo := messageID
			if _, err := sender.SendInlineKeyboard(ctx, key.ChatID, text, rows, &replyTo); err != nil {
				return fmt.Errorf("send approved decision fallback controls: %w", err)
			}
			return nil
		}
	}
	return EditDecisionMessageClearingInlineKeyboard(ctx, sender, key.ChatID, messageID, text)
}

func (a *ExecApprover) approvalWindowOfferRows(ctx context.Context, key session.SessionKey, adminUserID int64, decisionID string, decisionKind string) ([][]telegram.InlineButton, error) {
	if a == nil || a.approvalWindows == nil {
		return nil, nil
	}
	offer, created, err := a.approvalWindows.CreateApprovalWindowOfferForKey(ctx, key, adminUserID, session.ApprovalWindowOfferSourceDecision, decisionID, decisionKind)
	if err != nil || !created {
		return nil, err
	}
	return telegramcommands.ApprovalWindowRowsForOffer(offer), nil
}

func appendTelegramRows(base [][]telegram.InlineButton, extra [][]telegram.InlineButton) [][]telegram.InlineButton {
	out := append([][]telegram.InlineButton(nil), base...)
	for _, row := range extra {
		if len(row) == 0 {
			continue
		}
		out = append(out, row)
	}
	return out
}

func formatExecProposalDetails(req toolpkg.ExecApprovalRequest) string {
	return decisionprojection.FormatExecApprovalDetails(req.Proposal, req.Reason, req.Command, req.Workdir)
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
