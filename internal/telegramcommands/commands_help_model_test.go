//go:build linux

package telegramcommands

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandHelpHidesAdminRestartForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaEffort:  "sonnet",
		governorEffort: "medium",
		canRestart:     false,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/help",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if strings.Contains(sender.inline[0].text, "/restart - ") {
		t.Fatalf("help text = %q, want admin-only /restart hidden for non-admins", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) == 0 {
		t.Fatalf("help rows empty, want command buttons")
	}
}

func TestHandleTelegramCommandHelpShowsAdminRestartForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaEffort:  "sonnet",
		governorEffort: "medium",
		canRestart:     true,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/help",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "/restart - ") {
		t.Fatalf("help text = %q, want admin /restart command listed", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "/health - ") {
		t.Fatalf("help text = %q, want /health command listed", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) == 0 {
		t.Fatalf("help rows empty, want command buttons")
	}
}

func TestHandleTelegramCommandStartHidesAdminRestartForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaEffort:  "sonnet",
		governorEffort: "medium",
		canRestart:     false,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/start",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if strings.Contains(sender.inline[0].text, "/restart - ") {
		t.Fatalf("start text = %q, want admin-only /restart hidden for non-admins", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "/health - ") {
		t.Fatalf("start text = %q, want /health command listed", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) == 0 {
		t.Fatalf("start rows empty, want command buttons")
	}
}

func TestHandleTelegramCommandModelStatus(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotGovernor,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderOpenAI,
				Model:     "gpt-5.5",
				Effort:    "high",
				Transport: core.ModelTransportAuto,
			},
			Source: "override",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportOpenAIResponses,
			},
		}},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 21,
		Text:      "/model status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Main: openai/gpt-5.5 effort=high") {
		t.Fatalf("model status text = %q", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Transport: responses") {
		t.Fatalf("model status text = %q, want resolved transport", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) == 0 || sender.inline[0].rows[0][0].CallbackData != "model:slot:p" {
		t.Fatalf("rows = %#v, want model slot buttons", sender.inline[0].rows)
	}
	labels := modelTestButtonLabels(sender.inline[0].rows)
	for _, want := range []string{"Status", "Heartbeat", "Curiosity"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("labels = %#v, want %s slot button", labels, want)
		}
	}
}

func TestHandleTelegramCommandModelSetParsesSlotConfig(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 22,
		Text:      "/model set governor openai/gpt-5.5 effort=max speed=fast reason=debug swap",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setModelSlotInput.Slot != core.ModelSlotGovernor {
		t.Fatalf("slot = %q, want governor", router.setModelSlotInput.Slot)
	}
	if router.setModelSlotInput.Provider != core.ModelProviderOpenAI || router.setModelSlotInput.Model != "gpt-5.5" {
		t.Fatalf("provider/model = %s/%s", router.setModelSlotInput.Provider, router.setModelSlotInput.Model)
	}
	if router.setModelSlotInput.Effort != "xhigh" {
		t.Fatalf("effort = %q, want xhigh", router.setModelSlotInput.Effort)
	}
	if router.setModelSlotInput.ServiceTier != core.ModelServiceTierPriority {
		t.Fatalf("service tier = %q, want priority", router.setModelSlotInput.ServiceTier)
	}
	if router.setModelSlotReason != "debug swap" {
		t.Fatalf("reason = %q, want debug swap", router.setModelSlotReason)
	}
}

func TestHandleTelegramCommandModelSetParsesCuriositySlotConfig(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 22,
		Text:      "/model set curiosity anthropic/claude-haiku-4-5-20251001 effort=low reason=cheap silent lane",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setModelSlotInput.Slot != core.ModelSlotCuriosity {
		t.Fatalf("slot = %q, want curiosity", router.setModelSlotInput.Slot)
	}
	if router.setModelSlotInput.Provider != core.ModelProviderAnthropic || router.setModelSlotInput.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("provider/model = %s/%s", router.setModelSlotInput.Provider, router.setModelSlotInput.Model)
	}
	if router.setModelSlotInput.Effort != "low" {
		t.Fatalf("effort = %q, want low", router.setModelSlotInput.Effort)
	}
}

func TestHandleTelegramCommandModelSetRejectsTTL(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 22,
		Text:      "/model set governor openai/gpt-5.5 ttl=2h",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "ttl is not a /model option") {
		t.Fatalf("message = %#v, want ttl rejection", sender.msgs)
	}
	if router.setModelSlotInput.Slot != "" {
		t.Fatalf("setModelSlotInput = %#v, want no mutation", router.setModelSlotInput)
	}
}

