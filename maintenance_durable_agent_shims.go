//go:build linux

package main

import (
	"io"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/internal/maintenancecli"
)

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
func runDurableAgentPolicyCommand(args []string) error {
	return maintenancecli.RunDurableAgentPolicyCommand(args)
}
func runDurableAgentForensicCommand(args []string) error {
	return maintenancecli.RunDurableAgentForensicCommand(args)
}
func runDurableAgentEnrollmentCommand(args []string) error {
	return maintenancecli.RunDurableAgentEnrollmentCommand(args)
}
func runDurableAgentBootstrapCommand(args []string) error {
	return maintenancecli.RunDurableAgentBootstrapCommand(args)
}
func runDurableAgentListCommand(args []string) error {
	return maintenancecli.RunDurableAgentListCommand(args)
}
func runDurableAgentHealthCommand(args []string) error {
	return maintenancecli.RunDurableAgentHealthCommand(args)
}
func runDurableAgentReconcileCommand(args []string) error {
	return maintenancecli.RunDurableAgentReconcileCommand(args, maintenanceDurableAgentDeps())
}

type durableAgentReconcileOptions = maintenancecli.DurableAgentReconcileOptions
type durableAgentReconcileResult = maintenancecli.DurableAgentReconcileResult
type durableAgentReconcileRow = maintenancecli.DurableAgentReconcileRow

func reconcileDurableAgentsForConfig(cfg *config.Config, opts durableAgentReconcileOptions) (*durableAgentReconcileResult, error) {
	return maintenancecli.ReconcileDurableAgentsForConfig(cfg, opts, maintenanceDurableAgentDeps())
}
func printDurableAgentReconcileResult(w io.Writer, result *durableAgentReconcileResult) {
	maintenancecli.PrintDurableAgentReconcileResult(w, result)
}

const durableAgentReconcileGrowthMarker = maintenancecli.DurableAgentReconcileGrowthMarker

func runDurableAgentProvisionCommand(args []string) error {
	return maintenancecli.RunDurableAgentProvisionCommand(args)
}
