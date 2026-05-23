//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/telegram"
)

func TestDurableWizardInlineRowsFromTextInProgress(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: autonomy\nmissing: autonomy,surface_rules\nnext_question: Should the child be observe_only, local_drafts, review_before_reply, or reply_within_charter?\naddress: child-endpoint\nadapter: child_adapter\nautonomy: \nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter:\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) < 2 {
		t.Fatalf("rows len = %d, want at least option row and controls", len(rows))
	}
	foundObserveOnly := false
	for _, row := range rows {
		for _, button := range row {
			if strings.EqualFold(button.Text, "Observe only") {
				foundObserveOnly = true
			}
		}
	}
	if !foundObserveOnly {
		t.Fatalf("rows = %#v, want Observe only button", rows)
	}
	last := rows[len(rows)-1]
	if len(last) != 2 || last[0].Text != "Cancel" || last[1].Text != "Refresh" {
		t.Fatalf("last row = %#v, want [Cancel|Refresh] controls", last)
	}
}

func TestDurableWizardInlineRowsFromTextBootstrapProfile(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_profile\nmissing: bootstrap_profile,autonomy\nnext_question: Should this child inherit the parent bootstrap defaults or pin a child-custom bootstrap profile?\naddress: child-endpoint\nadapter: child_adapter\nbootstrap_profile: \nbootstrap_backend: native\nbootstrap_native_provider: anthropic\nbootstrap_model: claude-parent\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) < 2 {
		t.Fatalf("rows len = %d, want at least option row and controls", len(rows))
	}
	foundInherit := false
	foundCustom := false
	for _, row := range rows {
		for _, button := range row {
			if strings.EqualFold(button.Text, "Inherit parent") {
				foundInherit = true
			}
			if strings.EqualFold(button.Text, "Child custom") {
				foundCustom = true
			}
		}
	}
	if !foundInherit || !foundCustom {
		t.Fatalf("rows = %#v, want bootstrap profile buttons", rows)
	}
}

func TestDurableWizardInlineRowsFromTextBootstrapProfileCodex(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_profile\nmissing: bootstrap_profile,autonomy\nnext_question: This child uses a codex bootstrap backend; keep parent bootstrap defaults?\naddress: child-endpoint\nadapter: child_adapter\nbootstrap_profile: \nbootstrap_backend: codex\nbootstrap_model: \n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) < 2 {
		t.Fatalf("rows len = %d, want at least option row and controls", len(rows))
	}
	optionButtons := rows[0]
	if len(optionButtons) != 1 || optionButtons[0].Text != "Inherit parent" {
		t.Fatalf("option row = %#v, want only Inherit parent for codex", optionButtons)
	}
}

func TestDurableWizardInlineRowsFromTextBootstrapModel(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_model\nmissing: bootstrap_model\nnext_question: Which model should this child pin for child-custom bootstrap?\naddress: child-endpoint\nadapter: child_adapter\nbootstrap_profile: child_custom\nbootstrap_backend: native\nbootstrap_native_provider: anthropic\nbootstrap_model: claude-parent\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) < 2 {
		t.Fatalf("rows len = %d, want option rows plus controls", len(rows))
	}
	foundKeepParent := false
	foundSonnet := false
	for _, row := range rows {
		for _, button := range row {
			if strings.EqualFold(button.Text, "Parent model") {
				foundKeepParent = true
			}
			if strings.EqualFold(button.Text, "Sonnet 4.6") {
				foundSonnet = true
			}
		}
	}
	if !foundKeepParent || !foundSonnet {
		t.Fatalf("rows = %#v, want bootstrap model buttons", rows)
	}
}

func TestDurableWizardInlineRowsFromTextReady(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: ready\ncurrent_step: -\nmissing: -\naddress: child-endpoint\nadapter: child_adapter\nautonomy: observe_only\nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter: Read-only child.\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) != 1 {
		t.Fatalf("rows len = %d, want 1 finalize control row", len(rows))
	}
	if len(rows[0]) != 2 || rows[0][0].Text != "Cancel" || rows[0][1].Text != "Finalize" {
		t.Fatalf("row = %#v, want [Cancel|Finalize]", rows[0])
	}
}

