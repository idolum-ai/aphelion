//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
)

const (
	operatorAutoApprovalDefaultScope = session.OperatorAutoApprovalScopeAll
	operatorAutoApprovalMinDuration  = time.Minute
	operatorAutoApprovalMaxDuration  = 24 * time.Hour
)

type operatorAutoApprovalRequest struct {
	ChatID          int64
	TargetScopeKind string
	TargetScopeID   string
	Kind            string
	Choice          string
	DecisionID      string
	ProposalID      string
	Summary         string
	Details         string
	WorkMode        WorkMode
}

func (r *Runtime) ConfigureAutoApproval(ctx context.Context, chatID int64, adminUserID int64, args string) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.configureAutoApprovalForScope(ctx, chatID, scopeKind, scopeID, adminUserID, args)
}

func (r *Runtime) ConfigureAutoApprovalForKey(ctx context.Context, key session.SessionKey, adminUserID int64, args string) (string, error) {
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	return r.configureAutoApprovalForScope(ctx, key.ChatID, scopeKind, scopeID, adminUserID, args)
}

func (r *Runtime) configureAutoApprovalForScope(ctx context.Context, chatID int64, scopeKind string, scopeID string, adminUserID int64, args string) (string, error) {
	if r == nil || r.store == nil {
		return "Auto approvals are unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Auto approvals are admin only.", nil
	}
	action, spec, err := parseOperatorAutoApprovalCommand(args)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	switch action {
	case "status":
		return r.renderOperatorAutoApprovalStatusForScope(chatID, scopeKind, scopeID, adminUserID, now)
	case "off":
		revoked, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(chatID, adminUserID, scopeKind, scopeID, now)
		if err != nil {
			return "", err
		}
		r.recordOperatorAutoApprovalEvent(
			chatID,
			core.ExecutionEventAutoApprovalRevoked,
			"revoked",
			operatorAutoApprovalPrimaryLeaseForScope(revoked, chatID, scopeKind, scopeID, adminUserID),
			operatorAutoApprovalRevokedEventPayload(revoked, now),
		)
		return renderOperatorAutoApprovalRevoked(revoked, now), nil
	case "double":
		return r.doubleOperatorAutoApprovalForScope(ctx, chatID, scopeKind, scopeID, adminUserID, now)
	case "enable":
		lease := session.OperatorAutoApprovalLease{
			ID:          newOperatorAutoApprovalLeaseID(chatID, adminUserID, now),
			AdminUserID: adminUserID,
			ChatID:      chatID,
			ScopeKind:   scopeKind,
			ScopeID:     scopeID,
			Scope:       spec.Scope,
			Reason:      spec.Reason,
			MaxUses:     spec.MaxUses,
			CreatedAt:   now,
			ExpiresAt:   now.Add(spec.Duration),
			UpdatedAt:   now,
		}
		if _, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
			return "", err
		}
		created, err := r.store.CreateOperatorAutoApprovalLease(lease)
		if err != nil {
			return "", err
		}
		r.recordOperatorAutoApprovalEvent(chatID, core.ExecutionEventAutoApprovalGranted, "active", created, nil)
		blocked, err := r.operatorAutoApprovalBlockedReasonForScope(chatID, scopeKind, scopeID, adminUserID, created.Scope, now)
		if err != nil {
			return "", err
		}
		return renderOperatorAutoApprovalEnabled(created, now, blocked), nil
	default:
		return "", fmt.Errorf("unknown auto-approval action %q", action)
	}
}

func (r *Runtime) AutoApprovalStatus(ctx context.Context, chatID int64, adminUserID int64) (string, error) {
	_ = ctx
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.autoApprovalStatusForScope(chatID, scopeKind, scopeID, adminUserID)
}

func (r *Runtime) AutoApprovalStatusForKey(ctx context.Context, key session.SessionKey, adminUserID int64) (string, error) {
	_ = ctx
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	return r.autoApprovalStatusForScope(key.ChatID, scopeKind, scopeID, adminUserID)
}

func (r *Runtime) autoApprovalStatusForScope(chatID int64, scopeKind string, scopeID string, adminUserID int64) (string, error) {
	if r == nil || r.store == nil {
		return "Auto approvals are unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Auto approvals are admin only.", nil
	}
	return r.renderOperatorAutoApprovalStatusForScope(chatID, scopeKind, scopeID, adminUserID, time.Now().UTC())
}

