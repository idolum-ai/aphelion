//go:build linux

package mission

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/face"
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
	owner := CommandOwner(actor, senderID)
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: TelegramDMScopeRef(chatID)}
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

func (r *Runtime) MissionHome(ctx context.Context, chatID int64, senderID int64) ([]session.MissionState, session.WorkingObjective, bool, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return nil, session.WorkingObjective{}, false, fmt.Errorf("Mission Ledger is unavailable: session store is not configured")
	}
	actor, owner, err := r.missionCommandActor(senderID)
	if err != nil {
		return nil, session.WorkingObjective{}, false, err
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: TelegramDMScopeRef(chatID)}
	missions, err := r.store.Missions(session.MissionFilter{Owner: owner, Limit: 12})
	if err != nil {
		return nil, session.WorkingObjective{}, false, err
	}
	working, err := r.store.WorkingObjective(key)
	if err != nil {
		return nil, session.WorkingObjective{}, false, err
	}
	return missions, working, actor.Role == principal.RoleAdmin, nil
}

func (r *Runtime) MissionDetails(ctx context.Context, chatID int64, senderID int64, missionID string) (session.MissionState, []session.MissionEvent, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.MissionState{}, nil, fmt.Errorf("Mission Ledger is unavailable: session store is not configured")
	}
	actor, owner, err := r.missionCommandActor(senderID)
	if err != nil {
		return session.MissionState{}, nil, err
	}
	mission, err := r.loadAuthorizedMission(missionID, actor, owner)
	if err != nil {
		return session.MissionState{}, nil, err
	}
	events, err := r.store.MissionEvents(mission.ID, 8)
	if err != nil {
		return session.MissionState{}, nil, err
	}
	_ = chatID
	return mission, events, nil
}

func (r *Runtime) SetMissionPinned(ctx context.Context, chatID int64, senderID int64, missionID string, pinned bool) (session.MissionState, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.MissionState{}, fmt.Errorf("Mission Ledger is unavailable: session store is not configured")
	}
	actor, owner, err := r.missionCommandActor(senderID)
	if err != nil {
		return session.MissionState{}, err
	}
	if _, err := r.loadAuthorizedMission(missionID, actor, owner); err != nil {
		return session.MissionState{}, err
	}
	_ = chatID
	return r.store.SetMissionPinned(missionID, pinned, owner, "/mission button pin")
}

func (r *Runtime) UpdateMissionStatus(ctx context.Context, chatID int64, senderID int64, missionID string, status session.MissionStatus) (session.MissionState, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.MissionState{}, fmt.Errorf("Mission Ledger is unavailable: session store is not configured")
	}
	actor, owner, err := r.missionCommandActor(senderID)
	if err != nil {
		return session.MissionState{}, err
	}
	if _, err := r.loadAuthorizedMission(missionID, actor, owner); err != nil {
		return session.MissionState{}, err
	}
	status = session.NormalizeMissionStatus(status)
	if status == "" {
		return session.MissionState{}, fmt.Errorf("invalid mission status")
	}
	_ = chatID
	return r.store.UpdateMissionStatus(missionID, status, owner, "/mission button "+string(status))
}

func (r *Runtime) MissionLedgerHealth(ctx context.Context, senderID int64) (session.MissionLedgerHealth, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.MissionLedgerHealth{}, fmt.Errorf("Mission Ledger is unavailable: session store is not configured")
	}
	actor, _, err := r.missionCommandActor(senderID)
	if err != nil {
		return session.MissionLedgerHealth{}, err
	}
	if actor.Role != principal.RoleAdmin {
		return session.MissionLedgerHealth{}, fmt.Errorf("mission health is admin only")
	}
	return r.store.MissionLedgerHealth(time.Now().UTC())
}

func (r *Runtime) missionCommandActor(senderID int64) (principal.Principal, string, error) {
	if r == nil || r.resolver == nil {
		return principal.Principal{}, "", fmt.Errorf("Mission Ledger is unavailable for this sender")
	}
	actor, ok := r.resolver.ResolveTelegramUser(senderID)
	if !ok {
		return principal.Principal{}, "", ErrPrincipalDenied
	}
	return actor, CommandOwner(actor, senderID), nil
}

