//go:build linux

package main

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/maintenancecli"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

func runRepairCapabilityGrantsCommand(args []string) error {
	return maintenancecli.RunRepairCapabilityGrantsCommand(args)
}

func runRepairReviewRedactionsCommand(args []string) error {
	return maintenancecli.RunRepairReviewRedactionsCommand(args)
}

type capabilityGrantRepairManifest = maintenancecli.CapabilityGrantRepairManifest
type capabilityGrantRepairOptions = maintenancecli.CapabilityGrantRepairOptions
type capabilityGrantRepairResult = maintenancecli.CapabilityGrantRepairResult
type capabilityGrantRepairRow = maintenancecli.CapabilityGrantRepairRow

func repairCapabilityGrantDrift(ctx context.Context, store *session.SQLiteStore, manifests []capabilityGrantRepairManifest, opts capabilityGrantRepairOptions) (capabilityGrantRepairResult, error) {
	return maintenancecli.RepairCapabilityGrantDrift(ctx, store, manifests, opts)
}

func loadCapabilityRepairManifests(dir string) ([]capabilityGrantRepairManifest, error) {
	return maintenancecli.LoadCapabilityRepairManifests(dir)
}

func maintenanceRepairKey() session.SessionKey { return maintenancecli.MaintenanceRepairKey() }

func appendMaintenanceExecutionEvent(store *session.SQLiteStore, key session.SessionKey, eventType string, stage string, status string, payload map[string]any, createdAt time.Time) error {
	return maintenancecli.AppendMaintenanceExecutionEvent(store, key, eventType, stage, status, payload, createdAt)
}

type reviewRedactionRepairResult = maintenancecli.ReviewRedactionRepairResult

func repairReviewRedactions(ctx context.Context, store *session.SQLiteStore, limit int, dryRun bool, now time.Time) (reviewRedactionRepairResult, error) {
	return maintenancecli.RepairReviewRedactions(ctx, store, limit, dryRun, now)
}

func childRuntimeFromRepairManifest(entry capabilityGrantRepairManifest) (core.ChildRuntimeContract, bool, string) {
	return maintenancecli.ChildRuntimeFromRepairManifest(entry)
}

func runRepairLiveStateCommand(args []string) error {
	return maintenancecli.RunRepairLiveStateCommand(args, maintenanceLiveStateRepairDeps())
}

type liveStateRepairResult = maintenancecli.LiveStateRepairResult

func repairLiveState(ctx context.Context, store *session.SQLiteStore, source string, closeLive bool, now time.Time) (liveStateRepairResult, error) {
	return maintenancecli.RepairLiveState(ctx, store, source, closeLive, now, maintenanceLiveStateRepairDeps())
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
