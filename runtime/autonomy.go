//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const autonomyAuthorityBehavior = "existing proposal and approval flows"

func (r *Runtime) AutonomyStatusSnapshot() core.AutonomyStatusSnapshot {
	return r.autonomyStatusSnapshot(0, 0, time.Now().UTC())
}

func (r *Runtime) ChatAutonomyStatusSnapshot(chatID int64, adminUserID int64) (core.AutonomyStatusSnapshot, error) {
	return r.autonomyStatusSnapshot(chatID, adminUserID, time.Now().UTC()), nil
}

func (r *Runtime) ConfigureAutonomy(ctx context.Context, chatID int64, adminUserID int64, args string) (string, error) {
	if r == nil || r.store == nil {
		return "Autonomy controls are unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Autonomy controls are admin only.", nil
	}
	action, spec, err := parseOperatorAutonomyCommand(args)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	switch action {
	case "status":
		snapshot, err := r.ChatAutonomyStatusSnapshot(chatID, adminUserID)
		if err != nil {
			return "", err
		}
		return renderAutonomyCommandStatus(snapshot), nil
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
		return renderOperatorAutonomyRevoked(revoked, now), nil
	case "leased":
		if err := r.validateAutonomyLiveOverride(spec.Mode, spec.AutoApproval.Duration); err != nil {
			return "", err
		}
		reason := strings.TrimSpace(spec.AutoApproval.Reason)
		if reason == "" {
			reason = "autonomy leased override"
		}
		lease := session.OperatorAutoApprovalLease{
			ID:          newOperatorAutoApprovalLeaseID(chatID, adminUserID, now),
			AdminUserID: adminUserID,
			ChatID:      chatID,
			Scope:       spec.AutoApproval.Scope,
			Reason:      reason,
			MaxUses:     spec.AutoApproval.MaxUses,
			CreatedAt:   now,
			ExpiresAt:   now.Add(spec.AutoApproval.Duration),
			UpdatedAt:   now,
		}
		if _, err := r.store.RevokeOperatorAutoApprovalLeases(chatID, adminUserID, now); err != nil {
			return "", err
		}
		created, err := r.store.CreateOperatorAutoApprovalLease(lease)
		if err != nil {
			return "", err
		}
		r.recordOperatorAutoApprovalEvent(chatID, core.ExecutionEventAutoApprovalGranted, "active", created, map[string]any{
			"autonomy_mode": spec.Mode,
		})
		return renderOperatorAutonomyEnabled(created, now), nil
	case "mission":
		if err := r.validateAutonomyLiveOverride(spec.Mode, 0); err != nil {
			return "", err
		}
		return "", fmt.Errorf("mission autonomy is not active in this build; use leased mode for bounded approval cycling")
	default:
		return "", fmt.Errorf("unknown autonomy action %q", action)
	}
}

func (r *Runtime) autonomyStatusSnapshot(chatID int64, adminUserID int64, now time.Time) core.AutonomyStatusSnapshot {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	policy := config.EffectiveAutonomyPolicy(nil)
	source := "default"
	if r != nil && r.cfg != nil {
		policy = config.EffectiveAutonomyPolicy(r.cfg)
		source = "config"
	}
	snapshot := core.AutonomyStatusSnapshot{
		GeneratedAt:         time.Now().UTC(),
		DefaultMode:         policy.DefaultMode,
		Ceiling:             policy.Ceiling,
		AllowLiveOverrides:  policy.AllowLiveOverrides,
		MaxOverrideDuration: policy.MaxOverrideDuration,
		Source:              source,
		AuthorityBehavior:   autonomyAuthorityBehavior,
	}
	if chatID == 0 || r == nil || r.store == nil {
		return snapshot
	}
	lease, ok, err := r.activeAutonomyOverrideLease(chatID, adminUserID, now)
	if err != nil || !ok {
		return snapshot
	}
	snapshot.ActiveOverrideMode = "leased"
	snapshot.ActiveOverrideActor = strconv.FormatInt(lease.AdminUserID, 10)
	snapshot.ActiveOverrideScope = strings.TrimSpace(lease.Scope)
	snapshot.ActiveOverrideUsed = lease.UsedCount
	snapshot.ActiveOverrideMax = lease.MaxUses
	snapshot.ActiveOverrideExpiry = lease.ExpiresAt
	snapshot.AuthorityBehavior = "eligible approval prompts may use the active leased override"
	return snapshot
}

