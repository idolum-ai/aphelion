//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func (r *Runtime) promoteDurableChildAuthorityBundleRequests(agent core.DurableAgent, childKey session.SessionKey, result session.ChildTaskResult, now time.Time) error {
	parentKey := durableChildAuthorityBundleDefaultParentKey(agent)
	_, err := r.promoteDurableChildAuthorityBundleRequestsToParent(parentKey, agent, childKey, result, now)
	return err
}

func (r *Runtime) promoteDurableChildAuthorityBundleRequestsForParentKey(parentKey session.SessionKey, now time.Time) (int, error) {
	if r == nil || r.store == nil {
		return 0, nil
	}
	parentKey.Scope = session.NormalizeScopeRef(parentKey.Scope)
	if parentKey.Scope.IsZero() {
		parentKey.Scope = telegramDMScopeRef(parentKey.ChatID)
	}
	if parentKey.ChatID == 0 {
		return 0, nil
	}
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return 0, fmt.Errorf("list durable agents for child authority bundle promotion: %w", err)
	}
	promoted := 0
	for _, agent := range agents {
		if strings.TrimSpace(agent.AgentID) == "" || agent.ReviewTargetChatID != parentKey.ChatID {
			continue
		}
		childKey := session.SessionKey{ChatID: durableWakeSyntheticChatID(agent.AgentID), Scope: durableAgentScopeRef(agent)}
		count, err := r.promoteDurableChildAuthorityBundleRequestsToParent(parentKey, agent, childKey, session.ChildTaskResult{}, now)
		if err != nil {
			return promoted, err
		}
		promoted += count
	}
	return promoted, nil
}

func durableChildAuthorityBundleDefaultParentKey(agent core.DurableAgent) session.SessionKey {
	key := session.SessionKey{
		ChatID: agent.ReviewTargetChatID,
		Scope:  telegramDMScopeRef(agent.ReviewTargetChatID),
	}
	return key
}

func (r *Runtime) promoteDurableChildAuthorityBundleRequestsToParent(parentKey session.SessionKey, agent core.DurableAgent, childKey session.SessionKey, result session.ChildTaskResult, now time.Time) (int, error) {
	if r == nil || r.store == nil {
		return 0, nil
	}
	agentID := strings.TrimSpace(agent.AgentID)
	if agentID == "" || parentKey.ChatID == 0 {
		return 0, nil
	}
	parentKey.Scope = session.NormalizeScopeRef(parentKey.Scope)
	if parentKey.Scope.IsZero() {
		parentKey.Scope = telegramDMScopeRef(parentKey.ChatID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	actions, err := r.store.OpenNextActionsBySessionOperation(childKey, session.NextActionBlockedNeedsAuthority, "request_approval", "authority_bundle_request", 100)
	if err != nil {
		return 0, fmt.Errorf("load child authority bundle requests: %w", err)
	}
	if len(actions) == 0 {
		return 0, nil
	}
	promotedCount := 0
	for _, action := range actions {
		consumable, invalid := recoveryApprovalNextActionConsumable(action)
		if invalid {
			if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
				RecordID:    action.RecordID,
				Key:         childKey,
				Owner:       "runtime",
				SubjectKind: action.SubjectKind,
				SubjectRef:  action.SubjectRef,
				Reason:      "invalid_child_authority_bundle_handoff",
				ResolvedAt:  now,
			}); err != nil {
				return promotedCount, fmt.Errorf("resolve invalid child authority bundle handoff %s: %w", action.RecordID, err)
			}
			continue
		}
		if !consumable {
			continue
		}
		_, promoted, err := r.promoteDurableChildAuthorityBundleRequest(parentKey, childKey, agent, result, action, now)
		if err != nil {
			return promotedCount, err
		}
		if !promoted {
			continue
		}
		if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
			RecordID:    action.RecordID,
			Key:         childKey,
			Owner:       "runtime",
			SubjectKind: action.SubjectKind,
			SubjectRef:  action.SubjectRef,
			Reason:      "promoted_to_parent_authority_bundle",
			ResolvedAt:  now,
		}); err != nil {
			return promotedCount, fmt.Errorf("resolve promoted child authority bundle handoff %s: %w", action.RecordID, err)
		}
		promotedCount++
	}
	return promotedCount, nil
}

