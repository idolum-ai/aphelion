//go:build linux

package telegramcommands

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/telegram"
)

const durableWizardCallbackPrefix = "durable_wizard:"

const (
	staleDurableWizardCallbackText = "This wizard action is no longer active. Ask me to show the durable-agent wizard again."
	adminDurableWizardOnlyText     = "Durable-agent wizard controls are available to Telegram admins only."
)

type durableWizardCallbackAction string

const (
	durableWizardCallbackAnswer   durableWizardCallbackAction = "ans"
	durableWizardCallbackShow     durableWizardCallbackAction = "show"
	durableWizardCallbackFinalize durableWizardCallbackAction = "finalize"
	durableWizardCallbackCancel   durableWizardCallbackAction = "cancel"
)

type durableWizardCard struct {
	Action           string
	AgentID          string
	WizardStatus     string
	CurrentStep      string
	BootstrapBackend string
	BootstrapModel   string
}

type durableWizardChoice struct {
	Key   string
	Label string
}

func encodeDurableWizardAnswerCallbackData(step string, option string) string {
	return durableWizardCallbackPrefix + string(durableWizardCallbackAnswer) + ":" + strings.TrimSpace(step) + ":" + strings.TrimSpace(option)
}

func encodeDurableWizardActionCallbackData(action durableWizardCallbackAction) string {
	return durableWizardCallbackPrefix + string(action)
}

func decodeDurableWizardCallbackData(data string) (durableWizardCallbackAction, string, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, durableWizardCallbackPrefix) {
		return "", "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, durableWizardCallbackPrefix))
	if payload == "" {
		return "", "", "", false
	}
	parts := strings.Split(payload, ":")
	if len(parts) == 1 {
		action := durableWizardCallbackAction(strings.ToLower(strings.TrimSpace(parts[0])))
		switch action {
		case durableWizardCallbackShow, durableWizardCallbackFinalize, durableWizardCallbackCancel:
			return action, "", "", true
		default:
			return "", "", "", false
		}
	}
	if len(parts) == 3 {
		action := durableWizardCallbackAction(strings.ToLower(strings.TrimSpace(parts[0])))
		step := strings.ToLower(strings.TrimSpace(parts[1]))
		option := strings.ToLower(strings.TrimSpace(parts[2]))
		if action == durableWizardCallbackAnswer && step != "" && option != "" {
			return action, step, option, true
		}
	}
	return "", "", "", false
}

func handleDurableWizardCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action durableWizardCallbackAction, step string, option string) (bool, error) {
	chatID := int64(0)
	messageID := int64(0)
	messageText := ""
	if cb.Message != nil {
		messageID = cb.Message.MessageID
		messageText = cb.Message.Text
		if cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
		}
	}
	senderID := int64(0)
	if cb.From != nil {
		senderID = cb.From.ID
	}

	if chatID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleDurableWizardCallbackText); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), adminDurableWizardOnlyText); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		return true, nil
	}
	card, ok := parseDurableWizardCard(messageText)
	if !ok || strings.TrimSpace(card.AgentID) == "" {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleDurableWizardCallbackText); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		return true, nil
	}

	actionLabel := ""
	wizardAnswers := map[string]any(nil)
	switch action {
	case durableWizardCallbackAnswer:
		if strings.TrimSpace(card.CurrentStep) == "" || !strings.EqualFold(card.CurrentStep, step) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleDurableWizardCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		answers, valid := durableWizardAnswersForChoice(step, option, card)
		if !valid {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleDurableWizardCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		actionLabel = "wizard_answer"
		wizardAnswers = answers
	case durableWizardCallbackShow:
		actionLabel = "wizard_show"
	case durableWizardCallbackFinalize:
		if !strings.EqualFold(strings.TrimSpace(card.WizardStatus), "ready") {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleDurableWizardCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		actionLabel = "wizard_finalize"
	case durableWizardCallbackCancel:
		actionLabel = "wizard_cancel"
	default:
		return true, nil
	}

	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
	}
	output, err := router.RunDurableWizard(ctx, chatID, senderID, actionLabel, card.AgentID, wizardAnswers)
	if err != nil {
		return true, err
	}

	rows := durableWizardInlineRowsFromText(output)
	if len(rows) > 0 {
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, output, "", rows); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, output); err != nil {
		return true, err
	}
	return true, nil
}

