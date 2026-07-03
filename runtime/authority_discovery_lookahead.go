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
	Source             string
}

const MaxOutstandingLookaheadApprovalFrontiers = 5

type lookaheadAuthoritySelection struct {
	Action     session.NextActionRecord
	Simulation LookaheadSimulationResult
}

func (r *Runtime) selectNextLookaheadAuthorityFrontier(frontier lookaheadAuthorityFrontier, event session.ReviewEvent, senderID int64, now time.Time) (session.NextActionRecord, LookaheadSimulationResult, bool, error) {
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
	}
	selections, result, err := r.selectLookaheadAuthorityFrontiers(frontier, event, senderID, now, 1)
	if err != nil || len(selections) == 0 {
		return session.NextActionRecord{}, result, false, err
	}
	selection := selections[0]
	return selection.Action, selection.Simulation, true, nil
}

func (r *Runtime) selectLookaheadAuthorityFrontiers(frontier lookaheadAuthorityFrontier, event session.ReviewEvent, senderID int64, now time.Time, limit int) ([]lookaheadAuthoritySelection, LookaheadSimulationResult, error) {
	if limit <= 0 {
		limit = 1
	}
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
	}
	actions, err := r.lookaheadRecoveryApprovalActions(frontier, now, 1)
	if err != nil {
		return nil, LookaheadSimulationResult{Reason: "lookup_failed", Source: "next_action_records"}, err
	}
	if len(actions) > 0 {
		out := make([]lookaheadAuthoritySelection, 0, len(actions))
		for _, action := range actions {
			out = append(out, lookaheadAuthoritySelection{
				Action: action,
				Simulation: LookaheadSimulationResult{
					Found:              true,
					Key:                sessionKeyForNextActionRecord(action),
					NextActionRecordID: strings.TrimSpace(action.RecordID),
					Reason:             "existing_recovery_approval_frontier",
					Source:             "next_action_records",
				},
			})
		}
		return out, out[0].Simulation, nil
	}
	if strings.TrimSpace(frontier.RecordID) != "" {
		return nil, LookaheadSimulationResult{Reason: "exact_frontier_not_materializable", Source: "next_action_records"}, nil
	}
	return r.simulateLookaheadRecoveryApprovalActions(frontier, event, senderID, now, limit)
}

func (r *Runtime) simulateNextLookaheadRecoveryApprovalAction(frontier lookaheadAuthorityFrontier, event session.ReviewEvent, senderID int64, now time.Time) (session.NextActionRecord, LookaheadSimulationResult, bool, error) {
	selections, result, err := r.simulateLookaheadRecoveryApprovalActions(frontier, event, senderID, now, 1)
	if err != nil || len(selections) == 0 {
		return session.NextActionRecord{}, result, false, err
	}
	selection := selections[0]
	return selection.Action, selection.Simulation, true, nil
}

func (r *Runtime) simulateLookaheadRecoveryApprovalActions(frontier lookaheadAuthorityFrontier, event session.ReviewEvent, senderID int64, now time.Time, limit int) ([]lookaheadAuthoritySelection, LookaheadSimulationResult, error) {
	result := LookaheadSimulationResult{Reason: "no_simulatable_phase", Source: "operation_phase_plan"}
	if r == nil || r.store == nil {
		return nil, result, nil
	}
	if limit <= 0 {
		limit = 1
	}
	if strings.TrimSpace(frontier.RecordID) != "" || strings.TrimSpace(frontier.ContractID) != "" || strings.TrimSpace(frontier.ShapeHash) != "" {
		result.Reason = "exact_frontier_not_simulated"
		return nil, result, nil
	}
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
	}
	key := session.SessionKey{ChatID: event.TargetAdminChatID, UserID: 0, Scope: telegramDMScopeRef(event.TargetAdminChatID)}
	sessionID := session.SessionIDForKey(key)
	if strings.TrimSpace(frontier.SessionID) != "" && strings.TrimSpace(frontier.SessionID) != sessionID {
		result.Reason = "frontier_session_key_unavailable"
		return nil, result, nil
	}
	_, opState, exists, err := r.store.PlanAndOperationStateIfExists(key)
	if err != nil || !exists {
		return nil, result, err
	}
	opState = session.NormalizeOperationState(opState)
	if !opState.PhasePlan.Active() {
		result.Reason = "no_active_phase_plan"
		return nil, result, nil
	}
	collisions, err := r.lookaheadPhaseCapabilityCollisions(opState, now, limit)
	if err != nil || len(collisions) == 0 {
		return nil, result, err
	}
	if senderID == 0 {
		senderID = event.TargetAdminChatID
	}
	principal := fmt.Sprintf("telegram:%d", senderID)
	if len(collisions) > 1 {
		bundle, action, err := r.publishLookaheadPhaseAuthorityBundleCluster(key, opState, collisions, principal, now)
		if err != nil {
			return nil, result, err
		}
		executable, err := r.lookaheadRecoveryApprovalActionExecutable(action, now)
		if err != nil {
			return nil, result, err
		}
		if !executable {
			result.Reason = "no_materializable_simulated_phase_cluster"
			return nil, result, nil
		}
		simulated := LookaheadSimulationResult{
			Found:              true,
			Key:                key,
			PhaseID:            lookaheadPhaseClusterID(collisions),
			BundleID:           strings.TrimSpace(bundle.BundleID),
			NextActionRecordID: strings.TrimSpace(action.RecordID),
			Reason:             "operation_phase_required_capability_cluster",
			Source:             "operation_phase_plan",
		}
		return []lookaheadAuthoritySelection{{Action: action, Simulation: simulated}}, simulated, nil
	}
	seen := map[string]struct{}{}
	var selections []lookaheadAuthoritySelection
	for _, collision := range collisions {
		bundle, action, err := r.publishLookaheadPhaseAuthorityBundle(key, opState, collision.Phase, collision.Grants, principal, now)
		if err != nil {
			return nil, result, err
		}
		if _, ok := seen[action.RecordID]; ok {
			continue
		}
		seen[action.RecordID] = struct{}{}
		executable, err := r.lookaheadRecoveryApprovalActionExecutable(action, now)
		if err != nil {
			return nil, result, err
		}
		if !executable {
			continue
		}
		simulated := LookaheadSimulationResult{
			Found:              true,
			Key:                key,
			PhaseID:            strings.TrimSpace(collision.Phase.ID),
			BundleID:           strings.TrimSpace(bundle.BundleID),
			NextActionRecordID: strings.TrimSpace(action.RecordID),
			Reason:             "operation_phase_required_capability",
			Source:             "operation_phase_plan",
		}
		selections = append(selections, lookaheadAuthoritySelection{Action: action, Simulation: simulated})
		if len(selections) >= limit {
			break
		}
	}
	if len(selections) == 0 {
		result.Reason = "no_materializable_simulated_phase"
		return nil, result, nil
	}
	return selections, selections[0].Simulation, nil
}

