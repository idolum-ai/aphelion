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
	surfaces, err := r.store.TailnetSurfaces(session.TailnetSurfaceFilter{Limit: 100})
	if err != nil {
		return nil, err
	}
	return tailnetSurfaceStatusesFromRecords(surfaces), nil
}

func (r *Runtime) attachTailnetSurfaces(snapshot *core.TailnetStatusSnapshot) {
	if r == nil || snapshot == nil || r.store == nil {
		return
	}
	if snapshot.Parent != nil && snapshot.Parent.Enabled {
		record := tailnetSurfaceRecordFromParent(*snapshot.Parent, *snapshot)
		if record.SurfaceID != "" {
			if _, err := r.store.UpsertTailnetSurface(record); err != nil {
				snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
					Code:     "surface_registry_update_failed",
					Severity: "warning",
					Summary:  "Tailnet surface registry could not be updated: " + err.Error(),
				})
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

func firstTailnetSurfaceNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
