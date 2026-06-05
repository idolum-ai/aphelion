//go:build linux

package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func renderOperationPlanLease(b *strings.Builder, lease session.OperationPlanLease) {
	lease = session.NormalizeOperationPlanLease(lease)
	b.WriteString("plan_lease:\n")
	if lease.ID != "" {
		fmt.Fprintf(b, "- id: %s\n", lease.ID)
	}
	if lease.Summary != "" {
		fmt.Fprintf(b, "- summary: %s\n", lease.Summary)
	}
	if lease.Objective != "" {
		fmt.Fprintf(b, "- objective: %s\n", lease.Objective)
	}
	if lease.MissionID != "" {
		fmt.Fprintf(b, "- mission_id: %s\n", lease.MissionID)
	}
	if lease.OperationID != "" {
		fmt.Fprintf(b, "- operation_id: %s\n", lease.OperationID)
	}
	if lease.Status != "" {
		fmt.Fprintf(b, "- status: %s\n", lease.Status)
	}
	if lease.TurnBudget > 0 || lease.RemainingTurns > 0 {
		fmt.Fprintf(b, "- turns: budget=%d remaining=%d\n", lease.TurnBudget, lease.RemainingTurns)
	}
	if len(lease.CoveredPhaseIDs) > 0 {
		fmt.Fprintf(b, "- covered_phase_ids: %s\n", strings.Join(lease.CoveredPhaseIDs, ", "))
	}
	if !lease.ExpiresAt.IsZero() {
		fmt.Fprintf(b, "- expires_at: %s\n", lease.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if len(lease.Lanes) > 0 {
		b.WriteString("- lanes:\n")
		for _, lane := range lease.Lanes {
			fmt.Fprintf(b, "  - %s", lane.ID)
			if lane.Summary != "" {
				fmt.Fprintf(b, ": %s", lane.Summary)
			}
			b.WriteString("\n")
			if lane.AuthorityClass != "" {
				fmt.Fprintf(b, "    authority_class: %s\n", lane.AuthorityClass)
			}
			if lane.ExpectedTurns > 0 {
				fmt.Fprintf(b, "    expected_turns: %d\n", lane.ExpectedTurns)
			}
			if len(lane.AllowedActions) > 0 {
				fmt.Fprintf(b, "    allowed_actions: %s\n", strings.Join(lane.AllowedActions, ", "))
			}
			if len(lane.ForbiddenActions) > 0 {
				fmt.Fprintf(b, "    forbidden_actions: %s\n", strings.Join(lane.ForbiddenActions, ", "))
			}
		}
	}
	if len(lease.AllowedActions) > 0 {
		fmt.Fprintf(b, "- allowed_actions: %s\n", strings.Join(lease.AllowedActions, ", "))
	}
	if len(lease.ForbiddenActions) > 0 {
		fmt.Fprintf(b, "- forbidden_actions: %s\n", strings.Join(lease.ForbiddenActions, ", "))
	}
	if len(lease.ValidationGates) > 0 {
		fmt.Fprintf(b, "- validation_gates: %s\n", strings.Join(lease.ValidationGates, "; "))
	}
	if len(lease.ExitConditions) > 0 {
		fmt.Fprintf(b, "- exit_conditions: %s\n", strings.Join(lease.ExitConditions, "; "))
	}
	if len(lease.HardInterrupts) > 0 {
		fmt.Fprintf(b, "- hard_interrupts: %s\n", strings.Join(lease.HardInterrupts, ", "))
	}
	if len(lease.ChildInitiationLanes) > 0 {
		fmt.Fprintf(b, "- child_initiation_lanes: %s\n", strings.Join(lease.ChildInitiationLanes, ", "))
	}
	if lease.EvidenceDigest.Active() {
		b.WriteString("- evidence_digest:\n")
		fmt.Fprintf(b, "  turns_spent: %d\n", lease.EvidenceDigest.TurnsSpent)
		if len(lease.EvidenceDigest.LanesUsed) > 0 {
			fmt.Fprintf(b, "  lanes_used: %s\n", strings.Join(lease.EvidenceDigest.LanesUsed, ", "))
		}
		if len(lease.EvidenceDigest.Completed) > 0 {
			fmt.Fprintf(b, "  completed: %s\n", strings.Join(lease.EvidenceDigest.Completed, "; "))
		}
		if len(lease.EvidenceDigest.Blocked) > 0 {
			fmt.Fprintf(b, "  blocked: %s\n", strings.Join(lease.EvidenceDigest.Blocked, "; "))
		}
		if len(lease.EvidenceDigest.InterruptsRaised) > 0 {
			fmt.Fprintf(b, "  interrupts_raised: %s\n", strings.Join(lease.EvidenceDigest.InterruptsRaised, "; "))
		}
		if len(lease.EvidenceDigest.EvidenceRefs) > 0 {
			fmt.Fprintf(b, "  evidence_refs: %s\n", strings.Join(lease.EvidenceDigest.EvidenceRefs, ", "))
		}
		if len(lease.EvidenceDigest.ChangesMade) > 0 {
			fmt.Fprintf(b, "  changes_made: %s\n", strings.Join(lease.EvidenceDigest.ChangesMade, "; "))
		}
		if lease.EvidenceDigest.ResidualRisk != "" {
			fmt.Fprintf(b, "  residual_risk: %s\n", lease.EvidenceDigest.ResidualRisk)
		}
		if lease.EvidenceDigest.SuggestedNextLease != "" {
			fmt.Fprintf(b, "  suggested_next_lease: %s\n", lease.EvidenceDigest.SuggestedNextLease)
		}
	}
	b.WriteString("- authority_note: plan lease is a bounded plan envelope, not a capability grant\n")
}

func renderOperationUpdateAck(state session.OperationState, in updateOperationInput) string {
	state = session.NormalizeOperationState(state)
	var b strings.Builder
	b.WriteString("[OPERATION_UPDATED]\n")
	b.WriteString("ok: true\n")
	if state.ID != "" {
		fmt.Fprintf(&b, "id: %s\n", state.ID)
	}
	fmt.Fprintf(&b, "active: %t\n", state.Active())
	if state.Status != "" {
		fmt.Fprintf(&b, "status: %s\n", state.Status)
	}
	if state.Stage != "" {
		fmt.Fprintf(&b, "stage: %s\n", state.Stage)
	}
	if state.UpdatedAt.IsZero() {
		b.WriteString("snapshot: operation_state\n")
	} else {
		fmt.Fprintf(&b, "snapshot: operation_state@%s\n", state.UpdatedAt.UTC().Format(time.RFC3339))
		// TODO(turn-performance): replace timestamp-only snapshot pointers with ledger versions or CAS-addressable operation snapshots before adding version-aware update semantics.
	}
	fields := updateOperationReceivedFields(in)
	if len(fields) > 0 {
		fmt.Fprintf(&b, "received_fields: %s\n", strings.Join(fields, ", "))
	}
	if state.PhasePlan.Active() {
		current := strings.TrimSpace(state.PhasePlan.CurrentPhaseID)
		if current == "" && len(state.PhasePlan.Phases) > 0 {
			current = state.PhasePlan.Phases[0].ID
		}
		if current != "" {
			fmt.Fprintf(&b, "current_phase: %s\n", current)
		}
	}
	if state.Proposal.Active() {
		parts := []string{}
		if state.Proposal.ID != "" {
			parts = append(parts, state.Proposal.ID)
		}
		if state.Proposal.Status != "" {
			parts = append(parts, string(state.Proposal.Status))
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "proposal: %s\n", strings.Join(parts, " "))
		}
	}
	b.WriteString("note: full operation state is persisted; call update_operation with empty input to inspect")
	return strings.TrimSpace(b.String())
}

func updateOperationReceivedFields(in updateOperationInput) []string {
	fields := make([]string, 0, 10)
	if strings.TrimSpace(in.ID) != "" {
		fields = append(fields, "id")
	}
	if strings.TrimSpace(in.Objective) != "" {
		fields = append(fields, "objective")
	}
	if strings.TrimSpace(in.Status) != "" {
		fields = append(fields, "status")
	}
	if strings.TrimSpace(in.Stage) != "" {
		fields = append(fields, "stage")
	}
	if strings.TrimSpace(in.Summary) != "" {
		fields = append(fields, "summary")
	}
	if in.Proposal != nil {
		fields = append(fields, "proposal")
	}
	if in.PhasePlan != nil {
		fields = append(fields, "phase_plan")
	}
	if in.Findings != nil {
		fields = append(fields, "findings")
	}
	if in.Artifacts != nil {
		fields = append(fields, "artifacts")
	}
	return fields
}

func renderOperationState(header string, state session.OperationState) string {
	state = session.NormalizeOperationState(state)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "active: %t\n", state.Active())
	if state.ID != "" {
		fmt.Fprintf(&b, "id: %s\n", state.ID)
	}
	if state.Objective != "" {
		fmt.Fprintf(&b, "objective: %s\n", state.Objective)
	}
	if state.Status != "" {
		fmt.Fprintf(&b, "status: %s\n", state.Status)
	}
	if state.Stage != "" {
		fmt.Fprintf(&b, "stage: %s\n", state.Stage)
	}
	if state.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", state.Summary)
	}
	if state.Proposal.Active() {
		b.WriteString("proposal:\n")
		if state.Proposal.ID != "" {
			fmt.Fprintf(&b, "- id: %s\n", state.Proposal.ID)
		}
		if state.Proposal.Kind != "" {
			fmt.Fprintf(&b, "- kind: %s\n", state.Proposal.Kind)
		}
		if state.Proposal.Status != "" {
			fmt.Fprintf(&b, "- status: %s\n", state.Proposal.Status)
		}
		if state.Proposal.Summary != "" {
			fmt.Fprintf(&b, "- summary: %s\n", state.Proposal.Summary)
		}
		if state.Proposal.WhyNow != "" {
			fmt.Fprintf(&b, "- why_now: %s\n", state.Proposal.WhyNow)
		}
		if state.Proposal.BoundedEffect != "" {
			fmt.Fprintf(&b, "- bounded_effect: %s\n", state.Proposal.BoundedEffect)
		}
	} else {
		b.WriteString("proposal: none\n")
	}
	if state.PhasePlan.Active() {
		b.WriteString("phase_plan:\n")
		if state.PhasePlan.ID != "" {
			fmt.Fprintf(&b, "- id: %s\n", state.PhasePlan.ID)
		}
		if state.PhasePlan.Goal != "" {
			fmt.Fprintf(&b, "- goal: %s\n", state.PhasePlan.Goal)
		}
		if state.PhasePlan.CurrentPhaseID != "" {
			fmt.Fprintf(&b, "- current_phase_id: %s\n", state.PhasePlan.CurrentPhaseID)
		}
		if len(state.PhasePlan.Phases) == 0 {
			b.WriteString("- phases: none\n")
		} else {
			b.WriteString("- phases:\n")
			for _, phase := range state.PhasePlan.Phases {
				fmt.Fprintf(&b, "  - [%s] %s", phase.Status, phase.ID)
				if phase.Summary != "" {
					fmt.Fprintf(&b, ": %s", phase.Summary)
				}
				b.WriteString("\n")
				if phase.AuthorityClass != "" {
					fmt.Fprintf(&b, "    authority_class: %s\n", phase.AuthorityClass)
				}
				if phase.WhyNow != "" {
					fmt.Fprintf(&b, "    why_now: %s\n", phase.WhyNow)
				}
				if phase.BoundedEffect != "" {
					fmt.Fprintf(&b, "    bounded_effect: %s\n", phase.BoundedEffect)
				}
				if len(phase.AllowedActions) > 0 {
					fmt.Fprintf(&b, "    allowed_actions: %s\n", strings.Join(phase.AllowedActions, ", "))
				}
				if len(phase.ForbiddenActions) > 0 {
					fmt.Fprintf(&b, "    forbidden_actions: %s\n", strings.Join(phase.ForbiddenActions, ", "))
				}
				if len(phase.ValidationPlan) > 0 {
					fmt.Fprintf(&b, "    validation_plan: %s\n", strings.Join(phase.ValidationPlan, "; "))
				}
				if phase.GateLevel != "" {
					fmt.Fprintf(&b, "    gate_level: %s\n", phase.GateLevel)
				}
				if phase.GateReasonCode != "" {
					fmt.Fprintf(&b, "    gate_reason_code: %s\n", phase.GateReasonCode)
				}
				if phase.ApprovalSubject != "" {
					fmt.Fprintf(&b, "    approval_subject: %s\n", phase.ApprovalSubject)
				}
				if phase.AutoApproveEligible != nil {
					fmt.Fprintf(&b, "    autoapprove_eligible: %t\n", *phase.AutoApproveEligible)
				}
				if phase.BlockedReasonCode != "" {
					fmt.Fprintf(&b, "    blocked_reason_code: %s\n", phase.BlockedReasonCode)
				}
				if phase.RequiresConsent {
					b.WriteString("    requires_consent: true\n")
				}
				if phase.RequiresOptIn {
					b.WriteString("    requires_opt_in: true\n")
				}
				if len(phase.SupersedesPhaseIDs) > 0 {
					fmt.Fprintf(&b, "    supersedes_phase_ids: %s\n", strings.Join(phase.SupersedesPhaseIDs, ", "))
				}
				if phase.StaleAuthority {
					b.WriteString("    stale_authority: true\n")
				}
				if phase.LeaseID != "" {
					fmt.Fprintf(&b, "    lease_id: %s\n", phase.LeaseID)
				}
			}
		}
	} else {
		b.WriteString("phase_plan: none\n")
	}
	if state.PlanLease.Active() {
		renderOperationPlanLease(&b, state.PlanLease)
	} else {
		b.WriteString("plan_lease: none\n")
	}
	if len(state.Findings) == 0 {
		b.WriteString("findings: none\n")
	} else {
		b.WriteString("findings:\n")
		for _, finding := range state.Findings {
			fmt.Fprintf(&b, "- [%s] %s", finding.Confidence, finding.Claim)
			if finding.Basis != "" {
				fmt.Fprintf(&b, " (basis: %s)", finding.Basis)
			}
			b.WriteString("\n")
		}
	}
	if len(state.Artifacts) == 0 {
		b.WriteString("artifacts: none\n")
	} else {
		b.WriteString("artifacts:\n")
		for _, artifact := range state.Artifacts {
			if artifact.Label != "" {
				fmt.Fprintf(&b, "- %s: %s\n", artifact.Label, artifact.Ref)
				continue
			}
			fmt.Fprintf(&b, "- %s\n", artifact.Ref)
		}
	}
	return strings.TrimSpace(b.String())
}