func (r *Runtime) AutoResolveDecision(ctx context.Context, pending decision.PendingDecision) (decision.AutoResolution, error) {
	choice := autoApprovalChoiceForDecision(pending)
	if choice == "" {
		return decision.AutoResolution{}, nil
	}
	lease, ok, err := r.consumeOperatorAutoApproval(ctx, operatorAutoApprovalRequest{
		ChatID:          pending.ChatID,
		TargetScopeKind: firstNonEmptyContinuation(pending.ScopeKind, string(session.ScopeKindTelegramDM)),
		TargetScopeID:   firstNonEmptyContinuation(pending.ScopeID, fmt.Sprint(pending.ChatID)),
		Kind:            "decision:" + strings.TrimSpace(string(pending.Kind)),
		Choice:          choice,
		DecisionID:      strings.TrimSpace(pending.ID),
		Summary:         strings.TrimSpace(pending.Prompt),
		Details:         strings.TrimSpace(pending.Details),
	})
	if err != nil || !ok {
		return decision.AutoResolution{}, err
	}
	return decision.AutoResolution{Choice: choice, Reason: "auto_approved:" + strings.TrimSpace(lease.ID)}, nil
}

func (r *Runtime) maybeAutoApproveContinuationOffer(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState, source string) (bool, error) {
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending || state.RemainingTurns <= 0 {
		return false, nil
	}
	if state.ActionProposal.AutoApproveEligible != nil && !*state.ActionProposal.AutoApproveEligible {
		return false, nil
	}
	if inboundRequestsVisibleApprovalButtons(msg.Text) {
		return false, nil
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	lease, ok, err := r.consumeOperatorAutoApproval(ctx, operatorAutoApprovalRequest{
		ChatID:          key.ChatID,
		TargetScopeKind: scopeKind,
		TargetScopeID:   scopeID,
		Kind:            "continuation:" + strings.TrimSpace(source),
		Choice:          "approve",
		DecisionID:      strings.TrimSpace(state.DecisionID),
		ProposalID:      strings.TrimSpace(state.ActionProposal.ID),
		Summary:         firstNonEmptyContinuation(state.StageSummary, state.ActionProposal.Summary),
		Details:         firstNonEmptyContinuation(state.ActionProposal.BoundedEffect, state.GovernorIntent.Constraints),
		WorkMode:        continuationWorkMode(state),
	})
	if err != nil || !ok {
		return false, err
	}
	approved, err := r.ApproveContinuationForKey(key, lease.AdminUserID)
	if err != nil {
		return true, err
	}
	if approved.Status == session.ContinuationStatusApproved && approved.RemainingTurns > 0 {
		go r.triggerAutoApprovedContinuation(context.Background(), key, approved, lease)
	}
	_ = msg
	return true, nil
}

func inboundRequestsVisibleApprovalButtons(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" || !strings.Contains(lower, "button") {
		return false
	}
	return strings.Contains(lower, "approval") ||
		strings.Contains(lower, "approve") ||
		strings.Contains(lower, "request")
}

func (r *Runtime) triggerAutoApprovedContinuation(ctx context.Context, key session.SessionKey, state session.ContinuationState, lease session.OperatorAutoApprovalLease) {
	if r == nil || key.ChatID == 0 {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("auto-approved continuation trigger panic: %v", recovered)
			r.recordAutoApprovedContinuationTriggerFailure(ctx, key, state, lease, err)
		}
	}()
	if err := r.TriggerContinuationForKey(ctx, key); err != nil {
		r.recordAutoApprovedContinuationTriggerFailure(ctx, key, state, lease, err)
	}
}

