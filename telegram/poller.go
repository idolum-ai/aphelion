//go:build linux

package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type UpdateHandler func(context.Context, core.InboundMessage) error

type PollerOption func(*Poller)

type Poller struct {
	client             *Client
	handler            UpdateHandler
	pollTimeoutSeconds int
}

func NewPoller(client *Client, handler UpdateHandler, opts ...PollerOption) *Poller {
	p := &Poller{
		client:             client,
		handler:            handler,
		pollTimeoutSeconds: defaultPollTimeoutSeconds,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func WithPollerTimeout(seconds int) PollerOption {
	return func(p *Poller) {
		if seconds > 0 {
			p.pollTimeoutSeconds = seconds
		}
	}
}

func (p *Poller) Run(ctx context.Context) error {
	if p.client == nil || p.handler == nil {
		return errors.New("poller client and handler are required")
	}

	offset := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		updates, err := p.client.GetUpdates(ctx, offset, p.pollTimeoutSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		for _, upd := range updates {
			if inbound := NormalizeMessage(upd.Message); inbound != nil {
				if err := p.handler(ctx, *inbound); err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					return err
				}
			}
			if next := upd.UpdateID + 1; next > offset {
				offset = next
			}
		}
	}
}

func NormalizeMessage(msg *Message) *core.InboundMessage {
	if msg == nil || msg.Chat == nil || msg.Chat.Type != "private" {
		return nil
	}
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
		return nil
	}
	return &core.InboundMessage{
		ChatID:     msg.Chat.ID,
		SenderID:   senderID(msg.From),
		SenderName: buildSenderName(msg.From),
		Text:       text,
		MessageID:  msg.MessageID,
		Timestamp:  time.Unix(msg.Date, 0),
		Raw:        msg.Raw,
	}
}

func senderID(user *User) int64 {
	if user == nil {
		return 0
	}
	return user.ID
}
