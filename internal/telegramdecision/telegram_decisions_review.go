//go:build linux

package telegramdecision

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

// Review-event callback handling lives with Telegram decision handling, while
// runtime still owns review-event delivery, presentation, and inline button
// construction. The callback path acknowledges Telegram callbacks and applies
// durable session/capability/mission store transitions.
func (h *DecisionHandler) handleReviewEventCallback(ctx context.Context, cb telegram.CallbackQuery, eventID int64, action core.ReviewEventAction) error {
	if h == nil || h.sender == nil || h.store == nil {
		return nil
	}
	event, err := h.store.ReviewEventByID(eventID)
	if err != nil {
		return err
	}
	if event == nil {
		return h.answerReviewEventCallback(ctx, cb, "This review item is no longer available.")
	}
	if action == core.ReviewEventActionExpand || action == core.ReviewEventActionHide {
		if text, err := h.reviewEventDetailAuthorizationFailure(*event, cb); err != nil {
			return err
		} else if text != "" {
			return h.answerReviewEventCallback(ctx, cb, text)
		}
		return h.handleReviewEventDetailToggle(ctx, cb, *event, action == core.ReviewEventActionExpand)
	}
	if proposal, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		if reviewEventCallbackExpired(*event, time.Now()) {
			_ = h.editReviewEventCallbackMessage(ctx, cb, "Mission Control proposal timed out — use a fresh prompt.")
			return h.answerReviewEventCallback(ctx, cb, "Proposal timed out. Use a fresh prompt.")
		}
		return h.handleMissionControlProposalCallback(ctx, cb, *event, proposal, action)
	}
	if action == core.ReviewEventActionChildWakeRetry {
		return h.handleReviewEventChildWakeRetryCallback(ctx, cb, *event)
	}
	if action == core.ReviewEventActionLookaheadNext {
		return h.handleReviewEventLookaheadCallback(ctx, cb, *event)
	}
	if reviewEventCallbackExpired(*event, time.Now()) {
		_ = h.editReviewEventCallbackMessage(ctx, cb, "Approval timed out — use a fresh prompt.")
		return h.answerReviewEventCallback(ctx, cb, "Approval timed out. Use a fresh prompt.")
	}
	requestID := reviewEventCallbackCapabilityRequestID(*event)
	if requestID == "" {
		return h.answerReviewEventCallback(ctx, cb, "This review item is not actionable yet.")
	}
	record, ok, err := h.store.CapabilityRequest(requestID)
	if err != nil {
		return err
	}
	if !ok {
		return h.answerReviewEventCallback(ctx, cb, "Capability request not found.")
	}
	if event.Status == "dismissed" {
		_ = h.editReviewEventCallbackMessage(ctx, cb, "Stale approval card — use the newest prompt.")
		return h.answerReviewEventCallback(ctx, cb, "This approval is stale. Use the newest prompt.")
	}
	if !reviewEventRequestStillActionable(record, action) {
		_ = h.editReviewEventCallbackMessage(ctx, cb, "Stale approval card — use the newest prompt.")
		return h.answerReviewEventCallback(ctx, cb, "This approval is no longer active. Use the newest prompt.")
	}
	fromID := int64(0)
	if cb.From != nil {
		fromID = cb.From.ID
	}
	status, reviewerRole, err := reviewEventCapabilityStatusForAction(record, action, fromID, event.TargetAdminChatID)
	if err != nil {
		_ = h.answerReviewEventCallback(ctx, cb, err.Error())
		return nil
	}
	if status == session.CapabilityReviewStatusApproved {
		materializable, err := h.capabilityRequestHasCompiledGrantHandoff(record)
		if err != nil {
			return err
		}
		if !materializable {
			_ = h.editReviewEventCallbackMessage(ctx, cb, reviewEventMissingGrantContractText(record, *event))
			return h.answerReviewEventCallback(ctx, cb, "Approval needs an exact grant contract first.")
		}
	}
	review, _, err := h.store.ApplyCapabilityReviewTransition(session.CapabilityReviewTransitionInput{
		Review: session.CapabilityReview{
			ReviewID:     reviewEventCapabilityReviewID(event.ID, fromID, action),
			RequestID:    record.RequestID,
			Reviewer:     fmt.Sprintf("telegram:%d", fromID),
			ReviewerRole: reviewerRole,
			Status:       status,
			Rationale:    fmt.Sprintf("telegram review event %d", event.ID),
		},
		AllowedCurrentStatus: reviewEventAllowedCurrentStatuses(record, action),
	})
	if err != nil {
		return err
	}
	grant, grantActivated, grantErr := h.materializeApprovedCapabilityGrantFromReview(record, review, *event, fromID)
	if grantErr != nil {
		_ = h.editReviewEventCallbackMessage(ctx, cb, reviewEventConfirmationText(labelForCapabilityReview(review.Status), record, *event)+"\n\nGrant activation needs repair: "+compactSentence(grantErr.Error()))
		return h.answerReviewEventCallback(ctx, cb, "")
	}
	label := "approved"
	if review.Status == session.CapabilityReviewStatusParentApproved {
		label = "parent-approved"
	} else if review.Status == session.CapabilityReviewStatusRejected {
		label = "rejected"
	}
	if refreshed, ok, err := h.store.CapabilityRequest(record.RequestID); err == nil && ok {
		record = refreshed
	}
	text := reviewEventConfirmationText(label, record, *event)
	text = reviewEventConfirmationWithGrantActivation(text, record, grant, grantActivated, review.Status)
	if review.Status == session.CapabilityReviewStatusApproved {
		_ = h.editReviewEventCallbackMessageWithLookahead(ctx, cb, *event, text)
	} else {
		_ = h.editReviewEventCallbackMessage(ctx, cb, text)
	}
	return h.answerReviewEventCallback(ctx, cb, "")
}

