//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

const (
	operatorAutoApprovalDefaultScope = session.OperatorAutoApprovalScopeAll
	operatorAutoApprovalMinDuration  = time.Minute
	operatorAutoApprovalMaxDuration  = 24 * time.Hour
)

type operatorAutoApprovalRequest struct {
	ChatID     int64
	Kind       string
	Choice     string
	DecisionID string
	ProposalID string
	Summary    string
	Details    string
	WorkMode   WorkMode
}

func (r *Runtime) ConfigureAutoApproval(ctx context.Context, chatID int64, adminUserID int64, args string) (string, error) {
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
	switch action {
	case "status":
		return r.renderOperatorAutoApprovalStatus(chatID, adminUserID, now)
	case "off":
		revoked, err := r.store.RevokeOperatorAutoApprovalLeases(chatID, adminUserID, now)
		if err != nil {
			return "", err
		}
		r.recordOperatorAutoApprovalEvent(
			chatID,
			core.ExecutionEventAutoApprovalRevoked,
			"revoked",
			operatorAutoApprovalPrimaryLease(revoked, chatID, adminUserID),
			operatorAutoApprovalRevokedEventPayload(revoked, now),
		)
		return renderOperatorAutoApprovalRevoked(revoked, now), nil
	case "enable":
		if err := r.validateAutonomyLiveOverride("leased", spec.Duration); err != nil {
			return "", err
		}
		lease := session.OperatorAutoApprovalLease{
			ID:          newOperatorAutoApprovalLeaseID(chatID, adminUserID, now),
			AdminUserID: adminUserID,
			ChatID:      chatID,
			Scope:       spec.Scope,
			Reason:      spec.Reason,
			MaxUses:     spec.MaxUses,
			CreatedAt:   now,
			ExpiresAt:   now.Add(spec.Duration),
			UpdatedAt:   now,
		}
		if _, err := r.store.RevokeOperatorAutoApprovalLeases(chatID, adminUserID, now); err != nil {
			return "", err
		}
		created, err := r.store.CreateOperatorAutoApprovalLease(lease)
		if err != nil {
			return "", err
		}
		r.recordOperatorAutoApprovalEvent(chatID, core.ExecutionEventAutoApprovalGranted, "active", created, nil)
		return renderOperatorAutoApprovalEnabled(created, now), nil
	default:
		return "", fmt.Errorf("unknown auto-approval action %q", action)
	}
}

func (r *Runtime) AutoApprovalStatus(ctx context.Context, chatID int64, adminUserID int64) (string, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return "Auto approvals are unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Auto approvals are admin only.", nil
	}
	return r.renderOperatorAutoApprovalStatus(chatID, adminUserID, time.Now().UTC())
}

