//go:build linux

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

const (
	defaultInterruptTimeout         = 30 * time.Second
	defaultStopWordTimeout          = 15 * time.Second
	defaultExecApprovalTimeout      = 30 * time.Second
	defaultArtifactRetentionTimeout = 45 * time.Second
)

type telegramDecisionSender interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
	DeleteMessage(ctx context.Context, chatID int64, messageID int64) error
	AnswerCallbackQuery(ctx context.Context, id string, text string) error
}

type telegramDecisionRouter interface {
	Status(chatID int64) core.SessionStatus
	Stop(chatID int64) core.StopResult
	Route(ctx context.Context, msg core.InboundMessage)
}

type telegramDecisionHandler struct {
	sender                   telegramDecisionSender
	router                   telegramDecisionRouter
	broker                   *decision.Broker
	interruptTimeout         time.Duration
	stopWordTimeout          time.Duration
	artifactRetentionTimeout time.Duration
}

type telegramExecApprover struct {
	sender  telegramDecisionSender
	broker  *decision.Broker
	timeout time.Duration
}

func newTelegramDecisionHandler(sender telegramDecisionSender, router telegramDecisionRouter, broker *decision.Broker) *telegramDecisionHandler {
	return &telegramDecisionHandler{
		sender:                   sender,
		router:                   router,
		broker:                   broker,
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

func newTelegramDecisionBroker(sender telegramDecisionSender) *decision.Broker {
	return decision.NewBroker(func(ctx context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		text := strings.TrimSpace(pending.Prompt)
		if details := strings.TrimSpace(pending.Details); details != "" {
			if text != "" {
				text += "\n\n"
			}
			text += details
		}
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, inlineButtonRows(pending), replyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		return decision.Delivery{MessageID: msgID}, nil
	})
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
		Choices:       []decision.Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
		DefaultChoice: "deny",
		Timeout:       a.timeout,
	})
	if err != nil {
		return toolpkg.ExecApprovalDecision{}, err
	}

	if result.Choice == "approve" {
		if result.Delivery.MessageID != 0 {
			_ = a.sender.DeleteMessage(ctx, req.SessionKey.ChatID, result.Delivery.MessageID)
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

func (h *telegramDecisionHandler) HandleArtifactRetentionMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	if h == nil || h.sender == nil || h.router == nil || h.broker == nil {
		return false, nil
	}
	if !hasArtifactRetentionCandidates(msg) {
		return false, nil
	}

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
	if !h.router.Status(msg.ChatID).Active {
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

	result, err := h.broker.Request(ctx, req)
	if err != nil {
		return true, err
	}

	switch result.Choice {
	case "stop":
		if result.Delivery.MessageID != 0 {
			_ = h.sender.DeleteMessage(ctx, msg.ChatID, result.Delivery.MessageID)
		}
		h.router.Stop(msg.ChatID)
		if !isOnlyStopWord(msg.Text) {
			h.router.Route(ctx, msg)
		}
	case "queue":
		if result.Delivery.MessageID != 0 {
			text := "Got it — I'll process your message next. ⏳"
			if result.TimedOut {
				text = "Queued your message — processing after current task."
			}
			_ = h.sender.EditMessageText(ctx, msg.ChatID, result.Delivery.MessageID, text, "")
		}
		h.router.Route(ctx, msg)
	}
	return true, nil
}

func (h *telegramDecisionHandler) HandleCallbackQuery(ctx context.Context, cb telegram.CallbackQuery) error {
	if h == nil || h.sender == nil || h.broker == nil {
		return nil
	}
	if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, ""); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return err
		}
	}
	id, choice, ok := decision.DecodeCallbackData(cb.Data)
	if !ok {
		return nil
	}
	h.broker.Resolve(id, choice)
	return nil
}

func inlineButtonRows(pending decision.PendingDecision) [][]telegram.InlineButton {
	if len(pending.Choices) == 0 {
		return nil
	}
	row := make([]telegram.InlineButton, 0, len(pending.Choices))
	for _, choice := range pending.Choices {
		row = append(row, telegram.InlineButton{
			Text:         strings.TrimSpace(choice.Label),
			CallbackData: decision.EncodeCallbackData(pending.ID, choice.ID),
		})
	}
	return [][]telegram.InlineButton{row}
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