func (h *DecisionHandler) handleReviewEventChildWakeRetryCallback(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent) error {
	if reviewEventCallbackExpired(event, time.Now()) {
		_ = h.editReviewEventCallbackMessage(ctx, cb, "Retry card timed out — use a fresh child status prompt.")
		return h.answerReviewEventCallback(ctx, cb, "Retry card timed out. Use a fresh prompt.")
	}
	if event.Status == "dismissed" {
		_ = h.editReviewEventCallbackMessage(ctx, cb, "Stale retry card — use the newest child status.")
		return h.answerReviewEventCallback(ctx, cb, "This retry card is stale. Use the newest prompt.")
	}
	if h.reviewEventActionRunner == nil {
		return h.answerReviewEventCallback(ctx, cb, "Child retry controls are unavailable in this runtime.")
	}
	text, err := h.reviewEventActionRunner.HandleReviewEventAction(ctx, cb, event, core.ReviewEventActionChildWakeRetry)
	if err != nil {
		return h.answerReviewEventCallback(ctx, cb, compactSentence(err.Error()))
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = "Child wake retry approval surfaced."
	}
	_ = h.editReviewEventCallbackMessage(ctx, cb, text)
	return h.answerReviewEventCallback(ctx, cb, "")
}

func (h *DecisionHandler) handleReviewEventLookaheadCallback(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent) error {
	if reviewEventCallbackExpired(event, time.Now()) {
		_ = h.editReviewEventCallbackMessage(ctx, cb, "Lookahead timed out — use a fresh prompt.")
		return h.answerReviewEventCallback(ctx, cb, "Lookahead timed out. Use a fresh prompt.")
	}
	if event.Status == "dismissed" {
		_ = h.editReviewEventCallbackMessage(ctx, cb, "Stale lookahead card — use the newest prompt.")
		return h.answerReviewEventCallback(ctx, cb, "This lookahead card is stale. Use the newest prompt.")
	}
	if h.reviewEventActionRunner == nil {
		return h.answerReviewEventCallback(ctx, cb, "Lookahead controls are unavailable in this runtime.")
	}
	text, err := h.reviewEventActionRunner.HandleReviewEventAction(ctx, cb, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		return h.answerReviewEventCallback(ctx, cb, compactSentence(err.Error()))
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = "Next authority shape recorded for review."
	}
	_ = h.editReviewEventCallbackMessage(ctx, cb, text)
	return h.answerReviewEventCallback(ctx, cb, "")
}

func reviewEventActionForReactionAndState(reaction *core.InboundReaction, record session.CapabilityRequest) (core.ReviewEventAction, bool) {
	if reaction == nil {
		return "", false
	}
	record = session.NormalizeCapabilityRequest(record)
	for _, value := range reaction.New {
		switch strings.TrimSpace(value) {
		case "👍", "+1":
			if strings.TrimSpace(record.ParentPrincipal) != "" && record.ReviewStatus == session.CapabilityReviewStatusProposed {
				return core.ReviewEventActionParentApprove, true
			}
			return core.ReviewEventActionApprove, true
		case "👎", "-1":
			return core.ReviewEventActionReject, true
		}
	}
	return "", false
}

