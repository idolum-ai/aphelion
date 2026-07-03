//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type lookaheadAuthorityFrontier struct {
	RecordID   string
	SessionID  string
	ContractID string
	ShapeHash  string
}

func (r *Runtime) HandleReviewEventAction(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent, action core.ReviewEventAction) (string, error) {
	switch action {
	case core.ReviewEventActionChildWakeRetry:
		return r.handleReviewEventChildWakeRetry(ctx, cb, event)
	case core.ReviewEventActionLookaheadNext:
		return r.handleReviewEventLookaheadNext(ctx, cb, event)
	default:
		return "This review action is not handled by the runtime.", nil
	}
}

func (r *Runtime) handleReviewEventLookaheadNext(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent) (string, error) {
	if r == nil || r.store == nil {
		return "", fmt.Errorf("runtime store unavailable")
	}
	if response, err := reviewEventPrivateAdminCallbackResponse(cb, event, "lookahead"); err != nil {
		return "", err
	} else if response != "" {
		return response, nil
	}
	frontier := lookaheadAuthorityFrontierForReviewEvent(event)
	msg := core.InboundMessage{
		ChatID:    event.TargetAdminChatID,
		SenderID:  callbackReviewEventSenderID(cb),
		MessageID: callbackReviewEventMessageID(cb),
		Text:      fmt.Sprintf("review_event:%d:%s", event.ID, core.ReviewEventActionLookaheadNext),
	}
	now := time.Now().UTC()
	action, ok, err := r.nextLookaheadRecoveryApprovalAction(frontier, now)
	if err != nil {
		return "", err
	}
	if !ok {
		r.recordLookaheadNoFrontier(event, frontier, now)
		return "No unresolved authority frontier is available. No authority was approved or executed.", nil
	}
	key := sessionKeyForNextActionRecord(action)
	if err := r.recordLookaheadAuthorityFrontier(event, key, action, now); err != nil {
		return "", err
	}
	materialized, handled, err := r.materializeRecoveryApprovalNextActionLocked(ctx, key, msg, action, now)
	if err != nil {
		return "", err
	}
	if handled && !materialized {
		return "Next authority frontier is no longer materializable. No authority was approved or executed.", nil
	}
	if !materialized {
		return "Next authority frontier recorded, but no approval card was materialized. No authority was approved or executed.", nil
	}
	return "Next authority approval surfaced. No authority was approved or executed.", nil
}

func (r *Runtime) nextLookaheadRecoveryApprovalAction(frontier lookaheadAuthorityFrontier, now time.Time) (session.NextActionRecord, bool, error) {
	if strings.TrimSpace(frontier.RecordID) != "" {
		action, ok, err := r.store.NextActionByRecordID(frontier.RecordID)
		if err != nil || !ok {
			return session.NextActionRecord{}, false, err
		}
		matches, err := r.lookaheadActionMatchesFrontier(action, frontier)
		if err != nil {
			return session.NextActionRecord{}, false, err
		}
		if !matches || !r.lookaheadRecoveryApprovalActionExecutable(action, now) {
			return session.NextActionRecord{}, false, nil
		}
		return action, true, nil
	}
	sessionID := strings.TrimSpace(frontier.SessionID)
	if sessionID == "" {
		return session.NextActionRecord{}, false, nil
	}
	actions, err := r.store.OpenNextActionsBySessionIDOperation(sessionID, session.NextActionBlockedNeedsAuthority, "request_approval", "continuation_lease_request", 100)
	if err != nil {
		return session.NextActionRecord{}, false, err
	}
	bundleActions, err := r.store.OpenNextActionsBySessionIDOperation(sessionID, session.NextActionBlockedNeedsAuthority, "request_approval", "authority_bundle_request", 100)
	if err != nil {
		return session.NextActionRecord{}, false, err
	}
	actions = append(actions, bundleActions...)
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].CreatedAt.Equal(actions[j].CreatedAt) {
			return actions[i].RecordID < actions[j].RecordID
		}
		return actions[i].CreatedAt.Before(actions[j].CreatedAt)
	})
	for _, action := range actions {
		matches, err := r.lookaheadActionMatchesFrontier(action, frontier)
		if err != nil {
			return session.NextActionRecord{}, false, err
		}
		if !matches {
			continue
		}
		executable := r.lookaheadRecoveryApprovalActionExecutable(action, now)
		if executable {
			return action, true, nil
		}
	}
	return session.NextActionRecord{}, false, nil
}