func (r *Runtime) activeAutonomyOverrideLease(chatID int64, adminUserID int64, now time.Time) (session.OperatorAutoApprovalLease, bool, error) {
	if r == nil || r.store == nil || chatID == 0 {
		return session.OperatorAutoApprovalLease{}, false, nil
	}
	if err := r.validateAutonomyLiveOverride("leased", 0); err != nil {
		return session.OperatorAutoApprovalLease{}, false, nil
	}
	leases, err := r.store.ActiveOperatorAutoApprovalLeases(chatID, now)
	if err != nil {
		return session.OperatorAutoApprovalLease{}, false, err
	}
	var selected session.OperatorAutoApprovalLease
	for _, lease := range leases {
		lease = session.NormalizeOperatorAutoApprovalLease(lease)
		if !lease.ActiveAt(now) {
			continue
		}
		if adminUserID > 0 && lease.AdminUserID != adminUserID {
			continue
		}
		if selected.ID == "" || lease.ExpiresAt.After(selected.ExpiresAt) {
			selected = lease
		}
	}
	if selected.ID == "" && adminUserID > 0 {
		for _, lease := range leases {
			lease = session.NormalizeOperatorAutoApprovalLease(lease)
			if !lease.ActiveAt(now) {
				continue
			}
			if selected.ID == "" || lease.ExpiresAt.After(selected.ExpiresAt) {
				selected = lease
			}
		}
	}
	if selected.ID == "" {
		return session.OperatorAutoApprovalLease{}, false, nil
	}
	return selected, true, nil
}

func (r *Runtime) validateAutonomyLiveOverride(mode string, duration time.Duration) error {
	policy := config.EffectiveAutonomyPolicy(nil)
	if r != nil && r.cfg != nil {
		policy = config.EffectiveAutonomyPolicy(r.cfg)
	}
	if !policy.AllowLiveOverrides {
		return fmt.Errorf("autonomy live overrides are disabled by config")
	}
	ceilingRank, ok := config.AutonomyModeRank(policy.Ceiling)
	if !ok {
		return fmt.Errorf("autonomy ceiling is invalid")
	}
	modeRank, ok := config.AutonomyModeRank(mode)
	if !ok {
		return fmt.Errorf("autonomy mode must be one of off|review_only|ask_first|leased|mission")
	}
	if modeRank > ceilingRank {
		return fmt.Errorf("autonomy mode %s exceeds configured ceiling %s", config.NormalizeAutonomyMode(mode), policy.Ceiling)
	}
	if duration > 0 && duration > policy.MaxOverrideDuration {
		return fmt.Errorf("autonomy live override duration is capped at %s", policy.MaxOverrideDuration)
	}
	return nil
}

type operatorAutonomyCommandSpec struct {
	Mode         string
	AutoApproval operatorAutoApprovalCommandSpec
}