func (h *DecisionHandler) applyReviewEventReaction(ctx context.Context, msg core.InboundMessage, event session.ReviewEvent) error {
	if h == nil || h.store == nil {
		return nil
	}
	if reviewEventCallbackExpired(event, time.Now()) {
		_ = h.editReviewEventReactionMessage(ctx, msg, "Approval timed out — use a fresh prompt.")
		return nil
	}
	requestID := reviewEventCallbackCapabilityRequestID(event)
	if requestID == "" {
		return nil
	}
	record, ok, err := h.store.CapabilityRequest(requestID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	action, actionable := reviewEventActionForReactionAndState(msg.Reaction, record)
	if !actionable {
		return nil
	}
	if action != core.ReviewEventActionParentApprove && action != core.ReviewEventActionApprove && action != core.ReviewEventActionReject {
		return nil
	}
	if event.Status == "dismissed" {
		_ = h.editReviewEventReactionMessage(ctx, msg, "Stale approval card — use the newest prompt.")
		return nil
	}
	if !reviewEventRequestStillActionable(record, action) {
		_ = h.editReviewEventReactionMessage(ctx, msg, "Stale approval card — use the newest prompt.")
		return nil
	}
	status, reviewerRole, err := reviewEventCapabilityStatusForAction(record, action, msg.SenderID, event.TargetAdminChatID)
	if err != nil {
		_ = h.editReviewEventReactionMessage(ctx, msg, compactSentence(err.Error()))
		return nil
	}
	if status == session.CapabilityReviewStatusApproved {
		materializable, err := h.capabilityRequestHasCompiledGrantHandoff(record)
		if err != nil {
			return err
		}
		if !materializable {
			return h.editReviewEventReactionMessage(ctx, msg, reviewEventMissingGrantContractText(record, event))
		}
	}
	review, _, err := h.store.ApplyCapabilityReviewTransition(session.CapabilityReviewTransitionInput{
		Review: session.CapabilityReview{
			ReviewID:     reviewEventCapabilityReviewID(event.ID, msg.SenderID, action),
			RequestID:    record.RequestID,
			Reviewer:     fmt.Sprintf("telegram:%d", msg.SenderID),
			ReviewerRole: reviewerRole,
			Status:       status,
			Rationale:    fmt.Sprintf("telegram review event %d", event.ID),
		},
		AllowedCurrentStatus: reviewEventAllowedCurrentStatuses(record, action),
	})
	if err != nil {
		return err
	}
	grant, grantActivated, grantErr := h.materializeApprovedCapabilityGrantFromReview(record, review, event, msg.SenderID)
	if grantErr != nil {
		return h.editReviewEventReactionMessage(ctx, msg, reviewEventConfirmationText(labelForCapabilityReview(review.Status), record, event)+"\n\nGrant activation needs repair: "+compactSentence(grantErr.Error()))
	}
	label := labelForCapabilityReview(review.Status)
	if refreshed, ok, err := h.store.CapabilityRequest(record.RequestID); err == nil && ok {
		record = refreshed
	}
	text := reviewEventConfirmationText(label, record, event)
	text = reviewEventConfirmationWithGrantActivation(text, record, grant, grantActivated, review.Status)
	if review.Status == session.CapabilityReviewStatusApproved {
		return h.editReviewEventReactionMessageWithLookahead(ctx, msg, event, text)
	}
	return h.editReviewEventReactionMessage(ctx, msg, text)
}

func reviewEventConfirmationWithGrantActivation(text string, record session.CapabilityRequest, grant session.CapabilityGrant, activated bool, status session.CapabilityReviewStatus) string {
	if session.NormalizeCapabilityReviewStatus(status) != session.CapabilityReviewStatusApproved {
		return strings.TrimSpace(text)
	}
	if activated {
		return strings.TrimSpace(text + "\n\nGrant activation: active\nGrant: " + strings.TrimSpace(grant.GrantID))
	}
	if linked := strings.TrimSpace(record.GrantID); linked != "" {
		return strings.TrimSpace(text + "\n\nGrant activation: already linked\nGrant: " + linked)
	}
	return strings.TrimSpace(text + "\n\nGrant activation: not created\nNext: run capability_authority grant_set for this approved request before using the capability.")
}

func reviewEventMissingGrantContractText(record session.CapabilityRequest, event session.ReviewEvent) string {
	record = session.NormalizeCapabilityRequest(record)
	lines := []string{
		"Capability approval needs an exact grant contract before it can be approved.",
		"",
		"Request: " + strings.TrimSpace(record.RequestID),
	}
	if event.ID > 0 {
		lines = append(lines, fmt.Sprintf("Review event: %d", event.ID))
	}
	lines = append(lines,
		"",
		"Grant activation: missing compiled grant contract",
		"Next: create a capability request with structured allowed_actions or capability_action so approval can materialize a bounded active grant.",
	)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (h *DecisionHandler) editReviewEventReactionMessage(ctx context.Context, msg core.InboundMessage, text string) error {
	if h == nil || h.sender == nil || msg.ChatID == 0 || msg.Reaction == nil || msg.Reaction.MessageID <= 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	return EditDecisionMessageClearingInlineKeyboard(ctx, h.sender, msg.ChatID, msg.Reaction.MessageID, text)
}

func (h *DecisionHandler) editReviewEventReactionMessageWithLookahead(ctx context.Context, msg core.InboundMessage, event session.ReviewEvent, text string) error {
	if h == nil || h.sender == nil || msg.ChatID == 0 || msg.Reaction == nil || msg.Reaction.MessageID <= 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	if editor, ok := h.sender.(interface {
		EditMessageTextWithInlineKeyboard(context.Context, int64, int64, string, string, [][]telegram.InlineButton) error
	}); ok {
		return editor.EditMessageTextWithInlineKeyboard(ctx, msg.ChatID, msg.Reaction.MessageID, text, "", reviewEventLookaheadRows(event.ID))
	}
	return h.editReviewEventReactionMessage(ctx, msg, text)
}

func labelForCapabilityReview(status session.CapabilityReviewStatus) string {
	switch session.NormalizeCapabilityReviewStatus(status) {
	case session.CapabilityReviewStatusParentApproved:
		return "parent-approved"
	case session.CapabilityReviewStatusRejected:
		return "rejected"
	case session.CapabilityReviewStatusApproved:
		return "approved"
	default:
		return "reviewed"
	}
}

type reviewCapabilityGrantSetInput struct {
	Action           string          `json:"action"`
	RequestID        string          `json:"request_id,omitempty"`
	GrantID          string          `json:"grant_id,omitempty"`
	Kind             string          `json:"kind,omitempty"`
	TargetResource   string          `json:"target_resource,omitempty"`
	CapabilityAction string          `json:"capability_action,omitempty"`
	Principal        string          `json:"principal,omitempty"`
	AllowedActions   []string        `json:"allowed_actions,omitempty"`
	Contract         json.RawMessage `json:"contract,omitempty"`
	Constraints      json.RawMessage `json:"constraints,omitempty"`
	GrantStatus      string          `json:"grant_status,omitempty"`
	ExpiresInSeconds int             `json:"expires_in_seconds,omitempty"`
}

func (h *DecisionHandler) materializeApprovedCapabilityGrantFromReview(record session.CapabilityRequest, review session.CapabilityReview, event session.ReviewEvent, reviewerTelegramID int64) (session.CapabilityGrant, bool, error) {
	if h == nil || h.store == nil {
		return session.CapabilityGrant{}, false, nil
	}
	if session.NormalizeCapabilityReviewStatus(review.Status) != session.CapabilityReviewStatusApproved {
		return session.CapabilityGrant{}, false, nil
	}
	record = session.NormalizeCapabilityRequest(record)
	if record.RequestID == "" {
		return session.CapabilityGrant{}, false, nil
	}
	actions, err := h.store.OpenNextActionsBySubject("capability_request", record.RequestID, 100)
	if err != nil {
		return session.CapabilityGrant{}, false, fmt.Errorf("load compiled capability grant handoff: %w", err)
	}
	for _, action := range actions {
		input, ok, err := reviewCapabilityGrantSetFromNextAction(record, action)
		if err != nil {
			return session.CapabilityGrant{}, false, err
		}
		if !ok {
			continue
		}
		grant, err := h.applyApprovedCapabilityGrantSet(record, input, action, event, reviewerTelegramID)
		if err != nil {
			return session.CapabilityGrant{}, false, err
		}
		return grant, true, nil
	}
	return session.CapabilityGrant{}, false, nil
}

func (h *DecisionHandler) capabilityRequestHasCompiledGrantHandoff(record session.CapabilityRequest) (bool, error) {
	if h == nil || h.store == nil {
		return false, nil
	}
	record = session.NormalizeCapabilityRequest(record)
	if strings.TrimSpace(record.GrantID) != "" {
		return true, nil
	}
	if strings.TrimSpace(record.RequestID) == "" {
		return false, nil
	}
	actions, err := h.store.OpenNextActionsBySubject("capability_request", record.RequestID, 100)
	if err != nil {
		return false, fmt.Errorf("load compiled capability grant handoff: %w", err)
	}
	for _, action := range actions {
		_, ok, err := reviewCapabilityGrantSetFromNextAction(record, action)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func reviewCapabilityGrantSetFromNextAction(record session.CapabilityRequest, action session.NextActionRecord) (reviewCapabilityGrantSetInput, bool, error) {
	if session.NormalizeNextActionState(action.State) != session.NextActionBlockedNeedsAuthority {
		return reviewCapabilityGrantSetInput{}, false, nil
	}
	if strings.TrimSpace(action.OperationTool) != "capability_authority" || action.OperationKind != "capability_grant_review" {
		return reviewCapabilityGrantSetInput{}, false, nil
	}
	if action.SubjectKind != "capability_request" || strings.TrimSpace(action.SubjectRef) != strings.TrimSpace(record.RequestID) {
		return reviewCapabilityGrantSetInput{}, false, nil
	}
	var input reviewCapabilityGrantSetInput
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &input); err != nil {
		return reviewCapabilityGrantSetInput{}, false, fmt.Errorf("decode compiled capability grant handoff: %w", err)
	}
	if strings.TrimSpace(input.Action) != "grant_set" {
		return reviewCapabilityGrantSetInput{}, false, nil
	}
	if strings.TrimSpace(input.RequestID) != strings.TrimSpace(record.RequestID) {
		return reviewCapabilityGrantSetInput{}, false, fmt.Errorf("compiled capability grant handoff request mismatch: got %q want %q", strings.TrimSpace(input.RequestID), strings.TrimSpace(record.RequestID))
	}
	return input, true, nil
}

func (h *DecisionHandler) applyApprovedCapabilityGrantSet(record session.CapabilityRequest, input reviewCapabilityGrantSetInput, action session.NextActionRecord, event session.ReviewEvent, reviewerTelegramID int64) (session.CapabilityGrant, error) {
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(firstTelegramDecisionNonEmpty(input.Kind, string(record.Kind))))
	if kind == "" {
		return session.CapabilityGrant{}, fmt.Errorf("compiled capability grant handoff requires kind")
	}
	target := firstTelegramDecisionNonEmpty(input.TargetResource, record.TargetResource)
	if target == "" {
		return session.CapabilityGrant{}, fmt.Errorf("compiled capability grant handoff requires target_resource")
	}
	grantedTo := firstTelegramDecisionNonEmpty(input.Principal, record.RequestedFor, record.RequestedBy)
	if grantedTo == "" {
		return session.CapabilityGrant{}, fmt.Errorf("compiled capability grant handoff requires principal")
	}
	actions := session.NormalizeCapabilityActions(input.AllowedActions)
	if len(actions) == 0 && strings.TrimSpace(input.CapabilityAction) != "" {
		actions = session.NormalizeCapabilityActions([]string{input.CapabilityAction})
	}
	if len(actions) == 0 {
		actions = []string{"invoke"}
	}
	contract, err := reviewNormalizeCapabilityJSONBlobWithDefault(input.Contract, "contract", record.Contract)
	if err != nil {
		return session.CapabilityGrant{}, err
	}
	constraints, err := reviewNormalizeCapabilityJSONBlobWithDefault(input.Constraints, "constraints", record.Constraints)
	if err != nil {
		return session.CapabilityGrant{}, err
	}
	if reviewCapabilityContractHasUpdatePlan(contract) {
		return session.CapabilityGrant{}, fmt.Errorf("compiled capability grant handoff requires capability_authority execution for capability_update_plan")
	}
	status := session.NormalizeCapabilityGrantStatus(session.CapabilityGrantStatus(input.GrantStatus))
	if strings.TrimSpace(input.GrantStatus) != "" && status == "" {
		return session.CapabilityGrant{}, fmt.Errorf("compiled capability grant handoff grant_status is invalid")
	}
	if status == "" {
		status = session.CapabilityGrantStatusActive
	}
	if err := h.validateReviewCapabilityGrantTarget(grantedTo, status); err != nil {
		return session.CapabilityGrant{}, err
	}
	now := time.Now().UTC()
	expiresAt := time.Time{}
	if input.ExpiresInSeconds > 0 {
		expiresAt = now.Add(time.Duration(input.ExpiresInSeconds) * time.Second)
	}
	grantID := strings.TrimSpace(input.GrantID)
	if grantID == "" {
		grantID = reviewCapabilityGrantID(record.RequestID, action.RecordID)
	}
	policyHash := reviewCapabilityGrantPolicyHash(kind, target, grantedTo, actions, contract, constraints)
	grant, err := h.store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:            grantID,
		RequestID:          record.RequestID,
		GrantedBy:          fmt.Sprintf("telegram:%d", reviewerTelegramID),
		GrantedTo:          grantedTo,
		Kind:               kind,
		TargetResource:     target,
		AllowedActions:     actions,
		Contract:           contract,
		Constraints:        constraints,
		Status:             status,
		BaselinePolicyHash: policyHash,
		CurrentPolicyHash:  policyHash,
		AnchorFingerprint:  policyHash,
		GrantedAt:          now,
		ExpiresAt:          expiresAt,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		return session.CapabilityGrant{}, err
	}
	key := sessionKeyForReviewNextAction(action)
	payloadRaw, _ := json.Marshal(map[string]any{
		"grant_id":        grant.GrantID,
		"request_id":      grant.RequestID,
		"review_event_id": event.ID,
		"kind":            string(grant.Kind),
		"target_resource": grant.TargetResource,
		"granted_to":      grant.GrantedTo,
		"granted_by":      grant.GrantedBy,
		"status":          string(grant.Status),
		"allowed_actions": grant.AllowedActions,
	})
	if _, err := h.store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventCapabilityGrantChanged,
		Stage:       "capability_delegation",
		Status:      string(grant.Status),
		PayloadJSON: string(payloadRaw),
		CreatedAt:   now,
	}); err != nil {
		return session.CapabilityGrant{}, fmt.Errorf("record capability grant activation event: %w", err)
	}
	if grant.Status == session.CapabilityGrantStatusActive {
		if err := h.store.ResolveNextAction(session.NextActionResolutionInput{
			RecordID:    action.RecordID,
			Key:         key,
			Owner:       "capability_authority",
			SubjectKind: "capability_request",
			SubjectRef:  record.RequestID,
			Reason:      "capability_grant_active",
			ResolvedAt:  now,
		}); err != nil {
			return session.CapabilityGrant{}, fmt.Errorf("resolve capability grant blocker: %w", err)
		}
	}
	return grant, nil
}

