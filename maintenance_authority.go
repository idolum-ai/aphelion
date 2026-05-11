//go:build linux

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

func runAuthorityCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: authority <doctor|repair> [--config path]")
	}
	switch strings.TrimSpace(args[0]) {
	case "doctor":
		return runAuthorityDoctorCommand(args[1:])
	case "repair":
		return runAuthorityRepairCommand(args[1:])
	default:
		return fmt.Errorf("unknown authority command %q (known: doctor|repair)", args[0])
	}
}

func runAuthorityDoctorCommand(args []string) error {
	fs := flag.NewFlagSet("authority doctor", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	limitFlag := fs.Int("limit", 50, "maximum findings to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	snapshot, configPath, err := authoritySnapshotForCommand(*configFlag)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "action: authority-doctor")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	writeAuthoritySnapshot(os.Stdout, snapshot, *limitFlag, false)
	return nil
}

func runAuthorityRepairCommand(args []string) error {
	fs := flag.NewFlagSet("authority repair", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	limitFlag := fs.Int("limit", 50, "maximum repair previews to print")
	applyFlag := fs.Bool("apply", false, "apply supported repairs")
	findingFlag := fs.String("finding", "", "exact authority finding id to repair")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *applyFlag {
		return runAuthorityRepairApplyCommand(*configFlag, *findingFlag)
	}
	snapshot, configPath, err := authoritySnapshotForCommand(*configFlag)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "action: authority-repair")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintln(os.Stdout, "dry_run: true")
	writeAuthoritySnapshot(os.Stdout, snapshot, *limitFlag, true)
	return nil
}

func authoritySnapshotForCommand(configPathFlag string) (core.AuthorityStatusSnapshot, string, error) {
	store, configPath, closeStore, err := authorityStoreForCommand(configPathFlag)
	if err != nil {
		return core.AuthorityStatusSnapshot{}, "", err
	}
	defer closeStore()
	snapshot, err := authoritySnapshotFromStore(store)
	if err != nil {
		return core.AuthorityStatusSnapshot{}, "", err
	}
	return snapshot, configPath, nil
}

func authorityStoreForCommand(configPathFlag string) (*session.SQLiteStore, string, func(), error) {
	cfg, configPath, err := loadConfigForCommand(configPathFlag)
	if err != nil {
		return nil, "", nil, err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return nil, "", nil, err
	}
	return store, configPath, func() { _ = store.Close() }, nil
}

func authoritySnapshotFromStore(store *session.SQLiteStore) (core.AuthorityStatusSnapshot, error) {
	snapshot, err := aphruntime.AuthorityStatusSnapshotFromStore(store, time.Now().UTC())
	if err != nil {
		return core.AuthorityStatusSnapshot{}, err
	}
	return snapshot, nil
}

func runAuthorityRepairApplyCommand(configPathFlag string, findingID string) error {
	findingID = strings.TrimSpace(findingID)
	if findingID == "" {
		return fmt.Errorf("authority repair --apply requires --finding <finding_id> from a fresh authority repair preview")
	}
	store, configPath, closeStore, err := authorityStoreForCommand(configPathFlag)
	if err != nil {
		return err
	}
	defer closeStore()
	before, err := authoritySnapshotFromStore(store)
	if err != nil {
		return err
	}
	finding, ok := authorityFindingByID(before, findingID)
	if !ok {
		return fmt.Errorf("authority repair finding %q is not present; rerun authority repair and apply a current finding_id", findingID)
	}
	if !finding.Repairable {
		return fmt.Errorf("authority repair finding %q is preview-only; next_repair=%q", findingID, strings.TrimSpace(finding.NextRepairAction))
	}
	if !authorityRepairActionSupported(finding.RepairAction) {
		return fmt.Errorf("authority repair action %q for finding %q is not supported for --apply", strings.TrimSpace(finding.RepairAction), findingID)
	}
	now := time.Now().UTC()
	if err := applyAuthorityRepairFinding(store, finding, now); err != nil {
		return err
	}
	after, err := authoritySnapshotFromStore(store)
	if err != nil {
		return err
	}
	if _, stillPresent := authorityFindingByID(after, findingID); stillPresent {
		return fmt.Errorf("authority repair finding %q was not closed by %s", findingID, strings.TrimSpace(finding.RepairAction))
	}
	fmt.Fprintln(os.Stdout, "action: authority-repair")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintln(os.Stdout, "dry_run: false")
	fmt.Fprintln(os.Stdout, "applied: true")
	fmt.Fprintf(os.Stdout, "finding_id: %s\n", findingID)
	fmt.Fprintf(os.Stdout, "repair_action: %s\n", strings.TrimSpace(finding.RepairAction))
	fmt.Fprintf(os.Stdout, "before_status: %s\n", firstNonEmpty(strings.TrimSpace(before.Status), "healthy"))
	fmt.Fprintf(os.Stdout, "after_status: %s\n", firstNonEmpty(strings.TrimSpace(after.Status), "healthy"))
	fmt.Fprintf(os.Stdout, "before_findings: %d\n", before.FindingCount)
	fmt.Fprintf(os.Stdout, "after_findings: %d\n", after.FindingCount)
	return nil
}

func writeAuthoritySnapshot(out *os.File, snapshot core.AuthorityStatusSnapshot, limit int, repairOnly bool) {
	if limit <= 0 {
		limit = 50
	}
	fmt.Fprintf(out, "status: %s\n", firstNonEmpty(strings.TrimSpace(snapshot.Status), "healthy"))
	fmt.Fprintf(out, "findings: %d\n", snapshot.FindingCount)
	fmt.Fprintf(out, "errors: %d\n", snapshot.ErrorCount)
	fmt.Fprintf(out, "warnings: %d\n", snapshot.WarningCount)
	fmt.Fprintf(out, "continuation_records: %d\n", snapshot.ContinuationRecords)
	fmt.Fprintf(out, "operation_records: %d\n", snapshot.OperationRecords)
	fmt.Fprintf(out, "pending_decisions: %d\n", snapshot.PendingDecisions)
	fmt.Fprintf(out, "active_autoapproval_leases: %d\n", snapshot.AutoApprovalLeases)
	fmt.Fprintf(out, "active_capability_grants: %d\n", snapshot.CapabilityGrants)
	printed := 0
	repairable := 0
	for _, finding := range snapshot.Findings {
		if finding.Repairable {
			repairable++
		}
	}
	if repairOnly {
		fmt.Fprintf(out, "repairable: %d\n", repairable)
	}
	for _, finding := range snapshot.Findings {
		if repairOnly && strings.TrimSpace(finding.RepairAction) == "" {
			continue
		}
		if printed >= limit {
			break
		}
		printed++
		fmt.Fprintf(out, "- code=%s severity=%s source=%s:%s", finding.Code, finding.Severity, finding.SourceKind, finding.SourceID)
		if strings.TrimSpace(finding.FindingID) != "" {
			fmt.Fprintf(out, " finding_id=%s", strings.TrimSpace(finding.FindingID))
		}
		if finding.ChatID != 0 {
			fmt.Fprintf(out, " chat_id=%d", finding.ChatID)
		}
		if strings.TrimSpace(finding.SessionID) != "" {
			fmt.Fprintf(out, " session_id=%s", finding.SessionID)
		}
		if strings.TrimSpace(finding.RepairAction) != "" {
			fmt.Fprintf(out, " repair_action=%s", finding.RepairAction)
		}
		if finding.Repairable {
			fmt.Fprint(out, " repairable=true")
		}
		if strings.TrimSpace(finding.NextRepairAction) != "" {
			fmt.Fprintf(out, " next_repair=%q", finding.NextRepairAction)
		}
		fmt.Fprintln(out)
	}
	if printed == 0 {
		fmt.Fprintln(out, "- none")
	}
}

func authorityFindingByID(snapshot core.AuthorityStatusSnapshot, findingID string) (core.AuthorityFindingSnapshot, bool) {
	findingID = strings.TrimSpace(findingID)
	if findingID == "" {
		return core.AuthorityFindingSnapshot{}, false
	}
	for _, finding := range snapshot.Findings {
		if strings.TrimSpace(finding.FindingID) == findingID {
			return finding, true
		}
	}
	return core.AuthorityFindingSnapshot{}, false
}

func authorityRepairActionSupported(action string) bool {
	switch strings.TrimSpace(action) {
	case "expire_continuation_lease",
		"expire_operation_plan_lease",
		"expire_capability_grant",
		"revoke_capability_grant",
		"revoke_tailnet_grant_binding":
		return true
	default:
		return false
	}
}

func applyAuthorityRepairFinding(store *session.SQLiteStore, finding core.AuthorityFindingSnapshot, now time.Time) error {
	if store == nil {
		return fmt.Errorf("authority repair store is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	switch strings.TrimSpace(finding.RepairAction) {
	case "expire_continuation_lease":
		return applyAuthorityRepairExpireContinuationLease(store, finding, now)
	case "expire_operation_plan_lease":
		return applyAuthorityRepairExpireOperationPlanLease(store, finding, now)
	case "expire_capability_grant":
		return applyAuthorityRepairExpireCapabilityGrant(store, finding, now)
	case "revoke_capability_grant":
		return applyAuthorityRepairRevokeCapabilityGrant(store, finding, now)
	case "revoke_tailnet_grant_binding":
		return applyAuthorityRepairRevokeTailnetGrantBinding(store, finding, now)
	default:
		return fmt.Errorf("authority repair action %q is not supported for --apply", strings.TrimSpace(finding.RepairAction))
	}
}

func applyAuthorityRepairExpireContinuationLease(store *session.SQLiteStore, finding core.AuthorityFindingSnapshot, now time.Time) error {
	record, ok, err := authorityContinuationRecordForFinding(store, finding)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("authority repair finding %q no longer maps to a continuation lease", strings.TrimSpace(finding.FindingID))
	}
	state := session.NormalizeContinuationState(record.State)
	state.Status = session.ContinuationStatusIdle
	state.RemainingTurns = 0
	state.ApprovedBy = 0
	state.DecisionID = ""
	if state.ActionProposal.Active() {
		state.ActionProposal.Status = session.ProposalStatusExpired
		state.ActionProposal.UpdatedAt = now
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusExpired
		state.ContinuationLease.RemainingTurns = 0
		state.ContinuationLease.UpdatedAt = now
	}
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusExpired
		state.ApprovalBundle.UpdatedAt = now
	}
	state.UpdatedAt = now
	if err := store.UpdateContinuationState(record.Key, state); err != nil {
		return fmt.Errorf("expire continuation lease for authority repair: %w", err)
	}
	return appendAuthorityRepairExecutionEvent(store, record.Key, core.ExecutionEventRecoveryCompleted, "continuation_lease_expired", finding, now)
}

func applyAuthorityRepairExpireOperationPlanLease(store *session.SQLiteStore, finding core.AuthorityFindingSnapshot, now time.Time) error {
	record, ok, err := authorityOperationRecordForFinding(store, finding)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("authority repair finding %q no longer maps to an operation plan lease", strings.TrimSpace(finding.FindingID))
	}
	state := session.NormalizeOperationState(record.State)
	state.PlanLease.Status = session.PlanLeaseStatusExpired
	state.PlanLease.RemainingTurns = 0
	state.PlanLease.UpdatedAt = now
	state.PlanLease.EvidenceDigest.UpdatedAt = now
	state.Stage = "authority_repair"
	state.Summary = appendAuthorityRepairSummary(state.Summary, "Authority repair expired a stale operation plan lease; request a fresh lease before more work.")
	state.Artifacts = append(state.Artifacts, session.OperationArtifact{
		Label: "Authority repair",
		Ref:   "authority-repair://" + strings.TrimSpace(finding.FindingID) + "/" + now.Format(time.RFC3339Nano),
	})
	state.UpdatedAt = now
	if err := store.UpdateOperationState(record.Key, state); err != nil {
		return fmt.Errorf("expire operation plan lease for authority repair: %w", err)
	}
	return appendAuthorityRepairExecutionEvent(store, record.Key, core.ExecutionEventRecoveryCompleted, "operation_plan_lease_expired", finding, now)
}

