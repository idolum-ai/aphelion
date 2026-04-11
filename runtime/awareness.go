//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/prompt"
	providerpkg "github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type providerStateReporter interface {
	RuntimeState() providerpkg.RuntimeState
}

type governorExecution struct {
	Provider      agent.Provider
	Backend       string
	ProviderName  string
	ModelName     string
	ProviderPath  []string
	MediaAttached bool
	MediaMode     string
}

func (r *Runtime) governorRuntimeAwareness(scope sandbox.Scope, kind session.TurnRunKind, channel string, exec governorExecution) prompt.RuntimeAwareness {
	if strings.TrimSpace(exec.Backend) == "" {
		exec.Backend = strings.TrimSpace(r.governorBackend)
	}
	if exec.Provider == nil {
		exec.Provider = r.provider
	}
	if strings.TrimSpace(exec.ProviderName) == "" {
		exec.ProviderName = r.governorProviderName()
	}
	if strings.TrimSpace(exec.ModelName) == "" {
		exec.ModelName = r.governorModelName()
	}
	if len(exec.ProviderPath) == 0 {
		exec.ProviderPath = r.configuredGovernorProviderPath()
	}
	reasoning := r.reasoningOptionsForRun(kind)
	snapshot := r.currentRecipeSnapshot()
	aw := prompt.RuntimeAwareness{
		SessionKind:          sessionKindForRun(kind),
		RunKind:              string(kind),
		Channel:              strings.TrimSpace(channel),
		GovernorBackend:      strings.TrimSpace(exec.Backend),
		GovernorProvider:     strings.TrimSpace(exec.ProviderName),
		GovernorModel:        strings.TrimSpace(exec.ModelName),
		GovernorProviderPath: append([]string(nil), exec.ProviderPath...),
		ReasoningEffort:      string(reasoning.Reasoning.Effort),
		ReasoningSummary:     string(reasoning.Reasoning.Summary),
		GovernorEffortRecipe: snapshot.GovernorEffort,
		FaceBackend:          string(r.faceBackend),
		FaceProvider:         r.faceProviderName(),
		FaceModel:            r.faceModelName(),
		PersonaEffortRecipe:  snapshot.PersonaEffort,
		MediaAttached:        exec.MediaAttached,
		MediaMode:            strings.TrimSpace(exec.MediaMode),
		PromptRoot:           strings.TrimSpace(r.cfg.Agent.PromptRoot),
		ExecRoot:             strings.TrimSpace(r.cfg.Agent.ExecRoot),
		SharedMemoryRoot:     strings.TrimSpace(scope.SharedMemoryRoot),
		UserWorkspaceRoot:    strings.TrimSpace(scope.UserWorkspace),
		UserMemoryRoot:       strings.TrimSpace(scope.UserMemory),
		WorkingRoot:          strings.TrimSpace(scope.WorkingRoot),
		SandboxMode:          string(scope.Profile.Mode),
		NetworkPolicy:        string(scope.Profile.Network),
	}
	if strings.TrimSpace(aw.Channel) == "" {
		aw.Channel = "system"
	}
	if state, ok := currentProviderRuntimeState(exec.Provider); ok {
		aw.ActiveProvider = strings.TrimSpace(state.ActiveProvider)
		aw.FallbackActive = state.FallbackActive
	}
	if strings.TrimSpace(aw.ActiveProvider) == "" {
		aw.ActiveProvider = aw.GovernorProvider
	}
	return aw
}

func sessionKindForRun(kind session.TurnRunKind) string {
	switch kind {
	case session.TurnRunKindHeartbeat, session.TurnRunKindCron, session.TurnRunKindRecovery:
		return "system"
	default:
		return "interactive"
	}
}

func currentProviderRuntimeState(p agent.Provider) (providerpkg.RuntimeState, bool) {
	reporter, ok := p.(providerStateReporter)
	if !ok {
		return providerpkg.RuntimeState{}, false
	}
	return reporter.RuntimeState(), true
}

func (r *Runtime) configuredGovernorProviderPath() []string {
	if r == nil || r.cfg == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(r.governorBackend)) {
	case "codex":
		path := []string{"codex"}
		path = append(path, r.configuredNativeProviderPath()...)
		return path
	default:
		return r.configuredNativeProviderPath()
	}
}

func (r *Runtime) configuredNativeProviderPath() []string {
	if r == nil || r.cfg == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 1+len(r.cfg.Providers.FallbackChain))
	for _, raw := range append([]string{r.nativeProviderName()}, r.cfg.Providers.FallbackChain...) {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func (r *Runtime) nativeProviderName() string {
	if r == nil || r.cfg == nil {
		return ""
	}
	if name := strings.ToLower(strings.TrimSpace(r.cfg.Governor.NativeProvider)); name != "" {
		return name
	}
	if name := strings.ToLower(strings.TrimSpace(r.cfg.Providers.Default)); name != "" {
		return name
	}
	return "anthropic"
}

func (r *Runtime) governorProviderName() string {
	switch strings.ToLower(strings.TrimSpace(r.governorBackend)) {
	case "codex":
		return "codex"
	default:
		return r.nativeProviderName()
	}
}

func (r *Runtime) governorModelName() string {
	if r == nil || r.cfg == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(r.governorBackend)) {
	case "codex":
		return "codex"
	default:
		return r.nativeModelName()
	}
}

func (r *Runtime) nativeModelName() string {
	if r == nil || r.cfg == nil {
		return ""
	}
	switch r.nativeProviderName() {
	case "openrouter":
		return strings.TrimSpace(r.cfg.Providers.OpenRouter.Model)
	default:
		return strings.TrimSpace(r.cfg.Providers.Anthropic.Model)
	}
}

func (r *Runtime) faceProviderName() string {
	switch r.faceBackend {
	case face.BackendFloorFallback:
		if state, ok := currentProviderRuntimeState(r.provider); ok && strings.TrimSpace(state.ActiveProvider) != "" {
			return strings.TrimSpace(state.ActiveProvider)
		}
		return r.governorProviderName()
	default:
		return r.nativeProviderName()
	}
}