func reviewNormalizeCapabilityJSONBlobWithDefault(raw json.RawMessage, field string, fallback string) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		trimmed = strings.TrimSpace(fallback)
	}
	if trimmed == "" {
		trimmed = "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("capability %s must be valid json", strings.TrimSpace(field))
	}
	return trimmed, nil
}

func reviewCapabilityContractHasUpdatePlan(contract string) bool {
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(contract)), &payload); err != nil {
		return false
	}
	_, ok := payload["capability_update_plan"]
	return ok
}

func (h *DecisionHandler) validateReviewCapabilityGrantTarget(grantedTo string, status session.CapabilityGrantStatus) error {
	if status != session.CapabilityGrantStatusActive || h == nil || h.store == nil {
		return nil
	}
	agentID, ok := core.DurableAgentIDFromPrincipal(grantedTo)
	if !ok {
		return nil
	}
	if _, err := h.store.DurableAgent(agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("capability grant target durable agent %q does not exist", agentID)
		}
		return fmt.Errorf("load durable agent %q for capability grant: %w", agentID, err)
	}
	return nil
}

func reviewCapabilityGrantPolicyHash(kind session.CapabilityKind, target string, principalID string, actions []string, contract string, constraints string) string {
	payload := map[string]any{
		"kind":            string(kind),
		"target_resource": strings.TrimSpace(target),
		"principal":       strings.TrimSpace(principalID),
		"allowed_actions": session.NormalizeCapabilityActions(actions),
		"contract":        strings.TrimSpace(contract),
		"constraints":     strings.TrimSpace(constraints),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func reviewCapabilityGrantID(requestID string, actionRecordID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(requestID) + "\x00" + strings.TrimSpace(actionRecordID)))
	return "capg-review-" + hex.EncodeToString(sum[:8])
}

