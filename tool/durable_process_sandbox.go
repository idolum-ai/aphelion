//go:build linux

package tool

import (
	"errors"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

var ErrSandboxRequired = errors.New("sandbox required for durable-agent process tool execution")

func durableAgentExternalProcessTool(p principal.Principal, manifest ExternalToolManifest) bool {
	if p.Role != principal.RoleDurableAgent {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(manifest.Execution.Mode)) {
	case "process", "subprocess":
		return true
	default:
		return false
	}
}

func (r *Registry) durableAgentProcessSandboxReady(scope sandbox.Scope) bool {
	if r == nil || r.runner == nil {
		return false
	}
	return r.runner.Stage(scope) == sandbox.StageIsolatedBwrap
}

func (r *Registry) requireDurableAgentProcessSandbox(p principal.Principal, manifest ExternalToolManifest, scope sandbox.Scope) error {
	if !durableAgentExternalProcessTool(p, manifest) {
		return nil
	}
	if r.durableAgentProcessSandboxReady(scope) {
		return nil
	}
	return fmt.Errorf("%w: external tool %q requires an isolated sandbox runner", ErrSandboxRequired, strings.TrimSpace(manifest.Name))
}
