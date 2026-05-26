//go:build linux

package main

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type commandSender = telegramcommands.Sender
type commandCallbackSender = telegramcommands.CallbackSender
type commandRouter = telegramcommands.Router
type commandScopedStatusRouter = telegramcommands.ScopedStatusRouter
type commandScopedSessionRouter = telegramcommands.ScopedSessionRouter
type commandScopedMemoryRouter = telegramcommands.ScopedMemoryRouter
type commandThreadRouter = telegramcommands.ThreadRouter
type telegramThreadUserError = telegramcommands.ThreadUserError

type memoryReviewSource = telegramcommands.MemoryReviewSource
type memoryReviewSnapshot = telegramcommands.MemoryReviewSnapshot
type memoryReviewItem = telegramcommands.MemoryReviewItem

func handleTelegramCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	return telegramcommands.HandleTelegramCommand(ctx, sender, router, msg)
}

func handleTelegramCommandCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
	return telegramcommands.HandleTelegramCommandCallback(ctx, sender, router, cb)
}

func registerTelegramCommands(ctx context.Context, client *telegram.Client) error {
	return telegramcommands.RegisterTelegramCommands(ctx, client)
}

func parseTelegramCommand(text string) (string, bool) {
	return telegramcommands.ParseTelegramCommand(text)
}
func registeredTelegramCommand(command string) bool {
	return telegramcommands.RegisteredTelegramCommand(command)
}
func replyToMessageID(id int64) *int64 { return telegramcommands.ReplyToMessageID(id) }
func durableWizardInlineRowsFromText(text string) [][]telegram.InlineButton {
	return telegramcommands.DurableWizardInlineRowsFromText(text)
}
func compactThreadPreview(text string) string { return telegramcommands.CompactThreadPreview(text) }

func resolveTelegramThreadStartCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (core.InboundMessage, bool, bool, error) {
	return telegramcommands.ResolveTelegramThreadStartCommand(ctx, sender, router, msg)
}
func resolveTelegramThreadPrefix(ctx context.Context, sender commandSender, router commandThreadRouter, msg core.InboundMessage) (core.InboundMessage, bool, error) {
	return telegramcommands.ResolveTelegramThreadPrefix(ctx, sender, router, msg)
}
func resolveTelegramThreadReply(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (core.InboundMessage, bool, error) {
	return telegramcommands.ResolveTelegramThreadReply(ctx, sender, router, msg)
}
func resolveTelegramAgentReply(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	return telegramcommands.ResolveTelegramAgentReply(ctx, sender, router, msg)
}
func handleTelegramThreadCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage, command string) (bool, error) {
	return telegramcommands.HandleTelegramThreadCommand(ctx, sender, router, msg, command)
}
func encodeTelegramThreadPromoteCallback(threadID int64) string {
	return telegramcommands.EncodeTelegramThreadPromoteCallback(threadID)
}
func encodeTelegramThreadAbsorbCallback(threadID int64) string {
	return telegramcommands.EncodeTelegramThreadAbsorbCallback(threadID)
}
func parseTelegramThreadPrefix(text string) (int64, string, bool) {
	return telegramcommands.ParseTelegramThreadPrefix(text)
}
func renderTelegramThreadsPanel(threads []session.TelegramThread, view string, page int) (string, [][]telegram.InlineButton) {
	return telegramcommands.RenderTelegramThreadsPanel(threads, view, page)
}
func encodeHealthCallbackData(action string) string {
	return telegramcommands.EncodeHealthCallbackData(action)
}

const tailnetRevokeTokenCallbackPrefix = telegramcommands.TailnetRevokeTokenCallbackPrefix
const tailnetRevokeCallbackAsk = telegramcommands.TailnetRevokeCallbackAsk