func sessionKeyForReviewNextAction(record session.NextActionRecord) session.SessionKey {
	return session.SessionKey{
		ChatID: record.ChatID,
		UserID: record.UserID,
		Scope:  record.Scope,
	}
}

func (h *DecisionHandler) handleMissionControlProposalCallback(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent, proposal core.MissionControlProposal, action core.ReviewEventAction) error {
	if h == nil || h.store == nil {
		return nil
	}
	fromID := int64(0)
	if cb.From != nil {
		fromID = cb.From.ID
	}
	if fromID <= 0 || (event.TargetAdminChatID > 0 && fromID != event.TargetAdminChatID) {
		return h.answerReviewEventCallback(ctx, cb, "Only the target admin can review this Mission Control proposal.")
	}
	proposal = core.NormalizeMissionControlProposal(proposal)
	switch action {
	case core.ReviewEventActionMissionAdd:
		missionID := strings.TrimSpace(proposal.MissionID)
		if missionID == "" {
			missionID = fmt.Sprintf("mission-proposal-%d", event.ID)
		}
		owner := strings.TrimSpace(proposal.Owner)
		if owner == "" {
			owner = fmt.Sprintf("telegram:%d", fromID)
		}
		refs := append([]string(nil), proposal.SourceRefs...)
		refs = append(refs, fmt.Sprintf("review_event:%d", event.ID))
		mission, err := h.store.UpsertMission(session.MissionState{
			ID:                missionID,
			Title:             proposal.Title,
			Objective:         proposal.Objective,
			Origin:            firstTelegramDecisionNonEmpty(proposal.Origin, "proposed"),
			Scope:             firstTelegramDecisionNonEmpty(proposal.Scope, "principal"),
			Owner:             owner,
			Status:            session.MissionStatusCandidate,
			Pinned:            false,
			Tags:              proposal.Tags,
			SourceRefs:        refs,
			SuccessCriteria:   proposal.SuccessCriteria,
			NextAllowedAction: proposal.NextAllowedAction,
			Authority:         session.DefaultMissionAuthority(),
			Decay:             session.DefaultMissionDecay(),
		}, fmt.Sprintf("telegram:%d", fromID), "Mission Control proposal approved; candidate mission added")
		if err != nil {
			return err
		}
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("added", mission, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	case core.ReviewEventActionMissionAskEdit:
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("ask_edit", session.MissionState{}, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	case core.ReviewEventActionMissionPark:
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("parked", session.MissionState{}, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	case core.ReviewEventActionMissionReject:
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("rejected", session.MissionState{}, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	default:
		return h.answerReviewEventCallback(ctx, cb, "This Mission Control proposal action is not available.")
	}
}

func renderMissionControlProposalCallbackResult(status string, mission session.MissionState, proposal core.MissionControlProposal) string {
	proposal = core.NormalizeMissionControlProposal(proposal)
	title := strings.TrimSpace(mission.Title)
	if title == "" {
		title = strings.TrimSpace(proposal.Title)
	}
	if title == "" {
		title = strings.TrimSpace(proposal.Objective)
	}
	switch strings.TrimSpace(status) {
	case "added":
		lines := []string{"Mission Control proposal added."}
		if mission.ID != "" {
			lines = append(lines, "Mission: "+mission.ID)
		}
		if title != "" {
			lines = append(lines, "Title: "+title)
		}
		lines = append(lines, "Status: candidate", "No execution or self-continuation authority was granted.")
		return strings.Join(lines, "\n")
	case "ask_edit":
		return "Mission Control proposal needs edits. I will revise it before asking again. No mission was created."
	case "parked":
		return "Mission Control proposal parked. No mission was created and no execution authority was granted."
	case "rejected":
		return "Mission Control proposal rejected. No mission was created."
	default:
		return "Mission Control proposal reviewed."
	}
}

func firstTelegramDecisionNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type reviewEventArtifactMetadata struct {
	AgentID       string            `json:"agent_id"`
	Summary       string            `json:"summary"`
	IntervalLabel string            `json:"interval_label"`
	LocalActions  []string          `json:"local_actions"`
	Questions     []string          `json:"questions"`
	RiskFlags     []string          `json:"risk_flags"`
	ArtifactRefs  []string          `json:"artifact_refs"`
	Metadata      map[string]string `json:"metadata"`
}

func reviewEventDetailsExpandable(event session.ReviewEvent) bool {
	if _, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		return false
	}
	if strings.TrimSpace(event.Summary) == "" {
		return false
	}
	scope := session.NormalizeScopeRef(event.SourceScope)
	return scope.Kind == session.ScopeKindDurableAgent || strings.TrimSpace(scope.DurableAgentID) != "" || strings.TrimSpace(event.SourceRole) == "durable_agent"
}

func reviewEventInlineRowsExpanded(event session.ReviewEvent, expanded bool) [][]telegram.InlineButton {
	if _, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		return [][]telegram.InlineButton{{
			{Text: "Reject", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionReject)},
			{Text: "Add mission", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionAdd)},
		}, {
			{Text: "Park", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionPark)},
			{Text: "Change", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionAskEdit)},
		}}
	}
	rows := [][]telegram.InlineButton{}
	if reviewEventDetailsExpandable(event) {
		action := core.ReviewEventActionExpand
		label := "Details"
		if expanded {
			action = core.ReviewEventActionHide
			label = "Hide details"
		}
		rows = append(rows, []telegram.InlineButton{{Text: label, CallbackData: core.EncodeReviewEventCallbackData(event.ID, action)}})
	}
	requestID := reviewEventCallbackCapabilityRequestID(event)
	if requestID == "" {
		return rows
	}
	if reviewEventCallbackMetadataString(event, "parent_principal") != "" && reviewEventCallbackMetadataString(event, "review_status") == string(session.CapabilityReviewStatusProposed) {
		rows = append(rows, []telegram.InlineButton{{Text: "Parent approve", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionParentApprove)}})
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "Reject", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionReject)},
		{Text: "Approve", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionApprove)},
	})
	return rows
}

