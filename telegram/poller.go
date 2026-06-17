//go:build linux

package telegram

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"syscall"
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
	pollRetryInitial   time.Duration
	pollRetryMax       time.Duration
	pollRetrySleep     func(context.Context, time.Duration) error
	resolver           *principal.Resolver
	media              config.TelegramMediaConfig
	durableGroups      map[int64]durableGroupRoute
	botUser            *User
	callbackHandler    CallbackHandler
	allowUnresolvedDM  UnresolvedPrivatePredicate
	checkpoint         PollerCheckpoint
	ingressSurface     string
}

func NewPoller(client *Client, handler UpdateHandler, opts ...PollerOption) *Poller {
	p := &Poller{
		client:             client,
		handler:            handler,
		pollTimeoutSeconds: defaultPollTimeoutSeconds,
		pollRetryInitial:   time.Second,
		pollRetryMax:       30 * time.Second,
		pollRetrySleep:     sleepPollRetry,
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

func WithIngressSurface(surface string) PollerOption {
	return func(p *Poller) {
		p.ingressSurface = strings.TrimSpace(surface)
	}
}

func withPollerRetryBackoff(initial time.Duration, max time.Duration) PollerOption {
	return func(p *Poller) {
		if initial > 0 {
			p.pollRetryInitial = initial
		}
		if max > 0 {
			p.pollRetryMax = max
		}
		if p.pollRetryMax < p.pollRetryInitial {
			p.pollRetryMax = p.pollRetryInitial
		}
	}
}

func withPollerRetrySleep(sleep func(context.Context, time.Duration) error) PollerOption {
	return func(p *Poller) {
		if sleep != nil {
			p.pollRetrySleep = sleep
		}
	}
}

func (p *Poller) Run(ctx context.Context) error {
	if p.client == nil || p.handler == nil {
		return errors.New("poller client and handler are required")
	}
	p.normalizePollRetryPolicy()

	offset := int64(0)
	if p.checkpoint != nil {
		next, err := p.checkpoint.NextUpdateID(ctx)
		if err != nil {
			return err
		}
		offset = next
	}
	retryDelay := p.pollRetryInitial
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		updates, err := p.client.GetUpdates(ctx, offset, p.pollTimeoutSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if retryableTelegramPollError(err) {
				delay := telegramPollRetryDelay(err, retryDelay, p.pollRetryMax)
				log.Printf("WARN telegram getUpdates transient failure; retrying after %s err=%v", delay, err)
				if sleepErr := p.pollRetrySleep(ctx, delay); sleepErr != nil {
					if ctx.Err() != nil {
						return nil
					}
					return sleepErr
				}
				retryDelay = nextPollRetryDelay(retryDelay, p.pollRetryMax)
				continue
			}
			return err
		}
		retryDelay = p.pollRetryInitial

		for _, upd := range updates {
			state, err := p.updateState(ctx, upd.UpdateID)
			if err != nil {
				return err
			}
			if state.Terminal {
				offset, err = p.advanceOffset(ctx, offset, upd.UpdateID+1)
				if err != nil {
					return err
				}
				continue
			}
			if upd.MessageReaction != nil {
				if p.resolver != nil && shouldResolveReactionPrincipal(upd.MessageReaction) {
					if _, ok := p.resolver.ResolveTelegramUser(senderID(upd.MessageReaction.User)); !ok {
						if err := p.recordTerminal(ctx, upd, "message_reaction", PollerTerminalSkipped, "unresolved_reaction_principal"); err != nil {
							return err
						}
						offset, err = p.advanceOffset(ctx, offset, upd.UpdateID+1)
						if err != nil {
							return err
						}
						continue
					}
				}
				if inbound := NormalizeMessageReaction(upd.MessageReaction); inbound != nil {
					*inbound = p.bindIngressUpdate(*inbound, upd.UpdateID)
					accepted, err := p.recordAccepted(ctx, upd, "message_reaction", *inbound)
					if err != nil {
						return err
					}
					if !accepted.Dispatch {
						offset, err = p.advanceOffset(ctx, offset, upd.UpdateID+1)
						if err != nil {
							return err
						}
						continue
					}
					if err := p.handler(ctx, *inbound); err != nil {
						if errors.Is(err, context.Canceled) {
							return nil
						}
						if checkpointErr := p.recordFailure(ctx, upd, "message_reaction", err); checkpointErr != nil {
							return checkpointErr
						}
					} else if checkpointErr := p.recordHandled(ctx, upd.UpdateID); checkpointErr != nil {
						return checkpointErr
					}
				} else if err := p.recordTerminal(ctx, upd, "message_reaction", PollerTerminalSkipped, "ignored_message_reaction"); err != nil {
					return err
				}
				offset, err = p.advanceOffset(ctx, offset, upd.UpdateID+1)
				if err != nil {
					return err
				}
				continue
			}
			if upd.CallbackQuery != nil {
				upd.CallbackQuery.UpdateID = upd.UpdateID
				if p.resolver != nil && shouldResolveCallbackPrincipal(upd.CallbackQuery) {
					if _, ok := p.resolver.ResolveTelegramUser(senderID(upd.CallbackQuery.From)); !ok {
						if err := p.recordTerminal(ctx, upd, "callback_query", PollerTerminalSkipped, "unresolved_callback_principal"); err != nil {
							return err
						}
						offset, err = p.advanceOffset(ctx, offset, upd.UpdateID+1)
						if err != nil {
							return err
						}
						continue
					}
				}
				if err := p.dispatchCallback(ctx, *upd.CallbackQuery); err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					log.Printf("WARN telegram callback handler failed update_id=%d callback_id=%s err=%v", upd.UpdateID, strings.TrimSpace(upd.CallbackQuery.ID), err)
					if checkpointErr := p.recordFailure(ctx, upd, "callback_query", err); checkpointErr != nil {
						return checkpointErr
					}
				} else if checkpointErr := p.recordTerminal(ctx, upd, "callback_query", PollerTerminalCompleted, "callback_handled"); checkpointErr != nil {
					return checkpointErr
				}
				offset, err = p.advanceOffset(ctx, offset, upd.UpdateID+1)
				if err != nil {
					return err
				}
				continue
			}
			if p.resolver != nil && shouldResolvePrincipal(upd.Message) {
				allowMessage := true
				if _, ok := p.resolver.ResolveTelegramUser(senderID(upd.Message.From)); !ok {
					allowMessage = p.allowUnresolvedPrivateMessage(upd.Message)
				}
				if !allowMessage {
					if err := p.recordTerminal(ctx, upd, "message", PollerTerminalSkipped, "unresolved_message_principal"); err != nil {
						return err
					}
					offset, err = p.advanceOffset(ctx, offset, upd.UpdateID+1)
					if err != nil {
						return err
					}
					continue
				}
			}
			if inbound, err := p.normalizeUpdate(ctx, upd); err != nil {
				if checkpointErr := p.recordFailure(ctx, upd, "message", err); checkpointErr != nil {
					return checkpointErr
				}
			} else if inbound != nil {
				*inbound = p.bindIngressUpdate(*inbound, upd.UpdateID)
				accepted, err := p.recordAccepted(ctx, upd, "message", *inbound)
				if err != nil {
					return err
				}
				if !accepted.Dispatch {
					offset, err = p.advanceOffset(ctx, offset, upd.UpdateID+1)
					if err != nil {
						return err
					}
					continue
				}
				if err := p.handler(ctx, *inbound); err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					if checkpointErr := p.recordFailure(ctx, upd, "message", err); checkpointErr != nil {
						return checkpointErr
					}
				} else if checkpointErr := p.recordHandled(ctx, upd.UpdateID); checkpointErr != nil {
					return checkpointErr
				}
			} else if err == nil {
				if checkpointErr := p.recordTerminal(ctx, upd, "message", PollerTerminalSkipped, "ignored_message"); checkpointErr != nil {
					return checkpointErr
				}
			}
			offset, err = p.advanceOffset(ctx, offset, upd.UpdateID+1)
			if err != nil {
				return err
			}
		}
	}
}