func (r *Runtime) loadAuthorizedMission(missionID string, actor principal.Principal, owner string) (session.MissionState, error) {
	missionID = strings.TrimSpace(missionID)
	if missionID == "" {
		return session.MissionState{}, fmt.Errorf("mission id is required")
	}
	mission, ok, err := r.store.Mission(missionID)
	if err != nil {
		return session.MissionState{}, err
	}
	if !ok {
		return session.MissionState{}, fmt.Errorf("mission %q not found", missionID)
	}
	if actor.Role != principal.RoleAdmin && strings.TrimSpace(mission.Owner) != strings.TrimSpace(owner) {
		return session.MissionState{}, fmt.Errorf("mission %q is not owned by this sender", missionID)
	}
	return mission, nil
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
	details := make([]string, 0, 3)
	if strings.TrimSpace(working.Objective) != "" {
		details = append(details, "Working objective: "+working.Objective)
	} else {
		details = append(details, "Working objective: none")
	}
	details = append(details, renderMissionCommandListLines(missions)...)
	b.WriteString(renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Mission Ledger",
		State:   fmt.Sprintf("%d mission(s)", len(missions)),
		Why:     "Missions organize long-running intent but do not grant self-continuation or new capability by themselves.",
		Next:    "Use /mission create <objective>, /mission show <id>, /mission propose <id>, /mission summon [context], /mission pin <id>, or /mission archive <id>.",
		Details: details,
	}))
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

func commandOwnerLegacy(actor principal.Principal, senderID int64) string {
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
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   strings.TrimSpace(title),
		State:   fmt.Sprintf("%d mission(s)", len(missions)),
		Why:     "Mission matches are review context, not automatic authority.",
		Next:    "Show a mission for details or propose a bounded action.",
		Details: renderMissionCommandListLines(missions),
	})
}

func renderMissionCommandListLines(missions []session.MissionState) []string {
	if len(missions) == 0 {
		return []string{"No missions found."}
	}
	lines := make([]string, 0, len(missions))
	for _, mission := range missions {
		title := firstNonEmptyRuntime(strings.TrimSpace(mission.Title), strings.TrimSpace(mission.Objective), strings.TrimSpace(mission.ID))
		line := fmt.Sprintf("%s: %s", mission.ID, title)
		if status := strings.TrimSpace(string(mission.Status)); status != "" {
			line += " (" + status + ")"
		}
		flags := make([]string, 0, 2)
		if mission.Pinned {
			flags = append(flags, "pinned")
		}
		if mission.Authority.CanSelfContinue {
			flags = append(flags, "self-continuation enabled")
		}
		if len(flags) > 0 {
			line += "; " + strings.Join(flags, ", ")
		}
		lines = append(lines, line)
	}
	return lines
}

func renderMissionCommandShow(mission session.MissionState, events []session.MissionEvent) string {
	details := []string{
		"Title: " + firstNonEmptyRuntime(strings.TrimSpace(mission.Title), strings.TrimSpace(mission.ID)),
		"Objective: " + firstNonEmptyRuntime(strings.TrimSpace(mission.Objective), "-"),
		"Pinned: " + boolWord(mission.Pinned),
	}
	authority := []string{
		"Self-summon: " + boolWord(mission.Authority.CanSelfSummon),
		"Self-continuation: " + boolWord(mission.Authority.CanSelfContinue),
		"Requires review: " + boolWord(mission.Authority.RequiresUserReview),
	}
	if mission.BlockedReason != "" {
		details = append(details, "Blocked: "+mission.BlockedReason)
	}
	if len(mission.Tags) > 0 {
		details = append(details, "Tags: "+strings.Join(mission.Tags, ", "))
	}
	evidence := make([]string, 0, len(events)+len(authority))
	evidence = append(evidence, authority...)
	if len(events) > 0 {
		for _, event := range events {
			evidence = append(evidence, strings.TrimSpace(event.EventType)+" by "+strings.TrimSpace(event.Actor)+": "+strings.TrimSpace(event.Summary))
		}
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:    "Mission " + strings.TrimSpace(mission.ID),
		State:    firstNonEmptyRuntime(strings.TrimSpace(string(mission.Status)), "unknown"),
		Why:      "Mission state is context, not action. Anything that touches the world still needs your approval.",
		Next:     "Use /mission propose <id> for a bounded action, or update status with activate, pause, block, complete, or archive.",
		Details:  details,
		Evidence: evidence,
	})
}

func renderMissionCommandHealth(health session.MissionLedgerHealth) string {
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title: "Mission Ledger health",
		State: fmt.Sprintf("%d active, %d blocked", health.ActiveCount, health.BlockedCount),
		Why:   "Health highlights ledger state that may need review or cleanup.",
		Next:  "Inspect blocked missions or stale candidates before relying on the ledger.",
		Evidence: []string{
			fmt.Sprintf("Active: %d", health.ActiveCount),
			fmt.Sprintf("Pinned: %d", health.PinnedCount),
			fmt.Sprintf("Recurring: %d", health.RecurringCount),
			fmt.Sprintf("Blocked: %d", health.BlockedCount),
			fmt.Sprintf("Self-continuation enabled: %d", health.SelfContinuationEnabledCount),
			fmt.Sprintf("Stale candidates: %d", health.StaleCandidateCount),
			fmt.Sprintf("Pending handoffs: %d", health.PendingHandoffCount),
		},
	})
}

func boolWord(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func firstNonEmptyRuntime(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