func applyAuthorityRepairExpireCapabilityGrant(store *session.SQLiteStore, finding core.AuthorityFindingSnapshot, now time.Time) error {
	grant, ok, err := store.CapabilityGrant(strings.TrimSpace(finding.SourceID))
	if err != nil {
		return fmt.Errorf("load capability grant for authority repair: %w", err)
	}
	if !ok {
		return fmt.Errorf("authority repair finding %q no longer maps to a capability grant", strings.TrimSpace(finding.FindingID))
	}
	grant = session.NormalizeCapabilityGrant(grant)
	grant.Status = session.CapabilityGrantStatusExpired
	grant.UpdatedAt = now
	if _, err := store.UpsertCapabilityGrant(grant); err != nil {
		return fmt.Errorf("expire capability grant for authority repair: %w", err)
	}
	return appendAuthorityRepairExecutionEvent(store, maintenanceRepairKey(), core.ExecutionEventCapabilityGrantChanged, "expired", finding, now)
}

func applyAuthorityRepairRevokeCapabilityGrant(store *session.SQLiteStore, finding core.AuthorityFindingSnapshot, now time.Time) error {
	grant, ok, err := store.CapabilityGrant(strings.TrimSpace(finding.SourceID))
	if err != nil {
		return fmt.Errorf("load capability grant for authority repair: %w", err)
	}
	if !ok {
		return fmt.Errorf("authority repair finding %q no longer maps to a capability grant", strings.TrimSpace(finding.FindingID))
	}
	grant = session.NormalizeCapabilityGrant(grant)
	grant.Status = session.CapabilityGrantStatusRevoked
	if grant.RevokedAt.IsZero() {
		grant.RevokedAt = now
	}
	grant.UpdatedAt = now
	if _, err := store.UpsertCapabilityGrant(grant); err != nil {
		return fmt.Errorf("revoke capability grant for authority repair: %w", err)
	}
	return appendAuthorityRepairExecutionEvent(store, maintenanceRepairKey(), core.ExecutionEventCapabilityGrantChanged, "revoked", finding, now)
}