func (p *Poller) normalizePollRetryPolicy() {
	if p.pollRetryInitial <= 0 {
		p.pollRetryInitial = time.Second
	}
	if p.pollRetryMax <= 0 {
		p.pollRetryMax = 30 * time.Second
	}
	if p.pollRetryMax < p.pollRetryInitial {
		p.pollRetryMax = p.pollRetryInitial
	}
	if p.pollRetrySleep == nil {
		p.pollRetrySleep = sleepPollRetry
	}
}

func sleepPollRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextPollRetryDelay(current time.Duration, max time.Duration) time.Duration {
	if current <= 0 {
		current = time.Second
	}
	if max <= 0 {
		max = 30 * time.Second
	}
	next := current * 2
	if next < current || next > max {
		return max
	}
	return next
}

type timeoutLike interface {
	Timeout() bool
}

type temporaryLike interface {
	Temporary() bool
}

type retryAfterLike interface {
	RetryAfterDelay() time.Duration
}

func retryableTelegramPollError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && (dnsErr.IsTimeout || dnsErr.IsTemporary) {
		return true
	}
	var timeoutErr timeoutLike
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}
	var temporaryErr temporaryLike
	if errors.As(err, &temporaryErr) && temporaryErr.Temporary() {
		return true
	}
	lower := strings.ToLower(err.Error())
	if telegramPollErrorHasNonRetryableMarker(lower) {
		return false
	}
	if telegramPollRetryAfter(err) > 0 {
		return true
	}
	return telegramPollErrorHasRetryableMarker(lower)
}

