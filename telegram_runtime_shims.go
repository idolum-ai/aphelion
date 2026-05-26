//go:build linux

package main

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type telegramIngressCheckpoint = telegramruntime.IngressCheckpoint
type telegramIngressReplayLogger = telegramruntime.IngressReplayLogger
type telegramWorkSurfaceKind = telegramruntime.WorkSurfaceKind
type telegramWorkSurface = telegramruntime.WorkSurface
type telegramUIClient = telegramruntime.UIClient
type telegramSessionTarget = telegramruntime.SessionTarget

const (
	telegramPrimaryIngressSurface                         = telegramruntime.PrimaryIngressSurface
	telegramThreadSummaryIngressSurface                   = telegramruntime.ThreadSummaryIngressSurface
	telegramDoctorIngressSurface                          = telegramruntime.DoctorIngressSurface
	telegramContextClarificationIngressSurface            = telegramruntime.ContextClarificationIngressSurface
	telegramMemoryClarificationIngressSurface             = telegramruntime.MemoryClarificationIngressSurface
	telegramBusyDecisionResumeIngressSurface              = telegramruntime.BusyDecisionResumeIngressSurface
	telegramArtifactRetentionDecisionResumeIngressSurface = telegramruntime.ArtifactRetentionDecisionResumeIngressSurface

	telegramWorkSurfacePrimary        = telegramruntime.WorkSurfacePrimary
	telegramWorkSurfaceCallbackWork   = telegramruntime.WorkSurfaceCallbackWork
	telegramWorkSurfaceDecisionResume = telegramruntime.WorkSurfaceDecisionResume
)

func newTelegramIngressCheckpoint(store *session.SQLiteStore, surface string) telegramIngressCheckpoint {
	return telegramruntime.NewIngressCheckpoint(store, surface)
}

func replayPendingTelegramIngress(ctx context.Context, store *session.SQLiteStore, checkpoint telegram.PollerCheckpoint, handler telegram.UpdateHandler, surface string, limit int, logger telegramIngressReplayLogger) error {
	return telegramruntime.ReplayPendingIngress(ctx, store, checkpoint, handler, surface, limit, logger)
}

func telegramIngressReplayMessage(record session.TelegramIngressUpdateRecord) (core.InboundMessage, error) {
	return telegramruntime.IngressReplayMessage(record)
}

func telegramIngressFailureFromUpdateRecord(record session.TelegramIngressUpdateRecord, err error) telegram.PollerFailure {
	return telegramruntime.IngressFailureFromUpdateRecord(record, err)
}

func telegramStartupWorkSurfaces() []telegramWorkSurface {
	return telegramruntime.StartupWorkSurfaces()
}

func replayStartupTelegramIngress(ctx context.Context, store *session.SQLiteStore, handler telegram.UpdateHandler, logger telegramIngressReplayLogger) (telegram.PollerCheckpoint, error) {
	return telegramruntime.ReplayStartupIngress(ctx, store, handler, logger)
}

func newTelegramUIClient(client *telegram.Client) *telegramUIClient {
	return telegramruntime.NewUIClient(client)
}

func rewriteDurableRelayIntent(msg core.InboundMessage) core.InboundMessage {
	return telegramruntime.RewriteDurableRelayIntent(msg)
}

func parseDurableRelayIntent(text string) (agentID string, body string, ok bool) {
	return telegramruntime.ParseDurableRelayIntent(text)
}

func splitFirstToken(text string) (first string, remaining string, ok bool) {
	return telegramruntime.SplitFirstToken(text)
}

func isValidDurableRelayAgentID(value string) bool {
	return telegramruntime.IsValidDurableRelayAgentID(value)
}

func rewriteDurableWizardIntent(msg core.InboundMessage, router commandRouter) core.InboundMessage {
	return telegramruntime.RewriteDurableWizardIntent(msg, router)
}

func telegramSessionTargetForMessage(msg core.InboundMessage) telegramSessionTarget {
	return telegramruntime.SessionTargetForMessage(msg)
}

func telegramCommandMessageScope(msg core.InboundMessage) session.ScopeRef {
	return telegramruntime.CommandMessageScope(msg)
}

func telegramSessionTargetForScope(chatID int64, scope session.ScopeRef) telegramSessionTarget {
	return telegramruntime.SessionTargetForScope(chatID, scope)
}

func telegramThreadIDFromScope(chatID int64, scope session.ScopeRef) int64 {
	return telegramruntime.ThreadIDFromScope(chatID, scope)
}

func telegramInboundForSessionTarget(chatID int64, senderID int64, target telegramSessionTarget) core.InboundMessage {
	return telegramruntime.InboundForSessionTarget(chatID, senderID, target)
}

func telegramSessionOwnerKey(msg core.InboundMessage) string {
	return telegramruntime.SessionOwnerKey(msg)
}

func telegramThreadDisplayPrefixForMessage(msg core.InboundMessage) string {
	return telegramruntime.ThreadDisplayPrefixForMessage(msg)
}

func telegramInboundForTurnRun(run session.TurnRun, senderID int64) core.InboundMessage {
	return telegramruntime.InboundForTurnRun(run, senderID)
}
