//go:build linux

package face

import (
	"fmt"
	"strings"
	"time"

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

type RestartAwakeNotice struct {
	StartedAtUTC      string
	InterruptedCount  int
	RecoveredCount    int
	CandidateMissions int
	ActiveMissions    int
	PendingHandoffs   int
	MemoryNote        string
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
	return renderTelegramCommandSurface("Assistant ready", "ready", "Send a message, or use /status when you need live controls.", personaEffort, governorEffort, includeAdminCommands)
}

func RenderTelegramHelp(personaEffort, governorEffort string, includeAdminCommands bool) string {
	return renderTelegramCommandSurface("Command help", "ready", "Pick the narrowest command for the job; normal messages still start ordinary work.", personaEffort, governorEffort, includeAdminCommands)
}

func renderTelegramCommandSurface(title string, state string, next string, personaEffort string, governorEffort string, includeAdminCommands bool) string {
	details := []string{
		"Chat control: /status - show live status and controls; /stop - stop current work; /new - start fresh; /detach - detach pending work",
		"Health: /health - show status, trace, and diagnosis controls; /tailnet - show tailnet status and controls",
		"Memory and objectives: /memory - review memory candidates and set focus; /mission - show and manage the Mission Ledger",
		"Models and agents: /model - show and change model slots; /agents - list durable agents and controls",
	}
	if includeAdminCommands {
		details = append(details,
			"Admin operations: /auto - show automation mode, approval, and limit controls; /restart - force an immediate gateway restart",
		)
	}
	details = append(details,
		"Maintenance requests: /reinstall - queue a rebuild/reinstall/restart request",
	)
	return RenderOperatorPanel(OperatorPanel{
		Title:   title,
		State:   state,
		Why:     "Telegram is the control link; CLI commands remain the maintenance surface.",
		Next:    next,
		Details: details,
		Evidence: []string{
			fmt.Sprintf("Current persona effort: %s", strings.TrimSpace(personaEffort)),
			fmt.Sprintf("Current system effort: %s", strings.TrimSpace(governorEffort)),
		},
	})
}

func RenderTelegramAutonomyStatus(snapshot core.AutonomyStatusSnapshot) string {
	liveChanges := "disabled"
	if snapshot.AllowLiveOverrides {
		liveChanges = "enabled"
	}
	activeOverride := "none"
	if mode := strings.TrimSpace(snapshot.ActiveOverrideMode); mode != "" {
		activeOverride = autonomyModeLabel(mode)
		if scope := strings.TrimSpace(snapshot.ActiveOverrideScope); scope != "" {
			activeOverride += " for " + scope
		}
		if !snapshot.ActiveOverrideExpiry.IsZero() {
			activeOverride += " until " + snapshot.ActiveOverrideExpiry.UTC().Format(time.RFC3339)
		}
	}
	behavior := strings.TrimSpace(snapshot.AuthorityBehavior)
	if behavior == "" {
		behavior = "approval grants require an open auto mode gate"
	}
	return RenderCompactOperatorPanel(OperatorPanel{
		Title: "Auto mode",
		State: "default " + autonomyModeLabel(snapshot.DefaultMode) + ", ceiling " + autonomyModeLabel(snapshot.Ceiling),
		Why:   behavior + ". This report does not grant new authority by itself.",
		Next:  "Use /auto mode leased <duration> <scope> to open a bounded gate, or /auto mode off to close one.",
		Details: []string{
			"Default: " + autonomyModeLabel(snapshot.DefaultMode),
			"Ceiling: " + autonomyModeLabel(snapshot.Ceiling),
			"Live changes: " + liveChanges,
			"Maximum live change: " + snapshot.MaxOverrideDuration.Truncate(time.Second).String(),
			"Active override: " + activeOverride,
			"Authority behavior: " + behavior + ".",
		},
	}, OperatorPanelCompactOptions{DetailLimit: 6, EvidenceLimit: 0})
}

func RenderTelegramAutoLimits(snapshot core.AutonomyStatusSnapshot) string {
	liveChanges := "disabled"
	if snapshot.AllowLiveOverrides {
		liveChanges = "enabled"
	}
	behavior := strings.TrimSpace(snapshot.AuthorityBehavior)
	if behavior == "" {
		behavior = "approval grants require an open auto mode gate"
	}
	return RenderCompactOperatorPanel(OperatorPanel{
		Title: "Auto limits",
		State: "default " + autonomyModeLabel(snapshot.DefaultMode) + ", ceiling " + autonomyModeLabel(snapshot.Ceiling),
		Why:   "Configured limits bound live mode changes. This panel is read-only.",
		Next:  "Use /auto mode for the live gate or /auto approvals for spendable prompt grants.",
		Details: []string{
			"Default: " + autonomyModeLabel(snapshot.DefaultMode),
			"Ceiling: " + autonomyModeLabel(snapshot.Ceiling),
			"Live changes: " + liveChanges,
			"Maximum live change: " + snapshot.MaxOverrideDuration.Truncate(time.Second).String(),
			"Authority behavior: " + behavior + ".",
		},
	}, OperatorPanelCompactOptions{DetailLimit: 5, EvidenceLimit: 0})
}

func autonomyModeLabel(mode string) string {
	switch strings.TrimSpace(mode) {
	case "off":
		return "Off"
	case "review_only":
		return "Review only"
	case "ask_first":
		return "Ask first"
	case "leased":
		return "Leased"
	case "mission":
		return "Mission"
	default:
		if strings.TrimSpace(mode) == "" {
			return "Ask first"
		}
		return strings.TrimSpace(mode)
	}
}

func RenderTelegramStop(stopped core.StopResult) string {
	continuationClause := renderStoppedContinuationClause(stopped)
	switch {
	case stopped.ActiveCanceled && stopped.QueuedDropped && stopped.ContinuationRevoked:
		return "Stopped the current turn, cleared queued work, and " + continuationClause + "."
	case stopped.ActiveCanceled && stopped.ContinuationRevoked:
		return "Stopped the current turn and " + continuationClause + "."
	case stopped.QueuedDropped && stopped.ContinuationRevoked:
		return "Cleared queued work and " + continuationClause + "."
	case stopped.ContinuationRevoked:
		return capitalizeStopSentence(continuationClause) + "."
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

func renderStoppedContinuationClause(stopped core.StopResult) string {
	label := strings.TrimSpace(stopped.ContinuationLabel)
	if label == "" {
		return "revoked continuation approval for this chat"
	}
	return "stopped " + label
}

func capitalizeStopSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
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
	return "Restarting the gateway now. Active work and continuation leases will be parked for startup recovery."
}

func RenderTelegramRestartDenied() string {
	return "Restart denied. Only Telegram admins can run /restart."
}

func RenderTelegramQueuedReinstall() string {
	return "Queued a reinstall request as a normal turn in this chat."
}
