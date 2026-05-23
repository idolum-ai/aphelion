//go:build linux

package telegramcommands

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type Sender = commandSender
type CallbackSender = commandCallbackSender
type Router = commandRouter
type ScopedStatusRouter = commandScopedStatusRouter
type ScopedSessionRouter = commandScopedSessionRouter
type ScopedMemoryRouter = commandScopedMemoryRouter
type ThreadRouter = commandThreadRouter
type ThreadUserError = telegramThreadUserError

type MemoryReviewSource = memoryReviewSource
type MemoryReviewSnapshot = memoryReviewSnapshot
type MemoryReviewItem = memoryReviewItem

func HandleTelegramCommand(ctx context.Context, sender Sender, router Router, msg core.InboundMessage) (bool, error) {
	return handleTelegramCommand(ctx, sender, router, msg)
}

func HandleTelegramCommandCallback(ctx context.Context, sender CallbackSender, router Router, cb telegram.CallbackQuery) (bool, error) {
	return handleTelegramCommandCallback(ctx, sender, router, cb)
}

func RegisterTelegramCommands(ctx context.Context, client *telegram.Client) error {
	return registerTelegramCommands(ctx, client)
}

func ParseTelegramCommand(text string) (string, bool) { return parseTelegramCommand(text) }
func RegisteredTelegramCommand(command string) bool   { return registeredTelegramCommand(command) }
func ReplyToMessageID(id int64) *int64                { return replyToMessageID(id) }
func DurableWizardInlineRowsFromText(text string) [][]telegram.InlineButton {
	return durableWizardInlineRowsFromText(text)
}
func CompactThreadPreview(text string) string { return compactThreadPreview(text) }

const TelegramThreadSummaryIngressSurfaceName = telegramThreadSummaryIngressSurface

var DefaultTelegramCommands = defaultTelegramCommands

var _ session.ContinuationState

func ResolveTelegramThreadStartCommand(ctx context.Context, sender Sender, router Router, msg core.InboundMessage) (core.InboundMessage, bool, bool, error) {
	return resolveTelegramThreadStartCommand(ctx, sender, router, msg)
}
func ResolveTelegramThreadPrefix(ctx context.Context, sender Sender, router ThreadRouter, msg core.InboundMessage) (core.InboundMessage, bool, error) {
	return resolveTelegramThreadPrefix(ctx, sender, router, msg)
}
func ResolveTelegramThreadReply(ctx context.Context, sender Sender, router Router, msg core.InboundMessage) (core.InboundMessage, bool, error) {
	return resolveTelegramThreadReply(ctx, sender, router, msg)
}
func HandleTelegramThreadCommand(ctx context.Context, sender Sender, router Router, msg core.InboundMessage, command string) (bool, error) {
	return handleTelegramThreadCommand(ctx, sender, router, msg, command)
}
func EncodeTelegramThreadAbsorbCallback(threadID int64) string {
	return encodeTelegramThreadAbsorbCallback(threadID)
}
func ParseTelegramThreadPrefix(text string) (int64, string, bool) {
	return parseTelegramThreadPrefix(text)
}
func RenderTelegramThreadsPanel(threads []session.TelegramThread, view string, page int) (string, [][]telegram.InlineButton) {
	return renderTelegramThreadsPanel(threads, view, page)
}
func EncodeHealthCallbackData(action string) string { return encodeHealthCallbackData(action) }

const TailnetRevokeTokenCallbackPrefix = tailnetRevokeTokenCallbackPrefix
const TailnetRevokeCallbackAsk = tailnetRevokeCallbackAsk
const TelegramDoctorIngressSurface = telegramDoctorIngressSurface
