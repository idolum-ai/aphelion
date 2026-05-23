//go:build linux

package maintenancecli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type RestartParkResult struct {
	TurnRunsInterrupted          int
	ContinuationsParked          int
	PendingContinuationsParked   int
	ApprovedContinuationsParked  int
	AlreadyParkedContinuations   int
	SkippedContinuations         int
	ExpiredApprovedContinuations int
}

type LiveStateRepairDeps struct {
	ParkActiveWorkForRestart func(context.Context, *session.SQLiteStore, string, time.Time) (RestartParkResult, error)
}

type liveStateRepairResult struct {
	RestartPark                RestartParkResult
	ContinuationsClosed        int
	PlanLeasesRevoked          int
	AuthorityContractsRepaired int
	PendingDecisionsCleared    int
	PendingDecisionSnapshots   int
}

func runRepairLiveStateCommand(args []string, deps LiveStateRepairDeps) error {
	fs := flag.NewFlagSet("repair-live-state", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	sourceFlag := fs.String("source", "live_state_repair", "repair source label recorded in cleanup events")
	closeLive := fs.Bool("close-live", true, "close pending/approved live continuations and plan leases after parking")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	result, err := repairLiveState(context.Background(), store, *sourceFlag, *closeLive, time.Now().UTC(), deps)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: repair-live-state\n")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "source: %s\n", strings.TrimSpace(*sourceFlag))
	fmt.Fprintf(os.Stdout, "close_live: %t\n", *closeLive)
	fmt.Fprintf(os.Stdout, "turn_runs_interrupted: %d\n", result.RestartPark.TurnRunsInterrupted)
	fmt.Fprintf(os.Stdout, "continuations_parked: %d\n", result.RestartPark.ContinuationsParked)
	fmt.Fprintf(os.Stdout, "continuations_closed: %d\n", result.ContinuationsClosed)
	fmt.Fprintf(os.Stdout, "plan_leases_revoked: %d\n", result.PlanLeasesRevoked)
	fmt.Fprintf(os.Stdout, "authority_contracts_repaired: %d\n", result.AuthorityContractsRepaired)
	fmt.Fprintf(os.Stdout, "pending_decisions_cleared: %d\n", result.PendingDecisionsCleared)
	fmt.Fprintf(os.Stdout, "pending_decision_snapshots: %d\n", result.PendingDecisionSnapshots)
	return nil
}

func repairLiveState(ctx context.Context, store *session.SQLiteStore, source string, closeLive bool, now time.Time, deps LiveStateRepairDeps) (liveStateRepairResult, error) {
	result := liveStateRepairResult{}
	if store == nil {
		return result, fmt.Errorf("repair live state requires session store")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	source = strings.TrimSpace(source)
	if source == "" {
		source = "live_state_repair"
	}

	if deps.ParkActiveWorkForRestart == nil {
		return result, fmt.Errorf("repair live state restart parking dependency is unavailable")
	}
	park, err := deps.ParkActiveWorkForRestart(ctx, store, source, now)
	if err != nil {
		return result, err
	}
	result.RestartPark = park

	decisions, err := store.PendingDecisions()
	if err != nil {
		return result, fmt.Errorf("load pending decisions for live repair: %w", err)
	}
	result.PendingDecisionSnapshots = len(decisions)
	if len(decisions) > 0 {
		cleared, err := store.DeleteAllPendingDecisions()
		if err != nil {
			return result, fmt.Errorf("clear pending decisions for live repair: %w", err)
		}
		result.PendingDecisionsCleared = cleared
		if err := appendMaintenanceExecutionEvent(store, maintenanceRepairKey(), core.ExecutionEventDecisionDetached, "decision", "detached", map[string]any{
			"source":         source,
			"decision_count": cleared,
			"snapshot_count": len(decisions),
			"cleanup_reason": "live_state_repair",
		}, now); err != nil {
			return result, err
		}
	}

	records, err := store.ContinuationStates()
	if err != nil {
		return result, fmt.Errorf("load continuation states for live repair: %w", err)
	}
	if closeLive {
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			state, changed := closeContinuationForLiveRepair(record.State, source, now)
			if changed {
				if err := store.UpdateContinuationState(record.Key, state); err != nil {
					return result, fmt.Errorf("close live continuation chat_id=%d: %w", record.Key.ChatID, err)
				}
				result.ContinuationsClosed++
				if err := appendMaintenanceExecutionEvent(store, record.Key, core.ExecutionEventContinuationRevoked, "continuation", "revoked", map[string]any{
					"source":         source,
					"cleanup_reason": "live_state_repair",
					"prior_status":   string(record.State.Status),
					"decision_id":    record.State.DecisionID,
					"proposal_id":    record.State.ActionProposal.ID,
					"lease_id":       record.State.ContinuationLease.ID,
				}, now); err != nil {
					return result, err
				}
			}
		}
	}
	operationRecords, err := store.OperationStates()
	if err != nil {
		return result, fmt.Errorf("load operation states for live repair: %w", err)
	}
	if repaired, err := repairAuthorityContractDriftForLiveState(ctx, store, operationRecords, source, now); err != nil {
		return result, err
	} else {
		result.AuthorityContractsRepaired = repaired
	}
	if !closeLive {
		return result, nil
	}
	if result.AuthorityContractsRepaired > 0 {
		operationRecords, err = store.OperationStates()
		if err != nil {
			return result, fmt.Errorf("reload operation states after authority repair: %w", err)
		}
	}
	for _, record := range operationRecords {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if revoked, err := revokeOperationPlanLeaseForLiveRepair(store, record.Key, record.State, source, now); err != nil {
			return result, err
		} else if revoked {
			result.PlanLeasesRevoked++
		}
	}
	if err := appendMaintenanceExecutionEvent(store, maintenanceRepairKey(), core.ExecutionEventRecoveryCompleted, "recovery", "completed", map[string]any{
		"source":                       source,
		"phase":                        "live_state_repair",
		"turn_runs_interrupted":        result.RestartPark.TurnRunsInterrupted,
		"continuations_parked":         result.RestartPark.ContinuationsParked,
		"continuations_closed":         result.ContinuationsClosed,
		"plan_leases_revoked":          result.PlanLeasesRevoked,
		"authority_contracts_repaired": result.AuthorityContractsRepaired,
		"pending_decisions_cleared":    result.PendingDecisionsCleared,
	}, now); err != nil {
		return result, err
	}
	return result, nil
}

func repairAuthorityContractDriftForLiveState(ctx context.Context, store *session.SQLiteStore, records []session.OperationStateRecord, source string, now time.Time) (int, error) {
	if store == nil {
		return 0, nil
	}
	repaired := 0
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return repaired, err
		}
		op, changed, repairedPhaseIDs := repairOperationAuthorityContractDrift(record.State, now)
		if !changed {
			continue
		}
		if err := store.UpdateOperationState(record.Key, op); err != nil {
			return repaired, fmt.Errorf("repair authority contract drift chat_id=%d: %w", record.Key.ChatID, err)
		}
		if cont, exists, err := store.ContinuationStateIfExists(record.Key); err != nil {
			return repaired, fmt.Errorf("load continuation for authority repair chat_id=%d: %w", record.Key.ChatID, err)
		} else if exists {
			if repairedCont, contChanged := repairContinuationAuthorityContractDrift(cont, now); contChanged {
				if err := store.UpdateContinuationState(record.Key, repairedCont); err != nil {
					return repaired, fmt.Errorf("repair continuation authority contract drift chat_id=%d: %w", record.Key.ChatID, err)
				}
			}
		}
		repaired++
		if err := appendMaintenanceExecutionEvent(store, record.Key, core.ExecutionEventRecoveryCompleted, "recovery", "authority_contract_repaired", map[string]any{
			"source":          strings.TrimSpace(source),
			"cleanup_reason":  "authority_contract_drift_repair",
			"operation_id":    op.ID,
			"repaired_phases": repairedPhaseIDs,
			"authority_class": session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
		}, now); err != nil {
			return repaired, err
		}
	}
	return repaired, nil
}