func (r *Runtime) recordAutoApprovedContinuationTriggerFailure(ctx context.Context, key session.SessionKey, state session.ContinuationState, lease session.OperatorAutoApprovalLease, err error) {
	if r == nil || err == nil || key.ChatID == 0 {
		return
	}
	now := time.Now().UTC()
	payload := continuationExecutionPayload(state)
	payload["reason"] = "auto_approval_trigger_failed"
	payload["auto_approval_lease_id"] = strings.TrimSpace(lease.ID)
	payload["approved_by_user"] = lease.AdminUserID
	payload["error"] = truncatePreview(strings.TrimSpace(err.Error()), 500)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "auto_approval", "trigger_failed", payload, now)
	if r.outbound == nil {
		return
	}
	_, _ = r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID: key.ChatID,
		Text:   "Auto-approved continuation failed to start; I recorded the failure instead of silently dropping it. Error: " + truncatePreview(strings.TrimSpace(err.Error()), 300),
	})
}

func autoApprovalChoiceForDecision(pending decision.PendingDecision) string {
	switch pending.Kind {
	case decision.KindProposalApproval, decision.KindMemoryDelegation, decision.KindSnapshotRestore:
	default:
		return ""
	}
	for _, choice := range pending.Choices {
		if strings.TrimSpace(choice.ID) == "approve" {
			return "approve"
		}
	}
	return ""
}

func (r *Runtime) consumeOperatorAutoApproval(ctx context.Context, req operatorAutoApprovalRequest) (session.OperatorAutoApprovalLease, bool, error) {
	if r == nil || r.store == nil || req.ChatID == 0 {
		return session.OperatorAutoApprovalLease{}, false, nil
	}
	now := time.Now().UTC()
	gate, ok, err := r.operatorAutoModeGateForScope(req.ChatID, req.TargetScopeKind, req.TargetScopeID, 0, now)
	if err != nil || !ok {
		return session.OperatorAutoApprovalLease{}, false, err
	}
	if !operatorAutoModeScopeAllows(gate.Scope, req) {
		return session.OperatorAutoApprovalLease{}, false, nil
	}
	leases, err := r.store.ActiveOperatorAutoApprovalLeasesForScope(req.ChatID, req.TargetScopeKind, req.TargetScopeID, now)
	if err != nil {
		return session.OperatorAutoApprovalLease{}, false, err
	}
	for _, lease := range leases {
		if !operatorAutoApprovalScopeAllows(lease.Scope, req) {
			continue
		}
		used, ok, err := r.store.IncrementOperatorAutoApprovalUse(lease.ID, now)
		if err != nil {
			return session.OperatorAutoApprovalLease{}, false, err
		}
		if !ok {
			continue
		}
		r.recordOperatorAutoApprovalEvent(req.ChatID, core.ExecutionEventAutoApprovalUsed, "used", used, map[string]any{
			"auto_mode_source": strings.TrimSpace(gate.Source),
			"auto_mode_scope":  strings.TrimSpace(gate.Scope),
			"request_kind":     strings.TrimSpace(req.Kind),
			"choice":           strings.TrimSpace(req.Choice),
			"decision_id":      strings.TrimSpace(req.DecisionID),
			"proposal_id":      strings.TrimSpace(req.ProposalID),
			"summary":          truncatePreview(strings.TrimSpace(req.Summary), 220),
			"details":          truncatePreview(strings.TrimSpace(req.Details), 220),
			"work_mode":        strings.TrimSpace(string(req.WorkMode)),
		})
		_ = ctx
		return used, true, nil
	}
	return session.OperatorAutoApprovalLease{}, false, nil
}

func (r *Runtime) doubleOperatorAutoApproval(ctx context.Context, chatID int64, adminUserID int64, now time.Time) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.doubleOperatorAutoApprovalForScope(ctx, chatID, scopeKind, scopeID, adminUserID, now)
}

