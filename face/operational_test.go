//go:build linux

package face

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestRenderTelegramStopUsesContinuationLabel(t *testing.T) {
	t.Parallel()

	got := RenderTelegramStop(core.StopResult{
		ContinuationRevoked: true,
		ContinuationLabel:   "Plan: Resource-Owner Assistant (Phase J1)",
	})
	if got != "Stopped Plan: Resource-Owner Assistant (Phase J1)." {
		t.Fatalf("RenderTelegramStop() = %q, want labeled stop message", got)
	}
}

func TestRenderTelegramStopUsesGenericRevokeMessageWithoutContinuationLabel(t *testing.T) {
	t.Parallel()

	got := RenderTelegramStop(core.StopResult{ContinuationRevoked: true})
	if got != "Revoked continuation approval for this chat." {
		t.Fatalf("RenderTelegramStop() = %q, want generic revoke message", got)
	}
}

func TestRenderTelegramAutonomyStatusUsesNaturalLabels(t *testing.T) {
	t.Parallel()

	out := RenderTelegramAutonomyStatus(core.AutonomyStatusSnapshot{
		DefaultMode:         "ask_first",
		Ceiling:             "leased",
		AllowLiveOverrides:  true,
		MaxOverrideDuration: 2 * time.Hour,
		AuthorityBehavior:   "approvals require an open auto-mode window",
	})
	for _, want := range []string{
		"Auto mode",
		"Default: Ask first",
		"Ceiling: Leased",
		"Live changes: enabled",
		"Authority behavior: approvals require an open auto-mode window.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderTelegramAutonomyStatus() = %q, want %q", out, want)
		}
	}
}

func TestRenderTelegramOperationalCopyUsesNeutralPersonaLabels(t *testing.T) {
	t.Parallel()

	outputs := []string{
		RenderTelegramStart("medium", "high", true),
		RenderTelegramHelp("medium", "high", true),
	}
	for _, out := range outputs {
		if strings.Contains(out, "Idolum") {
			t.Fatalf("operational copy exposes default face name: %q", out)
		}
	}
	if strings.Contains(outputs[0], "/set_persona_model") || strings.Contains(outputs[0], "/set_governor_effort") {
		t.Fatalf("start copy includes retired model selector commands: %q", outputs[0])
	}
}

func TestRenderReviewDigestFormatsDurableSections(t *testing.T) {
	out := RenderReviewDigest(ReviewDigestNotice{
		SourceRole:  "durable_agent",
		SourceScope: "durable_agent:mail-child",
		SourceAgent: "mail-child",
		ParentScope: "telegram_dm:6313146",
		Summary: strings.Join([]string{
			"durable_agent=mail-child channel=external_channel parent=telegram_dm:6313146 interval=2026-04-26T22:33:00Z",
			"summary: cannot verify live inbox access until mailbox adapter grants materialize.",
			"local: Read profile/growth.md.; Ran connection_test.",
			"questions: Can the parent materialize mailbox adapter access?",
			"risks: parent_conversation_sync",
		}, "\n"),
	})

	for _, needle := range []string{
		"**Review: mail-child**",
		"external channel • 2026-04-26T22:33:00Z",
		"**Summary**",
		"cannot verify live inbox access",
		"**Checked**",
		"- Read profile/growth.md.",
		"- Ran connection_test.",
		"**Needs attention**",
		"- Can the parent materialize mailbox adapter access?",
		"**Risks**",
		"- parent_conversation_sync",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderReviewDigest() = %q, want substring %q", out, needle)
		}
	}
	for _, unwanted := range []string{"Source Chat:", "Source User:", "Source Role:", "durable_agent=", "parent=telegram_dm:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("RenderReviewDigest() = %q, should not contain raw label %q", out, unwanted)
		}
	}
}

func TestRenderReviewDigestExtractsInlineSummaryHighlights(t *testing.T) {
	out := RenderReviewDigest(ReviewDigestNotice{
		SourceRole:  "durable_agent",
		SourceScope: "durable_agent:idolum-daily-review",
		SourceAgent: "idolum-daily-review",
		ParentScope: "heartbeat:admin-house",
		Summary: strings.Join([]string{
			"durable_agent=idolum-daily-review channel=scheduled_review parent=heartbeat:admin-house interval=2026-04-26",
			"summary: Scheduled check-in from child for 2026-04-26. What matters: - Read the 2026-04-26 transcript: 420 staged entries. - Read the daily-review child profile files requested by parent guidance: - profile/growth.md - profile/capability-ledger.md - profile/scorecard.md - Current daily-review child capability ledger says no active grants. - What worked yesterday: - Daily review recovered: the 2026-04-25 review ran and summarized the prior day. - Semantic/memory cleanup made strong progress: - generated reports/semantic-quarantine-review.pdf",
			"local: Reviewed staged transcript for 2026-04-26 and drafted next-day actions.",
			"questions: What guidance should I apply before the next daily check-in?",
			"risks: scheduled_check_in",
		}, "\n"),
	})

	for _, needle := range []string{
		"**Daily review**",
		"Daily review • 2026-04-26",
		"**Summary**",
		"Scheduled check-in from child for 2026-04-26.",
		"**Highlights**",
		"- Read the 2026-04-26 transcript: 420 staged entries.",
		"- Current daily-review child capability ledger says no active grants.",
		"- What worked yesterday: Daily review recovered",
		"**Checked**",
		"- Reviewed staged transcript for 2026-04-26 and drafted next-day actions.",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderReviewDigest() = %q, want substring %q", out, needle)
		}
	}
	for _, unwanted := range []string{
		"durable_agent=",
		"parent=heartbeat:",
		"What matters: -",
		"- profile/growth.md",
		"- profile/capability-ledger.md",
		"- profile/scorecard.md",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("RenderReviewDigest() = %q, should not contain %q", out, unwanted)
		}
	}
}