func (r *Runtime) nextLookaheadPhaseCapabilityCollision(opState session.OperationState, now time.Time) (session.OperationPhase, []session.CapabilityGrantSpec, bool, error) {
	collisions, err := r.lookaheadPhaseCapabilityCollisions(opState, now, 1)
	if err != nil || len(collisions) == 0 {
		return session.OperationPhase{}, nil, false, err
	}
	return collisions[0].Phase, collisions[0].Grants, true, nil
}

type lookaheadPhaseCapabilityCollision struct {
	Phase  session.OperationPhase
	Grants []session.CapabilityGrantSpec
}

func (r *Runtime) lookaheadPhaseCapabilityCollisions(opState session.OperationState, now time.Time, limit int) ([]lookaheadPhaseCapabilityCollision, error) {
	if limit <= 0 {
		limit = 1
	}
	opState = session.NormalizeOperationState(opState)
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
	}
	var out []lookaheadPhaseCapabilityCollision
	start := operationPhasePlanStartIndex(opState.PhasePlan)
	for i := start; i < len(opState.PhasePlan.Phases); i++ {
		phase := normalizeSingleOperationPhase(opState.PhasePlan.Phases[i])
		if phase.Status == session.PlanStatusCompleted || len(phase.RequiredCapabilityGrants) == 0 {
			continue
		}
		grants, err := r.unmetLookaheadCapabilityGrantSpecs(phase.RequiredCapabilityGrants)
		if err != nil {
			return nil, err
		}
		if len(grants) == 0 {
			continue
		}
		out = append(out, lookaheadPhaseCapabilityCollision{Phase: phase, Grants: grants})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
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

func (r *Runtime) publishLookaheadPhaseAuthorityBundleCluster(key session.SessionKey, opState session.OperationState, collisions []lookaheadPhaseCapabilityCollision, principal string, now time.Time) (session.AuthorityBundleContract, session.NextActionRecord, error) {
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
	}
	opState = session.NormalizeOperationState(opState)
	if len(collisions) == 0 {
		return session.AuthorityBundleContract{}, session.NextActionRecord{}, fmt.Errorf("lookahead authority bundle cluster requires unmet grants")
	}
	var grants []session.CapabilityGrantSpec
	var phases []session.OperationPhase
	var components []session.AuthorityBundleComponent
	for _, collision := range collisions {
		phase := normalizeSingleOperationPhase(collision.Phase)
		phases = append(phases, phase)
		grants = append(grants, collision.Grants...)
		components = append(components, session.AuthorityBundleComponent{
			Kind:       session.AuthorityBundleComponentKindOperationPhase,
			RefID:      firstNonEmptyContinuation(phase.ID, "current"),
			Subject:    "operation",
			SubjectRef: strings.TrimSpace(opState.ID),
		})
	}
	grants = session.NormalizeCapabilityGrantSpecs(grants)
	if len(grants) == 0 {
		return session.AuthorityBundleContract{}, session.NextActionRecord{}, fmt.Errorf("lookahead authority bundle cluster requires unmet grants")
	}
	sessionID := session.SessionIDForKey(key)
	requestInstanceID := lookaheadPhaseClusterAuthorityBundleRequestInstanceID(sessionID, opState, phases, grants)
	allowed := lookaheadPhaseClusterAllowedActions(phases, grants)
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID:        requestInstanceID,
		SessionID:                sessionID,
		Principal:                strings.TrimSpace(principal),
		Objective:                firstNonEmptyContinuation(opState.Objective, opState.PhasePlan.Goal, "Continue the current operation plan."),
		Summary:                  lookaheadPhaseClusterAuthorityBundleSummary(opState, phases),
		AllowedActions:           allowed,
		ForbiddenActions:         lookaheadPhaseClusterForbiddenActions(phases, allowed),
		StopConditions:           lookaheadPhaseClusterStopConditions(phases),
		RequiredCapabilityGrants: grants,
		Components:               components,
		ExpiresAt:                now.Add(30 * time.Minute),
		CreatedAt:                now,
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
		CausalRefs:         append(lookaheadPhaseClusterCausalRefs(opState, phases), "authority_bundle:"+bundle.BundleID),
		NextAction:         "review the next plan-frontier authority bundle and approve only if the phases, grant scope, and stop conditions match the objective",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: fmt.Sprintf("Review next grants for %d operation phases.", len(phases)),
		CreatedAt:          now,
	}
	return r.store.RecordAuthorityBundleContractNextAction(bundle, actionInput)
}