func (r *Runtime) promoteDurableChildAuthorityBundleRequest(parentKey session.SessionKey, childKey session.SessionKey, agent core.DurableAgent, result session.ChildTaskResult, action session.NextActionRecord, now time.Time) (session.NextActionRecord, bool, error) {
	var handoff recoveryApprovalHandoffInput
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &handoff); err != nil {
		return session.NextActionRecord{}, false, fmt.Errorf("decode child authority bundle handoff %s: %w", action.RecordID, err)
	}
	childBundleID := strings.TrimSpace(handoff.ContractID)
	if childBundleID == "" {
		return session.NextActionRecord{}, false, nil
	}
	childBundle, ok, err := r.store.AuthorityBundleContract(childBundleID)
	if err != nil {
		return session.NextActionRecord{}, false, err
	}
	if !ok {
		return session.NextActionRecord{}, false, fmt.Errorf("child authority bundle %q not found", childBundleID)
	}
	childBundle = session.NormalizeAuthorityBundleContract(childBundle)
	if !childBundle.ExpiresAt.IsZero() && !now.Before(childBundle.ExpiresAt) {
		return session.NextActionRecord{}, false, nil
	}
	agentID := strings.TrimSpace(agent.AgentID)
	requestInstanceID := promotedDurableChildAuthorityBundleRequestInstanceID(parentKey, childKey, agentID, action.RecordID, childBundle.BundleID)
	primary, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID: requestInstanceID + ":child_wake",
		SessionID:         session.SessionIDForKey(parentKey),
		SubjectKind:       "continuation_lease_request",
		SubjectRef:        session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, agentID, "", "durable_agent", "wake_once", ""),
		Principal:         durableChildAuthorityBundleParentPrincipal(parentKey),
		LeaseClass:        session.ContinuationLeaseClassChildWake,
		AllowedActions:    []string{"wake_named_child"},
		Constraints:       map[string]string{"agent_id": agentID},
		Tool:              "durable_agent",
		ToolAction:        "wake_once",
		AgentID:           agentID,
		CreatedAt:         now,
	})
	if err != nil {
		return session.NextActionRecord{}, false, fmt.Errorf("compile parent child-wake continuation contract: %w", err)
	}
	primary, err = r.store.UpsertContinuationRecoveryContract(primary)
	if err != nil {
		return session.NextActionRecord{}, false, fmt.Errorf("store parent child-wake continuation contract: %w", err)
	}
	expiresAt := childBundle.ExpiresAt
	if expiresAt.IsZero() || expiresAt.After(now.Add(30*time.Minute)) {
		expiresAt = now.Add(30 * time.Minute)
	}
	components := []session.AuthorityBundleComponent{
		{Kind: "child_authority_bundle", RefID: childBundle.BundleID, Subject: action.SubjectKind, SubjectRef: action.SubjectRef},
		{Kind: "continuation_recovery_contract", RefID: primary.ContractID, Subject: primary.SubjectKind, SubjectRef: primary.SubjectRef},
	}
	if strings.TrimSpace(result.ResultID) != "" {
		components = append(components, session.AuthorityBundleComponent{Kind: "child_task_result", RefID: result.ResultID, Subject: "child_task_result", SubjectRef: result.PacketID})
	}
	parentBundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID:             requestInstanceID,
		SessionID:                     session.SessionIDForKey(parentKey),
		Principal:                     durableChildAuthorityBundleParentPrincipal(parentKey),
		Objective:                     firstNonEmpty(strings.TrimSpace(childBundle.Objective), "Review child-authored authority bundle for "+agentID),
		Summary:                       firstNonEmpty(strings.TrimSpace(childBundle.Summary), "Child "+agentID+" requested a bounded authority bundle."),
		SourceNextActionRecordIDs:     append([]string(nil), action.RecordID),
		AllowedActions:                append([]string(nil), childBundle.AllowedActions...),
		ForbiddenActions:              append([]string(nil), childBundle.ForbiddenActions...),
		StopConditions:                append([]string(nil), childBundle.StopConditions...),
		PrimaryContinuationContractID: primary.ContractID,
		RequiredCapabilityGrants:      append([]session.CapabilityGrantSpec(nil), childBundle.RequiredCapabilityGrants...),
		Components:                    components,
		ExpiresAt:                     expiresAt,
		CreatedAt:                     now,
	})
	if err != nil {
		return session.NextActionRecord{}, false, fmt.Errorf("compile parent authority bundle: %w", err)
	}
	parentBundle, err = r.store.UpsertAuthorityBundleContract(parentBundle)
	if err != nil {
		return session.NextActionRecord{}, false, fmt.Errorf("store parent authority bundle: %w", err)
	}
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(parentBundle.BundleID)
	if err != nil {
		return session.NextActionRecord{}, false, err
	}
	recordID := session.NextActionRecordID(session.SessionIDForKey(parentKey), "authority_bundle_request", parentBundle.BundleID, session.NextActionBlockedNeedsAuthority, time.Unix(0, 0).UTC())
	causalRefs := []string{"authority_bundle:" + childBundle.BundleID, "next_action:" + strings.TrimSpace(action.RecordID)}
	if strings.TrimSpace(result.ResultID) != "" {
		causalRefs = append(causalRefs, "child_result:"+strings.TrimSpace(result.ResultID))
	}
	parentAction, err := r.store.RecordNextAction(session.NextActionInput{
		RecordID:           recordID,
		Key:                parentKey,
		Owner:              "durable_child",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         parentBundle.BundleID,
		CausalRefs:         causalRefs,
		NextAction:         "review the child-authored bounded authority bundle and approve only if the boundaries match the current objective",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: fmt.Sprintf("Child %s requested a bounded authority bundle. Review allowed actions, forbidden actions, stop conditions, and required grants before approving.", agentID),
		CreatedAt:          now,
	})
	if err != nil {
		return session.NextActionRecord{}, false, fmt.Errorf("record parent authority bundle handoff: %w", err)
	}
	r.recordExecutionEvent(parentKey, core.ExecutionEventWorkflowNextState, "durable_child", "authority_bundle_promoted", map[string]any{
		"agent_id":                   agentID,
		"child_session_id":           session.SessionIDForKey(childKey),
		"child_authority_bundle_id":  childBundle.BundleID,
		"parent_authority_bundle_id": parentBundle.BundleID,
		"parent_next_action_id":      parentAction.RecordID,
		"child_next_action_id":       action.RecordID,
		"child_result_id":            result.ResultID,
	}, now)
	return parentAction, true, nil
}