func TestHandleTelegramCommandModelValidateRejectsBadTransport(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		validateModelSlotReturn: core.ModelValidation{
			Valid: false,
			Config: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderOpenAI,
				Model:     "gpt-5.5",
				Effort:    "high",
				Transport: core.ModelTransportOpenAIChat,
			},
			Error: "openai gpt-5.5 with tools and effort requires responses transport",
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 23,
		Text:      "/model validate governor openai/gpt-5.5 effort=high transport=chat_completions",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if !strings.Contains(sender.msgs[0].Text, "requires responses transport") {
		t.Fatalf("validation text = %q", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandModelDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1002,
		MessageID: 24,
		Text:      "/model status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "admin only") {
		t.Fatalf("message = %#v, want admin denial", sender.msgs)
	}
}

func TestHandleTelegramCommandCallbackModelSlotDetail(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotGovernor,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderAnthropic,
				Model:     "claude-sonnet-4-6",
				Effort:    "medium",
				Transport: core.ModelTransportAuto,
			},
			Default: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderAnthropic,
				Model:     "claude-sonnet-4-6",
				Effort:    "medium",
				Transport: core.ModelTransportAuto,
			},
			Source: "default",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportAnthropicMessages,
			},
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-slot",
		Data: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotGovernor, ""),
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 31,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if !strings.Contains(sender.editInline[0].text, "Main") {
		t.Fatalf("edit text = %q, want Main detail", sender.editInline[0].text)
	}
	if len(sender.editInline[0].rows) < 3 {
		t.Fatalf("rows = %#v, want slot controls", sender.editInline[0].rows)
	}
}

func TestHandleTelegramCommandCallbackModelEffortSetsSlot(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotGovernor,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderAnthropic,
				Model:     "claude-sonnet-4-6",
				Effort:    "medium",
				Transport: core.ModelTransportAuto,
			},
			Source: "default",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportAnthropicMessages,
			},
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-effort",
		Data: encodeModelCallbackData(modelCallbackEffort, core.ModelSlotGovernor, "xhigh"),
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 32,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setModelSlotInput.Effort != "xhigh" {
		t.Fatalf("set effort = %q, want xhigh", router.setModelSlotInput.Effort)
	}
	if router.setModelSlotActor != "telegram:1001" {
		t.Fatalf("actor = %q, want telegram:1001", router.setModelSlotActor)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
}

func TestHandleTelegramCommandCallbackModelSpeedSetsOpenAIServiceTier(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotGovernor,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderOpenAI,
				Model:     "gpt-5.5",
				Effort:    "high",
				Transport: core.ModelTransportAuto,
			},
			Source: "override",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportOpenAIResponses,
			},
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-speed",
		Data: encodeModelCallbackData(modelCallbackSpeed, core.ModelSlotGovernor, "fast"),
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 32,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setModelSlotInput.ServiceTier != core.ModelServiceTierPriority {
		t.Fatalf("service tier = %q, want priority", router.setModelSlotInput.ServiceTier)
	}
	if router.setModelSlotReason != "telegram button: fast speed" {
		t.Fatalf("reason = %q, want fast speed", router.setModelSlotReason)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
}

func TestHandleTelegramCommandCallbackModelPresetDoctorGPTUsesCodex(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotDoctor,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotDoctor,
				Provider:  core.ModelProviderAnthropic,
				Model:     "claude-sonnet-4-6",
				Effort:    "xhigh",
				Transport: core.ModelTransportAuto,
			},
			Source: "default",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportAnthropicMessages,
			},
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-preset-doctor",
		Data: encodeModelCallbackData(modelCallbackPreset, core.ModelSlotDoctor, "gpt55"),
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 33,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setModelSlotInput.Slot != core.ModelSlotDoctor {
		t.Fatalf("slot = %q, want doctor", router.setModelSlotInput.Slot)
	}
	if router.setModelSlotInput.Provider != core.ModelProviderCodex || router.setModelSlotInput.Model != "gpt-5.5" {
		t.Fatalf("provider/model = %s/%s, want codex/gpt-5.5", router.setModelSlotInput.Provider, router.setModelSlotInput.Model)
	}
	if router.setModelSlotInput.Effort != "xhigh" {
		t.Fatalf("effort = %q, want inherited xhigh", router.setModelSlotInput.Effort)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
}

