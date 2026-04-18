//go:build linux

package face

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

type ReviewDigestNotice struct {
	SourceChatID int64
	SourceUserID int64
	SourceRole   string
	SourceScope  string
	SourceAgent  string
	ParentScope  string
	TurnRange    string
	Summary      string
}

type StartupRecoveryNotice struct {
	InterruptedCount  int
	MostRecentRequest string
	LastTool          string
	RecoverySummary   string
}

type ToolProgressEntry struct {
	Text  string
	Count int
}

type ToolProgressNotice struct {
	Omitted int
	Entries []ToolProgressEntry
}

func RenderTelegramStart(personaEffort, governorEffort string) string {
	return strings.Join([]string{
		"Idolum is here.",
		"",
		"Commands:",
		"/help - show command help",
		"/status - show whether I am currently working",
		"/stop - stop current work in this chat",
		"/restart - force an immediate gateway restart",
		"/reinstall - queue a rebuild/reinstall/restart request",
		"/set_persona_model - choose Idolum model",
		"/set_governor_effort - choose governor reasoning effort",
		"/toggle_persona_effort - quick-toggle Sonnet/Opus",
		"/toggle_governor_effort - quick-toggle medium/high",
		"",
		fmt.Sprintf("Current persona effort: %s", strings.TrimSpace(personaEffort)),
		fmt.Sprintf("Current governor effort: %s", strings.TrimSpace(governorEffort)),
	}, "\n")
}

func RenderTelegramHelp(personaEffort, governorEffort string) string {
	return strings.Join([]string{
		"Here is the current command surface.",
		"",
		"/start - show intro and command help",
		"/help - show this help",
		"/status - show current work state",
		"/stop - stop current work in this chat",
		"/restart - force an immediate gateway restart",
		"/reinstall - queue a rebuild/reinstall/restart request",
		"/set_persona_model - choose Idolum model",
		"/set_governor_effort - choose governor reasoning effort",
		"/toggle_persona_effort - quick-toggle Sonnet/Opus",
		"/toggle_governor_effort - quick-toggle medium/high",
		"",
		fmt.Sprintf("Current persona effort: %s", strings.TrimSpace(personaEffort)),
		fmt.Sprintf("Current governor effort: %s", strings.TrimSpace(governorEffort)),
	}, "\n")
}

func RenderTelegramStatus(status core.SessionStatus, personaEffort, governorEffort string) string {
	state := "idle"
	if status.Active {
		state = "working"
	}
	lines := []string{fmt.Sprintf("Current state: %s.", state)}
	if status.Queued {
		lines = append(lines, "A newer message is queued behind the current turn.")
	}
	for _, diagnostic := range status.Diagnostics {
		diagnostic = strings.TrimSpace(diagnostic)
		if diagnostic == "" {
			continue
		}
		lines = append(lines, diagnostic)
	}
	lines = append(lines,
		fmt.Sprintf("Persona effort: %s.", strings.TrimSpace(personaEffort)),
		fmt.Sprintf("Governor effort: %s.", strings.TrimSpace(governorEffort)),
	)
	return strings.Join(lines, "\n")
}

func RenderTelegramStop(stopped core.StopResult) string {
	switch {
	case stopped.ActiveCanceled && stopped.QueuedDropped && stopped.ContinuationRevoked:
		return "Stopped the current turn, cleared queued work, and revoked continuation approval for this chat."
	case stopped.ActiveCanceled && stopped.ContinuationRevoked:
		return "Stopped the current turn and revoked continuation approval for this chat."
	case stopped.QueuedDropped && stopped.ContinuationRevoked:
		return "Cleared queued work and revoked continuation approval for this chat."
	case stopped.ContinuationRevoked:
		return "Revoked continuation approval for this chat."
	case stopped.ActiveCanceled && stopped.QueuedDropped:
		return "Stopped the current turn and cleared queued work for this chat."
	case stopped.ActiveCanceled:
		return "Stopped the current turn."
	case stopped.QueuedDropped:
		return "Cleared queued work for this chat."
	default:
		return "Continuation approval was already inactive for this chat."
	}
}

func RenderTelegramRestart() string {
	return "Restarting the gateway now. Active and queued work will be dropped."
}

func RenderTelegramRestartDenied() string {
	return "Restart denied. Only Telegram admins can run /restart."
}

func RenderTelegramQueuedReinstall() string {
	return "Queued a reinstall request as a normal turn in this chat."
}

