//go:build linux

package telegramruntime

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type IngressCheckpoint = telegramIngressCheckpoint
type IngressReplayLogger = telegramIngressReplayLogger
type WorkSurfaceKind = telegramWorkSurfaceKind
type WorkSurface = telegramWorkSurface
type UIClient = telegramUIClient
type SessionTarget = telegramSessionTarget

const (
	PrimaryIngressSurface                         = telegramPrimaryIngressSurface
	ThreadSummaryIngressSurface                   = telegramThreadSummaryIngressSurface
	DoctorIngressSurface                          = telegramDoctorIngressSurface
	BusyDecisionResumeIngressSurface              = telegramBusyDecisionResumeIngressSurface
	ArtifactRetentionDecisionResumeIngressSurface = telegramArtifactRetentionDecisionResumeIngressSurface

	WorkSurfacePrimary        = telegramWorkSurfacePrimary
	WorkSurfaceCallbackWork   = telegramWorkSurfaceCallbackWork
	WorkSurfaceDecisionResume = telegramWorkSurfaceDecisionResume
)

func NewIngressCheckpoint(store *session.SQLiteStore, surface string) IngressCheckpoint {
	return newTelegramIngressCheckpoint(store, surface)
}

func ReplayPendingIngress(ctx context.Context, store *session.SQLiteStore, checkpoint telegram.PollerCheckpoint, handler telegram.UpdateHandler, surface string, limit int, logger IngressReplayLogger) error {
	return replayPendingTelegramIngress(ctx, store, checkpoint, handler, surface, limit, logger)
}

func IngressReplayMessage(record session.TelegramIngressUpdateRecord) (core.InboundMessage, error) {
	return telegramIngressReplayMessage(record)
}

func IngressFailureFromUpdateRecord(record session.TelegramIngressUpdateRecord, err error) telegram.PollerFailure {
	return telegramIngressFailureFromUpdateRecord(record, err)
}

func StartupWorkSurfaces() []WorkSurface {
	return telegramStartupWorkSurfaces()
}

func ReplayStartupIngress(ctx context.Context, store *session.SQLiteStore, handler telegram.UpdateHandler, logger IngressReplayLogger) (telegram.PollerCheckpoint, error) {
	return replayStartupTelegramIngress(ctx, store, handler, logger)
}

func NewUIClient(client *telegram.Client) *UIClient {
	return newTelegramUIClient(client)
}

func RewriteDurableRelayIntent(msg core.InboundMessage) core.InboundMessage {
	return rewriteDurableRelayIntent(msg)
}

func ParseDurableRelayIntent(text string) (agentID string, body string, ok bool) {
	return parseDurableRelayIntent(text)
}

func SplitFirstToken(text string) (first string, remaining string, ok bool) {
	return splitFirstToken(text)
}

func IsValidDurableRelayAgentID(value string) bool {
	return isValidDurableRelayAgentID(value)
}

func RewriteDurableWizardIntent(msg core.InboundMessage, router any) core.InboundMessage {
	return rewriteDurableWizardIntent(msg, router)
}

func SessionTargetForMessage(msg core.InboundMessage) SessionTarget {
	return telegramSessionTargetForMessage(msg)
}

func CommandMessageScope(msg core.InboundMessage) session.ScopeRef {
	return telegramCommandMessageScope(msg)
}

func SessionTargetForScope(chatID int64, scope session.ScopeRef) SessionTarget {
	return telegramSessionTargetForScope(chatID, scope)
}

func ThreadIDFromScope(chatID int64, scope session.ScopeRef) int64 {
	return telegramThreadIDFromScope(chatID, scope)
}

func InboundForSessionTarget(chatID int64, senderID int64, target SessionTarget) core.InboundMessage {
	return telegramInboundForSessionTarget(chatID, senderID, target)
}

func SessionOwnerKey(msg core.InboundMessage) string {
	return telegramSessionOwnerKey(msg)
}

func ThreadDisplayPrefixForMessage(msg core.InboundMessage) string {
	return telegramThreadDisplayPrefixForMessage(msg)
}

func InboundForTurnRun(run session.TurnRun, senderID int64) core.InboundMessage {
	return telegramInboundForTurnRun(run, senderID)
}
