//go:build linux

package tool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/interpretation"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type Registry struct {
	workspace                       string
	timeout                         time.Duration
	maxOutputBytes                  int
	execApprover                    ExecApprover
	durableMemoryDelegationApprover DurableMemoryDelegationApprover
	durableSnapshotRestoreApprover  DurableSnapshotRestoreApprover
	sandbox                         *sandbox.Resolver
	runner                          *sandbox.Runner
	store                           *session.SQLiteStore
	interpret                       *interpretation.Service
	fileStore                       memstore.FileStore
	filePurpose                     string
	retrievalStore                  memstore.RetrievalStore
	defaultStore                    string
	nativeFetchUserAgent            string
	nativeFetchTransport            http.RoundTripper
	nativeFetchResolver             sandbox.NetworkResolver
	nativeFetchDialContext          nativeFetchDialContext
	semantic                        *memstore.SemanticEngine
	durableAgentBootstrapLLM        core.NodeLLMBootstrap
	externalManifests               []ExternalToolManifest
	externalExecutor                ExternalToolExecutor
	codexImageGenerationProvider    agent.Provider
	webSearchOptions                WebSearchOptions
	webSearchProviders              []WebSearchProvider
	remoteHostRunner                tailnet.OpenSSHRunner
	durableAgentPrincipalFallback   bool
	capabilityGrantObserver         func(context.Context, session.SessionKey, session.CapabilityGrant)
	configuredVisibility            ConfiguredCapabilityVisibilityOptions
}

func NewRegistry(workspace string, timeout time.Duration) *Registry {
	return &Registry{
		workspace:            workspace,
		timeout:              timeout,
		maxOutputBytes:       defaultMaxOutputBytes,
		nativeFetchUserAgent: DefaultNativeFetchUserAgent,
		externalExecutor:     defaultExternalToolExecutor{},
		webSearchOptions:     NormalizeWebSearchOptions(WebSearchOptions{}),
	}
}

func NewRegistryWithSandbox(workspace string, timeout time.Duration, resolver *sandbox.Resolver) *Registry {
	registry := NewRegistry(workspace, timeout)
	registry.sandbox = resolver
	registry.runner = sandbox.NewRunner()
	return registry
}

func (r *Registry) WithRunner(runner *sandbox.Runner) *Registry {
	r.runner = runner
	return r
}

func (r *Registry) WithUserAgent(userAgent string) *Registry {
	if r != nil {
		r.nativeFetchUserAgent = strings.TrimSpace(userAgent)
	}
	return r
}

func (r *Registry) WithSessionStore(store *session.SQLiteStore) *Registry {
	r.store = store
	service := interpretation.NewService(store)
	r.interpret = &service
	return r
}

func (r *Registry) WithInterpretationService(service interpretation.Service) *Registry {
	if r != nil {
		r.interpret = &service
	}
	return r
}

func (r *Registry) interpretationService() interpretation.Service {
	if r == nil {
		return interpretation.Service{}
	}
	if r.interpret != nil {
		return *r.interpret
	}
	return interpretation.NewService(r.store)
}

func (r *Registry) WithCapabilityGrantObserver(observer func(context.Context, session.SessionKey, session.CapabilityGrant)) *Registry {
	if r != nil {
		r.capabilityGrantObserver = observer
	}
	return r
}

func (r *Registry) notifyCapabilityGrantObserver(key session.SessionKey, grant session.CapabilityGrant) {
	if r == nil || r.capabilityGrantObserver == nil {
		return
	}
	observer := r.capabilityGrantObserver
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), capabilityGrantObserverTimeout)
		defer cancel()
		observer(ctx, key, grant)
	}()
}

func (r *Registry) WithCodexImageGenerationProvider(provider agent.Provider) *Registry {
	r.SetCodexImageGenerationProvider(provider)
	return r
}

func (r *Registry) WithRemoteHostSSH(sshPath string, timeout time.Duration) *Registry {
	if r == nil {
		return r
	}
	r.remoteHostRunner = tailnet.NewOpenSSHClient(tailnet.OpenSSHOptions{
		SSHPath:        sshPath,
		CommandTimeout: timeout,
	})
	return r
}

func (r *Registry) WithRemoteHostRunner(runner tailnet.OpenSSHRunner) *Registry {
	if r != nil {
		r.remoteHostRunner = runner
	}
	return r
}

func (r *Registry) SetCodexImageGenerationProvider(provider agent.Provider) {
	if r == nil {
		return
	}
	r.codexImageGenerationProvider = provider
}

func (r *Registry) WithDurableAgentPrincipalFallback() *Registry {
	if r != nil {
		r.durableAgentPrincipalFallback = true
	}
	return r
}

func (r *Registry) WithExecApprover(approver ExecApprover) *Registry {
	r.execApprover = approver
	return r
}

func (r *Registry) WithDurableMemoryDelegationApprover(approver DurableMemoryDelegationApprover) *Registry {
	r.durableMemoryDelegationApprover = approver
	return r
}

func (r *Registry) WithDurableSnapshotRestoreApprover(approver DurableSnapshotRestoreApprover) *Registry {
	r.durableSnapshotRestoreApprover = approver
	return r
}

func (r *Registry) WithFileStore(store memstore.FileStore, purpose string) *Registry {
	r.fileStore = store
	r.filePurpose = strings.TrimSpace(purpose)
	return r
}