func TestDurableWizardChoiceLabelsStayCompact(t *testing.T) {
	t.Parallel()

	steps := []string{
		"bootstrap_profile",
		"bootstrap_model",
		"autonomy",
		"surface_rules",
		"summarize_pdfs",
		"synthesis_cadence",
		"wakeup_mode",
		"poll_interval",
		"capabilities",
		"never_retain",
		"charter",
	}
	for _, step := range steps {
		for _, choice := range durableWizardChoicesForStep(step, durableWizardCard{}) {
			if words := strings.Fields(choice.Label); len(words) > 2 {
				t.Fatalf("%s button label %q has %d words, want at most 2", step, choice.Label, len(words))
			}
		}
	}
}

func TestHandleTelegramCommandCallbackDurableWizardAnswer(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:          true,
		durableWizardResult: "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: ready\ncurrent_step: -\nmissing: -\naddress: child-endpoint\nadapter: child_adapter\nautonomy: observe_only\nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter: Read-only child.\n",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-answer",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("autonomy", "observe_only"),
		Message: &telegram.Message{
			MessageID: 210,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: autonomy\nmissing: autonomy,surface_rules\naddress: child-endpoint\nadapter: child_adapter\nautonomy: \nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter:\n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardChatID != 7 || router.durableWizardSenderID != 1001 {
		t.Fatalf("wizard callback routing = (%d,%d), want (7,1001)", router.durableWizardChatID, router.durableWizardSenderID)
	}
	if router.durableWizardAction != "wizard_answer" {
		t.Fatalf("durableWizardAction = %q, want wizard_answer", router.durableWizardAction)
	}
	if router.durableWizardAgentID != "child-alpha" {
		t.Fatalf("durableWizardAgentID = %q, want child-alpha", router.durableWizardAgentID)
	}
	if got := router.durableWizardAnswers["autonomy"]; got != "observe_only" {
		t.Fatalf("durableWizardAnswers[autonomy] = %#v, want observe_only", got)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if len(sender.editInline[0].rows) != 1 || len(sender.editInline[0].rows[0]) != 2 {
		t.Fatalf("rows = %#v, want finalize controls", sender.editInline[0].rows)
	}
	if sender.editInline[0].rows[0][0].Text != "Cancel" || sender.editInline[0].rows[0][1].Text != "Finalize" {
		t.Fatalf("row = %#v, want [Cancel|Finalize]", sender.editInline[0].rows[0])
	}
}

func TestHandleTelegramCommandCallbackDurableWizardBootstrapModelKeepParent(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:          true,
		durableWizardResult: "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: ready\ncurrent_step: -\nmissing: -\nbootstrap_profile: child_custom\nbootstrap_model: claude-parent\n",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-bootstrap-model",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("bootstrap_model", "keep_parent_model"),
		Message: &telegram.Message{
			MessageID: 212,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_model\nmissing: bootstrap_model\nbootstrap_profile: child_custom\nbootstrap_model: claude-parent\n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardAction != "wizard_answer" {
		t.Fatalf("durableWizardAction = %q, want wizard_answer", router.durableWizardAction)
	}
	if got := router.durableWizardAnswers["bootstrap_model"]; got != "claude-parent" {
		t.Fatalf("durableWizardAnswers[bootstrap_model] = %#v, want claude-parent", got)
	}
}

func TestHandleTelegramCommandCallbackDurableWizardRejectsCodexChildCustom(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-codex-child-custom",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("bootstrap_profile", "child_custom"),
		Message: &telegram.Message{
			MessageID: 213,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_profile\nmissing: bootstrap_profile,autonomy\nbootstrap_backend: codex\nbootstrap_model: \n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardAction != "" {
		t.Fatalf("durableWizardAction = %q, want no wizard execution for invalid codex child_custom", router.durableWizardAction)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != staleDurableWizardCallbackText {
		t.Fatalf("answers = %#v, want stale durable wizard callback text", sender.answers)
	}
}

func TestHandleTelegramCommandCallbackDurableWizardRejectsStaleStep(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-stale",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("autonomy", "observe_only"),
		Message: &telegram.Message{
			MessageID: 211,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: adapter\nmissing: adapter,autonomy\naddress: child-endpoint\nadapter: \nautonomy: \n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardAction != "" {
		t.Fatalf("durableWizardAction = %q, want no wizard execution for stale callback", router.durableWizardAction)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if sender.answers[0].text != staleDurableWizardCallbackText {
		t.Fatalf("answer text = %q, want stale wizard callback warning", sender.answers[0].text)
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline count = %d, want 0 for stale callback", len(sender.editInline))
	}
}