func (r *Runtime) publishLookaheadPhaseAuthorityBundle(key session.SessionKey, opState session.OperationState, phase session.OperationPhase, grants []session.CapabilityGrantSpec, principal string, now time.Time) (session.AuthorityBundleContract, session.NextActionRecord, error) {
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
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

func lookaheadPhaseClusterAuthorityBundleRequestInstanceID(sessionID string, opState session.OperationState, phases []session.OperationPhase, grants []session.CapabilityGrantSpec) string {
	var phaseIDs []string
	for _, phase := range phases {
		phaseIDs = append(phaseIDs, firstNonEmptyContinuation(phase.ID, phase.Summary, "phase"))
	}
	payload := strings.Join([]string{
		strings.TrimSpace(sessionID),
		strings.TrimSpace(opState.ID),
		strings.TrimSpace(opState.PhasePlan.ID),
		strings.Join(phaseIDs, ","),
		lookaheadCapabilityGrantSpecsFingerprint(grants),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	token := hex.EncodeToString(sum[:])
	if len(token) > 16 {
		token = token[:16]
	}
	return "lookahead-frontier-" + token
}

func lookaheadPhaseClusterID(collisions []lookaheadPhaseCapabilityCollision) string {
	var phaseIDs []string
	for _, collision := range collisions {
		phaseID := strings.TrimSpace(collision.Phase.ID)
		if phaseID != "" {
			phaseIDs = append(phaseIDs, phaseID)
		}
	}
	if len(phaseIDs) == 0 {
		return "phase_cluster"
	}
	return "phase_cluster:" + strings.Join(phaseIDs, ",")
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

func lookaheadPhaseClusterAuthorityBundleSummary(opState session.OperationState, phases []session.OperationPhase) string {
	if len(phases) == 1 {
		return lookaheadPhaseAuthorityBundleSummary(opState, phases[0])
	}
	return fmt.Sprintf("Approve the next bounded authority bundle for %d operation phases in %s.", len(phases), firstNonEmptyContinuation(opState.PhasePlan.Goal, opState.Objective, "the current plan"))
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

func lookaheadPhaseClusterAllowedActions(phases []session.OperationPhase, grants []session.CapabilityGrantSpec) []string {
	var actions []string
	for _, phase := range phases {
		actions = append(actions, phase.AllowedActions...)
	}
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

func lookaheadPhaseClusterForbiddenActions(phases []session.OperationPhase, allowed []string) []string {
	var forbidden []string
	for _, phase := range phases {
		forbidden = append(forbidden, phase.ForbiddenActions...)
	}
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

func lookaheadPhaseClusterStopConditions(phases []session.OperationPhase) []string {
	var stops []string
	for _, phase := range phases {
		stops = append(stops, phase.ValidationPlan...)
	}
	if len(stops) == 0 {
		stops = []string{"stop after each bound phase completes or a typed blocker requires new authority"}
	}
	return stops
}

func lookaheadPhaseClusterCausalRefs(opState session.OperationState, phases []session.OperationPhase) []string {
	refs := []string{"operation:" + strings.TrimSpace(opState.ID)}
	for _, phase := range phases {
		if id := strings.TrimSpace(phase.ID); id != "" {
			refs = append(refs, "operation_phase:"+id)
		}
	}
	return refs
}
