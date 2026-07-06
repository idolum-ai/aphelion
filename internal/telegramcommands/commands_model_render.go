//go:build linux

package telegramcommands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func renderModelSlotStatuses(statuses []core.ModelSlotStatus) string {
	if len(statuses) == 0 {
		return renderTelegramCompactPanel(face.OperatorPanel{
			Title: "Models",
			State: "unavailable",
			Why:   "No model slot status was returned by the runtime.",
			Next:  "Run /health diagnose or check config if this persists.",
		}, false)
	}
	details := make([]string, 0, len(statuses))
	evidence := make([]string, 0, len(statuses)*2)
	for _, status := range statuses {
		line := modelSlotTitle(status.Slot) + ": " + renderModelSlotConfig(status.Effective)
		line += " from " + firstNonEmptyModelUI(status.Source, "default")
		details = append(details, line)
		if !status.Validation.Valid {
			evidence = append(evidence, modelSlotTitle(status.Slot)+" invalid: "+trimTelegramModelError(status.Validation.Error))
		} else if status.Validation.ResolvedTransport != "" {
			evidence = append(evidence, "Transport: "+status.Validation.ResolvedTransport)
		}
		if len(status.Validation.Warnings) > 0 {
			evidence = append(evidence, modelSlotTitle(status.Slot)+" warning: "+strings.Join(status.Validation.Warnings, "; "))
		}
	}
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:    "Models",
		State:    fmt.Sprintf("%d slot(s) configured", len(statuses)),
		Why:      "Model slots control which backend handles each kind of runtime work.",
		Next:     "Open a slot button, or use /model set <slot> <provider/model> effort=<low|medium|high|xhigh> speed=<standard|fast>. Clear restores this install's default.",
		Details:  details,
		Evidence: evidence,
	}, false)
}

func renderModelSlotStatusPanel(statuses []core.ModelSlotStatus) (string, [][]telegram.InlineButton) {
	return renderModelSlotStatuses(statuses), renderModelStatusRows()
}

func renderModelStatusRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			{Text: "Persona", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotPersona, "")},
			{Text: "Main", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotGovernor, "")},
		},
		{
			{Text: "Health", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotDoctor, "")},
			{Text: "Children", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotChildDefault, "")},
		},
		{
			{Text: "Status", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotStatusReadable, "")},
			{Text: "Heartbeat", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotHeartbeat, "")},
			{Text: "Curiosity", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotCuriosity, "")},
		},
		{
			{Text: "Refresh", CallbackData: encodeModelCallbackData(modelCallbackStatus, "", "")},
		},
	}
}

func renderModelSlotDetail(status core.ModelSlotStatus) string {
	details := []string{
		"Current: " + renderModelSlotProvider(status.Effective),
		"Thinking: " + firstNonEmptyModelUI(status.Effective.Effort, "default"),
		"Speed: " + renderModelSlotSpeed(status.Effective),
		"Source: " + firstNonEmptyModelUI(status.Source, "default"),
	}
	if status.Reason != "" {
		details = append(details, "Reason: "+status.Reason)
	}
	details = append(details, "Default: "+renderModelSlotConfig(status.Default))
	evidence := make([]string, 0, 2)
	state := "ready"
	if status.Validation.Valid {
		if status.Validation.ResolvedTransport != "" {
			evidence = append(evidence, "Transport: "+status.Validation.ResolvedTransport)
		}
	} else {
		state = "invalid"
		evidence = append(evidence, "Invalid: "+trimTelegramModelError(status.Validation.Error))
	}
	if len(status.Validation.Warnings) > 0 {
		evidence = append(evidence, "Warning: "+strings.Join(status.Validation.Warnings, "; "))
	}
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:    modelSlotTitle(status.Slot),
		State:    state,
		Why:      "This slot determines the backend used for its runtime role.",
		Next:     "Choose a preset, thinking level, speed when available, or clear the override to restore this install's default.",
		Details:  details,
		Evidence: evidence,
	}, false)
}