func formatReviewEventCompactMessage(event session.ReviewEvent) string {
	if proposal, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		proposal = core.NormalizeMissionControlProposal(proposal)
		return strings.TrimSpace("Mission Control proposal\n" + proposal.Title + "\n" + proposal.Objective)
	}
	meta, _ := parseReviewEventArtifactMetadata(event)
	title := "Review"
	if agent := strings.TrimSpace(meta.AgentID); agent != "" {
		title = "Review: " + agent
	} else if scope := session.NormalizeScopeRef(event.SourceScope); strings.TrimSpace(scope.DurableAgentID) != "" {
		title = "Review: " + strings.TrimSpace(scope.DurableAgentID)
	}
	lines := []string{"**" + title + "**"}
	if summary := strings.TrimSpace(meta.Summary); summary != "" {
		lines = append(lines, "", "**Summary**", summary)
	} else if summary := strings.TrimSpace(event.Summary); summary != "" {
		lines = append(lines, "", "**Summary**", summary)
	}
	if len(meta.LocalActions) > 0 {
		lines = append(lines, "", "**Key points**")
		for _, action := range meta.LocalActions {
			if action = strings.TrimSpace(action); action != "" {
				lines = append(lines, "- "+action)
			}
		}
	}
	lines = append(lines, "", "Details has the full child update.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatReviewEventDetailsMessage(event session.ReviewEvent) string {
	meta, ok := parseReviewEventArtifactMetadata(event)
	lines := []string{strings.TrimSpace(event.Summary)}
	if lines[0] == "" {
		lines[0] = "Review event details."
	}
	if ok && len(meta.ArtifactRefs) > 0 {
		lines = append(lines, "", "**Artifacts**")
		for _, ref := range meta.ArtifactRefs {
			if ref = strings.TrimSpace(ref); ref != "" {
				lines = append(lines, "- "+ref)
			}
		}
	}
	if ok && len(meta.Metadata) > 0 {
		keys := make([]string, 0, len(meta.Metadata))
		for key := range meta.Metadata {
			if strings.TrimSpace(key) != "" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			lines = append(lines, "", "**Metadata**")
			for _, key := range keys {
				value := strings.TrimSpace(meta.Metadata[key])
				if value != "" {
					lines = append(lines, "- "+key+": "+value)
				}
			}
		}
	}
	lines = append(lines, "", "Use Hide details to return to the compact summary.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func parseReviewEventArtifactMetadata(event session.ReviewEvent) (reviewEventArtifactMetadata, bool) {
	var meta reviewEventArtifactMetadata
	if strings.TrimSpace(event.MetadataJSON) == "" {
		return meta, false
	}
	if err := json.Unmarshal([]byte(event.MetadataJSON), &meta); err != nil {
		return reviewEventArtifactMetadata{}, false
	}
	return meta, true
}

func (h *DecisionHandler) handleReviewEventDetailToggle(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent, expanded bool) error {
	if !reviewEventDetailsExpandable(event) {
		return h.answerReviewEventCallback(ctx, cb, "This review item has no expandable details.")
	}
	if h == nil || h.sender == nil || cb.Message == nil || cb.Message.Chat == nil || cb.Message.MessageID == 0 {
		return h.answerReviewEventCallback(ctx, cb, "")
	}
	text := formatReviewEventCompactMessage(event)
	if expanded {
		text = formatReviewEventDetailsMessage(event)
	}
	rows := reviewEventInlineRowsExpanded(event, expanded)
	if editor, ok := h.sender.(DecisionKeyboardEditor); ok && len(rows) > 0 {
		if err := editor.EditMessageTextWithInlineKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, "", rows); err != nil {
			return err
		}
	} else if err := EditDecisionMessageClearingInlineKeyboard(ctx, h.sender, cb.Message.Chat.ID, cb.Message.MessageID, text); err != nil {
		return err
	}
	return h.answerReviewEventCallback(ctx, cb, "")
}