func repairOperationAuthorityContractDrift(prior session.OperationState, now time.Time) (session.OperationState, bool, []string) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	op := session.NormalizeOperationState(prior)
	contract, ok := session.AuthorityContractForToken(session.AuthorityClassLocalSecretMetadataReadLiveConfigRead)
	if !ok {
		return op, false, nil
	}
	changed := false
	repairedPhaseIDs := []string{}
	for i := range op.PhasePlan.Phases {
		phase := op.PhasePlan.Phases[i]
		if session.NormalizeActionProposal(session.ActionProposal{RiskClass: phase.AuthorityClass}).RiskClass != session.AuthorityClassLocalSecretMetadataReadLiveConfigRead {
			continue
		}
		repairedPhaseIDs = append(repairedPhaseIDs, strings.TrimSpace(phase.ID))
		cleanSummary := cleanAuthorityRepairText(phase.Summary)
		if cleanSummary != phase.Summary {
			op.PhasePlan.Phases[i].Summary = cleanSummary
			changed = true
		}
		cleanEffect := cleanAuthorityRepairText(phase.BoundedEffect)
		if cleanEffect != phase.BoundedEffect {
			op.PhasePlan.Phases[i].BoundedEffect = cleanEffect
			changed = true
		}
		if !sameStringSet(op.PhasePlan.Phases[i].AllowedActions, contract.AllowedActions) {
			op.PhasePlan.Phases[i].AllowedActions = append([]string(nil), contract.AllowedActions...)
			changed = true
		}
		if !sameStringSet(op.PhasePlan.Phases[i].ForbiddenActions, contract.ForbiddenActions) {
			op.PhasePlan.Phases[i].ForbiddenActions = append([]string(nil), contract.ForbiddenActions...)
			changed = true
		}
		if !sameStringSet(op.PhasePlan.Phases[i].ValidationPlan, contract.ValidationPlan) {
			op.PhasePlan.Phases[i].ValidationPlan = append([]string(nil), contract.ValidationPlan...)
			changed = true
		}
		if !op.PhasePlan.Phases[i].RequiresApproval {
			op.PhasePlan.Phases[i].RequiresApproval = true
			changed = true
		}
		if op.PhasePlan.Phases[i].Status == session.PlanStatusInProgress || op.PhasePlan.Phases[i].Status == "" {
			op.PhasePlan.Phases[i].Status = session.PlanStatusPending
			changed = true
		}
		if strings.TrimSpace(op.PhasePlan.Phases[i].LeaseID) != "" {
			op.PhasePlan.Phases[i].LeaseID = ""
			changed = true
		}
	}
	if len(repairedPhaseIDs) == 0 {
		return op, false, nil
	}
	if op.Proposal.Active() && session.NormalizeActionProposal(session.ActionProposal{RiskClass: op.Proposal.Kind}).RiskClass == session.AuthorityClassLocalSecretMetadataReadLiveConfigRead {
		op.Proposal.Status = session.ProposalStatusSuperseded
		op.Proposal.Summary = cleanAuthorityRepairText(op.Proposal.Summary)
		op.Proposal.BoundedEffect = cleanAuthorityRepairText(op.Proposal.BoundedEffect)
		op.Proposal.UpdatedAt = now
		changed = true
	}
	if op.Status != session.OperationStatusBlocked {
		op.Status = session.OperationStatusBlocked
		changed = true
	}
	if op.Stage != "phase_approval_authority_repaired" {
		op.Stage = "phase_approval_authority_repaired"
		changed = true
	}
	if changed {
		op.PhasePlan.UpdatedAt = now
		op.UpdatedAt = now
		op.Artifacts = append(op.Artifacts, session.OperationArtifact{
			Label: "Authority contract repair",
			Ref:   "repair://" + session.AuthorityClassLocalSecretMetadataReadLiveConfigRead + "/" + now.Format(time.RFC3339Nano),
		})
	}
	return session.NormalizeOperationState(op), changed, repairedPhaseIDs
}

