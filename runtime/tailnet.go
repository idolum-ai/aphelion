//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
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
		return snapshot, nil
	}
	if r.tailnetBackend == nil {
		return core.TailnetStatusSnapshot{
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
		}, nil
	}
	snapshot, err := r.tailnetBackend.Snapshot(ctx)
	snapshot.Parent = parent
	return snapshot, err
}

func (r *Runtime) SetTailnetParentStatusProvider(provider func() core.TailnetParentStatus) {
	if r == nil {
		return
	}
	r.tailnetParentStatus = provider
}
