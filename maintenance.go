//go:build linux

package main

import (
	"context"
	"embed"
	"io"
	"os"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/internal/maintenancecli"
	"github.com/idolum-ai/aphelion/internal/standalonecli"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

//go:embed defaults/agent/* defaults/agent/memory/* defaults/agent/face/persona/* defaults/agent/face/contracts/* defaults/agent/face/scenes/* defaults/agent/face/models/*
var defaultAgentFilesFS embed.FS

//go:embed recipes/durable-children/*.toml
var durableChildRecipeFilesFS embed.FS

var defaultPromptSeedFiles = []string{
	"SOUL.md",
	"IDENTITY.md",
	"USER.md",
	"AGENTS.md",
	"TOOLS.md",
	"BOOTSTRAP.md",
	"IDOLUM.md",
	"QUESTIONS-TO-IDOLUM.md",
	"face/persona/telos.md",
	"face/persona/name.md",
	"face/persona/anti-idolatry.md",
	"face/persona/voice.md",
	"face/contracts/material-floor.md",
	"face/contracts/no-new-authority.md",
	"face/contracts/no-new-facts.md",
	"face/contracts/semantic-memory-is-texture.md",
	"face/contracts/usefulness-not-obedience.md",
	"face/scenes/architecture-exploration.md",
	"face/scenes/approval-request.md",
	"face/scenes/blocked-notice.md",
	"face/scenes/completion-report.md",
	"face/scenes/refusal.md",
	"face/models/overlays.md",
	"face/models/openai-gpt-5.5.md",
	"face/models/anthropic-claude-sonnet-4-6.md",
}

var defaultSharedMemorySeedFiles = []string{
	"MEMORY.md",
	"HEARTBEAT.md",
	"memory/knowledge.md",
	"memory/decisions.md",
	"memory/questions.md",
	"memory/rhizome.md",
}

func runAuthorityCommand(args []string) error {
	return maintenancecli.RunAuthorityCommand(args, maintenancecli.AuthorityDeps{
		SnapshotFromStore: aphruntime.AuthorityStatusSnapshotFromStore,
	})
}

func runMaintenanceCommand(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "help":
		printTopLevelHelp(os.Stdout, "")
		return true, nil
	case "authority":
		return true, runAuthorityCommand(args[1:])
	case "quickstart":
		return true, standalonecli.RunQuickstartCommand(args[1:])
	case "init":
		return true, runInitCommand(args[1:])
	case "paths":
		return true, runPathsCommand(args[1:])
	case "park-restart":
		return true, runParkRestartCommand(args[1:])
	case "repair-live-state":
		return true, runRepairLiveStateCommand(args[1:])
	case "repair-capability-grants":
		return true, maintenancecli.RunRepairCapabilityGrantsCommand(args[1:])
	case "repair-review-redactions":
		return true, maintenancecli.RunRepairReviewRedactionsCommand(args[1:])
	case "gc":
		return true, maintenancecli.RunGCCommand(args[1:])
	case "forget":
		return true, maintenancecli.RunForgetCommand(args[1:])
	case "reset":
		return true, maintenancecli.RunResetCommand(args[1:])
	case "import-audit":
		return true, maintenancecli.RunImportAuditCommand(args[1:])
	case "import-semantic":
		return true, maintenancecli.RunImportSemanticCommand(args[1:])
	case "import-codex-sessions":
		return true, maintenancecli.RunImportCodexSessionsCommand(args[1:])
	case "verify-deploy":
		return true, runVerifyDeployCommand(args[1:])
	case "status":
		return true, standalonecli.RunStatusCommand(args[1:])
	case "durable-agent":
		return true, runDurableAgentCommand(args[1:])
	case "tailnet":
		return true, maintenancecli.RunTailnetCommand(args[1:])
	case "github-app":
		return true, maintenancecli.RunGitHubAppCommand(args[1:])
	case "sandbox-net":
		return true, maintenancecli.RunSandboxNetCommand(args[1:])
	case "schema":
		return true, maintenancecli.RunSchemaMaintenanceCommand(args[1:])
	case "telegram-child-bot":
		return true, runTelegramChildBotCommand(args[1:])
	case "telegram-threads":
		return true, maintenancecli.RunTelegramThreadsMaintenanceCommand(args[1:])
	case "evidence":
		return true, maintenancecli.RunEvidenceCommand(args[1:])
	case "eval":
		return true, runEvalCommand(args[1:])
	case "agency-eval":
		return true, standalonecli.RunAgencyEvalCommand(args[1:])
	case "version":
		return true, standalonecli.RunVersionCommand(args[1:])
	default:
		return false, nil
	}
}

// Root owns dependency assembly; maintenance behavior lives in internal/maintenancecli.
func runRepairLiveStateCommand(args []string) error {
	return maintenancecli.RunRepairLiveStateCommand(args, maintenanceLiveStateRepairDeps())
}

func maintenanceLiveStateRepairDeps() maintenancecli.LiveStateRepairDeps {
	return maintenancecli.LiveStateRepairDeps{ParkActiveWorkForRestart: func(ctx context.Context, store *session.SQLiteStore, source string, now time.Time) (maintenancecli.RestartParkResult, error) {
		park, err := aphruntime.ParkStoreActiveWorkForRestart(ctx, store, source, now)
		return maintenancecli.RestartParkResult{
			TurnRunsInterrupted:          park.TurnRunsInterrupted,
			ContinuationsParked:          park.ContinuationsParked,
			PendingContinuationsParked:   park.PendingContinuationsParked,
			ApprovedContinuationsParked:  park.ApprovedContinuationsParked,
			AlreadyParkedContinuations:   park.AlreadyParkedContinuations,
			SkippedContinuations:         park.SkippedContinuations,
			ExpiredApprovedContinuations: park.ExpiredApprovedContinuations,
		}, err
	}}
}

func maintenanceDurableAgentDeps() maintenancecli.DurableAgentDeps {
	return maintenancecli.DurableAgentDeps{
		RunRemote:        runDurableAgentRemoteCommand,
		RunWake:          runDurableAgentWakeCommand,
		RunChild:         runDurableAgentChildCommand,
		DefaultBootstrap: defaultDurableAgentBootstrapFromConfig,
	}
}

func runDurableAgentCommand(args []string) error {
	return maintenancecli.RunDurableAgentCommand(args, maintenanceDurableAgentDeps())
}

func runDurableAgentReconcileCommand(args []string) error {
	return maintenancecli.RunDurableAgentReconcileCommand(args, maintenanceDurableAgentDeps())
}

func reconcileDurableAgentsForConfig(cfg *config.Config, opts maintenancecli.DurableAgentReconcileOptions) (*maintenancecli.DurableAgentReconcileResult, error) {
	return maintenancecli.ReconcileDurableAgentsForConfig(cfg, opts, maintenanceDurableAgentDeps())
}
func printDurableAgentReconcileResult(w io.Writer, result *maintenancecli.DurableAgentReconcileResult) {
	maintenancecli.PrintDurableAgentReconcileResult(w, result)
}