func repairContinuationAuthorityContractDrift(prior session.ContinuationState, now time.Time) (session.ContinuationState, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state := session.NormalizeContinuationState(prior)
	if session.NormalizeActionProposal(session.ActionProposal{RiskClass: state.ActionProposal.RiskClass}).RiskClass != session.AuthorityClassLocalSecretMetadataReadLiveConfigRead {
		return state, false
	}
	changed := false
	if state.Status != session.ContinuationStatusRevoked {
		state.Status = session.ContinuationStatusRevoked
		changed = true
	}
	if state.RemainingTurns != 0 {
		state.RemainingTurns = 0
		changed = true
	}
	if state.ActionProposal.Status != session.ProposalStatusSuperseded {
		state.ActionProposal.Status = session.ProposalStatusSuperseded
		state.ActionProposal.UpdatedAt = now
		changed = true
	}
	if clean := cleanAuthorityRepairText(state.ActionProposal.Summary); clean != state.ActionProposal.Summary {
		state.ActionProposal.Summary = clean
		changed = true
	}
	if clean := cleanAuthorityRepairText(state.StageSummary); clean != state.StageSummary {
		state.StageSummary = clean
		changed = true
	}
	if state.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
		changed = true
	}
	if state.ContinuationLease.RemainingTurns != 0 {
		state.ContinuationLease.RemainingTurns = 0
		changed = true
	}
	reason := "Authority contract repair superseded a revoked metadata-read proposal that had been misclassified as workspace_write; request or materialize a fresh metadata-only read-only lease."
	if strings.TrimSpace(state.HandshakeBlockedReason) != reason {
		state.HandshakeBlockedReason = reason
		changed = true
	}
	if changed {
		state.ParkedAt = now
		state.ParkedReason = reason
		state.ParkedSource = "authority_contract_drift_repair"
		state.UpdatedAt = now
		state.ContinuationLease.UpdatedAt = now
	}
	return session.NormalizeContinuationState(state), changed
}