func (r *Runtime) AutoResolveDecision(ctx context.Context, pending decision.PendingDecision) (decision.AutoResolution, error) {
	choice := autoApprovalChoiceForDecision(pending)
	if choice == "" {
		return decision.AutoResolution{}, nil
	}
	lease, ok, err := r.consumeOperatorAutoApproval(ctx, operatorAutoApprovalRequest{
		ChatID:     pending.ChatID,
		Kind:       "decision:" + strings.TrimSpace(string(pending.Kind)),
		Choice:     choice,
		DecisionID: strings.TrimSpace(pending.ID),
		Summary:    strings.TrimSpace(pending.Prompt),
		Details:    strings.TrimSpace(pending.Details),
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
	lease, ok, err := r.consumeOperatorAutoApproval(ctx, operatorAutoApprovalRequest{
		ChatID:     key.ChatID,
		Kind:       "continuation:" + strings.TrimSpace(source),
		Choice:     "approve",
		DecisionID: strings.TrimSpace(state.DecisionID),
		ProposalID: strings.TrimSpace(state.ActionProposal.ID),
		Summary:    firstNonEmptyContinuation(state.StageSummary, state.ActionProposal.Summary),
		Details:    firstNonEmptyContinuation(state.ActionProposal.BoundedEffect, state.GovernorIntent.Constraints),
		WorkMode:   continuationWorkMode(state),
	})
	if err != nil || !ok {
		return false, err
	}
	approved, err := r.ApproveContinuation(key.ChatID, lease.AdminUserID)
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
	if err := r.TriggerContinuation(ctx, key.ChatID); err != nil {
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
	if err := r.validateAutonomyLiveOverride("leased", 0); err != nil {
		return session.OperatorAutoApprovalLease{}, false, nil
	}
	now := time.Now().UTC()
	leases, err := r.store.ActiveOperatorAutoApprovalLeases(req.ChatID, now)
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
			"request_kind": strings.TrimSpace(req.Kind),
			"choice":       strings.TrimSpace(req.Choice),
			"decision_id":  strings.TrimSpace(req.DecisionID),
			"proposal_id":  strings.TrimSpace(req.ProposalID),
			"summary":      truncatePreview(strings.TrimSpace(req.Summary), 220),
			"details":      truncatePreview(strings.TrimSpace(req.Details), 220),
			"work_mode":    strings.TrimSpace(string(req.WorkMode)),
		})
		_ = ctx
		return used, true, nil
	}
	return session.OperatorAutoApprovalLease{}, false, nil
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
	leases, err := r.store.ActiveOperatorAutoApprovalLeases(chatID, now)
	if err != nil {
		return "", err
	}
	for _, lease := range leases {
		if lease.AdminUserID == adminUserID && lease.ActiveAt(now) {
			return renderOperatorAutoApprovalStatusActive(lease, now), nil
		}
	}
	latest, ok, err := r.store.LatestOperatorAutoApprovalLease(chatID, adminUserID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "Auto approvals are inactive for this chat.", nil
	}
	return renderOperatorAutoApprovalStatusInactive(latest, now), nil
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
		return "", spec, fmt.Errorf("usage: /auto approvals <duration> [all|workspace|deploy] [uses=N] [reason]")
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

func renderOperatorAutoApprovalRevoked(leases []session.OperatorAutoApprovalLease, now time.Time) string {
	state := "off"
	next := "Use /auto approvals <duration> <scope> to create a new bounded grant."
	if len(leases) == 0 {
		return renderRuntimeCompactPanel(face.OperatorPanel{
			Title: "Auto approvals",
			State: state,
			Why:   "No active approval prompts will be answered automatically.",
			Next:  next,
			Details: []string{
				"Already off for this chat.",
			},
		})
	}
	active := operatorAutoApprovalActiveLeases(leases, now)
	detail := ""
	if len(active) > 0 {
		detail = "Cleared active grant: " + operatorAutoApprovalGrantSummary(active) + "."
	} else {
		latest := session.NormalizeOperatorAutoApprovalLease(leases[0])
		switch {
		case !latest.ExpiresAt.IsZero() && !latest.ExpiresAt.After(now.UTC()):
			detail = "Cleared old expired " + operatorAutoApprovalGrantNoun(leases) + operatorAutoApprovalClearedOldGrantDetail(leases) + "."
		case latest.MaxUses > 0 && latest.UsedCount >= latest.MaxUses:
			detail = "Cleared old spent " + operatorAutoApprovalGrantNoun(leases) + operatorAutoApprovalClearedOldGrantDetail(leases) + "."
		default:
			detail = "Cleared old " + operatorAutoApprovalGrantNoun(leases) + operatorAutoApprovalClearedOldGrantDetail(leases) + "."
		}
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title: "Auto approvals",
		State: state,
		Why:   "No active approval prompts will be answered automatically.",
		Next:  next,
		Details: []string{
			detail,
		},
		Evidence: []string{
			fmt.Sprintf("Revoked records: %d", len(leases)),
		},
	})
}

func operatorAutoApprovalActiveLeases(leases []session.OperatorAutoApprovalLease, now time.Time) []session.OperatorAutoApprovalLease {
	out := make([]session.OperatorAutoApprovalLease, 0, len(leases))
	for _, lease := range leases {
		lease = session.NormalizeOperatorAutoApprovalLease(lease)
		if lease.ActiveAt(now) {
			out = append(out, lease)
		}
	}
	return out
}

func operatorAutoApprovalClearedOldGrantDetail(leases []session.OperatorAutoApprovalLease) string {
	if len(leases) != 1 {
		return ""
	}
	return ": " + operatorAutoApprovalGrantSummary(leases)
}

func operatorAutoApprovalGrantSummary(leases []session.OperatorAutoApprovalLease) string {
	if len(leases) == 0 {
		return "0 grants"
	}
	if len(leases) > 1 {
		return fmt.Sprintf("%d grants", len(leases))
	}
	lease := session.NormalizeOperatorAutoApprovalLease(leases[0])
	used := fmt.Sprintf("used %d %s", lease.UsedCount, pluralWord(lease.UsedCount, "time", "times"))
	if lease.MaxUses > 0 {
		used = fmt.Sprintf("used %d/%d", lease.UsedCount, lease.MaxUses)
	}
	return operatorAutoApprovalScopeLabel(lease.Scope) + ", " + used
}