func (r *Runtime) lookaheadRecoveryApprovalActionExecutable(action session.NextActionRecord, now time.Time) bool {
	if !action.ResolvedAt.IsZero() {
		return false
	}
	consumable, invalid := recoveryApprovalNextActionConsumable(action)
	if invalid || !consumable {
		return false
	}
	key := sessionKeyForNextActionRecord(action)
	switch strings.TrimSpace(action.OperationKind) {
	case "continuation_lease_request":
		executable, invalid, err := r.recoveryApprovalContinuationContractExecutable(key, action)
		return err == nil && executable && !invalid
	case "authority_bundle_request":
		executable, invalid, _, err := r.recoveryApprovalAuthorityBundleExecutable(key, action, now)
		return err == nil && executable && !invalid
	default:
		return false
	}
}

func (r *Runtime) recordLookaheadNoFrontier(event session.ReviewEvent, frontier lookaheadAuthorityFrontier, now time.Time) {
	if r == nil {
		return
	}
	key := session.SessionKey{ChatID: event.TargetAdminChatID, UserID: 0, Scope: telegramDMScopeRef(event.TargetAdminChatID)}
	r.recordExecutionEvent(key, core.ExecutionEventAuthorityFindingReviewed, "authority_discovery", "no_frontier", map[string]any{
		"review_event_id":       event.ID,
		"source_session_id":     strings.TrimSpace(event.SourceSessionID),
		"target_session_id":     strings.TrimSpace(event.TargetSessionID),
		"frontier_session_id":   strings.TrimSpace(frontier.SessionID),
		"next_action_record_id": strings.TrimSpace(frontier.RecordID),
		"contract_id":           strings.TrimSpace(frontier.ContractID),
		"shape_hash":            strings.TrimSpace(frontier.ShapeHash),
	}, now)
}

func (r *Runtime) recordLookaheadAuthorityFrontier(event session.ReviewEvent, key session.SessionKey, action session.NextActionRecord, now time.Time) error {
	sessionID := session.SessionIDForKey(key)
	labelRef, approvalClass, shapeHash, err := r.lookaheadAuthorityFrontierLabel(action)
	if err != nil {
		return err
	}
	entry := session.IdentificationLedgerEntryInput{
		PlanID:      session.IdentificationPlanIDForSession(sessionID),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   sessionID,
		StepRef:     "next_action:" + strings.TrimSpace(action.RecordID),
		ShapeHash:   shapeHash,
		LabelRef:    labelRef,
		Status:      session.IdentificationLedgerStatusProposed,
		UpdatedAt:   now,
	}
	evidenceRef := "review_event:" + fmt.Sprint(event.ID)
	observations := []session.IdentificationLedgerObservationInput{{
		Method:      session.IdentificationObservationLookahead,
		Property:    session.IdentificationPropertyApprovalClass,
		Value:       approvalClass,
		EvidenceRef: evidenceRef,
		ObservedAt:  now,
	}, {
		Method:      session.IdentificationObservationLookahead,
		Property:    session.IdentificationPropertyTool,
		Value:       strings.TrimSpace(action.OperationTool),
		EvidenceRef: "next_action:" + strings.TrimSpace(action.RecordID),
		ObservedAt:  now,
	}}
	if labelRef != "" {
		observations = append(observations, session.IdentificationLedgerObservationInput{
			Method:      session.IdentificationObservationLookahead,
			Property:    session.IdentificationPropertyContract,
			Value:       labelRef,
			EvidenceRef: "next_action:" + strings.TrimSpace(action.RecordID),
			ObservedAt:  now,
		})
	}
	if _, _, err := r.store.RecordIdentificationLedgerObservations(entry, observations); err != nil {
		return fmt.Errorf("record lookahead authority frontier: %w", err)
	}
	return nil
}