func RenderTelegramTogglePersona(mode string) string {
	mode = strings.TrimSpace(mode)
	return fmt.Sprintf("Idolum persona effort is now %s. Future rendered turns will use the %s recipe.", mode, titleCaseWord(mode))
}

func RenderTelegramToggleGovernor(mode string) string {
	mode = strings.TrimSpace(mode)
	return fmt.Sprintf("Governor effort is now %s. Future interactive turns will use %s reasoning.", mode, mode)
}

func RenderTelegramPersonaModelSelector(current string, options []string) string {
	lines := []string{
		"Select Idolum's persona model.",
		fmt.Sprintf("Current: %s", strings.TrimSpace(current)),
	}
	if len(options) > 0 {
		lines = append(lines, "", "Available:")
		for _, option := range options {
			option = strings.TrimSpace(option)
			if option == "" {
				continue
			}
			lines = append(lines, "- "+option)
		}
	}
	return strings.Join(lines, "\n")
}

func RenderTelegramGovernorEffortSelector(current string, options []string) string {
	lines := []string{
		"Select governor reasoning effort for interactive/recovery turns.",
		fmt.Sprintf("Current: %s", strings.TrimSpace(current)),
	}
	if len(options) > 0 {
		lines = append(lines, "", "Available:")
		for _, option := range options {
			option = strings.TrimSpace(option)
			if option == "" {
				continue
			}
			lines = append(lines, "- "+option)
		}
	}
	return strings.Join(lines, "\n")
}

func RenderTelegramSetPersonaModel(model string) string {
	model = strings.TrimSpace(model)
	return fmt.Sprintf("Idolum persona model is now %s.", model)
}

func RenderTelegramSetGovernorEffort(effort string) string {
	effort = strings.TrimSpace(effort)
	return fmt.Sprintf("Governor effort is now %s.", effort)
}

func RenderReviewDigest(notice ReviewDigestNotice) string {
	meta := []string{
		fmt.Sprintf("source_chat=%d", notice.SourceChatID),
		fmt.Sprintf("source_user=%d", notice.SourceUserID),
		"source_role=" + strings.TrimSpace(notice.SourceRole),
	}
	if scope := strings.TrimSpace(notice.SourceScope); scope != "" {
		meta = append(meta, "source_scope="+scope)
	}
	if agent := strings.TrimSpace(notice.SourceAgent); agent != "" {
		meta = append(meta, "source_agent="+agent)
	}
	if parent := strings.TrimSpace(notice.ParentScope); parent != "" {
		meta = append(meta, "parent_scope="+parent)
	}
	if turns := strings.TrimSpace(notice.TurnRange); turns != "" {
		meta = append(meta, "turns="+turns)
	}
	lines := []string{
		"Review digest.",
		strings.Join(meta, " "),
	}
	if summary := strings.TrimSpace(notice.Summary); summary != "" {
		lines = append(lines, "", summary)
	}
	return strings.Join(lines, "\n")
}

func RenderStartupRecovery(notice StartupRecoveryNotice) string {
	parts := []string{"Restart catch-up."}
	if notice.InterruptedCount == 1 {
		parts = append(parts, "I recovered 1 interrupted turn.")
	} else {
		parts = append(parts, fmt.Sprintf("I recovered %d interrupted turns.", notice.InterruptedCount))
	}
	if request := strings.TrimSpace(notice.MostRecentRequest); request != "" {
		parts = append(parts, "Most recent interrupted request: "+fmt.Sprintf("%q", request)+".")
	}
	if tool := strings.TrimSpace(notice.LastTool); tool != "" {
		parts = append(parts, "Last tool in flight: "+tool+".")
	}
	if summary := strings.TrimSpace(notice.RecoverySummary); summary != "" {
		parts = append(parts, "Recovery note: "+summary)
	}
	parts = append(parts, "Next: investigate the interruption before returning to deferred work.")
	return strings.Join(parts, " ")
}

func RenderToolProgress(notice ToolProgressNotice) string {
	lines := []string{"Working on it."}
	if notice.Omitted > 0 {
		lines = append(lines, fmt.Sprintf("%d earlier steps omitted.", notice.Omitted))
	}
	for _, entry := range notice.Entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		if entry.Count > 1 {
			lines = append(lines, fmt.Sprintf("- %s ×%d", text, entry.Count))
		} else {
			lines = append(lines, "- "+text)
		}
	}
	return strings.Join(lines, "\n")
}

func titleCaseWord(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + strings.ToLower(value[1:])
}