func (r *Runtime) doubleOperatorAutoApprovalForScope(ctx context.Context, chatID int64, scopeKind string, scopeID string, adminUserID int64, now time.Time) (string, error) {
	_ = ctx
	lease, ok, err := r.activeOperatorAutoApprovalLeaseForAdminAndScope(chatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no active auto approval to double")
	}
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	previousDuration, doubledDuration := doubledOperatorWindowDuration(lease.CreatedAt, lease.ExpiresAt, now, operatorAutoApprovalMinDuration, operatorAutoApprovalMaxDuration)
	maxUses := lease.MaxUses
	if lease.MaxUses > 0 {
		remainingUses := lease.MaxUses - lease.UsedCount
		if remainingUses <= 0 {
			return "", fmt.Errorf("active auto approval has no remaining uses to double")
		}
		maxUses = remainingUses
	}
	createdLease := session.OperatorAutoApprovalLease{
		ID:          newOperatorAutoApprovalLeaseID(chatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Scope:       lease.Scope,
		Reason:      lease.Reason,
		MaxUses:     maxUses,
		CreatedAt:   now,
		ExpiresAt:   now.Add(doubledDuration),
		UpdatedAt:   now,
	}
	if _, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return "", err
	}
	created, err := r.store.CreateOperatorAutoApprovalLease(createdLease)
	if err != nil {
		return "", err
	}
	r.recordOperatorAutoApprovalEvent(chatID, core.ExecutionEventAutoApprovalGranted, "active", created, map[string]any{
		"doubled_from_lease_id":     strings.TrimSpace(lease.ID),
		"previous_duration_seconds": int64(previousDuration / time.Second),
		"new_duration_seconds":      int64(doubledDuration / time.Second),
	})
	blocked, err := r.operatorAutoApprovalBlockedReasonForScope(chatID, scopeKind, scopeID, adminUserID, created.Scope, now)
	if err != nil {
		return "", err
	}
	return renderOperatorAutoApprovalDoubled(created, now, blocked, previousDuration, doubledDuration), nil
}

func (r *Runtime) activeOperatorAutoApprovalLeaseForAdmin(chatID int64, adminUserID int64, now time.Time) (session.OperatorAutoApprovalLease, bool, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.activeOperatorAutoApprovalLeaseForAdminAndScope(chatID, scopeKind, scopeID, adminUserID, now)
}

func (r *Runtime) activeOperatorAutoApprovalLeaseForAdminAndScope(chatID int64, scopeKind string, scopeID string, adminUserID int64, now time.Time) (session.OperatorAutoApprovalLease, bool, error) {
	if r == nil || r.store == nil || chatID == 0 || adminUserID <= 0 {
		return session.OperatorAutoApprovalLease{}, false, nil
	}
	leases, err := r.store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, now)
	if err != nil {
		return session.OperatorAutoApprovalLease{}, false, err
	}
	for _, lease := range leases {
		lease = session.NormalizeOperatorAutoApprovalLease(lease)
		if lease.AdminUserID == adminUserID && lease.ActiveAt(now) {
			return lease, true, nil
		}
	}
	return session.OperatorAutoApprovalLease{}, false, nil
}

func doubledOperatorWindowDuration(createdAt time.Time, expiresAt time.Time, now time.Time, minDuration time.Duration, maxDuration time.Duration) (time.Duration, time.Duration) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if !createdAt.IsZero() {
		createdAt = createdAt.UTC()
	}
	if !expiresAt.IsZero() {
		expiresAt = expiresAt.UTC()
	}
	previous := time.Duration(0)
	if !createdAt.IsZero() && expiresAt.After(createdAt) {
		previous = expiresAt.Sub(createdAt)
	} else if expiresAt.After(now) {
		previous = expiresAt.Sub(now)
	}
	if previous < minDuration {
		previous = minDuration
	}
	doubled := previous * 2
	if maxDuration > 0 && doubled > maxDuration {
		doubled = maxDuration
	}
	if doubled < minDuration {
		doubled = minDuration
	}
	return previous, doubled
}

func operatorAutoApprovalScopeAllows(scope string, req operatorAutoApprovalRequest) bool {
	switch session.NormalizeOperatorAutoApprovalScope(scope) {
	case session.OperatorAutoApprovalScopeAll:
		return true
	case session.OperatorAutoApprovalScopeWorkspace:
		return operatorAutoApprovalRequestClass(req) == "workspace"
	case session.OperatorAutoApprovalScopeDeploy:
		return operatorAutoApprovalRequestClass(req) == "deploy"
	default:
		return false
	}
}

func operatorAutoApprovalRequestClass(req operatorAutoApprovalRequest) string {
	switch req.WorkMode {
	case WorkModeDeploy, WorkModeCommit:
		return "deploy"
	case WorkModeReadOnly, WorkModeWorkspaceWrite:
		return "workspace"
	}
	lower := strings.ToLower(strings.Join([]string{req.Kind, req.Summary, req.Details}, " "))
	for _, marker := range []string{"deploy", "restart", "systemctl", "commit", "push", "release", "install-user-service", "reinstall"} {
		if strings.Contains(lower, marker) {
			return "deploy"
		}
	}
	for _, marker := range []string{"workspace", "read_only", "status_check", "inspect", "review", "patch", "edit", "test", "diff", "exec"} {
		if strings.Contains(lower, marker) {
			return "workspace"
		}
	}
	return "generic"
}

