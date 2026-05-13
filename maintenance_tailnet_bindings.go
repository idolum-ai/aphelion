//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func tailnetBindingFromGrantAndSurface(grant session.CapabilityGrant, surface session.TailnetSurfaceRecord) session.TailnetGrantBinding {
	grant = session.NormalizeCapabilityGrant(grant)
	surface = session.NormalizeTailnetSurfaceRecord(surface)
	desired := map[string]any{
		"schema_version":  "aphelion.tailnet.grant_binding.v1",
		"grant_id":        strings.TrimSpace(grant.GrantID),
		"granted_to":      strings.TrimSpace(grant.GrantedTo),
		"capability_kind": string(grant.Kind),
		"target_resource": strings.TrimSpace(grant.TargetResource),
		"allowed_actions": append([]string(nil), grant.AllowedActions...),
		"surface_id":      strings.TrimSpace(surface.SurfaceID),
		"surface_kind":    strings.TrimSpace(surface.SurfaceKind),
		"hostname":        strings.TrimSpace(surface.Hostname),
		"tags":            append([]string(nil), surface.Tags...),
	}
	raw, _ := json.Marshal(desired)
	return session.TailnetGrantBinding{
		BindingID:         deterministicTailnetBindingID(grant.GrantID, surface.SurfaceID),
		GrantID:           strings.TrimSpace(grant.GrantID),
		SurfaceID:         strings.TrimSpace(surface.SurfaceID),
		GrantedTo:         strings.TrimSpace(grant.GrantedTo),
		CapabilityKind:    string(grant.Kind),
		TargetResource:    strings.TrimSpace(grant.TargetResource),
		DesiredPolicyJSON: string(raw),
		Status:            session.TailnetGrantBindingStatusProposed,
	}
}

func deterministicTailnetBindingID(grantID string, surfaceID string) string {
	return "tailnet-bind-" + safeTailnetBindingToken(grantID) + "-" + safeTailnetBindingToken(surfaceID)
}

func safeTailnetBindingToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unnamed"
	}
	if len(out) > 80 {
		return strings.Trim(out[:80], "-")
	}
	return out
}

func mutateTailnetBinding(configPath string, bindingID string, reason string, mutate func(*session.SQLiteStore) (session.TailnetGrantBinding, bool, error)) (tailnetCommandReport, error) {
	cfg, resolvedPath, err := loadConfigForCommand(configPath)
	if err != nil {
		return tailnetCommandReport{}, err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return tailnetCommandReport{}, fmt.Errorf("open sessions store: %w", err)
	}
	defer func() { _ = store.Close() }()
	stored, ok, err := mutate(store)
	if err != nil {
		return tailnetCommandReport{}, err
	}
	if !ok {
		return tailnetCommandReport{}, fmt.Errorf("tailnet grant binding %q not found", strings.TrimSpace(bindingID))
	}
	if err := appendTailnetGrantMaintenanceEvent(store, stored, stored.Status, reason); err != nil {
		return tailnetCommandReport{}, err
	}
	return tailnetCommandReport{
		Status:          stored.Status,
		ConfigPath:      resolvedPath,
		Enabled:         cfg.Tailscale.Enabled,
		Backend:         cfg.Tailscale.Backend,
		ExpectedTailnet: cfg.Tailscale.ExpectedTailnet,
		BindingID:       stored.BindingID,
		SurfaceID:       stored.SurfaceID,
		Reason:          strings.TrimSpace(reason),
		Bindings:        tailnetGrantBindingReports([]session.TailnetGrantBinding{stored}),
	}, nil
}
