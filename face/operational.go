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

func RenderTelegramStart(personaEffort, governorEffort string, includeAdminCommands bool) string {
	lines := []string{
		"Idolum is here.",
		"",
		"Commands:",
		"/help - show command help",
		"/status - show live status and controls",
		"/debug - show detailed runtime debug snapshot",
		"/doctor - run an admin runtime diagnosis",
		"/agents - list durable agents and controls",
		"/memory - review memory candidates and set focus",
		"/stop - stop current work in this chat",
		"/new - start a fresh chat session context",
		"/detach - detach this chat from pending work",
	}
	if includeAdminCommands {
		lines = append(lines, "/restart - force an immediate gateway restart")
	}
	lines = append(lines,
		"/reinstall - queue a rebuild/reinstall/restart request",
		"/set_persona_model - choose Idolum model",
		"/set_governor_effort - choose system reasoning effort",
		"",
		fmt.Sprintf("Current persona effort: %s", strings.TrimSpace(personaEffort)),
		fmt.Sprintf("Current system effort: %s", strings.TrimSpace(governorEffort)),
	)
	return strings.Join(lines, "\n")
}

func RenderTelegramHelp(personaEffort, governorEffort string, includeAdminCommands bool) string {
	lines := []string{
		"Here is the current command surface.",
		"",
		"/start - show intro and command help",
		"/help - show this help",
		"/status - show live status and controls",
		"/debug - show detailed runtime debug snapshot",
		"/doctor - run an admin runtime diagnosis",
		"/agents - list durable agents and controls",
		"/memory - review memory candidates and set focus",
		"/stop - stop current work in this chat",
		"/new - start a fresh chat session context",
		"/detach - detach this chat from pending work",
	}
	if includeAdminCommands {
		lines = append(lines, "/restart - force an immediate gateway restart")
	}
	lines = append(lines,
		"/reinstall - queue a rebuild/reinstall/restart request",
		"/set_persona_model - choose Idolum model",
		"/set_governor_effort - choose system reasoning effort",
		"",
		fmt.Sprintf("Current persona effort: %s", strings.TrimSpace(personaEffort)),
		fmt.Sprintf("Current system effort: %s", strings.TrimSpace(governorEffort)),
	)
	return strings.Join(lines, "\n")
}

func RenderTelegramStatus(status core.SessionStatus, personaEffort, governorEffort string) string {
	state := "idle"
	if status.Active {
		state = "working"
	}
	lines := []string{fmt.Sprintf("Current state: %s.", state)}
	if status.Queued {
		lines = append(lines, "Queued follow-up messages are waiting behind the current turn.")
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
		fmt.Sprintf("System effort: %s.", strings.TrimSpace(governorEffort)),
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

func RenderTelegramNewSession(result core.NewSessionResult) string {
	parts := make([]string, 0, 5)
	if result.ActiveCanceled {
		parts = append(parts, "stopped current turn")
	}
	if result.QueuedDropped {
		parts = append(parts, "cleared queued work")
	}
	if result.ContinuationRevoked {
		parts = append(parts, "revoked continuation")
	}
	if result.PendingDecisionsDetached > 0 {
		label := "pending decision"
		if result.PendingDecisionsDetached != 1 {
			label += "s"
		}
		parts = append(parts, fmt.Sprintf("detached %d %s", result.PendingDecisionsDetached, label))
	}
	if result.ContextCleared {
		parts = append(parts, "cleared prior session context")
	} else {
		parts = append(parts, "session context was already clear")
	}
	return "Started a new session for this chat: " + strings.Join(parts, ", ") + ". Memories were not changed."
}

func RenderTelegramDetach(detached core.DetachResult) string {
	parts := make([]string, 0, 4)
	if detached.ActiveCanceled {
		parts = append(parts, "stopped current turn")
	}
	if detached.QueuedDropped {
		parts = append(parts, "cleared queued work")
	}
	if detached.ContinuationRevoked {
		parts = append(parts, "revoked continuation")
	}
	if detached.PendingDecisionsDetached > 0 {
		label := "pending decision"
		if detached.PendingDecisionsDetached != 1 {
			label += "s"
		}
		parts = append(parts, fmt.Sprintf("detached %d %s", detached.PendingDecisionsDetached, label))
	}
	if len(parts) == 0 {
		return "Nothing was pending to detach for this chat."
	}
	return "Detached this chat from pending work: " + strings.Join(parts, ", ") + "."
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
		"Select system reasoning effort for interactive/recovery turns.",
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
	return fmt.Sprintf("System effort is now %s.", effort)
}

func RenderReviewDigest(notice ReviewDigestNotice) string {
	lines := []string{"Review digest."}
	lines = append(lines,
		fmt.Sprintf("Source Chat: %d", notice.SourceChatID),
		fmt.Sprintf("Source User: %d", notice.SourceUserID),
		"Source Role: "+firstNonEmpty(strings.TrimSpace(notice.SourceRole), "-"),
	)
	if scope := strings.TrimSpace(notice.SourceScope); scope != "" {
		lines = append(lines, "Source Scope: "+scope)
	}
	if agent := strings.TrimSpace(notice.SourceAgent); agent != "" {
		lines = append(lines, "Source Agent: "+agent)
	}
	if parent := strings.TrimSpace(notice.ParentScope); parent != "" {
		lines = append(lines, "Parent Scope: "+parent)
	}
	if turns := strings.TrimSpace(notice.TurnRange); turns != "" {
		lines = append(lines, "Turns: "+turns)
	}
	if summary := strings.TrimSpace(notice.Summary); summary != "" {
		lines = append(lines, "", "Summary:", summary)
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
			lines = append(lines, fmt.Sprintf("- %s (%dx)", text, entry.Count))
		} else {
			lines = append(lines, "- "+text)
		}
	}
	return strings.Join(lines, "\n")
}
