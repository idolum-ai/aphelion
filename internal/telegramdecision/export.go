//go:build linux

package telegramdecision

import (
	"context"

	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func NewDurableStore(store *session.SQLiteStore) decision.DurableStore {
	return newTelegramDecisionDurableStore(store)
}

func ApprovedConfirmationLabel(kind decision.Kind) string {
	return approvedDecisionConfirmationLabel(kind)
}
func InlineButtonRows(pending decision.PendingDecision) [][]telegram.InlineButton {
	return inlineButtonRows(pending)
}
func InlineButtonRowsExpanded(pending decision.PendingDecision, expanded bool) [][]telegram.InlineButton {
	return inlineButtonRowsExpanded(pending, expanded)
}
func OrderedDecisionChoices(choices []decision.Choice) []decision.Choice {
	return orderedDecisionChoices(choices)
}
func RenderPendingDecisionSummary(pending decision.PendingDecision) string {
	return renderPendingDecisionSummary(pending)
}
func RenderPendingDecisionExpanded(pending decision.PendingDecision) string {
	return renderPendingDecisionExpanded(pending)
}
func SummarizePendingDecision(pending decision.PendingDecision) string {
	return summarizePendingDecision(pending)
}
func FirstNonEmpty(values ...string) string { return firstNonEmpty(values...) }
func CompactSentence(text string) string    { return compactSentence(text) }
func TruncateSummaryText(text string, limit int) string {
	return truncateDecisionSummaryText(text, limit)
}

func ApprovedConfirmationText(label string, decisionID string, kind decision.Kind, details string) string {
	return approvedDecisionConfirmationText(label, decisionID, kind, details)
}

func ApprovedConfirmationRows(decisionID string, details string) [][]telegram.InlineButton {
	return approvedDecisionConfirmationRows(decisionID, details)
}

func ApprovedConfirmationRowsExpanded(decisionID string, details string, expanded bool) [][]telegram.InlineButton {
	return approvedDecisionConfirmationRowsExpanded(decisionID, details, expanded)
}

func FormatExecProposalDetails(req toolpkg.ExecApprovalRequest) string {
	return formatExecProposalDetails(req)
}
func FormatDurableMemoryDelegationDetails(req toolpkg.DurableMemoryDelegationApprovalRequest) string {
	return formatDurableMemoryDelegationDetails(req)
}
func FormatDurableSnapshotRestoreDetails(req toolpkg.DurableSnapshotRestoreApprovalRequest) string {
	return formatDurableSnapshotRestoreDetails(req)
}

var _ = context.Background