func cleanAuthorityRepairText(value string) string {
	trimmed := strings.TrimSpace(value)
	for _, marker := range []string{" BLOCKED:", "\nBLOCKED:", " blocked:", "\nblocked:"} {
		if idx := strings.Index(trimmed, marker); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}
	}
	return trimmed
}

func sameStringSet(a []string, b []string) bool {
	aa := session.NormalizeActionProposal(session.ActionProposal{AllowedActions: a}).AllowedActions
	bb := session.NormalizeActionProposal(session.ActionProposal{AllowedActions: b}).AllowedActions
	if len(aa) != len(bb) {
		return false
	}
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func closeContinuationForLiveRepair(prior session.ContinuationState, source string, now time.Time) (session.ContinuationState, bool) {
	prior = session.NormalizeContinuationState(prior)
	if prior.Status != session.ContinuationStatusPending && prior.Status != session.ContinuationStatusApproved {
		return prior, false
	}
	state := prior
	state.Status = session.ContinuationStatusRevoked
	state.RemainingTurns = 0
	state.HandshakeBlockedReason = "Live state repair closed stale pending/approved continuation; ask for a fresh proposal if this work is still needed."
	state.ParkedAt = now
	state.ParkedSource = strings.TrimSpace(source)
	state.ParkedReason = state.HandshakeBlockedReason
	if state.ActionProposal.Active() {
		state.ActionProposal.Status = session.ProposalStatusSuperseded
		state.ActionProposal.WhyNow = state.HandshakeBlockedReason
		state.ActionProposal.UpdatedAt = now
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
		state.ContinuationLease.RemainingTurns = 0
		state.ContinuationLease.UpdatedAt = now
	}
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state), true
}

func revokeOperationPlanLeaseForLiveRepair(store *session.SQLiteStore, key session.SessionKey, op session.OperationState, source string, now time.Time) (bool, error) {
	op = session.NormalizeOperationState(op)
	if !op.PlanLease.Active() {
		return false, nil
	}
	status := session.NormalizePlanLeaseStatus(op.PlanLease.Status)
	switch status {
	case session.PlanLeaseStatusProposed, session.PlanLeaseStatusApproved, session.PlanLeaseStatusActive, session.PlanLeaseStatusPaused:
	default:
		return false, nil
	}
	op.PlanLease.Status = session.PlanLeaseStatusRevoked
	op.PlanLease.RemainingTurns = 0
	op.PlanLease.UpdatedAt = now
	op.Status = session.OperationStatusBlocked
	op.Stage = "live_state_repair"
	op.Summary = strings.TrimSpace(op.Summary)
	if op.Summary != "" {
		op.Summary += "\n"
	}
	op.Summary += "Live state repair revoked stale plan lease; request a fresh lease before more autonomous work."
	op.Artifacts = append(op.Artifacts, session.OperationArtifact{
		Label: "Live state repair",
		Ref:   "cleanup://" + strings.TrimSpace(source) + "/" + now.UTC().Format(time.RFC3339Nano),
	})
	op.UpdatedAt = now
	if err := store.UpdateOperationState(key, op); err != nil {
		return false, fmt.Errorf("update operation state for live repair chat_id=%d: %w", key.ChatID, err)
	}
	if err := appendMaintenanceExecutionEvent(store, key, core.ExecutionEventRecoveryCompleted, "recovery", "plan_lease_revoked", map[string]any{
		"source":         strings.TrimSpace(source),
		"cleanup_reason": "live_state_repair",
		"operation_id":   op.ID,
		"plan_lease_id":  op.PlanLease.ID,
	}, now); err != nil {
		return false, err
	}
	return true, nil
}