func applyAuthorityRepairRevokeTailnetGrantBinding(store *session.SQLiteStore, finding core.AuthorityFindingSnapshot, now time.Time) error {
	_, ok, err := store.RevokeTailnetGrantBinding(strings.TrimSpace(finding.SourceID), "authority_repair:"+strings.TrimSpace(finding.Code), now)
	if err != nil {
		return fmt.Errorf("revoke tailnet grant binding for authority repair: %w", err)
	}
	if !ok {
		return fmt.Errorf("authority repair finding %q no longer maps to a tailnet grant binding", strings.TrimSpace(finding.FindingID))
	}
	return appendAuthorityRepairExecutionEvent(store, maintenanceRepairKey(), core.ExecutionEventTailnetGrantChanged, "revoked", finding, now)
}

func authorityContinuationRecordForFinding(store *session.SQLiteStore, finding core.AuthorityFindingSnapshot) (session.ContinuationStateRecord, bool, error) {
	records, err := store.ContinuationStates()
	if err != nil {
		return session.ContinuationStateRecord{}, false, fmt.Errorf("load continuation states for authority repair: %w", err)
	}
	for _, record := range records {
		state := session.NormalizeContinuationState(record.State)
		sessionID := session.SessionIDForKey(record.Key)
		if finding.ChatID != 0 && record.Key.ChatID != finding.ChatID {
			continue
		}
		if strings.TrimSpace(finding.SessionID) != "" && sessionID != strings.TrimSpace(finding.SessionID) {
			continue
		}
		sourceID := strings.TrimSpace(finding.SourceID)
		if sourceID != "" && sourceID != strings.TrimSpace(state.ContinuationLease.ID) && sourceID != sessionID {
			continue
		}
		return record, true, nil
	}
	return session.ContinuationStateRecord{}, false, nil
}

