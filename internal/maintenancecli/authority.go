//go:build linux

package maintenancecli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type AuthorityDeps struct {
	SnapshotFromStore func(*session.SQLiteStore, time.Time) (core.AuthorityStatusSnapshot, error)
}

func RunAuthorityCommand(args []string, deps AuthorityDeps) error {
	if commandGroupHelpRequested(args) {
		printCommandGroupHelp("authority", []string{"doctor", "repair", "revoke-grant", "revoke-continuation"})
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: authority <doctor|repair|revoke-grant|revoke-continuation> [--config path]")
	}
	switch strings.TrimSpace(args[0]) {
	case "doctor":
		return runAuthorityDoctorCommand(args[1:], deps)
	case "repair":
		return runAuthorityRepairCommand(args[1:], deps)
	case "revoke-grant":
		return runAuthorityRevokeGrantCommand(args[1:])
	case "revoke-continuation":
		return runAuthorityRevokeContinuationCommand(args[1:])
	default:
		return fmt.Errorf("unknown authority command %q (known: doctor|repair|revoke-grant|revoke-continuation)", args[0])
	}
}

func runAuthorityDoctorCommand(args []string, deps AuthorityDeps) error {
	fs := flag.NewFlagSet("authority doctor", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	limitFlag := fs.Int("limit", 50, "maximum findings to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	snapshot, configPath, err := authoritySnapshotForCommand(*configFlag, deps)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "action: authority-doctor")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	writeAuthoritySnapshot(os.Stdout, snapshot, *limitFlag, false)
	return nil
}

func runAuthorityRepairCommand(args []string, deps AuthorityDeps) error {
	fs := flag.NewFlagSet("authority repair", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	limitFlag := fs.Int("limit", 50, "maximum repair previews to print")
	applyFlag := fs.Bool("apply", false, "apply supported repairs")
	findingFlag := fs.String("finding", "", "exact authority finding id to repair")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *applyFlag {
		return runAuthorityRepairApplyCommand(*configFlag, *findingFlag, deps)
	}
	snapshot, configPath, err := authoritySnapshotForCommand(*configFlag, deps)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "action: authority-repair")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintln(os.Stdout, "dry_run: true")
	writeAuthoritySnapshot(os.Stdout, snapshot, *limitFlag, true)
	return nil
}

func runAuthorityRevokeGrantCommand(args []string) error {
	fs := flag.NewFlagSet("authority revoke-grant", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	grantIDFlag := fs.String("grant-id", "", "capability grant id to revoke")
	reasonFlag := fs.String("reason", "", "operator-visible reason recorded in authority evidence")
	applyFlag := fs.Bool("apply", false, "apply the revocation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*applyFlag {
		return fmt.Errorf("authority revoke-grant requires --apply to mutate authority state")
	}
	grantID := strings.TrimSpace(*grantIDFlag)
	reason := strings.TrimSpace(*reasonFlag)
	if grantID == "" {
		return fmt.Errorf("authority revoke-grant requires --grant-id")
	}
	if reason == "" {
		return fmt.Errorf("authority revoke-grant requires --reason")
	}
	store, configPath, closeStore, err := authorityStoreForCommand(*configFlag)
	if err != nil {
		return err
	}
	defer closeStore()
	grant, ok, err := store.CapabilityGrant(grantID)
	if err != nil {
		return fmt.Errorf("load capability grant for revoke: %w", err)
	}
	if !ok {
		return fmt.Errorf("capability grant %q not found", grantID)
	}
	grant = session.NormalizeCapabilityGrant(grant)
	priorStatus := grant.Status
	now := time.Now().UTC()
	changed := priorStatus != session.CapabilityGrantStatusRevoked
	if changed {
		grant.Status = session.CapabilityGrantStatusRevoked
		if grant.RevokedAt.IsZero() {
			grant.RevokedAt = now
		}
		grant.UpdatedAt = now
		if _, err := store.UpsertCapabilityGrant(grant); err != nil {
			return fmt.Errorf("revoke capability grant: %w", err)
		}
		if err := appendMaintenanceExecutionEvent(store, maintenanceRepairKey(), core.ExecutionEventCapabilityGrantChanged, "authority_maintenance", "revoked", map[string]any{
			"grant_id":        grant.GrantID,
			"request_id":      grant.RequestID,
			"granted_to":      grant.GrantedTo,
			"kind":            string(grant.Kind),
			"target_resource": grant.TargetResource,
			"prior_status":    string(priorStatus),
			"status":          string(grant.Status),
			"reason":          reason,
			"operator_action": "authority revoke-grant",
		}, now); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stdout, "action: authority-revoke-grant")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintln(os.Stdout, "applied: true")
	fmt.Fprintf(os.Stdout, "grant_id: %s\n", grant.GrantID)
	fmt.Fprintf(os.Stdout, "prior_status: %s\n", priorStatus)
	fmt.Fprintf(os.Stdout, "status: %s\n", grant.Status)
	fmt.Fprintf(os.Stdout, "changed: %t\n", changed)
	return nil
}

func runAuthorityRevokeContinuationCommand(args []string) error {
	fs := flag.NewFlagSet("authority revoke-continuation", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	chatIDFlag := fs.Int64("chat-id", 0, "Telegram chat id whose continuation should be revoked")
	proposalIDFlag := fs.String("proposal-id", "", "optional proposal id guard")
	reasonFlag := fs.String("reason", "", "operator-visible reason recorded in authority evidence")
	applyFlag := fs.Bool("apply", false, "apply the revocation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*applyFlag {
		return fmt.Errorf("authority revoke-continuation requires --apply to mutate authority state")
	}
	if *chatIDFlag == 0 {
		return fmt.Errorf("authority revoke-continuation requires --chat-id")
	}
	reason := strings.TrimSpace(*reasonFlag)
	if reason == "" {
		return fmt.Errorf("authority revoke-continuation requires --reason")
	}
	store, configPath, closeStore, err := authorityStoreForCommand(*configFlag)
	if err != nil {
		return err
	}
	defer closeStore()
	key := authorityMaintenanceTelegramDMKey(*chatIDFlag)
	state, exists, err := store.ContinuationStateIfExists(key)
	if err != nil {
		return fmt.Errorf("load continuation for revoke: %w", err)
	}
	if !exists {
		return fmt.Errorf("continuation for chat_id %d not found", *chatIDFlag)
	}
	state = session.NormalizeContinuationState(state)
	if guard := strings.TrimSpace(*proposalIDFlag); guard != "" && guard != strings.TrimSpace(state.ActionProposal.ID) {
		return fmt.Errorf("continuation proposal guard %q does not match current proposal %q", guard, strings.TrimSpace(state.ActionProposal.ID))
	}
	priorStatus := state.Status
	priorProposalStatus := state.ActionProposal.Status
	priorLeaseStatus := state.ContinuationLease.Status
	now := time.Now().UTC()
	repaired, changed := authorityMaintenanceRevokeContinuationState(state, reason, now)
	if changed {
		if err := store.UpdateContinuationState(key, repaired); err != nil {
			return fmt.Errorf("revoke continuation: %w", err)
		}
		if err := appendMaintenanceExecutionEvent(store, key, core.ExecutionEventContinuationRevoked, "authority_maintenance", "revoked", map[string]any{
			"chat_id":               *chatIDFlag,
			"session_id":            session.SessionIDForKey(key),
			"decision_id":           state.DecisionID,
			"proposal_id":           state.ActionProposal.ID,
			"lease_id":              state.ContinuationLease.ID,
			"prior_status":          string(priorStatus),
			"prior_proposal_status": string(priorProposalStatus),
			"prior_lease_status":    string(priorLeaseStatus),
			"status":                string(repaired.Status),
			"reason":                reason,
			"operator_action":       "authority revoke-continuation",
		}, now); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stdout, "action: authority-revoke-continuation")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintln(os.Stdout, "applied: true")
	fmt.Fprintf(os.Stdout, "chat_id: %d\n", *chatIDFlag)
	fmt.Fprintf(os.Stdout, "session_id: %s\n", session.SessionIDForKey(key))
	fmt.Fprintf(os.Stdout, "proposal_id: %s\n", strings.TrimSpace(state.ActionProposal.ID))
	fmt.Fprintf(os.Stdout, "prior_status: %s\n", priorStatus)
	fmt.Fprintf(os.Stdout, "status: %s\n", repaired.Status)
	fmt.Fprintf(os.Stdout, "changed: %t\n", changed)
	return nil
}

func authoritySnapshotForCommand(configPathFlag string, deps AuthorityDeps) (core.AuthorityStatusSnapshot, string, error) {
	store, configPath, closeStore, err := authorityStoreForCommand(configPathFlag)
	if err != nil {
		return core.AuthorityStatusSnapshot{}, "", err
	}
	defer closeStore()
	snapshot, err := authoritySnapshotFromStore(store, deps)
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

func authoritySnapshotFromStore(store *session.SQLiteStore, deps AuthorityDeps) (core.AuthorityStatusSnapshot, error) {
	if deps.SnapshotFromStore == nil {
		return core.AuthorityStatusSnapshot{}, fmt.Errorf("authority snapshot dependency is unavailable")
	}
	snapshot, err := deps.SnapshotFromStore(store, time.Now().UTC())
	if err != nil {
		return core.AuthorityStatusSnapshot{}, err
	}
	return snapshot, nil
}

func runAuthorityRepairApplyCommand(configPathFlag string, findingID string, deps AuthorityDeps) error {
	findingID = strings.TrimSpace(findingID)
	if findingID == "" {
		return fmt.Errorf("authority repair --apply requires --finding <finding_id> from a fresh authority repair preview")
	}
	store, configPath, closeStore, err := authorityStoreForCommand(configPathFlag)
	if err != nil {
		return err
	}
	defer closeStore()
	before, err := authoritySnapshotFromStore(store, deps)
	if err != nil {
		return err
	}
	finding, ok := authorityFindingByID(before, findingID)
	if !ok {
		return fmt.Errorf("authority repair finding %q is not present; rerun authority repair and apply a current finding_id", findingID)
	}
	if !finding.Applicable || strings.TrimSpace(finding.ApplyAction) == "" {
		return fmt.Errorf("authority repair finding %q has no apply_action; suggested_repair=%q", findingID, strings.TrimSpace(finding.SuggestedRepair))
	}
	if !authorityRepairActionSupported(finding.ApplyAction) {
		return fmt.Errorf("authority repair apply_action %q for finding %q is not supported for --apply", strings.TrimSpace(finding.ApplyAction), findingID)
	}
	now := time.Now().UTC()
	if err := applyAuthorityRepairFinding(store, finding, now); err != nil {
		return err
	}
	after, err := authoritySnapshotFromStore(store, deps)
	if err != nil {
		return err
	}
	if _, stillPresent := authorityFindingByID(after, findingID); stillPresent {
		return fmt.Errorf("authority repair finding %q was not closed by %s", findingID, strings.TrimSpace(finding.ApplyAction))
	}
	fmt.Fprintln(os.Stdout, "action: authority-repair")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintln(os.Stdout, "dry_run: false")
	fmt.Fprintln(os.Stdout, "applied: true")
	fmt.Fprintf(os.Stdout, "finding_id: %s\n", findingID)
	fmt.Fprintf(os.Stdout, "apply_action: %s\n", strings.TrimSpace(finding.ApplyAction))
	if strings.TrimSpace(finding.ApplyScope) != "" {
		fmt.Fprintf(os.Stdout, "apply_scope: %s\n", strings.TrimSpace(finding.ApplyScope))
	}
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
	applicable := 0
	for _, finding := range snapshot.Findings {
		if finding.Applicable {
			applicable++
		}
	}
	if repairOnly {
		fmt.Fprintf(out, "applicable: %d\n", applicable)
	}
	for _, finding := range snapshot.Findings {
		if repairOnly && strings.TrimSpace(finding.SuggestedRepair) == "" && strings.TrimSpace(finding.ApplyAction) == "" {
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
		if strings.TrimSpace(finding.SuggestedRepair) != "" {
			fmt.Fprintf(out, " suggested_repair=%q", finding.SuggestedRepair)
		}
		if strings.TrimSpace(finding.ApplyAction) != "" {
			fmt.Fprintf(out, " apply_action=%s", finding.ApplyAction)
		}
		if strings.TrimSpace(finding.ApplyScope) != "" {
			fmt.Fprintf(out, " apply_scope=%s", finding.ApplyScope)
		}
		if finding.Applicable {
			fmt.Fprint(out, " applicable=true")
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

func authorityFindingByCodeAndSource(snapshot core.AuthorityStatusSnapshot, code string, sourceKind string, sourceID string) (core.AuthorityFindingSnapshot, bool) {
	code = strings.TrimSpace(code)
	sourceKind = strings.TrimSpace(sourceKind)
	sourceID = strings.TrimSpace(sourceID)
	if code == "" || sourceKind == "" || sourceID == "" {
		return core.AuthorityFindingSnapshot{}, false
	}
	for _, finding := range snapshot.Findings {
		if strings.TrimSpace(finding.Code) == code &&
			strings.TrimSpace(finding.SourceKind) == sourceKind &&
			strings.TrimSpace(finding.SourceID) == sourceID {
			return finding, true
		}
	}
	return core.AuthorityFindingSnapshot{}, false
}

func authorityMaintenanceTelegramDMKey(chatID int64) session.SessionKey {
	id := fmt.Sprintf("%d", chatID)
	return session.SessionKey{
		ChatID: chatID,
		UserID: 0,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: id},
	}
}

func authorityMaintenanceRevokeContinuationState(prior session.ContinuationState, reason string, now time.Time) (session.ContinuationState, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Authority maintenance revoked a stale continuation; request a fresh proposal if this work is still needed."
	}
	state := session.NormalizeContinuationState(prior)
	changed := false
	switch state.Status {
	case session.ContinuationStatusPending, session.ContinuationStatusApproved:
		state.Status = session.ContinuationStatusRevoked
		changed = true
	}
	if state.RemainingTurns != 0 {
		state.RemainingTurns = 0
		changed = true
	}
	if state.ApprovedBy != 0 {
		state.ApprovedBy = 0
		changed = true
	}
	if strings.TrimSpace(state.DecisionID) != "" {
		state.DecisionID = ""
		changed = true
	}
	if state.ActionProposal.Active() && state.ActionProposal.Status != session.ProposalStatusApproved && state.ActionProposal.Status != session.ProposalStatusDenied {
		state.ActionProposal.Status = session.ProposalStatusDenied
		state.ActionProposal.WhyNow = reason
		state.ActionProposal.UpdatedAt = now
		changed = true
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		if state.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked {
			state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
			changed = true
		}
		if state.ContinuationLease.RemainingTurns != 0 {
			state.ContinuationLease.RemainingTurns = 0
			changed = true
		}
		if state.ContinuationLease.RevokedAt.IsZero() {
			state.ContinuationLease.RevokedAt = now
			changed = true
		}
		state.ContinuationLease.UpdatedAt = now
	}
	if state.ApprovalBundle.Active() {
		if state.ApprovalBundle.Status != session.ContinuationLeaseStatusRevoked {
			state.ApprovalBundle.Status = session.ContinuationLeaseStatusRevoked
			changed = true
		}
		if state.ApprovalBundle.RevokedAt.IsZero() {
			state.ApprovalBundle.RevokedAt = now
			changed = true
		}
		state.ApprovalBundle.UpdatedAt = now
	}
	if state.HandshakeBlockedReason != reason {
		state.HandshakeBlockedReason = reason
		changed = true
	}
	state.ParkedAt = now
	state.ParkedSource = "authority_maintenance"
	state.ParkedReason = reason
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state), changed
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
	switch strings.TrimSpace(finding.ApplyAction) {
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
		return fmt.Errorf("authority repair apply_action %q is not supported for --apply", strings.TrimSpace(finding.ApplyAction))
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
		"apply_action":   strings.TrimSpace(finding.ApplyAction),
		"apply_scope":    strings.TrimSpace(finding.ApplyScope),
		"repair_surface": "authority_repair",
	}
	return appendMaintenanceExecutionEvent(store, key, eventType, "authority_repair", strings.TrimSpace(status), payload, now)
}
