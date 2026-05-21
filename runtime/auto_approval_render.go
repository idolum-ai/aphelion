//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

func renderOperatorAutoApprovalRevoked(leases []session.OperatorAutoApprovalLease, now time.Time) string {
	state := "off"
	next := "Approve one request and use the inline approval-window controls to open a bounded grant."
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

func renderOperatorAutoApprovalEnabled(lease session.OperatorAutoApprovalLease, now time.Time, blockedReason string) string {
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
	why := "Eligible approval prompts in this chat may be answered automatically until the grant expires or is spent."
	if blockedReason = strings.TrimSpace(blockedReason); blockedReason != "" {
		details = append(details, "Mode: blocked - "+blockedReason+".")
		why = "This grant is recorded, but it will not be spent until auto mode allows matching prompts."
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Auto approvals",
		State:   "enabled",
		Why:     why,
		Next:    "Use approval-window controls to revoke it.",
		Details: details,
	})
}

func renderOperatorAutoApprovalDoubled(lease session.OperatorAutoApprovalLease, now time.Time, blockedReason string, previousDuration time.Duration, doubledDuration time.Duration) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	details := []string{
		"Scope: " + operatorAutoApprovalScopeLabel(lease.Scope) + ".",
		"Doubled: " + roundDuration(previousDuration) + " -> " + roundDuration(doubledDuration) + ".",
		"Expires: " + lease.ExpiresAt.UTC().Format(time.RFC3339) + " (" + roundDuration(lease.ExpiresAt.Sub(now)) + ").",
	}
	if lease.MaxUses > 0 {
		details = append(details, fmt.Sprintf("Use budget remaining: %d approval(s).", lease.MaxUses))
	}
	if reason := strings.TrimSpace(lease.Reason); reason != "" {
		details = append(details, "Reason: "+reason)
	}
	why := "Expanded the current auto approval grant by doubling its full time window."
	if blockedReason = strings.TrimSpace(blockedReason); blockedReason != "" {
		details = append(details, "Mode: blocked - "+blockedReason+".")
		why = "Expanded the grant, but it will not be spent until auto mode allows matching prompts."
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Auto approvals",
		State:   "enabled",
		Why:     why,
		Next:    "Use approval-window controls to revoke it, or press Double time again to extend within the cap.",
		Details: details,
	})
}

func renderOperatorAutoApprovalStatusActive(lease session.OperatorAutoApprovalLease, now time.Time, blockedReason string) string {
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
	why := "Eligible approval prompts in this chat can use this bounded grant."
	if blockedReason = strings.TrimSpace(blockedReason); blockedReason != "" {
		details = append(details, "Mode: blocked - "+blockedReason+".")
		why = "This grant is active, but it will not be spent until auto mode allows matching prompts."
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Auto approvals",
		State:   "active",
		Why:     why,
		Next:    "Use approval-window controls to revoke it.",
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
		Next:  "Approve one request and use the inline approval-window controls to open a bounded grant.",
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
