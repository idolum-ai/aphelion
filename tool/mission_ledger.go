//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const missionLedgerToolName = "mission_ledger"

type missionLedgerInput struct {
	Action             string                        `json:"action"`
	MissionID          string                        `json:"mission_id,omitempty"`
	Title              string                        `json:"title,omitempty"`
	Objective          string                        `json:"objective,omitempty"`
	Origin             string                        `json:"origin,omitempty"`
	Scope              string                        `json:"scope,omitempty"`
	Owner              string                        `json:"owner,omitempty"`
	Status             string                        `json:"status,omitempty"`
	Pinned             *bool                         `json:"pinned,omitempty"`
	Tags               []string                      `json:"tags,omitempty"`
	SourceRefs         []string                      `json:"source_refs,omitempty"`
	SuccessCriteria    []string                      `json:"success_criteria,omitempty"`
	Evidence           []session.MissionEvidenceItem `json:"evidence,omitempty"`
	NextAllowedAction  string                        `json:"next_allowed_action,omitempty"`
	BlockedReason      string                        `json:"blocked_reason,omitempty"`
	WaitingFor         string                        `json:"waiting_for,omitempty"`
	Context            string                        `json:"context,omitempty"`
	Summary            string                        `json:"summary,omitempty"`
	EventType          string                        `json:"event_type,omitempty"`
	ResultID           string                        `json:"result_id,omitempty"`
	Payload            json.RawMessage               `json:"payload,omitempty"`
	Limit              int                           `json:"limit,omitempty"`
	WorkingObjective   string                        `json:"working_objective,omitempty"`
	WorkingSource      string                        `json:"working_source,omitempty"`
	WorkingConfidence  string                        `json:"working_confidence,omitempty"`
	HandoffID          string                        `json:"handoff_id,omitempty"`
	OperationID        string                        `json:"operation_id,omitempty"`
	PlannedAction      string                        `json:"planned_action,omitempty"`
	ExpectedEvidence   []string                      `json:"expected_evidence,omitempty"`
	RecoveryQuestion   string                        `json:"recovery_question,omitempty"`
	ResultStatus       string                        `json:"result_status,omitempty"`
	EvidenceRefs       []string                      `json:"evidence_refs,omitempty"`
	RemainingRisk      string                        `json:"remaining_risk,omitempty"`
	WhyProposed        string                        `json:"why_proposed,omitempty"`
	NotIncluded        []string                      `json:"not_included,omitempty"`
	RiskClass          string                        `json:"risk_class,omitempty"`
	ReviewTargetChatID int64                         `json:"review_target_chat_id,omitempty"`
}

