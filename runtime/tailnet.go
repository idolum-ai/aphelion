//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
)

func buildTailnetBackend(cfg *config.Config) (tailnet.Backend, error) {
	if cfg == nil || !cfg.Tailscale.Enabled {
		return nil, nil
	}
	backend := strings.ToLower(strings.TrimSpace(cfg.Tailscale.Backend))
	if backend == "" {
		backend = "cli"
	}
	timeout := tailnet.DefaultCommandTimeout
	if raw := strings.TrimSpace(cfg.Tailscale.CommandTimeout); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("parse tailscale.command_timeout: %w", err)
		}
		if parsed > 0 {
			timeout = parsed
		}
	}
	switch backend {
	case "cli":
		return tailnet.NewCLIBackend(tailnet.CLIOptions{
			CLIPath:          cfg.Tailscale.CLIPath,
			CommandTimeout:   timeout,
			ExpectedTailnet:  cfg.Tailscale.ExpectedTailnet,
			ExpectedHostname: cfg.Tailscale.ExpectedHostname,
			ExpectedTags:     cfg.Tailscale.ExpectedTags,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported tailscale backend %q", cfg.Tailscale.Backend)
	}
}

func (r *Runtime) TailnetStatusSnapshot(ctx context.Context) (core.TailnetStatusSnapshot, error) {
	parent := (*core.TailnetParentStatus)(nil)
	if r != nil && r.tailnetParentStatus != nil {
		status := r.tailnetParentStatus()
		parent = &status
	}
	if r == nil || r.cfg == nil || !r.cfg.Tailscale.Enabled {
		snapshot := tailnet.DisabledSnapshot(time.Now().UTC())
		snapshot.Parent = parent
		r.attachTailnetSurfaces(&snapshot)
		return snapshot, nil
	}
	if r.tailnetBackend == nil {
		snapshot := core.TailnetStatusSnapshot{
			GeneratedAt:      time.Now().UTC(),
			Enabled:          true,
			Backend:          strings.TrimSpace(r.cfg.Tailscale.Backend),
			Status:           "degraded",
			Summary:          "Tailscale integration is enabled but no backend is available.",
			ExpectedTailnet:  strings.TrimSpace(r.cfg.Tailscale.ExpectedTailnet),
			ExpectedHostname: strings.TrimSpace(r.cfg.Tailscale.ExpectedHostname),
			ExpectedTags:     append([]string(nil), r.cfg.Tailscale.ExpectedTags...),
			Issues: []core.TailnetIssue{{
				Code:     "backend_unavailable",
				Severity: "error",
				Summary:  "Tailscale backend is unavailable.",
			}},
			Parent: parent,
		}
		r.attachTailnetSurfaces(&snapshot)
		return snapshot, nil
	}
	snapshot, err := r.tailnetBackend.Snapshot(ctx)
	snapshot.Parent = parent
	r.attachTailnetSurfaces(&snapshot)
	return snapshot, err
}

func (r *Runtime) SetTailnetParentStatusProvider(provider func() core.TailnetParentStatus) {
	if r == nil {
		return
	}
	r.tailnetParentStatus = provider
}

func (r *Runtime) TailnetSurfacesSnapshot() ([]core.TailnetSurfaceStatus, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	if err := r.syncDeclaredChildTailnetSurfaces(); err != nil {
		return nil, err
	}
	surfaces, err := r.store.TailnetSurfaces(session.TailnetSurfaceFilter{Limit: 100})
	if err != nil {
		return nil, err
	}
	return tailnetSurfaceStatusesFromRecords(surfaces), nil
}

func (r *Runtime) syncDeclaredChildTailnetSurfaces() error {
	if r == nil || r.store == nil {
		return nil
	}
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return err
	}
	for _, agent := range agents {
		record, ok := tailnetSurfaceRecordFromDurableAgent(agent)
		if !ok {
			continue
		}
		if r.cfg != nil {
			record.TailnetName = strings.TrimSpace(r.cfg.Tailscale.ExpectedTailnet)
		}
		previous, previousOK, err := r.store.TailnetSurface(record.SurfaceID)
		if err != nil {
			return err
		}
		if previousOK {
			switch strings.TrimSpace(previous.Status) {
			case session.TailnetSurfaceStatusActive, session.TailnetSurfaceStatusDegraded, session.TailnetSurfaceStatusRevoked:
				continue
			}
			if !tailnetSurfaceAuditNeeded(previous, true, record) {
				continue
			}
		}
		stored, err := r.store.UpsertTailnetSurface(record)
		if err != nil {
			return err
		}
		if tailnetSurfaceAuditNeeded(previous, previousOK, stored) {
			r.recordTailnetSurfaceAudit("declare", stored, previous, previousOK, "durable_agent_tailnet_policy")
		}
	}
	return nil
}

