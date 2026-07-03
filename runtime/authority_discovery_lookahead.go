//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

type LookaheadSimulationResult struct {
	Found              bool
	Key                session.SessionKey
	PhaseID            string
	BundleID           string
	NextActionRecordID string
	Reason             string
}

func (r *Runtime) simulateNextLookaheadRecoveryApprovalAction(frontier lookaheadAuthorityFrontier, event session.ReviewEvent, senderID int64, now time.Time) (session.NextActionRecord, LookaheadSimulationResult, bool, error) {
	result := LookaheadSimulationResult{Reason: "no_simulatable_phase"}
	if r == nil || r.store == nil {
		return session.NextActionRecord{}, result, false, nil
	}
	if strings.TrimSpace(frontier.RecordID) != "" || strings.TrimSpace(frontier.ContractID) != "" || strings.TrimSpace(frontier.ShapeHash) != "" {
		result.Reason = "exact_frontier_not_simulated"
		return session.NextActionRecord{}, result, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := session.SessionKey{ChatID: event.TargetAdminChatID, UserID: 0, Scope: telegramDMScopeRef(event.TargetAdminChatID)}
	sessionID := session.SessionIDForKey(key)
	if strings.TrimSpace(frontier.SessionID) != "" && strings.TrimSpace(frontier.SessionID) != sessionID {
		result.Reason = "frontier_session_key_unavailable"
		return session.NextActionRecord{}, result, false, nil
	}
	_, opState, exists, err := r.store.PlanAndOperationStateIfExists(key)
	if err != nil || !exists {
		return session.NextActionRecord{}, result, false, err
	}
	opState = session.NormalizeOperationState(opState)
	if !opState.PhasePlan.Active() {
		result.Reason = "no_active_phase_plan"
		return session.NextActionRecord{}, result, false, nil
	}
	phase, grants, ok, err := r.nextLookaheadPhaseCapabilityCollision(opState, now)
	if err != nil || !ok {
		return session.NextActionRecord{}, result, false, err
	}
	if senderID == 0 {
		senderID = event.TargetAdminChatID
	}
	principal := fmt.Sprintf("telegram:%d", senderID)
	bundle, action, err := r.publishLookaheadPhaseAuthorityBundle(key, opState, phase, grants, principal, now)
	if err != nil {
		return session.NextActionRecord{}, result, false, err
	}
	result = LookaheadSimulationResult{
		Found:              true,
		Key:                key,
		PhaseID:            strings.TrimSpace(phase.ID),
		BundleID:           strings.TrimSpace(bundle.BundleID),
		NextActionRecordID: strings.TrimSpace(action.RecordID),
		Reason:             "operation_phase_required_capability",
	}
	return action, result, true, nil
}

func (r *Runtime) nextLookaheadPhaseCapabilityCollision(opState session.OperationState, now time.Time) (session.OperationPhase, []session.CapabilityGrantSpec, bool, error) {
	opState = session.NormalizeOperationState(opState)
	start := operationPhasePlanStartIndex(opState.PhasePlan)
	for i := start; i < len(opState.PhasePlan.Phases); i++ {
		phase := normalizeSingleOperationPhase(opState.PhasePlan.Phases[i])
		if phase.Status == session.PlanStatusCompleted || len(phase.RequiredCapabilityGrants) == 0 {
			continue
		}
		grants, err := r.unmetLookaheadCapabilityGrantSpecs(phase.RequiredCapabilityGrants)
		if err != nil {
			return session.OperationPhase{}, nil, false, err
		}
		if len(grants) == 0 {
			continue
		}
		return phase, grants, true, nil
	}
	return session.OperationPhase{}, nil, false, nil
}

func (r *Runtime) unmetLookaheadCapabilityGrantSpecs(specs []session.CapabilityGrantSpec) ([]session.CapabilityGrantSpec, error) {
	resolved, err := r.validateRequiredCapabilityGrantSpecs(specs)
	if err != nil {
		return nil, err
	}
	out := make([]session.CapabilityGrantSpec, 0, len(resolved))
	for _, item := range resolved {
		if item.existing {
			continue
		}
		spec := session.NormalizeCapabilityGrantSpec(session.CapabilityGrantSpec{
			RequestID:      item.spec.RequestID,
			GrantID:        item.spec.GrantID,
			Kind:           item.kind,
			TargetResource: item.target,
			GrantedTo:      item.grantedTo,
			AllowedActions: item.actions,
			Contract:       firstNonEmptyContinuation(item.spec.Contract, item.request.Contract),
			Constraints:    firstNonEmptyContinuation(item.spec.Constraints, item.request.Constraints),
			ExpiresAt:      item.spec.ExpiresAt,
		})
		out = append(out, spec)
	}
	return session.NormalizeCapabilityGrantSpecs(out), nil
}

func (r *Runtime) publishLookaheadPhaseAuthorityBundle(key session.SessionKey, opState session.OperationState, phase session.OperationPhase, grants []session.CapabilityGrantSpec, principal string, now time.Time) (session.AuthorityBundleContract, session.NextActionRecord, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	grants = session.NormalizeCapabilityGrantSpecs(grants)
	if len(grants) == 0 {
		return session.AuthorityBundleContract{}, session.NextActionRecord{}, fmt.Errorf("lookahead authority bundle requires unmet grants")
	}
	sessionID := session.SessionIDForKey(key)
	requestInstanceID := lookaheadPhaseAuthorityBundleRequestInstanceID(sessionID, opState, phase, grants)
	allowed := lookaheadPhaseAllowedActions(phase, grants)
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID:        requestInstanceID,
		SessionID:                sessionID,
		Principal:                strings.TrimSpace(principal),
		Objective:                firstNonEmptyContinuation(opState.Objective, opState.PhasePlan.Goal, phase.Summary, "Continue the current operation plan."),
		Summary:                  lookaheadPhaseAuthorityBundleSummary(opState, phase),
		AllowedActions:           allowed,
		ForbiddenActions:         lookaheadPhaseForbiddenActions(phase, allowed),
		StopConditions:           lookaheadPhaseStopConditions(phase),
		RequiredCapabilityGrants: grants,
		Components: []session.AuthorityBundleComponent{{
			Kind:       session.AuthorityBundleComponentKindOperationPhase,
			RefID:      firstNonEmptyContinuation(phase.ID, "current"),
			Subject:    "operation",
			SubjectRef: strings.TrimSpace(opState.ID),
		}},
		ExpiresAt: now.Add(30 * time.Minute),
		CreatedAt: now,
	})
	if err != nil {
		return session.AuthorityBundleContract{}, session.NextActionRecord{}, err
	}
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(bundle.BundleID)
	if err != nil {
		return session.AuthorityBundleContract{}, session.NextActionRecord{}, err
	}
	actionInput := session.NextActionInput{
		RecordID:           session.NextActionRecordID(sessionID, "authority_bundle_request", bundle.BundleID, session.NextActionBlockedNeedsAuthority, time.Unix(0, 0).UTC()),
		Key:                key,
		Owner:              "lookahead",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         bundle.BundleID,
		CausalRefs:         []string{"operation:" + strings.TrimSpace(opState.ID), "operation_phase:" + strings.TrimSpace(phase.ID), "authority_bundle:" + bundle.BundleID},
		NextAction:         "review the next plan-phase authority bundle and approve only if the phase, grant scope, and stop conditions match the objective",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: "Review next grant for operation phase " + firstNonEmptyContinuation(phase.OperatorTitle, phase.PlanTitle, phase.Summary, phase.ID),
		CreatedAt:          now,
	}
	return r.store.RecordAuthorityBundleContractNextAction(bundle, actionInput)
}