func TestRenderReviewDigestKeepsSimpleReviewCompact(t *testing.T) {
	out := RenderReviewDigest(ReviewDigestNotice{
		SourceChatID: 7001,
		SourceUserID: 44,
		SourceRole:   "approved_user",
		TurnRange:    "1-3",
		Summary:      "user requested package install in isolated workspace",
	})

	for _, needle := range []string{
		"**Review: approved user**",
		"`turns=1-3 chat=7001 user=44 role=approved_user`",
		"**Summary**",
		"user requested package install in isolated workspace",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderReviewDigest() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderRestartAwakeFormatsOperatorCard(t *testing.T) {
	out := RenderRestartAwake(RestartAwakeNotice{
		StartedAtUTC:      "2026-05-01T14:29:56Z",
		InterruptedCount:  0,
		CandidateMissions: 4,
		ActiveMissions:    1,
		PendingHandoffs:   2,
		MemoryNote:        "continuity loaded",
	})
	for _, needle := range []string{
		"Awake after restart",
		"14:29 UTC",
		"No interrupted work needed recovery.",
		"Continuity is loaded.",
		"Mission control: 4 candidates, 1 active, 2 handoffs pending.",
		"Needs attention: review 2 pending handoffs.",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderRestartAwake() = %q, want substring %q", out, needle)
		}
	}
	for _, raw := range []string{
		"started_at_utc",
		"startup_recovery",
		"pending_handoffs",
		"mission_control:",
		"memory:",
		"use /status or /health trace",
	} {
		if strings.Contains(out, raw) {
			t.Fatalf("RenderRestartAwake() = %q, want no raw field %q", out, raw)
		}
	}
}

func TestRenderRestartAwakeHealthyRestartNeedsNoAction(t *testing.T) {
	out := RenderRestartAwake(RestartAwakeNotice{
		StartedAtUTC:      "2026-05-01T14:29:56Z",
		InterruptedCount:  0,
		CandidateMissions: 1,
		ActiveMissions:    0,
		PendingHandoffs:   0,
		MemoryNote:        "continuity loaded; no recovery rows pending",
	})
	for _, needle := range []string{
		"Awake after restart",
		"14:29 UTC",
		"No interrupted work needed recovery.",
		"Continuity is loaded.",
		"Mission control: 1 candidate, none active.",
		"No action needed.",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderRestartAwake() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderRestartAwakeInterruptedRecoveryIsReadable(t *testing.T) {
	complete := RenderRestartAwake(RestartAwakeNotice{
		InterruptedCount: 2,
		RecoveredCount:   2,
	})
	if !strings.Contains(complete, "Recovered 2 interrupted turns.") {
		t.Fatalf("RenderRestartAwake() = %q, want complete recovery summary", complete)
	}
	if !strings.Contains(complete, "No action needed.") {
		t.Fatalf("RenderRestartAwake() = %q, want no action needed for complete recovery", complete)
	}

	partial := RenderRestartAwake(RestartAwakeNotice{
		InterruptedCount: 3,
		RecoveredCount:   2,
	})
	for _, needle := range []string{
		"Recovered 2 of 3 interrupted turns.",
		"Needs attention: startup recovery was incomplete.",
	} {
		if !strings.Contains(partial, needle) {
			t.Fatalf("RenderRestartAwake() = %q, want substring %q", partial, needle)
		}
	}
}

func TestRenderRestartAwakeMemoryNoteRepairsAndParkedApprovals(t *testing.T) {
	out := RenderRestartAwake(RestartAwakeNotice{
		MemoryNote: "continuity loaded; no recovery rows pending; invalid pending approvals repaired=2; parked_continuations: reoffered=1 approved_reoffered=2 expired_reoffered=1 failed=1",
	})
	for _, needle := range []string{
		"Continuity is loaded.",
		"Repaired 2 stale approvals.",
		"Re-offered 4 parked approvals.",
		"Could not re-offer 1 parked approval.",
		"Needs attention: parked approval resume had failures.",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderRestartAwake() = %q, want substring %q", out, needle)
		}
	}
	for _, raw := range []string{
		"invalid pending approvals repaired",
		"parked_continuations",
		"approved_reoffered",
	} {
		if strings.Contains(out, raw) {
			t.Fatalf("RenderRestartAwake() = %q, want no raw memory detail %q", out, raw)
		}
	}
}