func (r *Runtime) RevokeTailnetSurface(ctx context.Context, surfaceID string, reason string) (core.TailnetSurfaceStatus, bool, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return core.TailnetSurfaceStatus{}, false, fmt.Errorf("runtime store unavailable")
	}
	surfaceID = strings.TrimSpace(surfaceID)
	if surfaceID == "" {
		return core.TailnetSurfaceStatus{}, false, fmt.Errorf("tailnet surface id is required")
	}
	previous, previousOK, err := r.store.TailnetSurface(surfaceID)
	if err != nil {
		return core.TailnetSurfaceStatus{}, false, err
	}
	revoked, ok, err := r.store.RevokeTailnetSurface(surfaceID, reason, time.Now().UTC())
	if err != nil || !ok {
		return core.TailnetSurfaceStatus{}, ok, err
	}
	r.recordTailnetSurfaceAudit("revoke", revoked, previous, previousOK, strings.TrimSpace(reason))
	return tailnetSurfaceStatusesFromRecords([]session.TailnetSurfaceRecord{revoked})[0], true, nil
}

func (r *Runtime) attachTailnetSurfaces(snapshot *core.TailnetStatusSnapshot) {
	if r == nil || snapshot == nil || r.store == nil {
		return
	}
	if snapshot.Parent != nil && snapshot.Parent.Enabled {
		record := tailnetSurfaceRecordFromParent(*snapshot.Parent, *snapshot)
		if record.SurfaceID != "" {
			previous, previousOK, err := r.store.TailnetSurface(record.SurfaceID)
			if err != nil {
				snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
					Code:     "surface_registry_read_failed",
					Severity: "warning",
					Summary:  "Tailnet surface registry could not be read before update: " + err.Error(),
				})
			} else if previousOK && previous.Status == session.TailnetSurfaceStatusRevoked {
				snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
					Code:     "surface_revoked_but_observed",
					Severity: "warning",
					Summary:  fmt.Sprintf("Tailnet surface %s is revoked in the registry but still observed from the parent tsnet listener.", record.SurfaceID),
				})
			} else if stored, err := r.store.UpsertTailnetSurface(record); err != nil {
				snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
					Code:     "surface_registry_update_failed",
					Severity: "warning",
					Summary:  "Tailnet surface registry could not be updated: " + err.Error(),
				})
			} else if tailnetSurfaceAuditNeeded(previous, previousOK, stored) {
				r.recordTailnetSurfaceAudit("upsert", stored, previous, previousOK, "parent_projection")
			}
		}
	}
	surfaces, err := r.TailnetSurfacesSnapshot()
	if err != nil {
		snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
			Code:     "surface_registry_read_failed",
			Severity: "warning",
			Summary:  "Tailnet surface registry could not be read: " + err.Error(),
		})
		return
	}
	snapshot.Surfaces = surfaces
	bindings, err := r.TailnetGrantBindingsSnapshot()
	if err != nil {
		snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
			Code:     "grant_binding_registry_read_failed",
			Severity: "warning",
			Summary:  "Tailnet grant binding registry could not be read: " + err.Error(),
		})
		return
	}
	snapshot.GrantBindings = bindings
	appendTailnetSurfaceRegistryIssues(snapshot)
	appendTailnetGrantBindingIssues(snapshot)
}

func (r *Runtime) TailnetGrantBindingsSnapshot() ([]core.TailnetGrantBindingStatus, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	bindings, err := r.store.TailnetGrantBindings(session.TailnetGrantBindingFilter{Limit: 100})
	if err != nil {
		return nil, err
	}
	return tailnetGrantBindingStatusesFromRecords(bindings), nil
}

func tailnetSurfaceRecordFromParent(parent core.TailnetParentStatus, snapshot core.TailnetStatusSnapshot) session.TailnetSurfaceRecord {
	status := session.TailnetSurfaceStatusDeclared
	if parent.Running {
		status = session.TailnetSurfaceStatusActive
	} else if strings.TrimSpace(parent.LastError) != "" {
		status = session.TailnetSurfaceStatusDegraded
	}
	url := strings.TrimSpace(parent.MagicDNSURL)
	if url != "" {
		url = strings.TrimRight(url, "/") + "/status"
	}
	now := time.Now().UTC()
	return session.TailnetSurfaceRecord{
		SurfaceID:      "parent:tsnet_http:status",
		OwnerKind:      "parent",
		OwnerID:        "aphelion",
		SurfaceKind:    "tsnet_http",
		Name:           "status",
		Hostname:       strings.TrimSpace(parent.Hostname),
		TailnetName:    firstTailnetSurfaceNonEmpty(snapshot.TailnetName, snapshot.ExpectedTailnet),
		ListenAddr:     strings.TrimSpace(parent.ListenAddr),
		URL:            url,
		Tags:           append([]string(nil), parent.Tags...),
		Status:         status,
		LastError:      strings.TrimSpace(parent.LastError),
		LastObservedAt: now,
		UpdatedAt:      now,
	}
}

