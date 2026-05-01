//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) MissionCommand(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return "Mission Ledger is unavailable: session store is not configured.", nil
	}
	actor, ok := r.resolver.ResolveTelegramUser(senderID)
	if !ok {
		return "Mission Ledger is unavailable for this sender.", ErrPrincipalDenied
	}
	owner := missionCommandOwner(actor, senderID)
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	action, rest := nextMissionToken(args)
	if action == "" || action == "list" {
		return r.renderMissionCommandHome(key, owner)
	}
	switch action {
	case "show":
		missionID, _ := nextMissionToken(rest)
		if missionID == "" {
			return "Usage: /mission show <mission_id>", nil
		}
		mission, ok, err := r.store.Mission(missionID)
		if err != nil {
			return "", err
		}
		if !ok {
			return fmt.Sprintf("Mission %q not found.", missionID), nil
		}
		events, err := r.store.MissionEvents(mission.ID, 8)
		if err != nil {
			return "", err
		}
		return renderMissionCommandShow(mission, events), nil
	case "create":
		objective := strings.TrimSpace(rest)
		if objective == "" {
			return "Usage: /mission create <objective>", nil
		}
		mission, err := r.store.UpsertMission(session.MissionState{
			Objective: objective,
			Origin:    "user_explicit",
			Scope:     "principal",
			Owner:     owner,
			Status:    session.MissionStatusCandidate,
			Authority: session.DefaultMissionAuthority(),
			Decay:     session.DefaultMissionDecay(),
		}, owner, "created from /mission create")
		if err != nil {
			return "", err
		}
		return renderMissionCommandShow(mission, nil), nil
	case "pin", "unpin":
		missionID, _ := nextMissionToken(rest)
		if missionID == "" {
			return "Usage: /mission " + action + " <mission_id>", nil
		}
		mission, err := r.store.SetMissionPinned(missionID, action == "pin", owner, "/mission "+action)
		if err != nil {
			return "", err
		}
		return renderMissionCommandShow(mission, nil), nil
	case "activate", "active":
		return r.updateMissionCommandStatus(rest, session.MissionStatusActive, owner, "/mission activate")
	case "pause", "dormant":
		return r.updateMissionCommandStatus(rest, session.MissionStatusDormant, owner, "/mission pause")
	case "complete":
		return r.updateMissionCommandStatus(rest, session.MissionStatusCompleted, owner, "/mission complete")
	case "archive":
		return r.updateMissionCommandStatus(rest, session.MissionStatusArchived, owner, "/mission archive")
	case "block":
		missionID, reason := nextMissionToken(rest)
		if missionID == "" {
			return "Usage: /mission block <mission_id> <reason>", nil
		}
		mission, err := r.store.BlockMission(missionID, strings.TrimSpace(reason), owner)
		if err != nil {
			return "", err
		}
		return renderMissionCommandShow(mission, nil), nil
	case "summon":
		missions, err := r.store.SummonMissions(session.MissionFilter{Owner: owner, Limit: 50}, rest, 8)
		if err != nil {
			return "", err
		}
		return renderMissionCommandList("Mission summons", missions), nil
	case "health":
		if actor.Role != principal.RoleAdmin {
			return "Mission health is admin only.", nil
		}
		health, err := r.store.MissionLedgerHealth(time.Now().UTC())
		if err != nil {
			return "", err
		}
		return renderMissionCommandHealth(health), nil
	default:
		return "Usage: /mission [list|show|create|pin|unpin|activate|pause|block|complete|archive|summon|health]", nil
	}
}

