//go:build linux

package main

import (
	"fmt"
	"strings"
)

func runDurableAgentCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("durable-agent requires a subcommand: list, health, policy, enrollment, forensic, bootstrap, provision, remote, wake, child-run, or reconcile")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "reconcile":
		return runDurableAgentReconcileCommand(args[1:])
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
		return runDurableAgentRemoteCommand(args[1:])
	case "wake":
		return runDurableAgentWakeCommand(args[1:])
	case "child-run":
		return runDurableAgentChildCommand(args[1:])
	default:
		return fmt.Errorf("durable-agent subcommand must be one of list|health|policy|enrollment|forensic|bootstrap|provision|remote|wake|child-run|reconcile")
	}
}