func missionLedgerToolDefinition() agent.ToolDef {
	return agent.ToolDef{
		Name:        missionLedgerToolName,
		Description: "Read and update the Mission Ledger. Missions are durable remembered objectives; self-summon is review-only and self-continuation requires separate authority. Do not use this to widen capabilities or bypass proposal/capability gates.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"action":{"type":"string","enum":["list","show","create_candidate","propose_candidate","update_evidence","block","archive","event","summon","health","working_objective_set","working_objective_show","handoff_create","result_record"],"description":"Mission Ledger operation"},
				"mission_id":{"type":"string","description":"Mission id for show/update/archive/event/handoff/result actions"},
				"title":{"type":"string","description":"Mission title when creating a candidate"},
				"objective":{"type":"string","description":"Mission objective when creating a candidate"},
				"origin":{"type":"string","description":"Mission origin such as user_explicit, proposed, recurring, or migrated"},
				"scope":{"type":"string","description":"Mission scope such as session, principal, child_agent, or system"},
				"owner":{"type":"string","description":"Mission owner/principal filter; defaults to the current principal for model-authored writes"},
				"status":{"type":"string","enum":["dormant","candidate","active","blocked","completed","expired","archived"],"description":"Mission status filter or target status where allowed"},
				"pinned":{"type":"boolean","description":"Pinned filter for list; create_candidate may not set pinned true"},
				"tags":{"type":"array","items":{"type":"string"}},
				"source_refs":{"type":"array","items":{"type":"string"}},
				"success_criteria":{"type":"array","items":{"type":"string"}},
				"evidence":{"type":"array","items":{"type":"object","properties":{"claim":{"type":"string"},"required":{"type":"boolean"},"evidence_ref":{"type":"string"},"status":{"type":"string"}},"required":["claim"]}},
				"next_allowed_action":{"type":"string"},
				"blocked_reason":{"type":"string"},
				"waiting_for":{"type":"string"},
				"context":{"type":"string","description":"Current context text for summon relevance scoring"},
				"summary":{"type":"string","description":"Event/result/handoff summary"},
				"event_type":{"type":"string","description":"Event type for event action"},
				"result_id":{"type":"string","description":"Optional result id for result_record"},
				"payload":{"type":"object","description":"Optional event payload"},
				"limit":{"type":"integer","minimum":1,"maximum":100},
				"working_objective":{"type":"string"},
				"working_source":{"type":"string"},
				"working_confidence":{"type":"string"},
				"handoff_id":{"type":"string"},
				"operation_id":{"type":"string"},
				"planned_action":{"type":"string"},
				"expected_evidence":{"type":"array","items":{"type":"string"}},
				"recovery_question":{"type":"string"},
				"result_status":{"type":"string","description":"completed, failed, partial, or unknown"},
				"evidence_refs":{"type":"array","items":{"type":"string"}},
				"remaining_risk":{"type":"string"},
				"why_proposed":{"type":"string","description":"Why the candidate mission is being proposed for Mission Control review"},
				"not_included":{"type":"array","items":{"type":"string"},"description":"Explicit non-scope boundaries for proposed candidate mission"},
				"risk_class":{"type":"string","description":"Operator-facing risk label for proposed candidate mission"},
				"review_target_chat_id":{"type":"integer","description":"Optional Telegram chat id for Mission Control proposal card; defaults to current chat"}
			},
			"required":["action"]
		}`),
	}
}

func (r *Registry) missionLedger(_ context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("mission_ledger requires transcript store")
	}
	var in missionLedgerInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("decode mission_ledger input: %w", err)
		}
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action == "" {
		return "", fmt.Errorf("mission_ledger action is required")
	}
	actor := missionActorForPrincipal(p)
	if actor == "" {
		actor = "system"
	}
	switch action {
	case "list":
		missions, err := r.store.Missions(missionFilterFromInput(in, p))
		if err != nil {
			return "", err
		}
		return renderMissionList("[MISSION_LEDGER]", missions), nil
	case "show":
		missionID := strings.TrimSpace(in.MissionID)
		if missionID == "" {
			return "", fmt.Errorf("mission_ledger show requires mission_id")
		}
		mission, ok, err := r.store.Mission(missionID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("mission %q not found", missionID)
		}
		events, err := r.store.MissionEvents(mission.ID, missionLimit(in.Limit, 10))
		if err != nil {
			return "", err
		}
		return renderMissionShow(mission, events), nil
	case "create_candidate":
		if in.Pinned != nil && *in.Pinned {
			return "", fmt.Errorf("mission_ledger create_candidate cannot pin missions; use a proposal or explicit command path")
		}
		objective := strings.TrimSpace(in.Objective)
		if objective == "" {
			return "", fmt.Errorf("mission_ledger create_candidate requires objective")
		}
		mission := session.MissionState{
			ID:                strings.TrimSpace(in.MissionID),
			Title:             strings.TrimSpace(in.Title),
			Objective:         objective,
			Origin:            firstMissionToolNonEmpty(in.Origin, "proposed"),
			Scope:             missionScopeFromInput(in, p, key),
			Owner:             missionOwnerFromInput(in, p),
			Status:            session.MissionStatusCandidate,
			Pinned:            false,
			Tags:              in.Tags,
			SourceRefs:        in.SourceRefs,
			SuccessCriteria:   in.SuccessCriteria,
			EvidenceChecklist: in.Evidence,
			NextAllowedAction: strings.TrimSpace(in.NextAllowedAction),
			WaitingFor:        strings.TrimSpace(in.WaitingFor),
			Authority:         session.DefaultMissionAuthority(),
			Decay:             session.DefaultMissionDecay(),
		}
		stored, err := r.store.UpsertMission(mission, actor, firstMissionToolNonEmpty(in.Summary, "candidate mission created"))
		if err != nil {
			return "", err
		}
		return renderMissionShow(stored, nil), nil
	case "propose_candidate":
		objective := strings.TrimSpace(in.Objective)
		if objective == "" {
			return "", fmt.Errorf("mission_ledger propose_candidate requires objective")
		}
		targetChatID := in.ReviewTargetChatID
		if targetChatID == 0 {
			targetChatID = key.ChatID
		}
		if targetChatID == 0 && p.TelegramUserID > 0 {
			targetChatID = p.TelegramUserID
		}
		if targetChatID == 0 {
			return "", fmt.Errorf("mission_ledger propose_candidate requires review_target_chat_id or current chat")
		}
		proposal := core.NormalizeMissionControlProposal(core.MissionControlProposal{
			MissionID:         strings.TrimSpace(in.MissionID),
			Title:             strings.TrimSpace(in.Title),
			Objective:         objective,
			WhyProposed:       firstMissionToolNonEmpty(in.WhyProposed, in.Summary),
			Scope:             missionScopeFromInput(in, p, key),
			Owner:             missionOwnerFromInput(in, p),
			Origin:            firstMissionToolNonEmpty(in.Origin, "proposed"),
			Tags:              in.Tags,
			SourceRefs:        in.SourceRefs,
			SuccessCriteria:   in.SuccessCriteria,
			NextAllowedAction: strings.TrimSpace(in.NextAllowedAction),
			NotIncluded:       in.NotIncluded,
			RiskClass:         strings.TrimSpace(in.RiskClass),
		})
		metadata, err := core.MissionControlProposalMetadataJSON(proposal)
		if err != nil {
			return "", fmt.Errorf("encode mission control proposal metadata: %w", err)
		}
		sourceScope := key.Scope
		if sourceScope.IsZero() && key.ChatID != 0 {
			sourceScope = session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: strconv.FormatInt(key.ChatID, 10)}
		}
		targetScope := session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: strconv.FormatInt(targetChatID, 10)}
		eventID, err := r.store.InsertReviewEvent(session.ReviewEvent{
			SourceChatID:      key.ChatID,
			SourceUserID:      p.TelegramUserID,
			SourceRole:        string(p.Role),
			SourceScope:       sourceScope,
			TargetAdminChatID: targetChatID,
			TargetScope:       targetScope,
			Summary:           renderMissionControlProposalSummary(proposal),
			MetadataJSON:      metadata,
			Status:            "pending",
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[MISSION_CONTROL_PROPOSAL]\nreview_event_id: %d\ntitle: %s\nstatus: pending\neffect: candidate_review_only", eventID, proposal.Title), nil
	case "update_evidence":
		if strings.TrimSpace(in.MissionID) == "" {
			return "", fmt.Errorf("mission_ledger update_evidence requires mission_id")
		}
		if len(in.Evidence) == 0 {
			return "", fmt.Errorf("mission_ledger update_evidence requires evidence")
		}
		stored, err := r.store.UpdateMissionEvidence(in.MissionID, in.Evidence, actor, firstMissionToolNonEmpty(in.Summary, "mission evidence updated"))
		if err != nil {
			return "", err
		}
		return renderMissionShow(stored, nil), nil
	case "block":
		if strings.TrimSpace(in.MissionID) == "" {
			return "", fmt.Errorf("mission_ledger block requires mission_id")
		}
		stored, err := r.store.BlockMission(in.MissionID, firstMissionToolNonEmpty(in.BlockedReason, in.Summary, "blocked by model-authored update"), actor)
		if err != nil {
			return "", err
		}
		return renderMissionShow(stored, nil), nil
	case "archive":
		if strings.TrimSpace(in.MissionID) == "" {
			return "", fmt.Errorf("mission_ledger archive requires mission_id")
		}
		stored, err := r.store.UpdateMissionStatus(in.MissionID, session.MissionStatusArchived, actor, firstMissionToolNonEmpty(in.Summary, "mission archived"))
		if err != nil {
			return "", err
		}
		return renderMissionShow(stored, nil), nil
	case "event":
		if strings.TrimSpace(in.MissionID) == "" {
			return "", fmt.Errorf("mission_ledger event requires mission_id")
		}
		payload := ""
		if len(in.Payload) > 0 {
			payload = strings.TrimSpace(string(in.Payload))
		}
		event, err := r.store.AppendMissionEvent(session.MissionEvent{MissionID: in.MissionID, EventType: firstMissionToolNonEmpty(in.EventType, "mission.note"), Actor: actor, Summary: strings.TrimSpace(in.Summary), Payload: payload})
		if err != nil {
			return "", err
		}
		return renderMissionEvent(event), nil
	case "summon":
		missions, err := r.store.SummonMissions(missionFilterFromInput(in, p), in.Context, missionLimit(in.Limit, 8))
		if err != nil {
			return "", err
		}
		return renderMissionList("[MISSION_SUMMON]", missions), nil
	case "health":
		health, err := r.store.MissionLedgerHealth(time.Now().UTC())
		if err != nil {
			return "", err
		}
		return renderMissionHealth(health), nil
	case "working_objective_set":
		if strings.TrimSpace(in.WorkingObjective) == "" {
			return "", fmt.Errorf("mission_ledger working_objective_set requires working_objective")
		}
		if err := r.store.UpdateWorkingObjective(key, session.WorkingObjective{Objective: in.WorkingObjective, Source: firstMissionToolNonEmpty(in.WorkingSource, "inferred"), Confidence: in.WorkingConfidence}); err != nil {
			return "", err
		}
		objective, _ := r.store.WorkingObjective(key)
		return renderWorkingObjective("[WORKING_OBJECTIVE_UPDATED]", objective), nil
	case "working_objective_show":
		objective, err := r.store.WorkingObjective(key)
		if err != nil {
			return "", err
		}
		return renderWorkingObjective("[WORKING_OBJECTIVE]", objective), nil
	case "handoff_create":
		expected := ""
		if len(in.ExpectedEvidence) > 0 {
			raw, err := json.Marshal(in.ExpectedEvidence)
			if err != nil {
				return "", fmt.Errorf("encode expected_evidence: %w", err)
			}
			expected = string(raw)
		}
		handoff, err := r.store.CreateMissionHandoff(session.MissionHandoff{ID: in.HandoffID, MissionID: in.MissionID, OperationID: in.OperationID, PlannedAction: in.PlannedAction, ExpectedEvidenceJSON: expected, RecoveryQuestion: in.RecoveryQuestion})
		if err != nil {
			return "", err
		}
		return renderMissionHandoff(handoff), nil
	case "result_record":
		evidenceRefs := ""
		if len(in.EvidenceRefs) > 0 {
			raw, err := json.Marshal(in.EvidenceRefs)
			if err != nil {
				return "", fmt.Errorf("encode evidence_refs: %w", err)
			}
			evidenceRefs = string(raw)
		}
		result, err := r.store.RecordMissionResult(session.MissionResult{ID: strings.TrimSpace(in.ResultID), HandoffID: in.HandoffID, MissionID: in.MissionID, OperationID: in.OperationID, Status: in.ResultStatus, EvidenceRefsJSON: evidenceRefs, Summary: in.Summary, RemainingRisk: in.RemainingRisk})
		if err != nil {
			return "", err
		}
		return renderMissionResult(result), nil
	default:
		return "", fmt.Errorf("mission_ledger action must be one of list|show|create_candidate|propose_candidate|update_evidence|block|archive|event|summon|health|working_objective_set|working_objective_show|handoff_create|result_record")
	}
}

func missionFilterFromInput(in missionLedgerInput, p principal.Principal) session.MissionFilter {
	filter := session.MissionFilter{Scope: strings.TrimSpace(in.Scope), Owner: strings.TrimSpace(in.Owner), Status: session.NormalizeMissionStatus(session.MissionStatus(in.Status)), Limit: missionLimit(in.Limit, 50)}
	if filter.Owner == "" {
		filter.Owner = missionActorForPrincipal(p)
	}
	if in.Pinned != nil {
		filter.Pinned = in.Pinned
	}
	return filter
}

func missionScopeFromInput(in missionLedgerInput, p principal.Principal, key session.SessionKey) string {
	if scope := strings.TrimSpace(in.Scope); scope != "" {
		return scope
	}
	if p.Role == principal.RoleDurableAgent && strings.TrimSpace(p.DurableAgentID) != "" {
		return "child_agent"
	}
	if key.Scope.Kind != "" {
		return string(key.Scope.Kind)
	}
	return "principal"
}

func missionOwnerFromInput(in missionLedgerInput, p principal.Principal) string {
	if owner := strings.TrimSpace(in.Owner); owner != "" {
		return owner
	}
	if actor := missionActorForPrincipal(p); actor != "" {
		return actor
	}
	return "system"
}

func missionActorForPrincipal(p principal.Principal) string {
	if strings.TrimSpace(p.DurableAgentID) != "" {
		return core.DurableAgentPrincipal(p.DurableAgentID)
	}
	if p.TelegramUserID > 0 {
		return "telegram:" + strconv.FormatInt(p.TelegramUserID, 10)
	}
	if p.Role != "" {
		return string(p.Role)
	}
	return ""
}

func missionLimit(value int, fallback int) int {
	if fallback <= 0 {
		fallback = 50
	}
	if value <= 0 {
		return fallback
	}
	if value > 200 {
		return 200
	}
	return value
}

func firstMissionToolNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func renderMissionControlProposalSummary(proposal core.MissionControlProposal) string {
	proposal = core.NormalizeMissionControlProposal(proposal)
	parts := []string{"Mission Control proposal"}
	if title := strings.TrimSpace(proposal.Title); title != "" {
		parts = append(parts, "title="+strconv.Quote(title))
	}
	if objective := strings.TrimSpace(proposal.Objective); objective != "" {
		parts = append(parts, "objective="+strconv.Quote(objective))
	}
	if why := strings.TrimSpace(proposal.WhyProposed); why != "" {
		parts = append(parts, "why="+strconv.Quote(why))
	}
	return strings.Join(parts, " ")
}

func renderMissionList(header string, missions []session.MissionState) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "count: %d\n", len(missions))
	if len(missions) == 0 {
		return strings.TrimSpace(b.String())
	}
	b.WriteString("missions:\n")
	for _, mission := range missions {
		fmt.Fprintf(&b, "- id=%s status=%s pinned=%t owner=%s title=%q authority=%s\n", mission.ID, mission.Status, mission.Pinned, mission.Owner, mission.Title, missionAuthoritySummary(mission))
	}
	return strings.TrimSpace(b.String())
}

func renderMissionShow(mission session.MissionState, events []session.MissionEvent) string {
	var b strings.Builder
	b.WriteString("[MISSION]\n")
	fmt.Fprintf(&b, "id: %s\n", mission.ID)
	fmt.Fprintf(&b, "title: %s\n", mission.Title)
	fmt.Fprintf(&b, "objective: %s\n", mission.Objective)
	fmt.Fprintf(&b, "status: %s\n", mission.Status)
	fmt.Fprintf(&b, "pinned: %t\n", mission.Pinned)
	fmt.Fprintf(&b, "scope: %s\n", mission.Scope)
	fmt.Fprintf(&b, "owner: %s\n", mission.Owner)
	fmt.Fprintf(&b, "authority: %s\n", missionAuthoritySummary(mission))
	if mission.Recurrence != nil {
		fmt.Fprintf(&b, "recurrence: %s\n", mission.Recurrence.Cadence)
	}
	if mission.NextAllowedAction != "" {
		fmt.Fprintf(&b, "next_allowed_action: %s\n", mission.NextAllowedAction)
	}
	if mission.BlockedReason != "" {
		fmt.Fprintf(&b, "blocked_reason: %s\n", mission.BlockedReason)
	}
	if len(mission.Tags) > 0 {
		fmt.Fprintf(&b, "tags: %s\n", strings.Join(mission.Tags, ","))
	}
	if len(mission.EvidenceChecklist) > 0 {
		b.WriteString("evidence:\n")
		for _, item := range mission.EvidenceChecklist {
			fmt.Fprintf(&b, "- [%s] %s", item.Status, item.Claim)
			if item.EvidenceRef != "" {
				fmt.Fprintf(&b, " ref=%s", item.EvidenceRef)
			}
			b.WriteString("\n")
		}
	}
	if len(events) > 0 {
		b.WriteString("events:\n")
		for _, event := range events {
			fmt.Fprintf(&b, "- seq=%d type=%s actor=%s summary=%q\n", event.Seq, event.EventType, event.Actor, event.Summary)
		}
	}
	return strings.TrimSpace(b.String())
}

func missionAuthoritySummary(mission session.MissionState) string {
	parts := []string{"review"}
	if mission.Authority.CanSelfSummon {
		parts = append(parts, "self_summon")
	}
	if mission.Authority.CanSelfContinue {
		parts = append(parts, "self_continue")
	}
	if mission.Authority.RequiresUserReview {
		parts = append(parts, "requires_user_review")
	}
	return strings.Join(parts, ",")
}

func renderMissionEvent(event session.MissionEvent) string {
	return fmt.Sprintf("[MISSION_EVENT]\nseq: %d\nmission_id: %s\nevent_type: %s\nactor: %s\nsummary: %s", event.Seq, event.MissionID, event.EventType, event.Actor, event.Summary)
}

func renderMissionHealth(health session.MissionLedgerHealth) string {
	return fmt.Sprintf("[MISSION_HEALTH]\nactive: %d\npinned: %d\nrecurring: %d\nblocked: %d\nself_continuation_enabled: %d\nstale_candidates: %d\npending_handoffs: %d", health.ActiveCount, health.PinnedCount, health.RecurringCount, health.BlockedCount, health.SelfContinuationEnabledCount, health.StaleCandidateCount, health.PendingHandoffCount)
}

func renderWorkingObjective(header string, objective session.WorkingObjective) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	if objective.Objective == "" {
		b.WriteString("objective: none")
		return b.String()
	}
	fmt.Fprintf(&b, "objective: %s\n", objective.Objective)
	fmt.Fprintf(&b, "source: %s\n", objective.Source)
	fmt.Fprintf(&b, "confidence: %s", objective.Confidence)
	return strings.TrimSpace(b.String())
}

func renderMissionHandoff(h session.MissionHandoff) string {
	return fmt.Sprintf("[MISSION_HANDOFF]\nid: %s\nmission_id: %s\noperation_id: %s\nplanned_action: %s\nstatus: %s\nrecovery_question: %s", h.ID, h.MissionID, h.OperationID, h.PlannedAction, h.Status, h.RecoveryQuestion)
}

func renderMissionResult(result session.MissionResult) string {
	return fmt.Sprintf("[MISSION_RESULT]\nid: %s\nhandoff_id: %s\nmission_id: %s\noperation_id: %s\nstatus: %s\nsummary: %s\nremaining_risk: %s", result.ID, result.HandoffID, result.MissionID, result.OperationID, result.Status, result.Summary, result.RemainingRisk)
}