func (r *Runtime) lookaheadAuthorityFrontierLabel(action session.NextActionRecord) (string, string, string, error) {
	var input recoveryApprovalHandoffInput
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &input); err != nil {
		return "", "", "", fmt.Errorf("decode lookahead recovery handoff: %w", err)
	}
	contractID := strings.TrimSpace(input.ContractID)
	switch strings.TrimSpace(action.OperationKind) {
	case "continuation_lease_request":
		contract, ok, err := r.store.ContinuationRecoveryContract(contractID)
		if err != nil {
			return "", "", "", err
		}
		if !ok {
			return "", "", "", fmt.Errorf("lookahead continuation recovery contract %q not found", contractID)
		}
		contract = session.NormalizeContinuationRecoveryContract(contract)
		return contract.ContractID, string(contract.LeaseClass), session.AuthorityShapeHashForContinuationRecoveryContract(contract), nil
	case "authority_bundle_request":
		if contractID == "" {
			return "", "", "", fmt.Errorf("lookahead authority bundle contract_id is required")
		}
		return contractID, "authority_bundle", session.AuthorityShapeHash(session.AuthorityShapeInput{
			Tool:          strings.TrimSpace(action.OperationTool),
			Action:        strings.TrimSpace(action.OperationKind),
			ResourceClass: "authority_bundle",
		}), nil
	default:
		return "", "", "", fmt.Errorf("unsupported lookahead operation kind %q", action.OperationKind)
	}
}

func lookaheadAuthorityFrontierForReviewEvent(event session.ReviewEvent) lookaheadAuthorityFrontier {
	frontier := lookaheadAuthorityFrontier{
		RecordID:   reviewEventAuthorityFrontierMetadataString(event, "next_action_record_id"),
		SessionID:  reviewEventAuthorityFrontierMetadataString(event, "next_action_session_id"),
		ContractID: reviewEventAuthorityFrontierMetadataString(event, "contract_id"),
		ShapeHash:  reviewEventAuthorityFrontierMetadataString(event, "authority_shape_hash"),
	}
	if frontier.ShapeHash == "" {
		frontier.ShapeHash = reviewEventAuthorityFrontierMetadataString(event, "shape_hash")
	}
	if frontier.ContractID == "" {
		frontier.ContractID = reviewEventAuthorityFrontierMetadataString(event, "continuation_recovery_contract_id")
	}
	if frontier.ContractID == "" {
		frontier.ContractID = reviewEventAuthorityFrontierMetadataString(event, "authority_bundle_contract_id")
	}
	if frontier.SessionID == "" {
		frontier.SessionID = strings.TrimSpace(event.SourceSessionID)
	}
	if frontier.SessionID == "" {
		frontier.SessionID = strings.TrimSpace(event.TargetSessionID)
	}
	return frontier
}

