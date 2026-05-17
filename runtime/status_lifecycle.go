//go:build linux

package runtime

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) staleRunningTurnRuns(now time.Time) ([]session.TurnRun, error) {
	if r == nil || r.staleTurnSweep == nil || r.staleTurnThreshold <= 0 {
		return nil, nil
	}
	cutoff := now.Add(-r.staleTurnThreshold)
	limit := r.staleTurnLimit
	if limit <= 0 {
		limit = 50
	}
	return r.staleTurnSweep(cutoff, limit)
}

func (r *Runtime) restartHealthSnapshot() core.RestartHealthSnapshot {
	if r == nil {
		return core.RestartHealthSnapshot{}
	}
	return core.RestartHealthSnapshot{
		WatchdogEnabled:            r.staleTurnWatchdogEnabled,
		WatchdogTriggered:          r.staleWatchdogTriggered.Load(),
		StaleTurnThreshold:         r.staleTurnThreshold,
		StaleTurnLimit:             r.staleTurnLimit,
		WatchdogRestartCooldown:    r.staleTurnRestartCooldown,
		WatchdogMaxRestartAttempts: r.staleTurnMaxRestarts,
		NextWatchdogAttemptAt:      r.staleWatchdogNextAttemptAt(),
	}
}

func cloneActiveTurnMap(in map[int64][]uint64) map[int64][]uint64 {
	out := make(map[int64][]uint64, len(in))
	for chatID, ids := range in {
		if len(ids) == 0 {
			continue
		}
		copied := append([]uint64(nil), ids...)
		sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
		out[chatID] = copied
	}
	return out
}

func cloneQueueDepthMap(in map[int64]int) map[int64]int {
	out := make(map[int64]int, len(in))
	for chatID, depth := range in {
		if depth <= 0 {
			continue
		}
		out[chatID] = depth
	}
	return out
}

func sortedInt64Keys(values map[int64][]uint64) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedInt64KeysFromInt(values map[int64]int) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortPendingItems(items []core.PendingItem) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.ChatID != b.ChatID {
			return a.ChatID < b.ChatID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Summary < b.Summary
	})
}

func attachPendingItemDebugBreadcrumbs(items []core.PendingItem) {
	for i := range items {
		if items[i].DebugBreadcrumb.Active() {
			items[i].DebugBreadcrumb = core.NormalizeDebugBreadcrumb(items[i].DebugBreadcrumb)
			continue
		}
		items[i].DebugBreadcrumb = pendingItemDebugBreadcrumb(items[i])
	}
}

func pendingItemDebugBreadcrumb(item core.PendingItem) core.DebugBreadcrumb {
	id := strings.TrimSpace(item.ID)
	traceID := strings.TrimSpace(string(item.Kind))
	if item.ChatID != 0 {
		traceID += ":" + strconv.FormatInt(item.ChatID, 10)
	}
	if id != "" {
		traceID += ":" + id
	}
	canonical := strings.TrimSpace(item.SourceSurface)
	if canonical == "" {
		canonical = "status.pending_items"
	}
	if item.SourceClass != "" {
		canonical = strings.TrimSpace(item.SourceClass) + ":" + canonical
	}
	inspect := "/health trace"
	if item.ChatID != 0 {
		inspect = "/health trace " + strconv.FormatInt(item.ChatID, 10)
	}
	return core.NormalizeDebugBreadcrumb(core.DebugBreadcrumb{
		TraceID:          traceID,
		CanonicalRecord:  canonical,
		Projection:       "status.pending_items",
		InspectCommand:   inspect,
		CodeOwner:        "runtime/status.go",
		NextRepairAction: pendingItemNextRepairAction(item.Kind),
	})
}

func pendingItemNextRepairAction(kind core.PendingItemKind) string {
	switch kind {
	case core.PendingItemKindDecision:
		return "open the pending decision and approve, reject, or let it expire"
	case core.PendingItemKindContinuation:
		return "inspect continuation state and approve, stop, or refresh the lease"
	case core.PendingItemKindReview:
		return "open the pending review event and deliver or resolve it"
	case core.PendingItemKindRecovery:
		return "inspect recovery events and resume or close the interrupted turn"
	case core.PendingItemKindStaleTurn:
		return "inspect the stale turn and interrupt, recover, or mark terminal"
	case core.PendingItemKindQueue:
		return "inspect router queue depth and drain blocked chat work"
	case core.PendingItemKindMission:
		return "inspect mission ledger and activate, park, or close the candidate"
	default:
		return "inspect the canonical record and choose the next bounded repair"
	}
}

