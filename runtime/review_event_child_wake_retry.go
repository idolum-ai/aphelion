//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func (r *Runtime) HandleReviewEventAction(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent, action core.ReviewEventAction) (string, error) {
	switch action {
	case core.ReviewEventActionChildWakeRetry:
		return r.handleReviewEventChildWakeRetry(ctx, cb, event)
	default:
		return "This review action is not handled by the runtime.", nil
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