func reviewEventAuthorityFrontierMetadataString(event session.ReviewEvent, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || strings.TrimSpace(event.MetadataJSON) == "" {
		return ""
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(event.MetadataJSON), &metadata); err != nil {
		return ""
	}
	if value, ok := metadata[key]; ok {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	if nested, ok := metadata["metadata"].(map[string]any); ok {
		if value, ok := nested[key]; ok {
			return strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return ""
}

func (r *Runtime) lookaheadActionMatchesFrontier(action session.NextActionRecord, frontier lookaheadAuthorityFrontier) (bool, error) {
	if frontier.RecordID != "" && strings.TrimSpace(action.RecordID) != frontier.RecordID {
		return false, nil
	}
	if frontier.SessionID != "" && strings.TrimSpace(action.SessionID) != frontier.SessionID {
		return false, nil
	}
	contractID := actionRecoveryApprovalContractID(action)
	if frontier.ContractID != "" && contractID != frontier.ContractID {
		return false, nil
	}
	if frontier.ShapeHash != "" {
		shapeHash, err := r.lookaheadAuthorityShapeHashForAction(action)
		if err != nil {
			return false, err
		}
		if shapeHash == "" || shapeHash != frontier.ShapeHash {
			return false, nil
		}
	}
	return true, nil
}

func actionRecoveryApprovalContractID(action session.NextActionRecord) string {
	var input recoveryApprovalHandoffInput
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &input); err != nil {
		return ""
	}
	return strings.TrimSpace(input.ContractID)
}

func (r *Runtime) lookaheadAuthorityShapeHashForAction(action session.NextActionRecord) (string, error) {
	switch strings.TrimSpace(action.OperationKind) {
	case "continuation_lease_request":
		contractID := actionRecoveryApprovalContractID(action)
		if contractID == "" || r == nil || r.store == nil {
			return "", nil
		}
		contract, ok, err := r.store.ContinuationRecoveryContract(contractID)
		if err != nil || !ok {
			return "", err
		}
		return session.AuthorityShapeHashForContinuationRecoveryContract(contract), nil
	case "authority_bundle_request":
		return session.AuthorityShapeHash(session.AuthorityShapeInput{
			Tool:          strings.TrimSpace(action.OperationTool),
			Action:        strings.TrimSpace(action.OperationKind),
			ResourceClass: "authority_bundle",
		}), nil
	default:
		return "", nil
	}
}

func sessionKeyForNextActionRecord(action session.NextActionRecord) session.SessionKey {
	return session.SessionKey{
		ChatID: action.ChatID,
		UserID: action.UserID,
		Scope:  session.NormalizeScopeRef(action.Scope),
	}
}

func (r *Runtime) handleReviewEventChildWakeRetry(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent) (string, error) {
	if r == nil || r.store == nil {
		return "", fmt.Errorf("runtime store unavailable")
	}
	chatID, senderID, response, err := childWakeRetryPrivateAdminCallbackTarget(cb, event)
	if err != nil {
		return "", err
	}
	if response != "" {
		return response, nil
	}
	action, ok, err := r.childWakeRetryActionForReviewEvent(event)
	if err != nil {
		return "", err
	}
	if !ok {
		return "This child wake retry is no longer actionable; use the newest child status.", nil
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	msg := core.InboundMessage{
		ChatID:    chatID,
		SenderID:  senderID,
		MessageID: callbackReviewEventMessageID(cb),
		Text:      fmt.Sprintf("review_event:%d:%s", event.ID, core.ReviewEventActionChildWakeRetry),
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: senderID}
	contract, err := r.childWakeRepairRetryRecoveryContract(key, msg, action)
	if err != nil {
		return "", err
	}
	contract, err = r.store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		return "", fmt.Errorf("store child_wake retry contract from review event: %w", err)
	}
	now := time.Now().UTC()
	if err := r.ensureChildWakeRepairRetryOperationState(key, contract, now); err != nil {
		return "", err
	}
	handoff, err := r.store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "review_event_callback",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		CausalRefs:         []string{"review_event:" + fmt.Sprint(event.ID), "next_action:" + strings.TrimSpace(action.RecordID), "contract:" + contract.ContractID},
		NextAction:         "approve one fresh bounded child_wake continuation before retrying wake_once",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contract.ContractID),
		OperatorProjection: "Approve one fresh bounded child_wake continuation before retrying durable_agent wake_once.",
		CreatedAt:          now,
	})
	if err != nil {
		return "", fmt.Errorf("record child_wake retry approval handoff from review event: %w", err)
	}
	if err := r.materializeChildWakeRepairRetryApprovalHandoff(ctx, key, msg, actor, action, handoff, now); err != nil {
		return "", err
	}
	return "Child wake retry approval surfaced. Use the approval card to run exactly one bounded wake.", nil
}

