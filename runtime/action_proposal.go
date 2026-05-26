//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) MissionActionProposal(ctx context.Context, chatID int64, senderID int64, missionID string) (session.ActionProposal, error) {
	_ = ctx
	missionID = strings.TrimSpace(missionID)
	if r == nil || r.store == nil {
		return session.ActionProposal{}, fmt.Errorf("runtime is unavailable")
	}
	if missionID == "" {
		return session.ActionProposal{}, fmt.Errorf("mission id is required")
	}
	if r.resolver == nil {
		return session.ActionProposal{}, fmt.Errorf("principal resolver is unavailable")
	}
	actor, ok := r.resolver.ResolveTelegramUser(senderID)
	if !ok {
		return session.ActionProposal{}, fmt.Errorf("mission proposal is unavailable for this sender")
	}
	mission, ok, err := r.store.Mission(missionID)
	if err != nil {
		return session.ActionProposal{}, err
	}
	if !ok {
		return session.ActionProposal{}, fmt.Errorf("mission %q not found", missionID)
	}
	owner := missionCommandOwner(actor, senderID)
	if actor.Role != principal.RoleAdmin && strings.TrimSpace(mission.Owner) != owner {
		return session.ActionProposal{}, fmt.Errorf("mission %q is not owned by this sender", missionID)
	}
	now := time.Now().UTC()
	proposal := session.ActionProposal{
		ID:               "aprop-" + strings.TrimSpace(mission.ID),
		MissionID:        strings.TrimSpace(mission.ID),
		Summary:          firstRuntimeNonEmpty(mission.NextAllowedAction, "Activate mission: "+mission.Title),
		WhyNow:           "Mission Control surfaced this candidate as review-only pending work.",
		BoundedEffect:    "If approved, mark the mission active for review/planning. This does not self-continue, run tools, or widen authority.",
		RiskClass:        "mission_control",
		AllowedActions:   []string{"mark_mission_active", "record_mission_event", "request_next_lease"},
		ForbiddenActions: []string{"self_continue_without_lease", "run_tools_without_separate_approval", "expand_authority_without_grant"},
		ValidationPlan:   []string{"record the approval decision", "keep mission authority review-only unless separately granted"},
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return session.NormalizeActionProposal(proposal), nil
}

func (r *Runtime) ApplyMissionActionProposalDecision(ctx context.Context, chatID int64, senderID int64, missionID string, choice string) (session.MissionState, bool, error) {
	_ = ctx
	missionID = strings.TrimSpace(missionID)
	choice = strings.TrimSpace(choice)
	if r == nil || r.store == nil {
		return session.MissionState{}, false, fmt.Errorf("runtime is unavailable")
	}
	if missionID == "" {
		return session.MissionState{}, false, fmt.Errorf("mission id is required")
	}
	if r.resolver == nil {
		return session.MissionState{}, false, fmt.Errorf("principal resolver is unavailable")
	}
	actor, ok := r.resolver.ResolveTelegramUser(senderID)
	if !ok {
		return session.MissionState{}, false, fmt.Errorf("mission proposal decision is unavailable for this sender")
	}
	mission, ok, err := r.store.Mission(missionID)
	if err != nil {
		return session.MissionState{}, false, err
	}
	if !ok {
		return session.MissionState{}, false, fmt.Errorf("mission %q not found", missionID)
	}
	owner := missionCommandOwner(actor, senderID)
	if actor.Role != principal.RoleAdmin && strings.TrimSpace(mission.Owner) != owner {
		return session.MissionState{}, false, fmt.Errorf("mission %q is not owned by this sender", missionID)
	}
	summary := "mission action proposal reviewed"
	changed := false
	switch choice {
	case "approve":
		mission.WaitingFor = ""
		if mission.Status == session.MissionStatusCandidate || mission.Status == session.MissionStatusDormant {
			mission.Status = session.MissionStatusActive
			changed = true
		}
		summary = "Mission proposal approved; mission marked active for review/planning"
	case "ask_edit", "ask-edit", "edit":
		mission.WaitingFor = "proposal_edit"
		changed = true
		summary = "Mission proposal needs change before approval"
	case "deny", "reject":
		mission.WaitingFor = "proposal_denied"
		changed = true
		summary = "Mission proposal rejected; mission left review-only"
	default:
		return session.MissionState{}, false, fmt.Errorf("unsupported action proposal choice %q", choice)
	}
	stored, err := r.store.UpsertMission(mission, owner, summary)
	if err != nil {
		return session.MissionState{}, false, err
	}
	return stored, changed, nil
}

func firstRuntimeNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