func tailnetSurfaceRecordFromDurableAgent(agent core.DurableAgent) (session.TailnetSurfaceRecord, bool) {
	agentID := strings.TrimSpace(agent.AgentID)
	if agentID == "" {
		return session.TailnetSurfaceRecord{}, false
	}
	policy := core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	if strings.TrimSpace(policy.TailnetMode) == "" {
		return session.TailnetSurfaceRecord{}, false
	}
	now := time.Now().UTC()
	return session.TailnetSurfaceRecord{
		SurfaceID:   durableAgentTailnetSurfaceID(agentID),
		OwnerKind:   "durable_agent",
		OwnerID:     agentID,
		SurfaceKind: "tsnet_http",
		Name:        "status",
		Hostname:    durableAgentTailnetHostname(agentID, policy.TailnetHostname),
		Tags:        append([]string(nil), policy.TailnetTags...),
		Status:      session.TailnetSurfaceStatusDeclared,
		DeclaredAt:  now,
		UpdatedAt:   now,
	}, true
}

func durableAgentTailnetSurfaceID(agentID string) string {
	return "durable_agent:" + strings.TrimSpace(agentID) + ":tsnet_http:status"
}

func durableAgentTailnetHostname(agentID string, configured string) string {
	if hostname := strings.ToLower(strings.TrimSpace(configured)); hostname != "" {
		return hostname
	}
	hostname := strings.ToLower(strings.TrimSpace(agentID))
	hostname = strings.ReplaceAll(hostname, "_", "-")
	hostname = strings.ReplaceAll(hostname, ":", "-")
	hostname = strings.ReplaceAll(hostname, " ", "-")
	return hostname
}

func tailnetSurfaceStatusesFromRecords(records []session.TailnetSurfaceRecord) []core.TailnetSurfaceStatus {
	if len(records) == 0 {
		return nil
	}
	out := make([]core.TailnetSurfaceStatus, 0, len(records))
	for _, record := range records {
		out = append(out, core.TailnetSurfaceStatus{
			SurfaceID:      record.SurfaceID,
			OwnerKind:      record.OwnerKind,
			OwnerID:        record.OwnerID,
			SurfaceKind:    record.SurfaceKind,
			Name:           record.Name,
			Hostname:       record.Hostname,
			TailnetName:    record.TailnetName,
			ListenAddr:     record.ListenAddr,
			URL:            record.URL,
			Tags:           append([]string(nil), record.Tags...),
			Status:         record.Status,
			LastError:      record.LastError,
			DeclaredAt:     record.DeclaredAt,
			ActivatedAt:    record.ActivatedAt,
			LastObservedAt: record.LastObservedAt,
			RevokedAt:      record.RevokedAt,
			UpdatedAt:      record.UpdatedAt,
		})
	}
	return out
}

func tailnetGrantBindingStatusesFromRecords(records []session.TailnetGrantBinding) []core.TailnetGrantBindingStatus {
	if len(records) == 0 {
		return nil
	}
	out := make([]core.TailnetGrantBindingStatus, 0, len(records))
	for _, record := range records {
		out = append(out, core.TailnetGrantBindingStatus{
			BindingID:          record.BindingID,
			GrantID:            record.GrantID,
			SurfaceID:          record.SurfaceID,
			GrantedTo:          record.GrantedTo,
			CapabilityKind:     record.CapabilityKind,
			TargetResource:     record.TargetResource,
			DesiredPolicyJSON:  record.DesiredPolicyJSON,
			AppliedPolicyHash:  record.AppliedPolicyHash,
			ObservedPolicyHash: record.ObservedPolicyHash,
			Status:             record.Status,
			DriftReason:        record.DriftReason,
			CreatedAt:          record.CreatedAt,
			UpdatedAt:          record.UpdatedAt,
			AppliedAt:          record.AppliedAt,
			RevokedAt:          record.RevokedAt,
		})
	}
	return out
}

func appendTailnetSurfaceRegistryIssues(snapshot *core.TailnetStatusSnapshot) {
	if snapshot == nil {
		return
	}
	for _, surface := range snapshot.Surfaces {
		switch strings.TrimSpace(surface.Status) {
		case session.TailnetSurfaceStatusDeclared:
			if surface.LastObservedAt.IsZero() {
				snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
					Code:     "surface_declared_not_observed",
					Severity: "warning",
					Summary:  fmt.Sprintf("Tailnet surface %s is declared but has not been observed active yet.", strings.TrimSpace(surface.SurfaceID)),
				})
			}
		}
	}
}

