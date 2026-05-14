//go:build linux

package main

import (
	"embed"
	"os"
)

//go:embed defaults/agent/* defaults/agent/memory/*
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
}

var defaultSharedMemorySeedFiles = []string{
	"MEMORY.md",
	"HEARTBEAT.md",
	"memory/knowledge.md",
	"memory/decisions.md",
	"memory/questions.md",
	"memory/rhizome.md",
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
		return true, runQuickstartCommand(args[1:])
	case "init":
		return true, runInitCommand(args[1:])
	case "paths":
		return true, runPathsCommand(args[1:])
	case "park-restart":
		return true, runParkRestartCommand(args[1:])
	case "repair-live-state":
		return true, runRepairLiveStateCommand(args[1:])
	case "repair-capability-grants":
		return true, runRepairCapabilityGrantsCommand(args[1:])
	case "repair-review-redactions":
		return true, runRepairReviewRedactionsCommand(args[1:])
	case "gc":
		return true, runGCCommand(args[1:])
	case "forget":
		return true, runForgetCommand(args[1:])
	case "reset":
		return true, runResetCommand(args[1:])
	case "import-audit":
		return true, runImportAuditCommand(args[1:])
	case "import-semantic":
		return true, runImportSemanticCommand(args[1:])
	case "import-codex-sessions":
		return true, runImportCodexSessionsCommand(args[1:])
	case "verify-deploy":
		return true, runVerifyDeployCommand(args[1:])
	case "durable-agent":
		return true, runDurableAgentCommand(args[1:])
	case "tailnet":
		return true, runTailnetCommand(args[1:])
	case "sandbox-net":
		return true, runSandboxNetCommand(args[1:])
	case "telegram-child-bot":
		return true, runTelegramChildBotCommand(args[1:])
	case "agency-eval":
		return true, runAgencyEvalCommand(args[1:])
	case "version":
		return true, runVersionCommand(args[1:])
	default:
		return false, nil
	}
}
