//go:build linux

package telegram

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
)

type UpdateHandler func(context.Context, core.InboundMessage) error
type CallbackHandler func(context.Context, CallbackQuery) error
type UnresolvedPrivatePredicate func(*Message) bool

type PollerOption func(*Poller)

type Poller struct {
	client             *Client
	handler            UpdateHandler
	pollTimeoutSeconds int
	resolver           *principal.Resolver
	media              config.TelegramMediaConfig
	durableGroups      map[int64]durableGroupRoute
	botUser            *User
	callbackHandler    CallbackHandler
	allowUnresolvedDM  UnresolvedPrivatePredicate
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

func WithPrincipalResolver(resolver *principal.Resolver) PollerOption {
	return func(p *Poller) {
		p.resolver = resolver
	}
}

func WithMediaConfig(cfg config.TelegramMediaConfig) PollerOption {
	return func(p *Poller) {
		p.media = cfg
	}
}

func WithDurableGroups(groups []config.TelegramDurableGroupConfig) PollerOption {
	return func(p *Poller) {
		p.durableGroups = durableGroupRoutes(groups)
	}
}

func WithBotIdentity(user *User) PollerOption {
	return func(p *Poller) {
		p.botUser = user
	}
}

func WithCallbackHandler(handler CallbackHandler) PollerOption {
	return func(p *Poller) {
		p.callbackHandler = handler
	}
}

func WithUnresolvedPrivatePredicate(predicate UnresolvedPrivatePredicate) PollerOption {
	return func(p *Poller) {
		p.allowUnresolvedDM = predicate
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
			if upd.CallbackQuery != nil {
				if p.resolver != nil && shouldResolveCallbackPrincipal(upd.CallbackQuery) {
					if _, ok := p.resolver.ResolveTelegramUser(senderID(upd.CallbackQuery.From)); !ok {
						if next := upd.UpdateID + 1; next > offset {
							offset = next
						}
						continue
					}
				}
				if err := p.dispatchCallback(ctx, *upd.CallbackQuery); err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					return err
				}
				if next := upd.UpdateID + 1; next > offset {
					offset = next
				}
				continue
			}
			if p.resolver != nil && shouldResolvePrincipal(upd.Message) {
				allowMessage := true
				if _, ok := p.resolver.ResolveTelegramUser(senderID(upd.Message.From)); !ok {
					allowMessage = p.allowUnresolvedPrivateMessage(upd.Message)
				}
				if !allowMessage {
					if next := upd.UpdateID + 1; next > offset {
						offset = next
					}
					continue
				}
			}
			if inbound, err := p.normalizeUpdate(ctx, upd); err != nil {
				return err
			} else if inbound != nil {
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

func (p *Poller) allowUnresolvedPrivateMessage(msg *Message) bool {
	if p == nil || p.allowUnresolvedDM == nil || msg == nil || msg.Chat == nil || msg.Chat.Type != "private" {
		return false
	}
	return p.allowUnresolvedDM(msg)
}

func (p *Poller) dispatchCallback(ctx context.Context, cb CallbackQuery) error {
	if p == nil || p.callbackHandler == nil {
		return nil
	}
	return p.callbackHandler(ctx, cb)
}

func (p *Poller) normalizeUpdate(ctx context.Context, upd Update) (*core.InboundMessage, error) {
	inbound := p.normalizeMessage(upd.Message)
	if inbound == nil {
		return nil, nil
	}
	if upd.Message != nil {
		artifacts, err := p.normalizeArtifacts(ctx, upd.Message)
		if err != nil {
			return nil, err
		}
		inbound.Artifacts = append(inbound.Artifacts, artifacts...)
	}
	return inbound, nil
}

func (p *Poller) normalizeMessage(msg *Message) *core.InboundMessage {
	if msg == nil || msg.Chat == nil {
		return nil
	}
	if route, ok := p.durableGroups[msg.Chat.ID]; ok {
		if inbound := normalizeDurableGroupMessage(msg, route, p.botUser); inbound != nil {
			return inbound
		}
	}
	return NormalizeMessage(msg)
}

func NormalizeMessage(msg *Message) *core.InboundMessage {
	if msg == nil || msg.Chat == nil || msg.Chat.Type != "private" {
		return nil
	}
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" && !hasNormalizableArtifacts(msg) {
		return nil
	}
	return &core.InboundMessage{
		ChatID:     msg.Chat.ID,
		ChatType:   msg.Chat.Type,
		ChatTitle:  strings.TrimSpace(msg.Chat.Title),
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

func shouldResolvePrincipal(msg *Message) bool {
	return msg != nil && msg.Chat != nil && msg.Chat.Type == "private"
}

func shouldResolveCallbackPrincipal(cb *CallbackQuery) bool {
	return cb != nil && cb.Message != nil && cb.Message.Chat != nil && cb.Message.Chat.Type == "private"
}