func appendTailnetGrantBindingIssues(snapshot *core.TailnetStatusSnapshot) {
	if snapshot == nil {
		return
	}
	surfaces := make(map[string]core.TailnetSurfaceStatus, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		surfaces[strings.TrimSpace(surface.SurfaceID)] = surface
	}
	for _, binding := range snapshot.GrantBindings {
		status := strings.TrimSpace(binding.Status)
		if status == "" || status == session.TailnetGrantBindingStatusRevoked {
			continue
		}
		surfaceID := strings.TrimSpace(binding.SurfaceID)
		surface, ok := surfaces[surfaceID]
		if !ok {
			snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
				Code:     "grant_binding_surface_missing",
				Severity: "error",
				Summary:  fmt.Sprintf("Tailnet grant binding %s references missing surface %s.", strings.TrimSpace(binding.BindingID), surfaceID),
			})
			continue
		}
		if status == session.TailnetGrantBindingStatusApplied && strings.TrimSpace(surface.Status) != session.TailnetSurfaceStatusActive {
			snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
				Code:     "grant_binding_surface_not_active",
				Severity: "warning",
				Summary:  fmt.Sprintf("Tailnet grant binding %s is applied but surface %s is %s.", strings.TrimSpace(binding.BindingID), surfaceID, firstTailnetSurfaceNonEmpty(surface.Status, "unknown")),
			})
		}
		if status == session.TailnetGrantBindingStatusDrifted {
			snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
				Code:     "grant_binding_drifted",
				Severity: "warning",
				Summary:  fmt.Sprintf("Tailnet grant binding %s is drifted: %s.", strings.TrimSpace(binding.BindingID), firstTailnetSurfaceNonEmpty(binding.DriftReason, "policy evidence diverged")),
			})
		}
		if strings.TrimSpace(binding.AppliedPolicyHash) != "" &&
			strings.TrimSpace(binding.ObservedPolicyHash) != "" &&
			strings.TrimSpace(binding.AppliedPolicyHash) != strings.TrimSpace(binding.ObservedPolicyHash) {
			snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
				Code:     "grant_binding_policy_hash_mismatch",
				Severity: "warning",
				Summary:  fmt.Sprintf("Tailnet grant binding %s observed policy hash differs from the applied hash.", strings.TrimSpace(binding.BindingID)),
			})
		}
	}
}

func tailnetSurfaceAuditNeeded(previous session.TailnetSurfaceRecord, previousOK bool, next session.TailnetSurfaceRecord) bool {
	if !previousOK {
		return true
	}
	return strings.TrimSpace(previous.Status) != strings.TrimSpace(next.Status) ||
		strings.TrimSpace(previous.URL) != strings.TrimSpace(next.URL) ||
		strings.TrimSpace(previous.ListenAddr) != strings.TrimSpace(next.ListenAddr) ||
		strings.TrimSpace(previous.Hostname) != strings.TrimSpace(next.Hostname) ||
		strings.TrimSpace(previous.TailnetName) != strings.TrimSpace(next.TailnetName) ||
		strings.TrimSpace(previous.LastError) != strings.TrimSpace(next.LastError) ||
		strings.Join(session.NormalizeTailnetSurfaceRecord(previous).Tags, ",") != strings.Join(session.NormalizeTailnetSurfaceRecord(next).Tags, ",")
}

func (r *Runtime) recordTailnetSurfaceAudit(action string, next session.TailnetSurfaceRecord, previous session.TailnetSurfaceRecord, previousOK bool, reason string) {
	if r == nil {
		return
	}
	payload := map[string]any{
		"action":       strings.TrimSpace(action),
		"surface_id":   strings.TrimSpace(next.SurfaceID),
		"owner_kind":   strings.TrimSpace(next.OwnerKind),
		"owner_id":     strings.TrimSpace(next.OwnerID),
		"surface_kind": strings.TrimSpace(next.SurfaceKind),
		"name":         strings.TrimSpace(next.Name),
		"status":       strings.TrimSpace(next.Status),
		"url":          strings.TrimSpace(next.URL),
		"reason":       strings.TrimSpace(reason),
	}
	if previousOK {
		payload["previous_status"] = strings.TrimSpace(previous.Status)
		payload["previous_url"] = strings.TrimSpace(previous.URL)
	}
	if errText := strings.TrimSpace(next.LastError); errText != "" {
		payload["last_error"] = errText
	}
	r.recordExecutionEvent(
		session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()},
		core.ExecutionEventTailnetSurfaceChanged,
		"tailnet",
		strings.TrimSpace(next.Status),
		payload,
		time.Now().UTC(),
	)
}

func firstTailnetSurfaceNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
