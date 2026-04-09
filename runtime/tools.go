//go:build linux

package runtime

import (
	"context"
	"encoding/json"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
)

type principalAwareToolExecutor interface {
	ExecuteForPrincipal(ctx context.Context, p principal.Principal, name string, input json.RawMessage) (string, error)
}

type principalAwareToolSupport interface {
	SupportsPrincipal(p principal.Principal) bool
}

type principalScopedTools struct {
	base      agent.ToolRegistry
	executor  principalAwareToolExecutor
	principal principal.Principal
}

func (p *principalScopedTools) Definitions() []agent.ToolDef {
	return p.base.Definitions()
}

func (p *principalScopedTools) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	return p.executor.ExecuteForPrincipal(ctx, p.principal, name, input)
}

func (r *Runtime) toolsForPrincipal(p principal.Principal) agent.ToolRegistry {
	if r.tools == nil {
		return nil
	}

	executor, hasExecutor := r.tools.(principalAwareToolExecutor)
	support, hasSupport := r.tools.(principalAwareToolSupport)
	principalAwareReady := hasExecutor && hasSupport && support.SupportsPrincipal(p)

	if p.Role == principal.RoleApprovedUser && !principalAwareReady {
		return nil
	}

	if principalAwareReady {
		return &principalScopedTools{
			base:      r.tools,
			executor:  executor,
			principal: p,
		}
	}

	return r.tools
}