func TestHandleTelegramCommandCallbackModelPresetCheapUsesSlotDefault(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotStatusReadable,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotStatusReadable,
				Provider:  core.ModelProviderAnthropic,
				Model:     "claude-opus-4-8",
				Effort:    "high",
				Transport: core.ModelTransportAuto,
			},
			Default: core.ModelSlotConfig{
				Slot:      core.ModelSlotStatusReadable,
				Provider:  core.ModelProviderOpenAI,
				Model:     "gpt-5.4-mini",
				Effort:    "low",
				Transport: core.ModelTransportAuto,
			},
			Source: "override",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportAnthropicMessages,
			},
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-preset-cheap",
		Data: encodeModelCallbackData(modelCallbackPreset, core.ModelSlotStatusReadable, "cheap"),
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 34,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setModelSlotInput.Slot != core.ModelSlotStatusReadable {
		t.Fatalf("slot = %q, want status", router.setModelSlotInput.Slot)
	}
	if router.setModelSlotInput.Provider != core.ModelProviderOpenAI || router.setModelSlotInput.Model != "gpt-5.4-mini" || router.setModelSlotInput.Effort != "low" {
		t.Fatalf("set model slot = %#v, want cheap status default", router.setModelSlotInput)
	}
}

func TestRenderModelSlotRowsHidesMaxForDoctorDirectOpenAI(t *testing.T) {
	t.Parallel()

	rows := renderModelSlotRows(core.ModelSlotStatus{
		Slot: core.ModelSlotDoctor,
		Effective: core.ModelSlotConfig{
			Slot:     core.ModelSlotDoctor,
			Provider: core.ModelProviderOpenAI,
			Model:    "gpt-5.5",
			Effort:   "high",
		},
	})
	var labels []string
	for _, row := range rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	if !slices.Contains(labels, "Codex GPT-5.5") {
		t.Fatalf("labels = %#v, want doctor GPT preset labeled as Codex", labels)
	}
	if slices.Contains(labels, "Max") {
		t.Fatalf("labels = %#v, should hide Max for direct OpenAI doctor slot", labels)
	}
	if slices.Contains(labels, "Cheap") {
		t.Fatalf("labels = %#v, should not show Cheap for doctor slot", labels)
	}
	if !slices.Contains(labels, "Fast") || !slices.Contains(labels, "Standard") {
		t.Fatalf("labels = %#v, want OpenAI speed controls", labels)
	}
}

func TestRenderModelSlotRowsShowsCheapPresetForCheapLanes(t *testing.T) {
	t.Parallel()

	rows := renderModelSlotRows(core.ModelSlotStatus{
		Slot: core.ModelSlotStatusReadable,
		Effective: core.ModelSlotConfig{
			Slot:     core.ModelSlotStatusReadable,
			Provider: core.ModelProviderOpenAI,
			Model:    "gpt-5.4-mini",
			Effort:   "low",
		},
	})
	labels := modelTestButtonLabels(rows)
	if !slices.Contains(labels, "Cheap") {
		t.Fatalf("labels = %#v, want Cheap preset", labels)
	}
}

func TestRenderModelSlotRowsHidesSpeedForNonOpenAI(t *testing.T) {
	t.Parallel()

	rows := renderModelSlotRows(core.ModelSlotStatus{
		Slot: core.ModelSlotGovernor,
		Effective: core.ModelSlotConfig{
			Slot:     core.ModelSlotGovernor,
			Provider: core.ModelProviderAnthropic,
			Model:    "claude-sonnet-4-6",
			Effort:   "medium",
		},
	})
	var labels []string
	for _, row := range rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	if slices.Contains(labels, "Fast") || slices.Contains(labels, "Standard") {
		t.Fatalf("labels = %#v, want no speed controls for non-OpenAI slot", labels)
	}
}

func modelTestButtonLabels(rows [][]telegram.InlineButton) []string {
	var labels []string
	for _, row := range rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	return labels
}

func TestHandleTelegramCommandCallbackModelDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-denied",
		Data: encodeModelCallbackData(modelCallbackStatus, "", ""),
		From: &telegram.User{ID: 1002},
		Message: &telegram.Message{
			MessageID: 33,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "admin only") {
		t.Fatalf("answers = %#v, want admin denial", sender.answers)
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline count = %d, want 0", len(sender.editInline))
	}
}
