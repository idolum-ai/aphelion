//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/internal/standalonecli"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const (
	verifyDeployDefaultTimeout          = standalonecli.VerifyDeployDefaultTimeout
	verifyDeployBlessingPrefix          = standalonecli.VerifyDeployBlessingPrefix
	verifyDeployProbePrompt             = standalonecli.VerifyDeployProbePrompt
	verifyDeployDurableChildrenStatus   = standalonecli.VerifyDeployDurableChildrenStatus
	verifyDeployDurableChildrenRequired = standalonecli.VerifyDeployDurableChildrenRequired
	verifyDeployDurableChildrenWarn     = standalonecli.VerifyDeployDurableChildrenWarn
	verifyDeployDurableChildrenOff      = standalonecli.VerifyDeployDurableChildrenOff
	deployProbeStatusPass               = standalonecli.DeployProbeStatusPass
	deployProbeStatusFail               = standalonecli.DeployProbeStatusFail
)

type deployProbeStatus = standalonecli.DeployProbeStatus
type deployProbeResult = standalonecli.DeployProbeResult
type deployVerificationReport = standalonecli.DeployVerificationReport
type deployVerificationOptions = standalonecli.DeployVerificationOptions
type deployTurnRunner = standalonecli.DeployTurnRunner
type deployVerificationSender = standalonecli.DeployVerificationSender
type builtDeployVerificationRuntime = standalonecli.BuiltDeployVerificationRuntime

var deployVerificationRuntimeBuilder = defaultDeployVerificationRuntimeBuilder
var deployVerificationRunner = verifyDeployment

func verifyDeployDeps() standalonecli.VerifyDeployDeps {
	return standalonecli.VerifyDeployDeps{
		RuntimeBuilder:                      deployVerificationRuntimeBuilder,
		TESRetentionConfigSafety:            tesRetentionConfigSafety,
		PrepareFilesystem:                   prepareFilesystem,
		SyncConfiguredTelegramDurableGroups: syncConfiguredTelegramDurableGroups,
	}
}

func runVerifyDeployCommand(args []string) error {
	deps := verifyDeployDeps()
	deps.VerifyRunner = func(ctx context.Context, cfg *config.Config, opts standalonecli.DeployVerificationOptions) (standalonecli.DeployVerificationReport, error) {
		return deployVerificationRunner(ctx, cfg, opts)
	}
	return standalonecli.RunVerifyDeployCommand(args, deps)
}

func verifyDeployment(ctx context.Context, cfg *config.Config, opts deployVerificationOptions) (deployVerificationReport, error) {
	return standalonecli.VerifyDeployment(ctx, cfg, opts, verifyDeployDeps())
}

func defaultDeployVerificationRuntimeBuilder(cfg *config.Config, store *session.SQLiteStore) (builtDeployVerificationRuntime, error) {
	httpClient := &http.Client{Timeout: 90 * time.Second}
	llm, err := buildNativeProviderChain(cfg, httpClient)
	if err != nil {
		return builtDeployVerificationRuntime{}, err
	}

	sandboxRoots := sandbox.Roots{
		GlobalRoot:        cfg.Agent.PromptRoot,
		AdminExecRoot:     cfg.Agent.ExecRoot,
		SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
		UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
		UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
	}
	sandboxResolver, err := sandbox.NewResolver(sandboxRoots, sandbox.DefaultProfiles())
	if err != nil {
		return builtDeployVerificationRuntime{}, err
	}
	registry := tool.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Duration(cfg.Agent.ToolTimeout)*time.Second, sandboxResolver).
		WithSessionStore(store).
		WithRemoteHostSSH(cfg.Tailscale.SSHPath, remoteHostSSHTimeoutFromConfig(cfg))

	semanticEngine, err := newSemanticEngineForConfig(cfg, false)
	if err != nil {
		return builtDeployVerificationRuntime{}, err
	}
	registry.WithSemanticEngine(semanticEngine)

	fileStore, retrievalStore, err := buildOpenAIPlatformServices(cfg, httpClient)
	if err != nil {
		if semanticEngine != nil {
			semanticEngine.Close()
		}
		return builtDeployVerificationRuntime{}, err
	}
	if fileStore != nil {
		registry.WithFileStore(fileStore, cfg.OpenAI.Files.Purpose)
	}
	if retrievalStore != nil {
		registry.WithRetrievalStore(retrievalStore, cfg.OpenAI.VectorStores.DefaultStore)
	}

	sender := &deployVerificationSender{}
	rt, err := runtime.New(cfg, store, llm, registry, sender)
	if err != nil {
		if semanticEngine != nil {
			semanticEngine.Close()
		}
		return builtDeployVerificationRuntime{}, err
	}
	return builtDeployVerificationRuntime{
		Runner: rt,
		Sender: sender,
		Probe: func(ctx context.Context, key session.SessionKey, p principal.Principal) (string, error) {
			raw := json.RawMessage(`{
				"explanation": "Deployment verification plan probe",
				"plan": [
					{"step": "Verify the deploy tool path", "status": "in_progress"},
					{"step": "Confirm persisted verification state", "status": "pending"}
				]
			}`)
			out, err := registry.ExecuteForSessionPrincipal(ctx, p, key, "update_plan", raw)
			if err != nil {
				return "", err
			}
			state, err := store.PlanState(key)
			if err != nil {
				return "", err
			}
			if len(state.Steps) != 2 {
				return "", fmt.Errorf("tool probe persisted %d plan steps, want 2", len(state.Steps))
			}
			if state.Steps[0].Status != session.PlanStatusInProgress {
				return "", fmt.Errorf("tool probe first step status = %q, want in_progress", state.Steps[0].Status)
			}
			if !strings.Contains(out, "[PLAN_UPDATED]") {
				return "", fmt.Errorf("tool probe output missing [PLAN_UPDATED] header: %q", out)
			}
			return "update_plan executed and persisted session state", nil
		},
		DurableChildWake: rt.RunDurableAgentChildWake,
		Cleanup: func() {
			if semanticEngine != nil {
				semanticEngine.Close()
			}
		},
	}, nil
}