func (r *Runtime) renderOperatorAutoApprovalStatus(chatID int64, adminUserID int64, now time.Time) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.renderOperatorAutoApprovalStatusForScope(chatID, scopeKind, scopeID, adminUserID, now)
}

func (r *Runtime) renderOperatorAutoApprovalStatusForScope(chatID int64, scopeKind string, scopeID string, adminUserID int64, now time.Time) (string, error) {
	leases, err := r.store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, now)
	if err != nil {
		return "", err
	}
	for _, lease := range leases {
		if lease.AdminUserID == adminUserID && lease.ActiveAt(now) {
			blocked, err := r.operatorAutoApprovalBlockedReason(chatID, adminUserID, lease.Scope, now)
			if err != nil {
				return "", err
			}
			return renderOperatorAutoApprovalStatusActive(lease, now, blocked), nil
		}
	}
	latest, ok, err := r.store.LatestOperatorAutoApprovalLeaseForScope(chatID, adminUserID, scopeKind, scopeID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "Auto approvals are inactive for this chat.", nil
	}
	return renderOperatorAutoApprovalStatusInactive(latest, now), nil
}

func (r *Runtime) operatorAutoApprovalBlockedReason(chatID int64, adminUserID int64, approvalScope string, now time.Time) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.operatorAutoApprovalBlockedReasonForScope(chatID, scopeKind, scopeID, adminUserID, approvalScope, now)
}

func (r *Runtime) operatorAutoApprovalBlockedReasonForScope(chatID int64, scopeKind string, scopeID string, adminUserID int64, approvalScope string, now time.Time) (string, error) {
	gate, ok, err := r.operatorAutoModeGateForScope(chatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return "", err
	}
	if !ok {
		return "open an approval window before this grant can answer prompts", nil
	}
	if !operatorAutoModeScopeIntersects(gate.Scope, approvalScope) {
		return "current auto mode allows " + operatorAutoApprovalScopeLabel(gate.Scope), nil
	}
	return "", nil
}

type operatorAutoApprovalCommandSpec struct {
	Duration time.Duration
	Scope    string
	MaxUses  int
	Reason   string
}

func parseOperatorAutoApprovalCommand(raw string) (string, operatorAutoApprovalCommandSpec, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return "status", operatorAutoApprovalCommandSpec{}, nil
	}
	switch strings.ToLower(strings.TrimSpace(fields[0])) {
	case "status":
		return "status", operatorAutoApprovalCommandSpec{}, nil
	case "off", "disable", "revoke", "stop":
		return "off", operatorAutoApprovalCommandSpec{}, nil
	case "double", "2x", "2×", "extend":
		return "double", operatorAutoApprovalCommandSpec{}, nil
	}
	spec := operatorAutoApprovalCommandSpec{Scope: operatorAutoApprovalDefaultScope}
	durationSet := false
	reason := make([]string, 0)
	for _, field := range fields {
		token := strings.TrimSpace(field)
		lower := strings.ToLower(token)
		if token == "" {
			continue
		}
		if strings.HasPrefix(lower, "uses=") || strings.HasPrefix(lower, "max_uses=") || strings.HasPrefix(lower, "max=") {
			value := strings.TrimSpace(token[strings.IndexByte(token, '=')+1:])
			parsed, err := parsePositiveInt(value)
			if err != nil {
				return "", spec, fmt.Errorf("invalid auto-approval max uses %q", value)
			}
			spec.MaxUses = parsed
			continue
		}
		if isOperatorAutoApprovalScope(lower) {
			spec.Scope = session.NormalizeOperatorAutoApprovalScope(lower)
			continue
		}
		if !durationSet {
			if parsed, err := time.ParseDuration(lower); err == nil {
				spec.Duration = parsed
				durationSet = true
				continue
			}
		}
		reason = append(reason, token)
	}
	if !durationSet {
		return "", spec, fmt.Errorf("approval duration, scope, optional use budget, and optional reason are required")
	}
	if spec.Duration < operatorAutoApprovalMinDuration {
		return "", spec, fmt.Errorf("auto-approval duration must be at least %s", operatorAutoApprovalMinDuration)
	}
	if spec.Duration > operatorAutoApprovalMaxDuration {
		return "", spec, fmt.Errorf("auto-approval duration is capped at %s", operatorAutoApprovalMaxDuration)
	}
	spec.Reason = strings.TrimSpace(strings.Join(reason, " "))
	return "enable", spec, nil
}

