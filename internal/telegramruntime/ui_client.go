//go:build linux

package telegramruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/telegram"
)

type telegramUIClient struct {
	*telegram.Client
}

func newTelegramUIClient(client *telegram.Client) *telegramUIClient {
	if client == nil {
		return nil
	}
	return &telegramUIClient{Client: client}
}

func (c *telegramUIClient) SendMessage(ctx context.Context, msg core.OutboundMessage) (int64, error) {
	if c == nil || c.Client == nil {
		return 0, fmt.Errorf("telegram client is unavailable")
	}
	if len(msg.Media) == 0 && strings.TrimSpace(msg.ParseMode) == "" {
		if rows := telegramcommands.DurableWizardInlineRowsFromText(msg.Text); len(rows) > 0 {
			return c.Client.SendInlineKeyboard(ctx, msg.ChatID, msg.Text, rows, msg.ReplyTo)
		}
	}
	return c.Client.SendMessage(ctx, msg)
}
