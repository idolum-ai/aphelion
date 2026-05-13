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
	"github.com/idolum-ai/aphelion/face"
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
		return "Auto policy is unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Auto policy is admin only.", nil
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
			return "", operatorAutonomyCommandSpec{}, fmt.Errorf("usage: /auto policy leased <duration> [all|workspace|deploy] [uses=N] [reason]")
		}
		return "leased", operatorAutonomyCommandSpec{Mode: "leased", AutoApproval: spec}, nil
	case "mission":
		return "mission", operatorAutonomyCommandSpec{Mode: "mission"}, nil
	default:
		return "", operatorAutonomyCommandSpec{}, fmt.Errorf("usage: /auto policy [status|off|leased <duration> [all|workspace|deploy] [uses=N] [reason]]")
	}
}

func renderAutonomyCommandStatus(snapshot core.AutonomyStatusSnapshot) string {
	if strings.TrimSpace(snapshot.ActiveOverrideMode) == "" {
		return renderRuntimeCompactPanel(face.OperatorPanel{
			Title: "Auto policy",
			State: "no live override",
			Why:   "The configured default and ceiling control whether approval cycling can be leased.",
			Next:  "Use /auto policy leased <duration> <scope> to create a bounded override if config allows it.",
			Details: []string{
				"Default: " + autonomyModeRuntimeLabel(snapshot.DefaultMode) + ".",
				"Ceiling: " + autonomyModeRuntimeLabel(snapshot.Ceiling) + ".",
			},
		})
	}
	details := []string{
		"Mode: " + autonomyModeRuntimeLabel(snapshot.ActiveOverrideMode) + ".",
	}
	if scope := strings.TrimSpace(snapshot.ActiveOverrideScope); scope != "" {
		details = append(details, "Scope: "+operatorAutoApprovalScopeLabel(scope)+".")
	}
	if !snapshot.ActiveOverrideExpiry.IsZero() {
		details = append(details, "Expires: "+snapshot.ActiveOverrideExpiry.UTC().Format(time.RFC3339)+".")
	}
	if snapshot.ActiveOverrideMax > 0 {
		details = append(details, fmt.Sprintf("Used: %d/%d.", snapshot.ActiveOverrideUsed, snapshot.ActiveOverrideMax))
	} else {
		details = append(details, fmt.Sprintf("Used: %d.", snapshot.ActiveOverrideUsed))
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Auto policy override",
		State:   "live override active",
		Why:     "Eligible approval prompts may use the active leased override.",
		Next:    "Use /auto policy off to revoke it.",
		Details: details,
	})
}

func renderOperatorAutonomyEnabled(lease session.OperatorAutoApprovalLease, now time.Time) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	details := []string{
		"Mode: Leased.",
		"Scope: " + operatorAutoApprovalScopeLabel(lease.Scope) + ".",
		"Expires: " + lease.ExpiresAt.UTC().Format(time.RFC3339) + " (" + roundDuration(lease.ExpiresAt.Sub(now)) + ").",
	}
	if lease.MaxUses > 0 {
		details = append(details, fmt.Sprintf("Use budget: %d approval(s).", lease.MaxUses))
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Auto policy override",
		State:   "leased override enabled",
		Why:     "Eligible approval prompts may use this bounded override until it expires or is spent.",
		Next:    "Use /auto policy off to revoke it.",
		Details: details,
	})
}

func renderOperatorAutonomyRevoked(leases []session.OperatorAutoApprovalLease, now time.Time) string {
	if len(leases) == 0 {
		return renderRuntimeCompactPanel(face.OperatorPanel{
			Title: "Auto policy",
			State: "off",
			Why:   "No live autonomy override is active for this chat.",
			Next:  "Use /auto policy leased <duration> <scope> if a bounded override is needed.",
			Details: []string{
				"Already off for this chat.",
			},
		})
	}
	active := operatorAutoApprovalActiveLeases(leases, now)
	if len(active) > 0 {
		return renderRuntimeCompactPanel(face.OperatorPanel{
			Title: "Auto policy",
			State: "off",
			Why:   "No live autonomy override is active for this chat.",
			Next:  "Use /auto policy leased <duration> <scope> if a bounded override is needed.",
			Details: []string{
				"Cleared active override: " + operatorAutoApprovalGrantSummary(active) + ".",
			},
			Evidence: []string{fmt.Sprintf("Revoked records: %d", len(leases))},
		})
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title: "Auto policy",
		State: "off",
		Why:   "No live autonomy override is active for this chat.",
		Next:  "Use /auto policy leased <duration> <scope> if a bounded override is needed.",
		Details: []string{
			"Cleared old " + operatorAutoApprovalGrantNoun(leases) + operatorAutoApprovalClearedOldGrantDetail(leases) + ".",
		},
		Evidence: []string{fmt.Sprintf("Revoked records: %d", len(leases))},
	})
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