func buildHotChatRollups(snapshot core.SystemStatusSnapshot) []core.ChatStatusRollup {
	rollups := map[int64]*core.ChatStatusRollup{}
	ensure := func(chatID int64) *core.ChatStatusRollup {
		rollup := rollups[chatID]
		if rollup == nil {
			rollup = &core.ChatStatusRollup{ChatID: chatID}
			rollups[chatID] = rollup
		}
		return rollup
	}

	for chatID, ids := range snapshot.ActiveTurnsByChat {
		rollup := ensure(chatID)
		rollup.ActiveTurnCount = len(ids)
	}
	for chatID, depth := range snapshot.QueueDepthByChat {
		rollup := ensure(chatID)
		rollup.QueueDepth = depth
	}
	for _, pending := range snapshot.PendingItems {
		if pending.Kind == core.PendingItemKindMission {
			continue
		}
		rollup := ensure(pending.ChatID)
		rollup.PendingCount++
	}
	for chatID, run := range snapshot.LatestTurnRunsByChat {
		rollup := ensure(chatID)
		rollup.LatestStatus = strings.TrimSpace(run.Status)
		rollup.LastActivityAt = run.LastActivityAt
	}

	out := make([]core.ChatStatusRollup, 0, len(rollups))
	for _, rollup := range rollups {
		out = append(out, *rollup)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if left.PendingCount != right.PendingCount {
			return left.PendingCount > right.PendingCount
		}
		if left.ActiveTurnCount != right.ActiveTurnCount {
			return left.ActiveTurnCount > right.ActiveTurnCount
		}
		if left.QueueDepth != right.QueueDepth {
			return left.QueueDepth > right.QueueDepth
		}
		if !left.LastActivityAt.Equal(right.LastActivityAt) {
			return left.LastActivityAt.After(right.LastActivityAt)
		}
		return left.ChatID < right.ChatID
	})
	return out
}

func (r *Runtime) toolLifecycleStatusSnapshot(limit int) ([]core.ToolLifecycleStatusSnapshot, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	records, err := r.store.ToolInstallRecords("", limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.ToolLifecycleStatusSnapshot, 0, len(records))
	for _, record := range records {
		probe, _, err := r.store.ToolProbeRecord(record.ToolName)
		if err != nil {
			return nil, err
		}
		audit, _, err := r.store.ToolAuditRecord(record.ToolName)
		if err != nil {
			return nil, err
		}
		traceStage := ""
		traceSummary := ""
		traceArtifactCount := 0
		traceUpdatedAt := time.Time{}
		considerTrace := func(stage string, updatedAt time.Time, rationale string, refs []session.RecordReference) {
			rationale = strings.TrimSpace(rationale)
			if updatedAt.IsZero() || rationale == "" {
				return
			}
			if traceUpdatedAt.IsZero() || updatedAt.After(traceUpdatedAt) {
				traceStage = strings.TrimSpace(stage)
				traceSummary = rationale
				traceArtifactCount = len(session.NormalizeRecordReferences(refs))
				traceUpdatedAt = updatedAt
			}
		}
		considerTrace("install", record.UpdatedAt, record.Rationale, record.ArtifactRefs)
		considerTrace("probe", probe.UpdatedAt, probe.Rationale, probe.ArtifactRefs)
		considerTrace("audit", audit.UpdatedAt, audit.Rationale, audit.ArtifactRefs)
		attestationStatus := "unattested"
		switch {
		case strings.TrimSpace(record.StaleReason) != "" || strings.TrimSpace(string(record.DriftSource)) != "" || record.Status == session.ToolInstallStatusStale:
			attestationStatus = "stale"
		case record.Status == session.ToolInstallStatusVerified && !record.AttestedAt.IsZero():
			attestationStatus = "fresh"
		case record.Status == session.ToolInstallStatusVerified:
			attestationStatus = "verified_without_attestation_time"
		}
		out = append(out, core.ToolLifecycleStatusSnapshot{
			ToolName:             record.ToolName,
			InstallStatus:        strings.TrimSpace(string(record.Status)),
			ProbeStatus:          strings.TrimSpace(string(probe.Status)),
			AuditStatus:          strings.TrimSpace(string(audit.Status)),
			InstallRef:           strings.TrimSpace(record.InstallRef),
			BaselineFingerprint:  strings.TrimSpace(record.BaselineFingerprint),
			CurrentFingerprint:   strings.TrimSpace(record.CurrentFingerprint),
			ManifestHash:         strings.TrimSpace(record.CurrentManifestHash),
			WorkspaceFingerprint: strings.TrimSpace(record.CurrentWorkspaceFingerprint),
			DriftSource:          strings.TrimSpace(string(record.DriftSource)),
			StaleReason:          strings.TrimSpace(record.StaleReason),
			AttestationStatus:    attestationStatus,
			InstallFailures:      record.ConsecutiveFailures,
			ProbeFailures:        probe.ConsecutiveFailures,
			AuditFailures:        audit.ConsecutiveFailures,
			TraceStage:           traceStage,
			TraceSummary:         traceSummary,
			TraceArtifactCount:   traceArtifactCount,
			InstalledAt:          record.InstalledAt,
			LastProbedAt:         probe.ProbedAt,
			AuditedAt:            audit.AuditedAt,
			AttestedAt:           record.AttestedAt,
		})
	}
	return out, nil
}