func (h *DecisionHandler) reviewEventDetailAuthorizationFailure(event session.ReviewEvent, cb telegram.CallbackQuery) (string, error) {
	fromID := callbackSenderID(cb)
	if fromID <= 0 {
		return "Only the target reviewer can view these review details.", nil
	}
	if _, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		if event.TargetAdminChatID > 0 && fromID == event.TargetAdminChatID {
			return "", nil
		}
		return "Only the target admin can view this Mission Control proposal.", nil
	}
	requestID := reviewEventCallbackCapabilityRequestID(event)
	if requestID != "" {
		record, ok, err := h.store.CapabilityRequest(requestID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "Capability request not found.", nil
		}
		if reviewEventCapabilityActorCanViewDetails(record, fromID, event.TargetAdminChatID) {
			return "", nil
		}
		return "Only the admin or parent can view these review details.", nil
	}
	if event.TargetAdminChatID > 0 && fromID == event.TargetAdminChatID {
		return "", nil
	}
	return "Only the target admin can view these review details.", nil
}

func reviewEventCapabilityActorCanViewDetails(record session.CapabilityRequest, fromID int64, targetChatID int64) bool {
	if fromID <= 0 {
		return false
	}
	record = session.NormalizeCapabilityRequest(record)
	isAdmin := telegramPrincipalMatches(record.AdminPrincipal, fromID) || (strings.TrimSpace(record.AdminPrincipal) == "" && targetChatID == fromID)
	isParent := telegramPrincipalMatches(record.ParentPrincipal, fromID)
	return isAdmin || isParent
}

func reviewEventConfirmationText(label string, record session.CapabilityRequest, event session.ReviewEvent) string {
	record = session.NormalizeCapabilityRequest(record)
	label = strings.TrimSpace(label)
	if label == "" {
		label = "reviewed"
	}
	lines := []string{"Capability request " + label + "."}
	if record.RequestID != "" {
		lines = append(lines, "Request: "+record.RequestID)
	}
	if event.ID > 0 {
		lines = append(lines, fmt.Sprintf("Review event: %d", event.ID))
	}
	meta := make([]string, 0, 3)
	if record.Kind != "" {
		meta = append(meta, "Kind: "+string(record.Kind))
	}
	if target := strings.TrimSpace(record.TargetResource); target != "" {
		meta = append(meta, "Target: "+target)
	}
	if risk := strings.TrimSpace(record.RiskClass); risk != "" {
		meta = append(meta, "Risk: "+risk)
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, " · "))
	}
	if purpose := strings.TrimSpace(record.Purpose); purpose != "" {
		lines = append(lines, "Purpose: "+compactSentence(purpose))
	}
	if summary := strings.TrimSpace(event.Summary); summary != "" {
		lines = append(lines, "", "Approved content:", summary)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (h *DecisionHandler) answerReviewEventCallback(ctx context.Context, cb telegram.CallbackQuery, text string) error {
	if h == nil || h.sender == nil {
		return nil
	}
	if err := h.sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return err
	}
	return nil
}

func (h *DecisionHandler) editReviewEventCallbackMessage(ctx context.Context, cb telegram.CallbackQuery, text string) error {
	if h == nil || h.sender == nil || cb.Message == nil || cb.Message.Chat == nil || cb.Message.MessageID == 0 {
		return nil
	}
	return EditDecisionMessageClearingInlineKeyboard(ctx, h.sender, cb.Message.Chat.ID, cb.Message.MessageID, text)
}

