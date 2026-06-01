//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) renderOperatorAutoApprovalRevoked(leases []session.OperatorAutoApprovalLease, now time.Time) string {
	if len(leases) == 0 {
		return "Auto-approval is already off for this chat. Approve one request and open an approval window to use it again."
	}
	active := operatorAutoApprovalActiveLeases(leases, now)
	if len(active) > 0 {
		return "Auto-approval is off; cleared " + operatorAutoApprovalWindowSummary(active) + ". New prompts need manual approval again."
	}
	latest := session.NormalizeOperatorAutoApprovalLease(leases[0])
	reason := "old"
	switch {
	case !latest.ExpiresAt.IsZero() && !latest.ExpiresAt.After(now.UTC()):
		reason = "old expired"
	case latest.MaxUses > 0 && latest.UsedCount >= latest.MaxUses:
		reason = "old spent"
	}
	return "Auto-approval is off; cleared " + reason + " " + operatorAutoApprovalWindowNoun(leases) + operatorAutoApprovalClearedOldWindowDetail(leases) + ". New prompts need manual approval again."
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

func operatorAutoApprovalClearedOldWindowDetail(leases []session.OperatorAutoApprovalLease) string {
	if len(leases) != 1 {
		return ""
	}
	return ": " + operatorAutoApprovalWindowSummary(leases)
}

func operatorAutoApprovalWindowSummary(leases []session.OperatorAutoApprovalLease) string {
	if len(leases) == 0 {
		return "0 approval windows"
	}
	if len(leases) > 1 {
		return fmt.Sprintf("%d approval windows", len(leases))
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

func operatorAutoApprovalWindowNoun(leases []session.OperatorAutoApprovalLease) string {
	if len(leases) == 1 {
		return "approval window"
	}
	return "approval windows"
}

func pluralWord(count int, singular string, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func (r *Runtime) renderOperatorAutoApprovalEnabled(lease session.OperatorAutoApprovalLease, now time.Time, blockedReason string) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	return renderOperatorAutoApprovalActiveLine("active", lease, now, r.approvalWindowDisplayLocation(), blockedReason, "")
}

func (r *Runtime) renderOperatorAutoApprovalDoubled(lease session.OperatorAutoApprovalLease, now time.Time, blockedReason string, previousDuration time.Duration, doubledDuration time.Duration) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	detail := "extended from " + roundDuration(previousDuration) + " to " + roundDuration(doubledDuration)
	return renderOperatorAutoApprovalActiveLine("extended", lease, now, r.approvalWindowDisplayLocation(), blockedReason, detail)
}

func (r *Runtime) renderOperatorAutoApprovalStatusActive(lease session.OperatorAutoApprovalLease, now time.Time, blockedReason string) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	return renderOperatorAutoApprovalActiveLine("active", lease, now, r.approvalWindowDisplayLocation(), blockedReason, "")
}

func renderOperatorAutoApprovalActiveLine(state string, lease session.OperatorAutoApprovalLease, now time.Time, loc *time.Location, blockedReason string, detail string) string {
	scope := strings.TrimSuffix(operatorAutoApprovalScopeLabel(lease.Scope), ".")
	if scope == "" {
		scope = "matching prompts"
	}
	expires := formatApprovalWindowExpiry(lease.ExpiresAt, now, loc)
	used := fmt.Sprintf("used %d times", lease.UsedCount)
	if lease.MaxUses > 0 {
		used = fmt.Sprintf("used %d/%d approvals", lease.UsedCount, lease.MaxUses)
	}
	prefix := "Auto-approval is active"
	if state == "extended" {
		prefix = "Auto-approval was extended"
	}
	if br := strings.TrimSpace(blockedReason); br != "" {
		return "Auto-approval exists for " + scope + " until " + expires + ", but it is blocked: " + br + "."
	}
	if d := strings.TrimSpace(detail); d != "" {
		return prefix + " for " + scope + " until " + expires + " (" + d + "); " + used + "."
	}
	return prefix + " for " + scope + " until " + expires + "; " + used + "."
}

func (r *Runtime) renderOperatorAutoApprovalStatusInactive(lease session.OperatorAutoApprovalLease, now time.Time) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	reason := "inactive"
	switch {
	case !lease.RevokedAt.IsZero():
		reason = "revoked"
	case lease.MaxUses > 0 && lease.UsedCount >= lease.MaxUses:
		reason = fmt.Sprintf("spent: used %d/%d approvals", lease.UsedCount, lease.MaxUses)
	case !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now):
		reason = "expired"
	}
	return "Auto-approval is off; last approval window was " + reason + ". Approve one request and open an approval window to use it again."
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