func renderModelSlotRows(status core.ModelSlotStatus) [][]telegram.InlineButton {
	slot := core.NormalizeModelSlot(status.Slot)
	effortRow := []telegram.InlineButton{
		{Text: "Low", CallbackData: encodeModelCallbackData(modelCallbackEffort, slot, "low")},
		{Text: "Medium", CallbackData: encodeModelCallbackData(modelCallbackEffort, slot, "medium")},
		{Text: "High", CallbackData: encodeModelCallbackData(modelCallbackEffort, slot, "high")},
	}
	if !hideModelSlotMaxEffort(status) {
		effortRow = append(effortRow, telegram.InlineButton{Text: "Max", CallbackData: encodeModelCallbackData(modelCallbackEffort, slot, "xhigh")})
	}
	rows := [][]telegram.InlineButton{
		{
			{Text: "Sonnet", CallbackData: encodeModelCallbackData(modelCallbackPreset, slot, "sonnet")},
			{Text: "Opus 4.8", CallbackData: encodeModelCallbackData(modelCallbackPreset, slot, "opus48")},
			{Text: modelGPT55PresetLabel(slot), CallbackData: encodeModelCallbackData(modelCallbackPreset, slot, "gpt55")},
		},
		effortRow,
	}
	if modelSlotSupportsCheapPreset(slot) {
		rows = append([][]telegram.InlineButton{{
			{Text: "Cheap", CallbackData: encodeModelCallbackData(modelCallbackPreset, slot, "cheap")},
		}}, rows...)
	}
	if core.NormalizeModelProvider(status.Effective.Provider) == core.ModelProviderOpenAI {
		rows = append(rows, []telegram.InlineButton{
			{Text: "Standard", CallbackData: encodeModelCallbackData(modelCallbackSpeed, slot, "standard")},
			{Text: "Fast", CallbackData: encodeModelCallbackData(modelCallbackSpeed, slot, "fast")},
		})
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "Changes", CallbackData: encodeModelCallbackData(modelCallbackChanges, slot, "")},
		{Text: "Refresh", CallbackData: encodeModelCallbackData(modelCallbackSlot, slot, "")},
		{Text: "All", CallbackData: encodeModelCallbackData(modelCallbackStatus, "", "")},
	})
	if strings.EqualFold(strings.TrimSpace(status.Source), "override") {
		rows = append(rows, []telegram.InlineButton{
			{Text: "Clear", CallbackData: encodeModelCallbackData(modelCallbackClear, slot, "")},
		})
	}
	return rows
}

func modelGPT55PresetLabel(slot string) string {
	if core.NormalizeModelSlot(slot) == core.ModelSlotDoctor {
		return "Codex GPT-5.5"
	}
	return "GPT-5.5"
}

func hideModelSlotMaxEffort(status core.ModelSlotStatus) bool {
	return core.NormalizeModelSlot(status.Slot) == core.ModelSlotDoctor &&
		core.NormalizeModelProvider(status.Effective.Provider) == core.ModelProviderOpenAI
}

func renderModelChangesRows(slot string) [][]telegram.InlineButton {
	slot = core.NormalizeModelSlot(slot)
	if slot == "" {
		return [][]telegram.InlineButton{{
			{Text: "All", CallbackData: encodeModelCallbackData(modelCallbackStatus, "", "")},
			{Text: "Refresh", CallbackData: encodeModelCallbackData(modelCallbackChanges, "", "")},
		}}
	}
	return [][]telegram.InlineButton{
		{
			{Text: modelSlotTitle(slot), CallbackData: encodeModelCallbackData(modelCallbackSlot, slot, "")},
			{Text: "Refresh", CallbackData: encodeModelCallbackData(modelCallbackChanges, slot, "")},
			{Text: "All", CallbackData: encodeModelCallbackData(modelCallbackStatus, "", "")},
		},
	}
}

func renderModelSlotValidation(validation core.ModelValidation) string {
	state := "valid"
	details := make([]string, 0, 3)
	evidence := make([]string, 0, 2)
	if validation.Valid {
		details = append(details, renderModelSlotConfig(validation.Config))
		if validation.ResolvedTransport != "" {
			evidence = append(evidence, "Transport: "+validation.ResolvedTransport)
		}
	} else {
		state = "invalid"
		evidence = append(evidence, trimTelegramModelError(validation.Error))
	}
	if len(validation.Warnings) > 0 {
		evidence = append(evidence, "Warning: "+strings.Join(validation.Warnings, "; "))
	}
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:    "Model validation",
		State:    state,
		Why:      "Validation checks whether the selected provider, model, effort, and transport can be used.",
		Next:     "Use /model set with the same values if the config is valid.",
		Details:  details,
		Evidence: evidence,
	}, false)
}

func renderModelSlotChange(prefix string, status core.ModelSlotStatus) string {
	details := []string{
		"Effective: " + renderModelSlotConfig(status.Effective),
		"Source: " + firstNonEmptyModelUI(status.Source, "default"),
	}
	evidence := make([]string, 0, 1)
	if status.Validation.ResolvedTransport != "" {
		evidence = append(evidence, "Transport: "+status.Validation.ResolvedTransport)
	}
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:    modelSlotTitle(status.Slot),
		State:    strings.ToLower(strings.TrimSpace(prefix)),
		Why:      "The runtime will use this effective model slot until it changes or is cleared.",
		Next:     "Use Changes to inspect edits, Clear when shown, or All to return.",
		Details:  details,
		Evidence: evidence,
	}, false)
}

