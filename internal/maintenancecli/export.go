//go:build linux

package maintenancecli

import (
	"context"
	"io"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
)

func RunTailnetCommand(args []string) error    { return runTailnetCommand(args) }
func RunSandboxNetCommand(args []string) error { return runSandboxNetCommand(args) }
func RunTelegramThreadsMaintenanceCommand(args []string) error {
	return runTelegramThreadsMaintenanceCommand(args)
}
func RunGCCommand(args []string) error                  { return runGCCommand(args) }
func RunForgetCommand(args []string) error              { return runForgetCommand(args) }
func RunResetCommand(args []string) error               { return runResetCommand(args) }
func RunImportAuditCommand(args []string) error         { return runImportAuditCommand(args) }
func RunImportSemanticCommand(args []string) error      { return runImportSemanticCommand(args) }
func RunImportCodexSessionsCommand(args []string) error { return runImportCodexSessionsCommand(args) }

func ClearSharedDynamicMemory(cfg *config.Config) (int, error) { return clearSharedDynamicMemory(cfg) }
func ArchiveColdDailyNotes(cfg *config.Config, now time.Time) (int, error) {
	return archiveColdDailyNotes(cfg, now)
}
func ArchiveOversizedCuratedMemory(cfg *config.Config, now time.Time) (int, error) {
	return archiveOversizedCuratedMemory(cfg, now)
}
func PruneExecutionEventsForRetention(cfg *config.Config, now time.Time) (int, string, error) {
	return pruneExecutionEventsForRetention(cfg, now)
}
func TESRetentionConfigSafety(cfg *config.Config) (config.SessionsTESRetentionConfig, time.Duration, string, error) {
	return tesRetentionConfigSafety(cfg)
}

type TESRetentionExportBundle = tesRetentionExportBundle
type CodexSessionImportCommandOptions = codexSessionImportCommandOptions

func DefaultCodexSessionImportCommandOptions(cfg *config.Config) CodexSessionImportCommandOptions {
	return defaultCodexSessionImportCommandOptions(cfg)
}
func ImportCodexSessionsForConfig(ctx context.Context, cfg *config.Config, opts CodexSessionImportCommandOptions) (*memstore.CodexSessionImportResult, error) {
	return importCodexSessionsForConfig(ctx, cfg, opts)
}
func PrintCodexSessionImportResult(w io.Writer, result *memstore.CodexSessionImportResult, state memstore.SemanticImportState) {
	printCodexSessionImportResult(w, result, state)
}

func RunRepairCapabilityGrantsCommand(args []string) error {
	return runRepairCapabilityGrantsCommand(args)
}
func RunRepairReviewRedactionsCommand(args []string) error {
	return runRepairReviewRedactionsCommand(args)
}

type CapabilityGrantRepairManifest = capabilityGrantRepairManifest
type CapabilityGrantRepairOptions = capabilityGrantRepairOptions
type CapabilityGrantRepairResult = capabilityGrantRepairResult
type CapabilityGrantRepairRow = capabilityGrantRepairRow

func RepairCapabilityGrantDrift(ctx context.Context, store *session.SQLiteStore, manifests []CapabilityGrantRepairManifest, opts CapabilityGrantRepairOptions) (CapabilityGrantRepairResult, error) {
	return repairCapabilityGrantDrift(ctx, store, manifests, opts)
}
func LoadCapabilityRepairManifests(dir string) ([]CapabilityGrantRepairManifest, error) {
	return loadCapabilityRepairManifests(dir)
}

func MaintenanceRepairKey() session.SessionKey { return maintenanceRepairKey() }
func AppendMaintenanceExecutionEvent(store *session.SQLiteStore, key session.SessionKey, eventType string, stage string, status string, payload map[string]any, createdAt time.Time) error {
	return appendMaintenanceExecutionEvent(store, key, eventType, stage, status, payload, createdAt)
}

type ReviewRedactionRepairResult = reviewRedactionRepairResult

func RepairReviewRedactions(ctx context.Context, store *session.SQLiteStore, limit int, dryRun bool, now time.Time) (ReviewRedactionRepairResult, error) {
	return repairReviewRedactions(ctx, store, limit, dryRun, now)
}

func ChildRuntimeFromRepairManifest(entry CapabilityGrantRepairManifest) (core.ChildRuntimeContract, bool, string) {
	return childRuntimeFromRepairManifest(entry)
}

func RunDurableAgentPolicyCommand(args []string) error   { return runDurableAgentPolicyCommand(args) }
func RunDurableAgentForensicCommand(args []string) error { return runDurableAgentForensicCommand(args) }
func RunDurableAgentEnrollmentCommand(args []string) error {
	return runDurableAgentEnrollmentCommand(args)
}
func RunDurableAgentBootstrapCommand(args []string) error {
	return runDurableAgentBootstrapCommand(args)
}
func RunDurableAgentListCommand(args []string) error   { return runDurableAgentListCommand(args) }
func RunDurableAgentHealthCommand(args []string) error { return runDurableAgentHealthCommand(args) }
func RunDurableAgentReconcileCommand(args []string, deps DurableAgentDeps) error {
	return runDurableAgentReconcileCommand(args, deps)
}

type DurableAgentReconcileOptions = durableAgentReconcileOptions
type DurableAgentReconcileResult = durableAgentReconcileResult

type DurableAgentReconcileRow = durableAgentReconcileRow

func PrintDurableAgentReconcileResult(w io.Writer, result *DurableAgentReconcileResult) {
	printDurableAgentReconcileResult(w, result)
}

const DurableAgentReconcileGrowthMarker = durableAgentReconcileGrowthMarker

func RunDurableAgentProvisionCommand(args []string) error {
	return runDurableAgentProvisionCommand(args)
}

func RunRepairLiveStateCommand(args []string, deps LiveStateRepairDeps) error {
	return runRepairLiveStateCommand(args, deps)
}

type LiveStateRepairResult = liveStateRepairResult

func RepairLiveState(ctx context.Context, store *session.SQLiteStore, source string, closeLive bool, now time.Time, deps LiveStateRepairDeps) (LiveStateRepairResult, error) {
	return repairLiveState(ctx, store, source, closeLive, now, deps)
}