func parseOperatorAutonomyCommand(raw string) (string, operatorAutonomyCommandSpec, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return "status", operatorAutonomyCommandSpec{}, nil
	}
	first := strings.ToLower(strings.TrimSpace(fields[0]))
	switch first {
	case "status":
		return "status", operatorAutonomyCommandSpec{}, nil
	case "off", "disable", "revoke", "stop", "review", "review-only", "review_only", "ask", "ask-first", "ask_first":
		return "off", operatorAutonomyCommandSpec{Mode: config.NormalizeAutonomyMode(first)}, nil
	case "lease", "leased":
		action, spec, err := parseOperatorAutoApprovalCommand(strings.Join(fields[1:], " "))
		if err != nil {
			return "", operatorAutonomyCommandSpec{}, err
		}
		if action != "enable" {
			return "", operatorAutonomyCommandSpec{}, fmt.Errorf("usage: /autonomy leased <duration> [all|workspace|deploy] [uses=N] [reason]")
		}
		return "leased", operatorAutonomyCommandSpec{Mode: "leased", AutoApproval: spec}, nil
	case "mission":
		return "mission", operatorAutonomyCommandSpec{Mode: "mission"}, nil
	default:
		return "", operatorAutonomyCommandSpec{}, fmt.Errorf("usage: /autonomy [status|off|leased <duration> [all|workspace|deploy] [uses=N] [reason]]")
	}
}

func renderAutonomyCommandStatus(snapshot core.AutonomyStatusSnapshot) string {
	if strings.TrimSpace(snapshot.ActiveOverrideMode) == "" {
		return "Autonomy live override is inactive. Default: " + autonomyModeRuntimeLabel(snapshot.DefaultMode) + ". Ceiling: " + autonomyModeRuntimeLabel(snapshot.Ceiling) + "."
	}
	lines := []string{
		"Autonomy live override is active.",
		"Mode: " + autonomyModeRuntimeLabel(snapshot.ActiveOverrideMode) + ".",
	}
	if scope := strings.TrimSpace(snapshot.ActiveOverrideScope); scope != "" {
		lines = append(lines, "Scope: "+operatorAutoApprovalScopeLabel(scope)+".")
	}
	if !snapshot.ActiveOverrideExpiry.IsZero() {
		lines = append(lines, "Expires: "+snapshot.ActiveOverrideExpiry.UTC().Format(time.RFC3339)+".")
	}
	if snapshot.ActiveOverrideMax > 0 {
		lines = append(lines, fmt.Sprintf("Used: %d/%d.", snapshot.ActiveOverrideUsed, snapshot.ActiveOverrideMax))
	} else {
		lines = append(lines, fmt.Sprintf("Used: %d.", snapshot.ActiveOverrideUsed))
	}
	return strings.Join(lines, "\n")
}

func renderOperatorAutonomyEnabled(lease session.OperatorAutoApprovalLease, now time.Time) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	parts := []string{
		"Autonomy override enabled for this chat.",
		"Mode: Leased.",
		"Scope: " + operatorAutoApprovalScopeLabel(lease.Scope) + ".",
		"Expires: " + lease.ExpiresAt.UTC().Format(time.RFC3339) + " (" + roundDuration(lease.ExpiresAt.Sub(now)) + ").",
	}
	if lease.MaxUses > 0 {
		parts = append(parts, fmt.Sprintf("Use budget: %d approval(s).", lease.MaxUses))
	}
	parts = append(parts, "Use /autonomy off to revoke it.")
	return strings.Join(parts, "\n")
}

func renderOperatorAutonomyRevoked(leases []session.OperatorAutoApprovalLease, now time.Time) string {
	if len(leases) == 0 {
		return "Autonomy live override is already off for this chat."
	}
	active := operatorAutoApprovalActiveLeases(leases, now)
	if len(active) > 0 {
		return "Autonomy live override is off for this chat. Cleared: " + operatorAutoApprovalGrantSummary(active) + "."
	}
	return "Autonomy live override is off for this chat. Cleared the old " + operatorAutoApprovalGrantNoun(leases) + operatorAutoApprovalClearedOldGrantDetail(leases) + "."
}

func autonomyModeRuntimeLabel(mode string) string {
	switch config.NormalizeAutonomyMode(mode) {
	case "off":
		return "Off"
	case "review_only":
		return "Review only"
	case "ask_first":
		return "Ask first"
	case "leased":
		return "Leased"
	case "mission":
		return "Mission"
	default:
		if strings.TrimSpace(mode) == "" {
			return "Ask first"
		}
		return strings.TrimSpace(mode)
	}
}