func (r *Runtime) childWakeRetryActionForReviewEvent(event session.ReviewEvent) (session.NextActionRecord, bool, error) {
	meta, ok := parseReviewEventArtifactMetadata(event)
	if !ok {
		return session.NextActionRecord{}, false, nil
	}
	agentID := strings.TrimSpace(meta.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(meta.Metadata["durable_agent_id"])
	}
	packetID := strings.TrimSpace(meta.Metadata["child_task_packet_id"])
	if agentID == "" || packetID == "" {
		return session.NextActionRecord{}, false, nil
	}
	if strings.TrimSpace(meta.Metadata["child_blocker_kind"]) != "external_transient" ||
		strings.TrimSpace(meta.Metadata["child_next_state"]) != string(session.NextActionScheduledRetry) ||
		strings.TrimSpace(meta.Metadata["retry_policy"]) != "bounded_backoff" {
		return session.NextActionRecord{}, false, nil
	}
	actions, err := r.store.OpenNextActionsBySessionSubject(r.durableAgentExecutionKey(agentID), "task_packet", packetID, 20)
	if err != nil {
		return session.NextActionRecord{}, false, err
	}
	wantRecordID := strings.TrimSpace(meta.Metadata["next_action_record_id"])
	if wantRecordID != "" {
		for _, action := range actions {
			if strings.TrimSpace(action.RecordID) == wantRecordID && childWakeTaskPacketRetryActionCanRequestRetry(action) {
				return action, true, nil
			}
		}
		return session.NextActionRecord{}, false, nil
	}
	var candidates []session.NextActionRecord
	for _, action := range actions {
		if childWakeTaskPacketRetryActionCanRequestRetry(action) {
			candidates = append(candidates, action)
		}
	}
	if len(candidates) == 0 {
		return session.NextActionRecord{}, false, nil
	}
	return newestNextActionRecord(candidates), true, nil
}

func childWakeRetryPrivateAdminCallbackTarget(cb telegram.CallbackQuery, event session.ReviewEvent) (int64, int64, string, error) {
	senderID := callbackReviewEventSenderID(cb)
	if senderID == 0 {
		return 0, 0, "", fmt.Errorf("child wake retry requires a Telegram admin callback")
	}
	targetAdminChatID := event.TargetAdminChatID
	if targetAdminChatID == 0 {
		return 0, senderID, "", fmt.Errorf("child wake retry requires a target admin review event")
	}
	if senderID != targetAdminChatID {
		return 0, senderID, "Only the target admin can retry this child wake.", nil
	}
	if cb.Message == nil || cb.Message.Chat == nil || cb.Message.Chat.ID != targetAdminChatID {
		return 0, senderID, "This child wake retry button is only actionable from the delivered private admin card.", nil
	}
	if event.DeliveryMessageID != 0 && cb.Message.MessageID != event.DeliveryMessageID {
		return 0, senderID, "This child wake retry is no longer actionable; use the newest child status.", nil
	}
	return targetAdminChatID, senderID, "", nil
}

func reviewEventPrivateAdminCallbackResponse(cb telegram.CallbackQuery, event session.ReviewEvent, label string) (string, error) {
	senderID := callbackReviewEventSenderID(cb)
	if senderID == 0 {
		return "", fmt.Errorf("%s requires a Telegram admin callback", label)
	}
	targetAdminChatID := event.TargetAdminChatID
	if targetAdminChatID == 0 {
		return "", fmt.Errorf("%s requires a target admin review event", label)
	}
	if senderID != targetAdminChatID {
		return "Only the target admin can use this control.", nil
	}
	if cb.Message == nil || cb.Message.Chat == nil || cb.Message.Chat.ID != targetAdminChatID {
		return "This control is only actionable from the delivered private admin card.", nil
	}
	if event.DeliveryMessageID != 0 && cb.Message.MessageID != event.DeliveryMessageID {
		return "This control is no longer actionable; use the newest card.", nil
	}
	return "", nil
}

func callbackReviewEventSenderID(cb telegram.CallbackQuery) int64 {
	if cb.From == nil {
		return 0
	}
	return cb.From.ID
}

func callbackReviewEventMessageID(cb telegram.CallbackQuery) int64 {
	if cb.Message == nil {
		return 0
	}
	return cb.Message.MessageID
}