func lookaheadPhaseAuthorityBundleRequestInstanceID(sessionID string, opState session.OperationState, phase session.OperationPhase, grants []session.CapabilityGrantSpec) string {
	phaseID := firstNonEmptyContinuation(phase.ID, phase.Summary, "phase")
	payload := strings.Join([]string{
		strings.TrimSpace(sessionID),
		strings.TrimSpace(opState.ID),
		strings.TrimSpace(opState.PhasePlan.ID),
		strings.TrimSpace(phaseID),
		lookaheadCapabilityGrantSpecsFingerprint(grants),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	token := hex.EncodeToString(sum[:])
	if len(token) > 16 {
		token = token[:16]
	}
	return "lookahead-phase-" + safeContinuationIDPart(phaseID) + "-" + token
}

func lookaheadCapabilityGrantSpecsFingerprint(grants []session.CapabilityGrantSpec) string {
	grants = session.NormalizeCapabilityGrantSpecs(grants)
	var parts []string
	for _, grant := range grants {
		parts = append(parts, strings.Join([]string{
			string(grant.Kind),
			session.AuthorityResourceClass(grant.TargetResource),
			strings.Join(grant.AllowedActions, ","),
			grant.RequestID,
			grant.GrantID,
		}, ":"))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func lookaheadPhaseAuthorityBundleSummary(opState session.OperationState, phase session.OperationPhase) string {
	return firstNonEmptyContinuation(
		phase.OperatorTitle,
		phase.PlanTitle,
		phase.Summary,
		opState.PhasePlan.Goal,
		opState.Objective,
		"Approve the next bounded plan-phase authority.",
	)
}

func lookaheadPhaseAllowedActions(phase session.OperationPhase, grants []session.CapabilityGrantSpec) []string {
	actions := append([]string(nil), phase.AllowedActions...)
	if len(actions) == 0 {
		for _, grant := range grants {
			actions = append(actions, grant.AllowedActions...)
		}
	}
	if len(actions) == 0 {
		actions = []string{"use_required_capability_grants"}
	}
	return session.NormalizeCapabilityActions(actions)
}

func lookaheadPhaseForbiddenActions(phase session.OperationPhase, allowed []string) []string {
	forbidden := append([]string(nil), phase.ForbiddenActions...)
	forbidden = append(forbidden,
		"expand_authority_without_new_approval",
		"execute_phase_outside_bundle",
		"skip_stop_gate",
		"credentials_or_tokens",
		"unbounded_retry_loop",
	)
	allowedSet := map[string]struct{}{}
	for _, action := range session.NormalizeCapabilityActions(allowed) {
		allowedSet[action] = struct{}{}
	}
	out := []string{}
	for _, action := range session.NormalizeCapabilityActions(forbidden) {
		if _, ok := allowedSet[action]; ok {
			continue
		}
		out = append(out, action)
	}
	return out
}

func lookaheadPhaseStopConditions(phase session.OperationPhase) []string {
	stops := append([]string(nil), phase.ValidationPlan...)
	if len(stops) == 0 {
		stops = []string{"stop after the bound phase completes or a typed blocker requires new authority"}
	}
	return stops
}