func telegramPollRetryDelay(err error, fallback time.Duration, max time.Duration) time.Duration {
	if retryAfter := telegramPollRetryAfter(err); retryAfter > 0 {
		if max > 0 && retryAfter > max {
			return max
		}
		return retryAfter
	}
	return fallback
}

func telegramPollRetryAfter(err error) time.Duration {
	var retryAfter retryAfterLike
	if errors.As(err, &retryAfter) {
		return retryAfter.RetryAfterDelay()
	}
	return 0
}

func telegramPollErrorHasNonRetryableMarker(lower string) bool {
	markers := []string{
		"unauthorized",
		"forbidden",
		"unexpected status 401",
		"unexpected status 403",
		"unexpected status 404",
		"unexpected status 409",
		"conflict:",
		"terminated by other getupdates request",
		"webhook is active",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func telegramPollErrorHasRetryableMarker(lower string) bool {
	markers := []string{
		"unexpected status 429",
		"unexpected status 500",
		"unexpected status 502",
		"unexpected status 503",
		"unexpected status 504",
		"too many requests",
		"internal server error",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
		"connection reset by peer",
		"connection refused",
		"connection timed out",
		"connection aborted",
		"broken pipe",
		"unexpected eof",
		"server closed idle connection",
		"tls handshake timeout",
		"i/o timeout",
		"timeout awaiting response headers",
		"temporary failure in name resolution",
		"no such host",
		"network is unreachable",
		"host is unreachable",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (p *Poller) bindIngressUpdate(msg core.InboundMessage, updateID int64) core.InboundMessage {
	msg.IngressUpdateID = updateID
	if p != nil {
		msg.IngressSurface = strings.TrimSpace(p.ingressSurface)
	}
	return msg
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
	hasArtifacts := hasNormalizableArtifacts(msg)
	text := inboundMessageText(msg, hasArtifacts)
	if text == "" && !hasArtifacts {
		return nil
	}
	return &core.InboundMessage{
		ChatID:     msg.Chat.ID,
		ChatType:   msg.Chat.Type,
		ChatTitle:  strings.TrimSpace(msg.Chat.Title),
		SenderID:   senderID(msg.From),
		SenderName: buildSenderName(msg.From),
		Text:       text,
		ReplyTo:    inboundReplyToMessageID(msg),
		MessageID:  msg.MessageID,
		Timestamp:  time.Unix(msg.Date, 0),
		Raw:        msg.Raw,
	}
}

func NormalizeMessageReaction(reaction *MessageReactionUpdated) *core.InboundMessage {
	if reaction == nil || reaction.Chat == nil {
		return nil
	}
	oldReactions := normalizeReactionTypes(reaction.OldReaction)
	newReactions := normalizeReactionTypes(reaction.NewReaction)
	text := "reaction_update"
	if len(newReactions) == 0 {
		text = "reaction_removed"
	}
	text += " message_id=" + strconv.FormatInt(reaction.MessageID, 10)
	if len(oldReactions) > 0 {
		text += " old=" + strings.Join(oldReactions, ",")
	}
	if len(newReactions) > 0 {
		text += " new=" + strings.Join(newReactions, ",")
	}
	return &core.InboundMessage{
		ChatID:     reaction.Chat.ID,
		ChatType:   reaction.Chat.Type,
		ChatTitle:  strings.TrimSpace(reaction.Chat.Title),
		SenderID:   senderID(reaction.User),
		SenderName: buildSenderName(reaction.User),
		Text:       text,
		MessageID:  reaction.MessageID,
		Timestamp:  time.Unix(reaction.Date, 0),
		Reaction: &core.InboundReaction{
			MessageID: reaction.MessageID,
			Old:       oldReactions,
			New:       newReactions,
		},
		Raw: reaction.Raw,
	}
}

func normalizeReactionTypes(values []ReactionType) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		switch strings.TrimSpace(value.Type) {
		case "emoji":
			if emoji := strings.TrimSpace(value.Emoji); emoji != "" {
				out = append(out, emoji)
			}
		case "custom_emoji":
			if id := strings.TrimSpace(value.CustomEmojiID); id != "" {
				out = append(out, "custom_emoji:"+id)
			}
		case "paid":
			out = append(out, "paid")
		}
	}
	return out
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

func shouldResolveReactionPrincipal(reaction *MessageReactionUpdated) bool {
	return reaction != nil && reaction.Chat != nil && reaction.Chat.Type == "private"
}