func (r *Registry) WithRetrievalStore(store memstore.RetrievalStore, defaultStore string) *Registry {
	r.retrievalStore = store
	r.defaultStore = strings.TrimSpace(defaultStore)
	return r
}

func (r *Registry) WithSemanticEngine(engine *memstore.SemanticEngine) *Registry {
	r.semantic = engine
	return r
}

func (r *Registry) WithDurableAgentBootstrapLLM(bootstrap core.NodeLLMBootstrap) *Registry {
	r.durableAgentBootstrapLLM = core.NormalizeNodeLLMBootstrap(bootstrap)
	return r
}

func (r *Registry) WithExternalToolExecutor(executor ExternalToolExecutor) *Registry {
	r.externalExecutor = executor
	return r
}

func (r *Registry) WithExternalToolManifestDir(dir string) (*Registry, error) {
	manifests, err := LoadExternalToolManifestDir(dir)
	if err != nil {
		return nil, err
	}
	return r.WithExternalToolManifests(manifests)
}

func (r *Registry) WithExternalToolManifests(manifests []ExternalToolManifest) (*Registry, error) {
	if r == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	normalized := make([]ExternalToolManifest, 0, len(manifests))
	seen := make(map[string]struct{}, len(manifests))
	for _, manifest := range manifests {
		manifest = NormalizeExternalToolManifest(manifest)
		if err := validateExternalToolManifest(manifest); err != nil {
			return nil, err
		}
		if _, exists := seen[manifest.Name]; exists {
			return nil, fmt.Errorf("duplicate external tool manifest name %q", manifest.Name)
		}
		seen[manifest.Name] = struct{}{}
		normalized = append(normalized, manifest)
	}
	for _, def := range r.Definitions() {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		for _, manifest := range normalized {
			if strings.EqualFold(name, manifest.Name) {
				return nil, fmt.Errorf("external tool manifest name %q collides with native tool definition", manifest.Name)
			}
		}
	}
	r.externalManifests = append([]ExternalToolManifest(nil), normalized...)
	return r, nil
}

func (r *Registry) SupportsPrincipal(p principal.Principal) bool {
	if r == nil || r.sandbox == nil {
		return false
	}

	scope, err := r.scopeForPrincipalToolExecution(p)
	if err != nil {
		return false
	}
	if r.durableAgentPrincipalFallback && p.Role == principal.RoleDurableAgent && strings.TrimSpace(p.DurableAgentID) != "" {
		return true
	}
	if r.runner == nil {
		return p.Role == principal.RoleAdmin
	}
	return r.runner.Supports(scope)
}

func (r *Registry) scopeForPrincipalToolExecution(p principal.Principal) (sandbox.Scope, error) {
	if r == nil || r.sandbox == nil {
		return sandbox.Scope{}, fmt.Errorf("principal-aware execution requires sandbox resolver")
	}
	if scope, ok, err := r.durableAgentScopeForPrincipalToolExecution(p); ok || err != nil {
		return scope, err
	}
	scope, err := r.sandbox.Resolve(p)
	if err == nil {
		return scope, nil
	}
	return sandbox.Scope{}, err
}

func (r *Registry) durableAgentScopeForPrincipalToolExecution(p principal.Principal) (sandbox.Scope, bool, error) {
	if p.Role != principal.RoleDurableAgent {
		return sandbox.Scope{}, false, nil
	}
	agentID := strings.TrimSpace(p.DurableAgentID)
	if agentID == "" {
		return sandbox.Scope{}, true, fmt.Errorf("durable_agent principal requires durable agent id")
	}

	globalRoot := strings.TrimSpace(r.workspace)
	durableProfile := sandbox.Profile{}
	if r.sandbox != nil {
		roots := r.sandbox.Roots()
		globalRoot = firstNonEmpty(roots.GlobalRoot, globalRoot)
		durableProfile = r.sandbox.Profiles().DurableAgent
	}

	var workingRoot, memoryRoot, networkPolicy string
	if r.store != nil {
		agent, err := r.store.DurableAgent(agentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return sandbox.Scope{}, true, fmt.Errorf("load durable agent %q for tool scope: %w", agentID, err)
		}
		if agent != nil {
			workingRoot, memoryRoot = durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
			if workingRoot == "" || memoryRoot == "" {
				workingRoot, memoryRoot = durableagent.DefaultLocalRoots(r.store.DBPath(), agent.AgentID)
			}
			networkPolicy = agent.NetworkPolicy
		}
		if workingRoot == "" || memoryRoot == "" {
			workingRoot, memoryRoot = durableagent.DefaultLocalRoots(r.store.DBPath(), agentID)
		}
	}

	if workingRoot == "" || memoryRoot == "" {
		if r.durableAgentPrincipalFallback && strings.TrimSpace(r.workspace) != "" {
			workingRoot = strings.TrimSpace(r.workspace)
			memoryRoot = strings.TrimSpace(r.workspace)
		} else {
			return sandbox.Scope{}, true, fmt.Errorf("durable_agent principal %q requires durable local roots", agentID)
		}
	}

	scope, err := sandbox.DurableAgentScopeWithProfile(agentID, globalRoot, workingRoot, memoryRoot, durableProfile, networkPolicy)
	if err != nil {
		return sandbox.Scope{}, true, err
	}
	return scope, true, nil
}
