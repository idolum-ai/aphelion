//go:build linux

package main

import (
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/telegramdecision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"time"
)

type telegramExecApprover = telegramdecision.ExecApprover
type telegramDurableMemoryDelegationApprover = telegramdecision.DurableMemoryDelegationApprover
type telegramDurableSnapshotRestoreApprover = telegramdecision.DurableSnapshotRestoreApprover

func newTelegramExecApprover(sender telegramDecisionSender, broker *decision.Broker) *telegramExecApprover {
	return telegramdecision.NewExecApprover(sender, broker, defaultExecApprovalTimeout)
}

func newTelegramDurableMemoryDelegationApprover(sender telegramDecisionSender, broker *decision.Broker) *telegramDurableMemoryDelegationApprover {
	return telegramdecision.NewDurableMemoryDelegationApprover(sender, broker, defaultMemoryDelegationTimeout)
}

func newTelegramDurableSnapshotRestoreApprover(sender telegramDecisionSender, broker *decision.Broker) *telegramDurableSnapshotRestoreApprover {
	return telegramdecision.NewDurableSnapshotRestoreApprover(sender, broker, defaultSnapshotRestoreTimeout)
}

func newTelegramDecisionDurableStore(store *session.SQLiteStore) decision.DurableStore {
	return telegramdecision.NewDurableStore(store)
}

func approvedDecisionConfirmationLabel(kind decision.Kind) string {
	return telegramdecision.ApprovedConfirmationLabel(kind)
}

func inlineButtonRows(pending decision.PendingDecision) [][]telegram.InlineButton {
	return telegramdecision.InlineButtonRows(pending)
}

func inlineButtonRowsExpanded(pending decision.PendingDecision, expanded bool) [][]telegram.InlineButton {
	return telegramdecision.InlineButtonRowsExpanded(pending, expanded)
}

func orderedDecisionChoices(choices []decision.Choice) []decision.Choice {
	return telegramdecision.OrderedDecisionChoices(choices)
}

func renderPendingDecisionSummary(pending decision.PendingDecision) string {
	return telegramdecision.RenderPendingDecisionSummary(pending)
}

func renderPendingDecisionExpanded(pending decision.PendingDecision) string {
	return telegramdecision.RenderPendingDecisionExpanded(pending)
}

func summarizePendingDecision(pending decision.PendingDecision) string {
	return telegramdecision.SummarizePendingDecision(pending)
}

func firstNonEmpty(values ...string) string { return telegramdecision.FirstNonEmpty(values...) }
func compactSentence(text string) string    { return telegramdecision.CompactSentence(text) }
func truncateDecisionSummaryText(text string, limit int) string {
	return telegramdecision.TruncateSummaryText(text, limit)
}

func approvedDecisionConfirmationText(label string, decisionID string, kind decision.Kind, details string) string {
	return telegramdecision.ApprovedConfirmationText(label, decisionID, kind, details)
}

func approvedDecisionConfirmationRows(decisionID string, details string) [][]telegram.InlineButton {
	return telegramdecision.ApprovedConfirmationRows(decisionID, details)
}

func approvedDecisionConfirmationRowsExpanded(decisionID string, details string, expanded bool) [][]telegram.InlineButton {
	return telegramdecision.ApprovedConfirmationRowsExpanded(decisionID, details, expanded)
}

type telegramDecisionResumeStatus = telegramdecision.DecisionResumeStatus

type permanentArtifactKeepCopy = telegramdecision.PermanentArtifactKeepCopy

const (
	telegramDecisionResumeMissing    = telegramdecision.DecisionResumeMissing
	telegramDecisionResumeInProgress = telegramdecision.DecisionResumeInProgress
	telegramDecisionResumeTerminal   = telegramdecision.DecisionResumeTerminal
)

func telegramDecisionResumeUpdateID(msg core.InboundMessage, surface string) int64 {
	return telegramdecision.DecisionResumeUpdateID(msg, surface)
}

func permanentArtifactKeepSubject(msg core.InboundMessage) permanentArtifactKeepCopy {
	return telegramdecision.PermanentArtifactKeepSubject(msg)
}

func artifactRetentionChoices() []decision.Choice { return telegramdecision.ArtifactRetentionChoices() }
func hasArtifactRetentionCandidates(msg core.InboundMessage) bool {
	return telegramdecision.HasArtifactRetentionCandidates(msg)
}
func hasArtifactRetentionApprovalCandidates(msg core.InboundMessage) bool {
	return telegramdecision.HasArtifactRetentionApprovalCandidates(msg)
}
func hasPermanentArtifactKeepCandidates(msg core.InboundMessage) bool {
	return telegramdecision.HasPermanentArtifactKeepCandidates(msg)
}
func artifactRetentionCandidate(artifact core.Artifact) bool {
	return telegramdecision.ArtifactRetentionCandidate(artifact)
}
func artifactNeedsRetentionApproval(artifact core.Artifact) bool {
	return telegramdecision.ArtifactNeedsRetentionApproval(artifact)
}
func formatArtifactRetentionDetails(msg core.InboundMessage) string {
	return telegramdecision.FormatArtifactRetentionDetails(msg)
}
func applyArtifactRetentionChoice(msg core.InboundMessage, choice string) core.InboundMessage {
	return telegramdecision.ApplyArtifactRetentionChoice(msg, choice)
}
func markMediaProcessingAgentDecision(msg core.InboundMessage) core.InboundMessage {
	return telegramdecision.MarkMediaProcessingAgentDecision(msg)
}
func artifactRetentionResolutionText(result decision.Result) string {
	return telegramdecision.ArtifactRetentionResolutionText(result)
}
func encodePermanentArtifactKeepCallbackData(messageID int64) string {
	return telegramdecision.EncodePermanentArtifactKeepCallbackData(messageID)
}
func decodePermanentArtifactKeepCallbackData(data string) (int64, bool) {
	return telegramdecision.DecodePermanentArtifactKeepCallbackData(data)
}
func isStopWord(text string) bool         { return telegramdecision.IsStopWord(text) }
func isOnlyStopWord(text string) bool     { return telegramdecision.IsOnlyStopWord(text) }
func stopChoiceLabel(text string) string  { return telegramdecision.StopChoiceLabel(text) }
func queueChoiceLabel(text string) string { return telegramdecision.QueueChoiceLabel(text) }
func pendingBusyDecisionMessage(record session.PendingBusyDecisionRecord) (core.InboundMessage, error) {
	return telegramdecision.PendingBusyDecisionMessage(record)
}
func pendingArtifactRetentionMessage(record session.PendingArtifactRetentionRecord) (core.InboundMessage, error) {
	return telegramdecision.PendingArtifactRetentionMessage(record)
}
func decisionRecordExpired(createdAt time.Time, timeout time.Duration, now time.Time) bool {
	return telegramdecision.DecisionRecordExpired(createdAt, timeout, now)
}