func authorityOperationRecordForFinding(store *session.SQLiteStore, finding core.AuthorityFindingSnapshot) (session.OperationStateRecord, bool, error) {
	records, err := store.OperationStates()
	if err != nil {
		return session.OperationStateRecord{}, false, fmt.Errorf("load operation states for authority repair: %w", err)
	}
	for _, record := range records {
		state := session.NormalizeOperationState(record.State)
		sessionID := session.SessionIDForKey(record.Key)
		if finding.ChatID != 0 && record.Key.ChatID != finding.ChatID {
			continue
		}
		if strings.TrimSpace(finding.SessionID) != "" && sessionID != strings.TrimSpace(finding.SessionID) {
			continue
		}
		sourceID := strings.TrimSpace(finding.SourceID)
		if sourceID != "" && sourceID != strings.TrimSpace(state.PlanLease.ID) && sourceID != strings.TrimSpace(state.ID) && sourceID != sessionID {
			continue
		}
		return record, true, nil
	}
	return session.OperationStateRecord{}, false, nil
}

func appendAuthorityRepairSummary(existing string, addition string) string {
	existing = strings.TrimSpace(existing)
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return existing
	}
	if existing == "" {
		return addition
	}
	if strings.Contains(existing, addition) {
		return existing
	}
	return existing + "\n" + addition
}

func appendAuthorityRepairExecutionEvent(store *session.SQLiteStore, key session.SessionKey, eventType string, status string, finding core.AuthorityFindingSnapshot, now time.Time) error {
	payload := map[string]any{
		"finding_id":     strings.TrimSpace(finding.FindingID),
		"code":           strings.TrimSpace(finding.Code),
		"severity":       strings.TrimSpace(finding.Severity),
		"source_kind":    strings.TrimSpace(finding.SourceKind),
		"source_id":      strings.TrimSpace(finding.SourceID),
		"session_id":     strings.TrimSpace(finding.SessionID),
		"chat_id":        finding.ChatID,
		"repair_action":  strings.TrimSpace(finding.RepairAction),
		"repair_surface": "authority_repair",
	}
	return appendMaintenanceExecutionEvent(store, key, eventType, "authority_repair", strings.TrimSpace(status), payload, now)
}