func (h *DecisionHandler) editReviewEventCallbackMessageWithLookahead(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent, text string) error {
	if h == nil || h.sender == nil || cb.Message == nil || cb.Message.Chat == nil || cb.Message.MessageID == 0 {
		return nil
	}
	if editor, ok := h.sender.(interface {
		EditMessageTextWithInlineKeyboard(context.Context, int64, int64, string, string, [][]telegram.InlineButton) error
	}); ok {
		return editor.EditMessageTextWithInlineKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, "", reviewEventLookaheadRows(event.ID))
	}
	return h.editReviewEventCallbackMessage(ctx, cb, text)
}

func reviewEventLookaheadRows(eventID int64) [][]telegram.InlineButton {
	data := core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionLookaheadNext)
	if strings.TrimSpace(data) == "" {
		return nil
	}
	return [][]telegram.InlineButton{{{Text: "Next grant", CallbackData: data}}}
}

func reviewEventCallbackExpired(event session.ReviewEvent, now time.Time) bool {
	start := event.DeliveredAt
	if start.IsZero() {
		start = event.CreatedAt
	}
	if start.IsZero() {
		return false
	}
	return now.After(start.Add(DefaultUserApprovalTimeout))
}

func reviewEventCallbackCapabilityRequestID(event session.ReviewEvent) string {
	if id := reviewEventCallbackMetadataString(event, "request_id"); id != "" {
		return id
	}
	return reviewEventCallbackMetadataString(event, "capability_request_id")
}

func reviewEventCallbackMetadataString(event session.ReviewEvent, key string) string {
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
	return ""
}

func reviewEventRequestStillActionable(record session.CapabilityRequest, action core.ReviewEventAction) bool {
	record = session.NormalizeCapabilityRequest(record)
	switch action {
	case core.ReviewEventActionParentApprove:
		return record.ReviewStatus == session.CapabilityReviewStatusProposed
	case core.ReviewEventActionApprove:
		if strings.TrimSpace(record.ParentPrincipal) != "" {
			return record.ReviewStatus == session.CapabilityReviewStatusParentApproved
		}
		return record.ReviewStatus == session.CapabilityReviewStatusProposed
	case core.ReviewEventActionReject:
		return record.ReviewStatus == session.CapabilityReviewStatusProposed || record.ReviewStatus == session.CapabilityReviewStatusParentApproved
	default:
		return false
	}
}

func reviewEventAllowedCurrentStatuses(record session.CapabilityRequest, action core.ReviewEventAction) []session.CapabilityReviewStatus {
	record = session.NormalizeCapabilityRequest(record)
	switch action {
	case core.ReviewEventActionParentApprove:
		return []session.CapabilityReviewStatus{session.CapabilityReviewStatusProposed}
	case core.ReviewEventActionApprove:
		if strings.TrimSpace(record.ParentPrincipal) != "" {
			return []session.CapabilityReviewStatus{session.CapabilityReviewStatusParentApproved}
		}
		return []session.CapabilityReviewStatus{session.CapabilityReviewStatusProposed}
	case core.ReviewEventActionReject:
		return []session.CapabilityReviewStatus{session.CapabilityReviewStatusProposed, session.CapabilityReviewStatusParentApproved}
	default:
		return nil
	}
}

func reviewEventCapabilityReviewID(eventID int64, reviewerID int64, action core.ReviewEventAction) string {
	actionPart := strings.Trim(strings.ToLower(string(action)), " _-")
	if actionPart == "" {
		actionPart = "review"
	}
	return fmt.Sprintf("capr-review-event-%d-%d-%s", eventID, reviewerID, actionPart)
}

func reviewEventCapabilityStatusForAction(record session.CapabilityRequest, action core.ReviewEventAction, fromID int64, targetChatID int64) (session.CapabilityReviewStatus, string, error) {
	if fromID <= 0 {
		return "", "", fmt.Errorf("Telegram reviewer is unknown.")
	}
	// Empty AdminPrincipal means the delivered one-to-one admin review target owns
	// final approval. Telegram group chats are negative IDs, so they cannot satisfy
	// this positive user/chat equality fallback.
	isAdmin := telegramPrincipalMatches(record.AdminPrincipal, fromID) || (strings.TrimSpace(record.AdminPrincipal) == "" && targetChatID == fromID)
	isParent := telegramPrincipalMatches(record.ParentPrincipal, fromID)
	switch action {
	case core.ReviewEventActionParentApprove:
		if strings.TrimSpace(record.ParentPrincipal) == "" {
			return "", "", fmt.Errorf("This request has no parent approval step.")
		}
		if !isParent && !isAdmin {
			return "", "", fmt.Errorf("Only the parent or admin can parent-approve this request.")
		}
		return session.CapabilityReviewStatusParentApproved, reviewerRoleForReview(isAdmin && !isParent), nil
	case core.ReviewEventActionApprove:
		if strings.TrimSpace(record.ParentPrincipal) != "" && record.ReviewStatus == session.CapabilityReviewStatusProposed {
			return "", "", fmt.Errorf("Parent approval is required first.")
		}
		if !isAdmin {
			return "", "", fmt.Errorf("Only the admin can approve this request.")
		}
		return session.CapabilityReviewStatusApproved, string(principal.RoleAdmin), nil
	case core.ReviewEventActionReject:
		if !isAdmin && !isParent {
			return "", "", fmt.Errorf("Only the admin or parent can reject this request.")
		}
		return session.CapabilityReviewStatusRejected, reviewerRoleForReview(isAdmin), nil
	default:
		return "", "", fmt.Errorf("Unknown review action.")
	}
}

func reviewerRoleForReview(admin bool) string {
	if admin {
		return string(principal.RoleAdmin)
	}
	return string(principal.RoleApprovedUser)
}

func telegramPrincipalMatches(target string, userID int64) bool {
	return userID > 0 && strings.TrimSpace(target) == fmt.Sprintf("telegram:%d", userID)
}
