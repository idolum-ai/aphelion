//go:build linux

package main

import (
	"context"
	"io"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/internal/maintenancecli"
	memstore "github.com/idolum-ai/aphelion/memory"
)

func runImportAuditCommand(args []string) error { return maintenancecli.RunImportAuditCommand(args) }
func runImportSemanticCommand(args []string) error {
	return maintenancecli.RunImportSemanticCommand(args)
}
func runImportCodexSessionsCommand(args []string) error {
	return maintenancecli.RunImportCodexSessionsCommand(args)
}
func runGCCommand(args []string) error     { return maintenancecli.RunGCCommand(args) }
func runForgetCommand(args []string) error { return maintenancecli.RunForgetCommand(args) }
func runResetCommand(args []string) error  { return maintenancecli.RunResetCommand(args) }

type codexSessionImportCommandOptions = maintenancecli.CodexSessionImportCommandOptions

func defaultCodexSessionImportCommandOptions(cfg *config.Config) codexSessionImportCommandOptions {
	return maintenancecli.DefaultCodexSessionImportCommandOptions(cfg)
}
func importCodexSessionsForConfig(ctx context.Context, cfg *config.Config, opts codexSessionImportCommandOptions) (*memstore.CodexSessionImportResult, error) {
	return maintenancecli.ImportCodexSessionsForConfig(ctx, cfg, opts)
}
func printCodexSessionImportResult(w io.Writer, result *memstore.CodexSessionImportResult, state memstore.SemanticImportState) {
	maintenancecli.PrintCodexSessionImportResult(w, result, state)
}

var clearSharedDynamicMemory = maintenancecli.ClearSharedDynamicMemory
var archiveColdDailyNotes = maintenancecli.ArchiveColdDailyNotes
var archiveOversizedCuratedMemory = maintenancecli.ArchiveOversizedCuratedMemory
var pruneExecutionEventsForRetention = maintenancecli.PruneExecutionEventsForRetention
var tesRetentionConfigSafety = maintenancecli.TESRetentionConfigSafety

type tesRetentionExportBundle = maintenancecli.TESRetentionExportBundle

var _ = time.Time{}