func renderModelSlotChanges(records []session.ModelSlotOverrideRecord) string {
	if len(records) == 0 {
		return renderTelegramCompactPanel(face.OperatorPanel{
			Title: "Model changes",
			State: "empty",
			Next:  "Set or change a slot to create override records.",
		}, false)
	}
	details := make([]string, 0, len(records))
	for _, record := range records {
		line := strconv.FormatInt(record.ID, 10) + " " + modelSlotTitle(record.Slot) + " " + record.Status + ": " + renderModelSlotConfig(record.Config)
		if !record.CreatedAt.IsZero() {
			line += " at " + record.CreatedAt.UTC().Format("2006-01-02 15:04Z")
		}
		details = append(details, line)
	}
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:   "Model changes",
		State:   fmt.Sprintf("%d record(s)", len(records)),
		Why:     "Changes show operator edits to model-slot overrides.",
		Next:    "Return to the slot or all slots after inspection.",
		Details: details,
	}, false)
}

func renderModelSlotConfig(cfg core.ModelSlotConfig) string {
	cfg = core.NormalizeModelSlotConfig(cfg)
	var parts []string
	parts = append(parts, cfg.Provider+"/"+cfg.Model)
	if cfg.Effort != "" {
		parts = append(parts, "effort="+cfg.Effort)
	}
	if cfg.Transport != "" && cfg.Transport != core.ModelTransportAuto {
		parts = append(parts, "transport="+cfg.Transport)
	}
	if cfg.ServiceTier == core.ModelServiceTierPriority {
		parts = append(parts, "speed=fast")
	}
	if len(cfg.Fallbacks) > 0 {
		fallbacks := make([]string, 0, len(cfg.Fallbacks))
		for _, fallback := range cfg.Fallbacks {
			fallbacks = append(fallbacks, fallback.Provider+"/"+fallback.Model)
		}
		parts = append(parts, "fallbacks="+strings.Join(fallbacks, ","))
	}
	return strings.Join(parts, " ")
}

func renderModelSlotProvider(cfg core.ModelSlotConfig) string {
	cfg = core.NormalizeModelSlotConfig(cfg)
	return cfg.Provider + "/" + cfg.Model
}

func renderModelSlotSpeed(cfg core.ModelSlotConfig) string {
	cfg = core.NormalizeModelSlotConfig(cfg)
	if cfg.ServiceTier == core.ModelServiceTierPriority {
		return "fast"
	}
	if cfg.Provider == core.ModelProviderOpenAI {
		return "standard"
	}
	return "provider default"
}

func renderModelCommandHelp() string {
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title: "Model controls",
		State: "ready",
		Why:   "Model slots route runtime roles to configured providers and transports.",
		Next:  "Use /model status, then open a slot or set an override.",
		Details: []string{
			"/model status",
			"/model validate <slot> <provider/model> effort=high speed=fast transport=auto",
			"/model set <slot> <provider/model> effort=high speed=fast reason=why",
			"/model clear <slot>",
			"/model changes [slot] limit=8",
		},
		Evidence: []string{
			"Slots: persona, governor, doctor, child_default, status, heartbeat, curiosity",
			"Providers: openai, anthropic, openrouter, codex",
			"Defaults are role-specific and local to this Aphelion install.",
		},
	}, false)
}

func modelSlotTitle(slot string) string {
	switch core.NormalizeModelSlot(slot) {
	case core.ModelSlotPersona:
		return "Persona"
	case core.ModelSlotGovernor:
		return "Main"
	case core.ModelSlotDoctor:
		return "Health"
	case core.ModelSlotChildDefault:
		return "Children"
	case core.ModelSlotStatusReadable:
		return "Status"
	case core.ModelSlotHeartbeat:
		return "Heartbeat"
	case core.ModelSlotCuriosity:
		return "Curiosity"
	default:
		return strings.TrimSpace(slot)
	}
}

func modelSlotSupportsCheapPreset(slot string) bool {
	switch core.NormalizeModelSlot(slot) {
	case core.ModelSlotStatusReadable, core.ModelSlotHeartbeat, core.ModelSlotCuriosity:
		return true
	default:
		return false
	}
}

func trimTelegramModelError(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "unknown error"
	}
	return text
}

func clampTelegramModelText(text string) string {
	text = strings.TrimSpace(text)
	const limit = 4096
	if len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit-32]) + "\n[truncated]"
}

func firstNonEmptyModelUI(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
