//go:build linux

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

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