func (r *Runtime) materializePromotedDurableChildAuthorityBundle(ctx context.Context, key session.SessionKey, msg core.InboundMessage, now time.Time) error {
	unlock := r.lockSession(key)
	defer unlock()
	handled, _, err := r.materializePendingRecoveryApprovalNextActionLocked(ctx, key, msg, now)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}
	// If another parent action was chosen first, leave this handoff open for the next normal materialization pass.
	return nil
}

func (r *Runtime) materializePromotedDurableChildAuthorityBundleAfterApprovedRetry(ctx context.Context, key session.SessionKey, reservation approvedContinuationReservation, wakeResult toolpkg.DurableAgentWakeOnceRenderedResult, result session.ChildTaskResult, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	agentID := firstNonEmpty(strings.TrimSpace(wakeResult.AgentID), strings.TrimSpace(result.AgentID))
	if _, err := r.promoteDurableChildAuthorityBundleRequestsForParentKey(key, now); err != nil {
		log.Printf("WARN promoted child authority bundle sweep failed agent_id=%s result_id=%s err=%v", agentID, strings.TrimSpace(result.ResultID), err)
	}
	senderID := reservation.ApprovedBy
	if senderID == 0 {
		senderID = key.UserID
	}
	if senderID == 0 {
		senderID = key.ChatID
	}
	msg := core.InboundMessage{
		ChatID:         key.ChatID,
		ChatType:       "private",
		SenderID:       senderID,
		SenderName:     "admin",
		Text:           "Review child-authored authority bundle.",
		MessageID:      durableWakeMessageID(now),
		Timestamp:      now.UTC(),
		DurableAgentID: agentID,
	}
	if err := r.materializePromotedDurableChildAuthorityBundle(ctx, key, msg, now); err != nil {
		log.Printf("WARN promoted child authority bundle materialization failed agent_id=%s result_id=%s err=%v", agentID, strings.TrimSpace(result.ResultID), err)
	}
}

func promotedDurableChildAuthorityBundleOperationInput(bundleID string) (string, error) {
	raw, _ := json.Marshal(map[string]any{
		"action":                  "request_authority_bundle",
		"contract_id":             strings.TrimSpace(bundleID),
		"recovery_contract":       "aphelion.recovery_handoff.v1",
		"recovery_operation_kind": "authority_bundle_request",
	})
	if err := toolpkg.ValidateRecoveryHandoffToolInput(session.NextActionBlockedNeedsAuthority, "request_approval", string(raw)); err != nil {
		return "", err
	}
	return string(raw), nil
}

func promotedDurableChildAuthorityBundleRequestInstanceID(parentKey session.SessionKey, childKey session.SessionKey, agentID string, actionID string, bundleID string) string {
	payload := strings.Join([]string{
		session.SessionIDForKey(parentKey),
		session.SessionIDForKey(childKey),
		strings.TrimSpace(agentID),
		strings.TrimSpace(actionID),
		strings.TrimSpace(bundleID),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	token := hex.EncodeToString(sum[:])
	if len(token) > 24 {
		token = token[:24]
	}
	return "child-authority-bundle-" + token
}

func durableChildAuthorityBundleParentPrincipal(key session.SessionKey) string {
	if key.UserID != 0 {
		return fmt.Sprintf("telegram:%d", key.UserID)
	}
	if key.ChatID != 0 {
		return fmt.Sprintf("telegram:%d", key.ChatID)
	}
	return "telegram:admin"
}
