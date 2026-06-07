//go:build linux

package runtime

import (
	"context"
	"encoding/json"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type principalAwareToolExecutor interface {
	ExecuteForPrincipal(ctx context.Context, p principal.Principal, name string, input json.RawMessage) (string, error)
}

type sessionAwareToolExecutor interface {
	ExecuteForSessionPrincipal(ctx context.Context, p principal.Principal, key session.SessionKey, name string, input json.RawMessage) (string, error)
}

type principalAwareToolSupport interface {
	SupportsPrincipal(p principal.Principal) bool
}

type principalAwareToolDefinitions interface {
	DefinitionsForPrincipal(p principal.Principal) []agent.ToolDef
}

type principalAwareToolManifest interface {
	ManifestForPrincipal(p principal.Principal) string
}

type principalScopedTools struct {
	base      agent.ToolRegistry
	executor  principalAwareToolExecutor
	sessioner sessionAwareToolExecutor
	principal principal.Principal
	key       session.SessionKey
}

func (p *principalScopedTools) Definitions() []agent.ToolDef {
	if defsByPrincipal, ok := p.base.(principalAwareToolDefinitions); ok {
		return defsByPrincipal.DefinitionsForPrincipal(p.principal)
	}
	return p.base.Definitions()
}

func (p *principalScopedTools) Manifest() string {
	if manifestByPrincipal, ok := p.base.(principalAwareToolManifest); ok {
		return manifestByPrincipal.ManifestForPrincipal(p.principal)
	}
	if provider, ok := p.base.(toolManifestProvider); ok {
		return provider.Manifest()
	}
	return ""
}

func (p *principalScopedTools) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	if p.sessioner != nil {
		return p.sessioner.ExecuteForSessionPrincipal(ctx, p.principal, p.key, name, input)
	}
	return p.executor.ExecuteForPrincipal(ctx, p.principal, name, input)
}

func (p *principalScopedTools) SupportsParallelToolCall(name string, input json.RawMessage) bool {
	parallelSafe, ok := p.base.(agent.ParallelSafeToolRegistry)
	if !ok {
		return false
	}
	return parallelSafe.SupportsParallelToolCall(name, input)
}

func (r *Runtime) toolsForPrincipal(p principal.Principal, key session.SessionKey) agent.ToolRegistry {
	if r.tools == nil {
		return nil
	}

	executor, hasExecutor := r.tools.(principalAwareToolExecutor)
	sessioner, _ := r.tools.(sessionAwareToolExecutor)
	support, hasSupport := r.tools.(principalAwareToolSupport)
	principalAwareReady := (hasExecutor || sessioner != nil) && hasSupport && support.SupportsPrincipal(p)

	if !principalAwareReady {
		return nil
	}

	return &principalScopedTools{
		base:      r.tools,
		executor:  executor,
		sessioner: sessioner,
		principal: p,
		key:       key,
	}
}