func (r *Runtime) capabilityStatusSnapshot(limit int) ([]core.CapabilityRequestStatusSnapshot, []core.CapabilityGrantStatusSnapshot, error) {
	if r == nil || r.store == nil {
		return nil, nil, nil
	}
	requests, err := r.store.CapabilityRequests(limit, "", "", "")
	if err != nil {
		return nil, nil, err
	}
	grants, err := r.store.CapabilityGrants(limit, "", "", "")
	if err != nil {
		return nil, nil, err
	}
	requestRows := make([]core.CapabilityRequestStatusSnapshot, 0, len(requests))
	for _, request := range requests {
		request = session.NormalizeCapabilityRequest(request)
		requestRows = append(requestRows, core.CapabilityRequestStatusSnapshot{
			RequestID:       request.RequestID,
			Kind:            strings.TrimSpace(string(request.Kind)),
			TargetResource:  strings.TrimSpace(request.TargetResource),
			ReviewStatus:    strings.TrimSpace(string(request.ReviewStatus)),
			RequestedBy:     strings.TrimSpace(request.RequestedBy),
			RequestedFor:    strings.TrimSpace(request.RequestedFor),
			ParentPrincipal: strings.TrimSpace(request.ParentPrincipal),
			AdminPrincipal:  strings.TrimSpace(request.AdminPrincipal),
			RiskClass:       strings.TrimSpace(request.RiskClass),
			Purpose:         strings.TrimSpace(request.Purpose),
			GrantID:         strings.TrimSpace(request.GrantID),
			CreatedAt:       request.CreatedAt,
			UpdatedAt:       request.UpdatedAt,
		})
	}
	grantRows := make([]core.CapabilityGrantStatusSnapshot, 0, len(grants))
	for _, grant := range grants {
		grant = session.NormalizeCapabilityGrant(grant)
		material, materialOK, materialErr := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints)
		materialMissing := ""
		if materialErr != nil {
			materialMissing = "invalid_child_runtime_contract: " + materialErr.Error()
		} else if materialOK {
			materialMissing = firstMissingChildRuntimeMaterial(material)
		}
		grantRows = append(grantRows, core.CapabilityGrantStatusSnapshot{
			GrantID:                grant.GrantID,
			RequestID:              grant.RequestID,
			Kind:                   strings.TrimSpace(string(grant.Kind)),
			TargetResource:         strings.TrimSpace(grant.TargetResource),
			Status:                 strings.TrimSpace(string(grant.Status)),
			GrantedTo:              strings.TrimSpace(grant.GrantedTo),
			GrantedBy:              strings.TrimSpace(grant.GrantedBy),
			AllowedActions:         append([]string{}, grant.AllowedActions...),
			AnchorFingerprint:      strings.TrimSpace(grant.AnchorFingerprint),
			DriftSource:            strings.TrimSpace(string(grant.DriftSource)),
			StaleReason:            strings.TrimSpace(grant.StaleReason),
			ToolInvocationScope:    capabilityGrantToolInvocationScopeSummaryForStatus(grant),
			ChildRuntimePresent:    materialOK,
			RuntimeMaterialMissing: materialMissing,
			InvocationCount:        grant.InvocationCount,
			FailureCount:           grant.FailureCount,
			GrantedAt:              grant.GrantedAt,
			ExpiresAt:              grant.ExpiresAt,
			RevokedAt:              grant.RevokedAt,
			LastInvokedAt:          grant.LastInvokedAt,
		})
	}
	return requestRows, grantRows, nil
}
