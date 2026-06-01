//go:build linux

package runtime

import (
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"time"
)

func (r *Runtime) StatusDiagnostics(chatID int64) ([]string, error) {
	if r == nil || r.store == nil || chatID == 0 {
		return nil, nil
	}

	chatSnapshot, err := r.ChatStatusSnapshot(chatID, core.RouterStatusSnapshot{})
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, 8)
	if latest := chatSnapshot.LatestTurnRun; latest != nil {
		lines = append(lines, fmt.Sprintf("Latest persisted turn: %s (%s).", strings.TrimSpace(latest.Status), strings.TrimSpace(latest.Kind)))
		if !latest.LastActivityAt.IsZero() {
			lines = append(lines, "Last activity: "+latest.LastActivityAt.UTC().Format(time.RFC3339)+".")
		}
		if tool := strings.TrimSpace(latest.LastToolName); tool != "" {
			lines = append(lines, "Last tool: "+tool+".")
		}
		if latest.ProgressMessageID != 0 {
			lines = append(lines, fmt.Sprintf("Progress message id: %d.", latest.ProgressMessageID))
		}
		if errorText := strings.TrimSpace(latest.ErrorText); errorText != "" {
			lines = append(lines, "Last error: "+truncateStatusDiagnostic(errorText, 180)+".")
		}
	}
	if perception := chatSnapshot.LatestPerceptionBudget; perception != nil {
		lines = append(lines, "Perception budget: "+summarizePerceptionBudgetStatus(*perception)+".")
	}
	if continuation := chatSnapshot.Continuation; continuation != nil {
		status := strings.ToLower(strings.TrimSpace(continuation.Status))
		if status == "pending" || status == "approved" || status == "revoked" {
			line := "Continuation: " + status
			if continuation.RemainingTurns > 0 {
				if continuation.RemainingTurns == 1 {
					line += " (1 turn remaining)"
				} else {
					line += fmt.Sprintf(" (%d turns remaining)", continuation.RemainingTurns)
				}
			}
			lines = append(lines, line+".")
		}
	}
	if auto := chatSnapshot.AutoApproval; auto != nil && auto.Active {
		line := "Auto approvals: active"
		if !auto.Usable && strings.TrimSpace(auto.BlockedReason) != "" {
			line += " but blocked by auto mode"
		}
		if scope := strings.TrimSpace(auto.Scope); scope != "" {
			line += " (" + scope + ")"
		}
		if !auto.ExpiresAt.IsZero() {
			line += ", expires " + auto.ExpiresAt.UTC().Format(time.RFC3339)
		}
		if auto.MaxUses > 0 {
			line += fmt.Sprintf(", used %d/%d", auto.UsedCount, auto.MaxUses)
		} else {
			line += fmt.Sprintf(", used %d", auto.UsedCount)
		}
		if !auto.Usable && strings.TrimSpace(auto.BlockedReason) != "" {
			line += ", " + strings.TrimSpace(auto.BlockedReason)
		}
		lines = append(lines, line+".")
	}
	if stuck, ok := r.operationApprovalAffordanceDiagnostic(chatID, chatSnapshot); ok {
		lines = append(lines, stuck)
	}
	if len(chatSnapshot.RecentAdjudications) > 0 {
		lines = append(lines, statusAdjudicationDiagnosticLine(chatSnapshot.RecentAdjudications[0]))
	}
	return lines, nil
}

func (r *Runtime) operationApprovalAffordanceDiagnostic(chatID int64, snapshot core.ChatStatusSnapshot) (string, bool) {
	if r == nil || r.store == nil || chatID == 0 {
		return "", false
	}
	if snapshot.Continuation != nil {
		status := strings.ToLower(strings.TrimSpace(snapshot.Continuation.Status))
		if status == "pending" || status == "approved" {
			return "", false
		}
	}
	for _, item := range snapshot.PendingItems {
		if item.Kind == core.PendingItemKindContinuation || item.Kind == core.PendingItemKindDecision {
			return "", false
		}
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	_, opState, exists, err := r.store.PlanAndOperationStateIfExists(key)
	if err != nil || !exists {
		return "", false
	}
	opState = session.NormalizeOperationState(opState)
	if !operationStateNeedsApprovalAffordance(opState) {
		return "", false
	}
	currentID := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID)
	staleCount := operationPhasePlanStaleInProgressCount(opState.PhasePlan)
	parts := []string{"Approval affordance gap: operation has pending approval work but no pending continuation or decision."}
	if currentID != "" {
		parts = append(parts, "current_phase="+currentID)
	}
	if staleCount > 0 {
		parts = append(parts, fmt.Sprintf("stale_in_progress_phases=%d", staleCount))
	}
	return strings.Join(parts, " ") + ".", true
}

func operationStateNeedsApprovalAffordance(opState session.OperationState) bool {
	opState = session.NormalizeOperationState(opState)
	if pendingOperationPlanLeaseNeedsButton(opState.PlanLease) || pendingOperationProposalNeedsButton(opState.Proposal) {
		return true
	}
	if _, ok := operationPlanLeaseFromPhasePlan(opState, time.Now().UTC()); ok {
		return true
	}
	if _, ok := nextOperationPhaseBundleForApproval(opState); ok {
		return true
	}
	if _, ok := nextOperationPhaseForApproval(opState); ok {
		return true
	}
	return false
}

func operationPhasePlanStaleInProgressCount(plan session.OperationPhasePlan) int {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	currentID := strings.TrimSpace(plan.CurrentPhaseID)
	if currentID == "" {
		return 0
	}
	count := 0
	for _, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if phase.Status == session.PlanStatusInProgress && strings.TrimSpace(phase.ID) != currentID {
			count++
		}
	}
	return count
}