func operatorAutoApprovalScopeLabel(scope string) string {
	switch session.NormalizeOperatorAutoApprovalScope(scope) {
	case session.OperatorAutoApprovalScopeWorkspace:
		return "workspace prompts"
	case session.OperatorAutoApprovalScopeDeploy:
		return "deploy/restart prompts"
	default:
		return "all prompts"
	}
}

func operatorAutoApprovalGrantNoun(leases []session.OperatorAutoApprovalLease) string {
	if len(leases) == 1 {
		return "grant"
	}
	return "grants"
}

func pluralWord(count int, singular string, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func operatorAutoApprovalPrimaryLease(leases []session.OperatorAutoApprovalLease, chatID int64, adminUserID int64) session.OperatorAutoApprovalLease {
	if len(leases) > 0 {
		return session.NormalizeOperatorAutoApprovalLease(leases[0])
	}
	return session.OperatorAutoApprovalLease{
		AdminUserID: adminUserID,
		ChatID:      chatID,
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

func renderOperatorAutoApprovalEnabled(lease session.OperatorAutoApprovalLease, now time.Time) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	details := []string{
		"Scope: " + operatorAutoApprovalScopeLabel(lease.Scope) + ".",
		"Expires: " + lease.ExpiresAt.UTC().Format(time.RFC3339) + " (" + roundDuration(lease.ExpiresAt.Sub(now)) + ").",
	}
	if lease.MaxUses > 0 {
		details = append(details, fmt.Sprintf("Use budget: %d approval(s).", lease.MaxUses))
	}
	if reason := strings.TrimSpace(lease.Reason); reason != "" {
		details = append(details, "Reason: "+reason)
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Auto approvals",
		State:   "enabled",
		Why:     "Eligible approval prompts in this chat may be answered automatically until the grant expires or is spent.",
		Next:    "Use /auto approvals off to revoke it.",
		Details: details,
	})
}

func renderOperatorAutoApprovalStatusActive(lease session.OperatorAutoApprovalLease, now time.Time) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	details := []string{
		"Scope: " + operatorAutoApprovalScopeLabel(lease.Scope) + ".",
		"Expires: " + lease.ExpiresAt.UTC().Format(time.RFC3339) + " (" + roundDuration(lease.ExpiresAt.Sub(now)) + ").",
		fmt.Sprintf("Used: %d", lease.UsedCount),
	}
	if lease.MaxUses > 0 {
		details[len(details)-1] = fmt.Sprintf("Used: %d/%d", lease.UsedCount, lease.MaxUses)
	}
	if lease.Reason != "" {
		details = append(details, "Reason: "+lease.Reason)
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Auto approvals",
		State:   "active",
		Why:     "Eligible approval prompts in this chat can use this bounded grant.",
		Next:    "Use /auto approvals off to revoke it.",
		Details: details,
	})
}

func renderOperatorAutoApprovalStatusInactive(lease session.OperatorAutoApprovalLease, now time.Time) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	reason := "expired"
	if !lease.RevokedAt.IsZero() {
		reason = "revoked"
	} else if lease.MaxUses > 0 && lease.UsedCount >= lease.MaxUses {
		reason = "use budget exhausted"
	} else if lease.ExpiresAt.After(now) {
		reason = "inactive"
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title: "Auto approvals",
		State: "inactive",
		Why:   "No current approval prompt will use this old grant.",
		Next:  "Use /auto approvals <duration> <scope> to create a new bounded grant.",
		Details: []string{
			"Last grant: " + reason + ".",
		},
	})
}

func roundDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= time.Hour {
		return d.Round(time.Minute).String()
	}
	return d.Round(time.Second).String()
}

func (r *Runtime) recordOperatorAutoApprovalEvent(chatID int64, eventType string, status string, lease session.OperatorAutoApprovalLease, extra map[string]any) {
	if r == nil || r.store == nil || chatID == 0 {
		return
	}
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	payload := map[string]any{
		"lease_id":      strings.TrimSpace(lease.ID),
		"admin_user_id": lease.AdminUserID,
		"scope":         strings.TrimSpace(lease.Scope),
		"reason":        strings.TrimSpace(lease.Reason),
		"max_uses":      lease.MaxUses,
		"used_count":    lease.UsedCount,
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