func parseDurableWizardCard(text string) (durableWizardCard, bool) {
	fields := parseWizardKVLines(text)
	action := strings.ToLower(strings.TrimSpace(fields["action"]))
	switch action {
	case "durable-agent wizard show", "durable-agent wizard finalize":
	default:
		return durableWizardCard{}, false
	}
	card := durableWizardCard{
		Action:           action,
		AgentID:          strings.TrimSpace(fields["agent_id"]),
		WizardStatus:     strings.ToLower(strings.TrimSpace(fields["wizard_status"])),
		CurrentStep:      strings.ToLower(strings.TrimSpace(fields["current_step"])),
		BootstrapBackend: strings.ToLower(strings.TrimSpace(fields["bootstrap_backend"])),
		BootstrapModel:   strings.TrimSpace(fields["bootstrap_model"]),
	}
	if card.CurrentStep == "-" {
		card.CurrentStep = ""
	}
	if card.WizardStatus == "" {
		card.WizardStatus = "in_progress"
	}
	return card, true
}

func parseWizardKVLines(text string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		sep := strings.Index(line, ":")
		if sep <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:sep]))
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(line[sep+1:])
	}
	return out
}

func durableWizardInlineRowsFromText(text string) [][]telegram.InlineButton {
	card, ok := parseDurableWizardCard(text)
	if !ok {
		return nil
	}
	return durableWizardInlineRows(card)
}

func durableWizardInlineRows(card durableWizardCard) [][]telegram.InlineButton {
	status := strings.ToLower(strings.TrimSpace(card.WizardStatus))
	switch status {
	case "finalized", "cancelled":
		return nil
	case "ready":
		return [][]telegram.InlineButton{{
			{Text: "Cancel", CallbackData: encodeDurableWizardActionCallbackData(durableWizardCallbackCancel)},
			{Text: "Finalize", CallbackData: encodeDurableWizardActionCallbackData(durableWizardCallbackFinalize)},
		}}
	default:
		rows := durableWizardAnswerRows(card.CurrentStep, card)
		rows = append(rows, []telegram.InlineButton{
			{Text: "Cancel", CallbackData: encodeDurableWizardActionCallbackData(durableWizardCallbackCancel)},
			{Text: "Refresh", CallbackData: encodeDurableWizardActionCallbackData(durableWizardCallbackShow)},
		})
		return rows
	}
}