func isOperatorAutoApprovalScope(scope string) bool {
	switch session.NormalizeOperatorAutoApprovalScope(scope) {
	case session.OperatorAutoApprovalScopeAll:
		return strings.TrimSpace(scope) == "" || strings.EqualFold(strings.TrimSpace(scope), "all")
	case session.OperatorAutoApprovalScopeWorkspace, session.OperatorAutoApprovalScopeDeploy:
		return true
	default:
		return false
	}
}

func parsePositiveInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty integer")
	}
	var out int
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid integer")
		}
		out = out*10 + int(r-'0')
	}
	if out <= 0 {
		return 0, fmt.Errorf("integer must be positive")
	}
	return out, nil
}

func newOperatorAutoApprovalLeaseID(chatID int64, adminUserID int64, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return fmt.Sprintf("auto-%d-%d-%d", adminUserID, chatID, now.UTC().UnixNano())
}

func operatorAutoApprovalPrimaryLease(leases []session.OperatorAutoApprovalLease, chatID int64, adminUserID int64) session.OperatorAutoApprovalLease {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return operatorAutoApprovalPrimaryLeaseForScope(leases, chatID, scopeKind, scopeID, adminUserID)
}

func operatorAutoApprovalPrimaryLeaseForScope(leases []session.OperatorAutoApprovalLease, chatID int64, scopeKind string, scopeID string, adminUserID int64) session.OperatorAutoApprovalLease {
	if len(leases) > 0 {
		return session.NormalizeOperatorAutoApprovalLease(leases[0])
	}
	return session.OperatorAutoApprovalLease{
		AdminUserID: adminUserID,
		ChatID:      chatID,
		ScopeKind:   strings.TrimSpace(scopeKind),
		ScopeID:     strings.TrimSpace(scopeID),
	}
}

func operatorAutoApprovalRevokedEventPayload(leases []session.OperatorAutoApprovalLease, now time.Time) map[string]any {
	ids := make([]string, 0, len(leases))
	activeCount := 0
	for _, lease := range leases {
		lease = session.NormalizeOperatorAutoApprovalLease(lease)
		if lease.ID != "" {
			ids = append(ids, lease.ID)
		}
		if lease.ActiveAt(now) {
			activeCount++
		}
	}
	payload := map[string]any{
		"revoked_count":        len(leases),
		"revoked_active_count": activeCount,
	}
	if len(ids) > 0 {
		payload["revoked_lease_ids"] = ids
	}
	return payload
}

func (r *Runtime) recordOperatorAutoApprovalEvent(chatID int64, eventType string, status string, lease session.OperatorAutoApprovalLease, extra map[string]any) {
	if r == nil || r.store == nil || chatID == 0 {
		return
	}
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	payload := map[string]any{
		"lease_id":          strings.TrimSpace(lease.ID),
		"admin_user_id":     lease.AdminUserID,
		"scope":             strings.TrimSpace(lease.Scope),
		"target_scope_kind": strings.TrimSpace(lease.ScopeKind),
		"target_scope_id":   strings.TrimSpace(lease.ScopeID),
		"reason":            strings.TrimSpace(lease.Reason),
		"max_uses":          lease.MaxUses,
		"used_count":        lease.UsedCount,
	}
	if !lease.ExpiresAt.IsZero() {
		payload["expires_at"] = lease.ExpiresAt.UTC().Format(time.RFC3339)
	}
	for key, value := range extra {
		if strings.TrimSpace(key) != "" {
			payload[key] = value
		}
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	r.recordExecutionEvent(key, eventType, "auto_approval", status, payload, time.Now().UTC())
}
