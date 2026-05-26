//go:build linux

package telegramruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	telegramPrimaryIngressSurface                         = "telegram:primary"
	telegramThreadSummaryIngressSurface                   = "telegram:callback-work:thread-summary"
	telegramDoctorIngressSurface                          = "telegram:callback-work:doctor"
	telegramContextClarificationIngressSurface            = "telegram:callback-work:context-clarification"
	telegramMemoryClarificationIngressSurface             = "telegram:callback-work:memory-clarification"
	telegramMissionClarificationIngressSurface            = "telegram:callback-work:mission-clarification"
	telegramBusyDecisionResumeIngressSurface              = "telegram:decision-resume:busy"
	telegramArtifactRetentionDecisionResumeIngressSurface = "telegram:decision-resume:artifact-retention"
)

type telegramWorkSurfaceKind string

const (
	telegramWorkSurfacePrimary        telegramWorkSurfaceKind = "primary"
	telegramWorkSurfaceCallbackWork   telegramWorkSurfaceKind = "callback_work"
	telegramWorkSurfaceDecisionResume telegramWorkSurfaceKind = "decision_resume"
)

type telegramWorkSurface struct {
	Name        string
	Surface     string
	Kind        telegramWorkSurfaceKind
	ReplayLimit int
}

func telegramStartupWorkSurfaces() []telegramWorkSurface {
	return []telegramWorkSurface{
		{Name: "primary", Surface: telegramPrimaryIngressSurface, Kind: telegramWorkSurfacePrimary, ReplayLimit: 100},
		{Name: "thread_summary", Surface: telegramThreadSummaryIngressSurface, Kind: telegramWorkSurfaceCallbackWork, ReplayLimit: 100},
		{Name: "doctor", Surface: telegramDoctorIngressSurface, Kind: telegramWorkSurfaceCallbackWork, ReplayLimit: 100},
		{Name: "context_clarification", Surface: telegramContextClarificationIngressSurface, Kind: telegramWorkSurfaceCallbackWork, ReplayLimit: 100},
		{Name: "memory_clarification", Surface: telegramMemoryClarificationIngressSurface, Kind: telegramWorkSurfaceCallbackWork, ReplayLimit: 100},
		{Name: "mission_clarification", Surface: telegramMissionClarificationIngressSurface, Kind: telegramWorkSurfaceCallbackWork, ReplayLimit: 100},
		{Name: "busy_decision_resume", Surface: telegramBusyDecisionResumeIngressSurface, Kind: telegramWorkSurfaceDecisionResume, ReplayLimit: 100},
		{Name: "artifact_retention_decision_resume", Surface: telegramArtifactRetentionDecisionResumeIngressSurface, Kind: telegramWorkSurfaceDecisionResume, ReplayLimit: 100},
	}
}

func replayStartupTelegramIngress(ctx context.Context, store *session.SQLiteStore, handler telegram.UpdateHandler, logger telegramIngressReplayLogger) (telegram.PollerCheckpoint, error) {
	primaryCheckpoint := newTelegramIngressCheckpoint(store, telegramPrimaryIngressSurface)
	for _, workSurface := range telegramStartupWorkSurfaces() {
		surface := strings.TrimSpace(workSurface.Surface)
		if surface == "" {
			continue
		}
		checkpoint := newTelegramIngressCheckpoint(store, surface)
		if workSurface.Kind == telegramWorkSurfacePrimary {
			primaryCheckpoint = checkpoint
		}
		limit := workSurface.ReplayLimit
		if limit <= 0 {
			limit = 100
		}
		if err := replayPendingTelegramIngress(ctx, store, checkpoint, handler, surface, limit, logger); err != nil {
			return primaryCheckpoint, fmt.Errorf("replay telegram work surface %s (%s): %w", strings.TrimSpace(workSurface.Name), surface, err)
		}
	}
	return primaryCheckpoint, nil
}