func durableWizardAnswerRows(step string, card durableWizardCard) [][]telegram.InlineButton {
	choices := durableWizardChoicesForStep(step, card)
	if len(choices) == 0 {
		return nil
	}
	rows := make([][]telegram.InlineButton, 0, (len(choices)+1)/2)
	row := make([]telegram.InlineButton, 0, 2)
	for _, choice := range choices {
		row = append(row, telegram.InlineButton{
			Text:         choice.Label,
			CallbackData: encodeDurableWizardAnswerCallbackData(step, choice.Key),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = make([]telegram.InlineButton, 0, 2)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

func durableWizardChoicesForStep(step string, card durableWizardCard) []durableWizardChoice {
	switch strings.ToLower(strings.TrimSpace(step)) {
	case "adapter":
		return nil
	case "bootstrap_profile":
		if strings.TrimSpace(card.BootstrapBackend) == "codex" {
			return []durableWizardChoice{
				{Key: "inherit_parent", Label: "Inherit parent"},
			}
		}
		return []durableWizardChoice{
			{Key: "inherit_parent", Label: "Inherit parent"},
			{Key: "child_custom", Label: "Child custom"},
		}
	case "bootstrap_model":
		return []durableWizardChoice{
			{Key: "keep_parent_model", Label: "Parent model"},
			{Key: "claude-sonnet-4-6", Label: "Sonnet 4.6"},
			{Key: "claude-opus-4-8", Label: "Opus 4.8"},
		}
	case "autonomy":
		return []durableWizardChoice{
			{Key: "observe_only", Label: "Observe only"},
			{Key: "local_drafts", Label: "Local drafts"},
			{Key: "review_before_reply", Label: "Review first"},
			{Key: "reply_within_charter", Label: "Charter reply"},
		}
	case "surface_rules":
		return []durableWizardChoice{
			{Key: "important_only", Label: "Important only"},
			{Key: "broad_triage", Label: "Broad triage"},
		}
	case "summarize_pdfs":
		return []durableWizardChoice{
			{Key: "yes", Label: "Summarize PDFs"},
			{Key: "no", Label: "No PDFs"},
		}
	case "synthesis_cadence":
		return []durableWizardChoice{
			{Key: "1h", Label: "Every 1h"},
			{Key: "4h", Label: "Every 4h"},
			{Key: "24h", Label: "Daily"},
		}
	case "wakeup_mode":
		return []durableWizardChoice{
			{Key: "poll", Label: "Poll"},
			{Key: "push", Label: "Push"},
			{Key: "poll_or_push", Label: "Either"},
		}
	case "poll_interval":
		return []durableWizardChoice{
			{Key: "2m", Label: "2m"},
			{Key: "5m", Label: "5m"},
			{Key: "15m", Label: "15m"},
		}
	case "capabilities":
		return []durableWizardChoice{
			{Key: "read_core", Label: "Read-only core"},
			{Key: "read_classify", Label: "Classify"},
		}
	case "never_retain":
		return []durableWizardChoice{
			{Key: "strict", Label: "Strict privacy"},
			{Key: "moderate", Label: "Moderate privacy"},
		}
	case "charter":
		return []durableWizardChoice{
			{Key: "readonly_digest", Label: "Read-only digest"},
			{Key: "jobs_digest", Label: "Jobs digest"},
		}
	default:
		return nil
	}
}

func durableWizardAnswersForChoice(step string, option string, card durableWizardCard) (map[string]any, bool) {
	step = strings.ToLower(strings.TrimSpace(step))
	option = strings.ToLower(strings.TrimSpace(option))
	switch step {
	case "adapter":
		return nil, false
	case "bootstrap_profile":
		switch option {
		case "inherit_parent":
			return map[string]any{"bootstrap_profile": option}, true
		case "child_custom":
			if strings.TrimSpace(card.BootstrapBackend) == "codex" {
				return nil, false
			}
			return map[string]any{"bootstrap_profile": option}, true
		}
	case "bootstrap_model":
		if strings.TrimSpace(card.BootstrapBackend) == "codex" {
			return nil, false
		}
		switch option {
		case "keep_parent_model":
			if strings.TrimSpace(card.BootstrapModel) == "" {
				return nil, false
			}
			return map[string]any{"bootstrap_model": strings.TrimSpace(card.BootstrapModel)}, true
		case "claude-sonnet-4-6", "claude-opus-4-8":
			return map[string]any{"bootstrap_model": option}, true
		case "claude-opus-4-6", "claude-opus-4-7", "claude-opus-4.7":
			return map[string]any{"bootstrap_model": "claude-opus-4-8"}, true
		}
	case "autonomy":
		switch option {
		case "observe_only", "local_drafts", "review_before_reply", "reply_within_charter":
			return map[string]any{"autonomy": option}, true
		}
	case "surface_rules":
		switch option {
		case "important_only":
			return map[string]any{"surface_rules": []string{"urgent", "high_signal", "job_opportunity", "external_interest", "pdf"}}, true
		case "broad_triage":
			return map[string]any{"surface_rules": []string{"urgent", "high_signal", "job_opportunity", "external_interest", "pdf", "financial", "legal", "account_security"}}, true
		}
	case "summarize_pdfs":
		switch option {
		case "yes":
			return map[string]any{"summarize_pdfs": true}, true
		case "no":
			return map[string]any{"summarize_pdfs": false}, true
		}
	case "synthesis_cadence":
		switch option {
		case "1h", "4h", "24h":
			return map[string]any{"synthesis_cadence": option}, true
		}
	case "wakeup_mode":
		switch option {
		case "poll", "push", "poll_or_push":
			return map[string]any{"wakeup_mode": option}, true
		}
	case "poll_interval":
		switch option {
		case "2m", "5m", "15m":
			return map[string]any{"poll_interval": option}, true
		}
	case "capabilities":
		switch option {
		case "read_core":
			return map[string]any{"capabilities": []string{"read_channel", "bounded_review_artifact", "summarize_pdf"}}, true
		case "read_classify":
			return map[string]any{"capabilities": []string{"read_channel", "classify_channel_signal", "extract_attachment_text", "bounded_review_artifact", "summarize_pdf"}}, true
		}
	case "never_retain":
		switch option {
		case "strict":
			return map[string]any{"never_retain": []string{"auth_tokens", "secrets", "raw_message_bodies", "full_attachments"}}, true
		case "moderate":
			return map[string]any{"never_retain": []string{"auth_tokens", "secrets", "full_attachments"}}, true
		}
	case "charter":
		switch option {
		case "readonly_digest":
			return map[string]any{"charter": "Read-only child that checks a configured channel, summarizes important threads and PDFs, and never sends outbound messages."}, true
		case "jobs_digest":
			return map[string]any{"charter": "Read-only scout focused on opportunities and inbound interest; summarize what matters and never send outbound messages."}, true
		}
	}
	return nil, false
}