func (r *Runtime) renderMissionCommandHome(key session.SessionKey, owner string) (string, error) {
	missions, err := r.store.Missions(session.MissionFilter{Owner: owner, Limit: 12})
	if err != nil {
		return "", err
	}
	working, err := r.store.WorkingObjective(key)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Mission Ledger\n")
	if strings.TrimSpace(working.Objective) != "" {
		fmt.Fprintf(&b, "working_objective: %s\n", working.Objective)
	} else {
		b.WriteString("working_objective: none\n")
	}
	b.WriteString(renderMissionCommandList("Missions", missions))
	b.WriteString("\n\nCommands: /mission create <objective>, /mission show <id>, /mission summon [context], /mission pin <id>, /mission archive <id>.")
	return strings.TrimSpace(b.String()), nil
}

func (r *Runtime) updateMissionCommandStatus(raw string, status session.MissionStatus, actor string, summary string) (string, error) {
	missionID, _ := nextMissionToken(raw)
	if missionID == "" {
		return "Usage: /mission <status> <mission_id>", nil
	}
	mission, err := r.store.UpdateMissionStatus(missionID, status, actor, summary)
	if err != nil {
		return "", err
	}
	return renderMissionCommandShow(mission, nil), nil
}

func missionCommandOwner(actor principal.Principal, senderID int64) string {
	if strings.TrimSpace(actor.DurableAgentID) != "" {
		return "durable_agent:" + strings.TrimSpace(actor.DurableAgentID)
	}
	if actor.TelegramUserID > 0 {
		return "telegram:" + strconv.FormatInt(actor.TelegramUserID, 10)
	}
	if senderID > 0 {
		return "telegram:" + strconv.FormatInt(senderID, 10)
	}
	return "system"
}

func nextMissionToken(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if idx := strings.IndexAny(raw, " \n\t"); idx >= 0 {
		return strings.ToLower(strings.TrimSpace(raw[:idx])), strings.TrimSpace(raw[idx+1:])
	}
	return strings.ToLower(raw), ""
}

func renderMissionCommandList(title string, missions []session.MissionState) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(title))
	b.WriteString("\n")
	if len(missions) == 0 {
		b.WriteString("- none")
		return b.String()
	}
	for _, mission := range missions {
		line := fmt.Sprintf("- %s [%s] %s", mission.ID, mission.Status, mission.Title)
		if mission.Pinned {
			line += " pinned=true"
		}
		if mission.Authority.CanSelfContinue {
			line += " self_continue=true"
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimSpace(b.String())
}

func renderMissionCommandShow(mission session.MissionState, events []session.MissionEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Mission %s\n", mission.ID)
	fmt.Fprintf(&b, "title: %s\n", mission.Title)
	fmt.Fprintf(&b, "objective: %s\n", mission.Objective)
	fmt.Fprintf(&b, "status: %s\n", mission.Status)
	fmt.Fprintf(&b, "pinned: %t\n", mission.Pinned)
	fmt.Fprintf(&b, "authority: self_summon=%t self_continue=%t requires_review=%t\n", mission.Authority.CanSelfSummon, mission.Authority.CanSelfContinue, mission.Authority.RequiresUserReview)
	if mission.BlockedReason != "" {
		fmt.Fprintf(&b, "blocked_reason: %s\n", mission.BlockedReason)
	}
	if len(mission.Tags) > 0 {
		fmt.Fprintf(&b, "tags: %s\n", strings.Join(mission.Tags, ","))
	}
	if len(events) > 0 {
		b.WriteString("events:\n")
		for _, event := range events {
			fmt.Fprintf(&b, "- %s %s %q\n", event.EventType, event.Actor, event.Summary)
		}
	}
	return strings.TrimSpace(b.String())
}

func renderMissionCommandHealth(health session.MissionLedgerHealth) string {
	return fmt.Sprintf("Mission Ledger health\nactive: %d\npinned: %d\nrecurring: %d\nblocked: %d\nself_continuation_enabled: %d\nstale_candidates: %d\npending_handoffs: %d", health.ActiveCount, health.PinnedCount, health.RecurringCount, health.BlockedCount, health.SelfContinuationEnabledCount, health.StaleCandidateCount, health.PendingHandoffCount)
}
