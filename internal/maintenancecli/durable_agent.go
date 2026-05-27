//go:build linux

package maintenancecli

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
)

type DurableAgentDeps struct {
	RunRemote        func([]string) error
	RunWake          func([]string) error
	RunChild         func([]string) error
	DefaultBootstrap func(*config.Config) core.NodeLLMBootstrap
}

func RunDurableAgentCommand(args []string, deps DurableAgentDeps) error {
	if commandGroupHelpRequested(args) {
		printCommandGroupHelp("durable-agent", []string{"list", "health", "policy", "enrollment", "forensic", "bootstrap", "provision", "remote", "wake", "child-run", "reconcile"})
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("durable-agent requires a subcommand: list, health, policy, enrollment, forensic, bootstrap, provision, remote, wake, child-run, or reconcile")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "reconcile":
		return runDurableAgentReconcileCommand(args[1:], deps)
	case "list":
		return runDurableAgentListCommand(args[1:])
	case "health":
		return runDurableAgentHealthCommand(args[1:])
	case "policy":
		return runDurableAgentPolicyCommand(args[1:])
	case "enrollment":
		return runDurableAgentEnrollmentCommand(args[1:])
	case "forensic":
		return runDurableAgentForensicCommand(args[1:])
	case "bootstrap":
		return runDurableAgentBootstrapCommand(args[1:])
	case "provision":
		return runDurableAgentProvisionCommand(args[1:])
	case "remote":
		if deps.RunRemote == nil {
			return fmt.Errorf("durable-agent remote dependency is unavailable")
		}
		return deps.RunRemote(args[1:])
	case "wake":
		if deps.RunWake == nil {
			return fmt.Errorf("durable-agent wake dependency is unavailable")
		}
		return deps.RunWake(args[1:])
	case "child-run":
		if deps.RunChild == nil {
			return fmt.Errorf("durable-agent child-run dependency is unavailable")
		}
		return deps.RunChild(args[1:])
	default:
		return fmt.Errorf("durable-agent subcommand must be one of list|health|policy|enrollment|forensic|bootstrap|provision|remote|wake|child-run|reconcile")
	}
}
