//go:build linux

package telegram

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
)

type UpdateHandler func(context.Context, core.InboundMessage) error

type PollerOption func(*Poller)

type Poller struct {
	client             *Client
	handler            UpdateHandler
	pollTimeoutSeconds int
	resolver           *principal.Resolver
	media              config.TelegramMediaConfig
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
			if p.resolver != nil && shouldResolvePrincipal(upd.Message) {
				if _, ok := p.resolver.ResolveTelegramUser(senderID(upd.Message.From)); !ok {
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

func (p *Poller) normalizeUpdate(ctx context.Context, upd Update) (*core.InboundMessage, error) {
	inbound := NormalizeMessage(upd.Message)
	if inbound == nil {
		return nil, nil
	}
	if upd.Message != nil && p.client != nil {
		maxBytes, _ := config.ParseByteSize(p.media.DownloadMaxSize)
		if upd.Message.Voice != nil {
			data, err := p.client.DownloadFileChecked(ctx, upd.Message.Voice.FileID, maxBytes)
			if err != nil {
				return nil, fmt.Errorf("download telegram voice %s: %w", upd.Message.Voice.FileID, err)
			}
			inbound.Media = append(inbound.Media, core.Media{
				Type:     "voice",
				Data:     data,
				MimeType: upd.Message.Voice.MimeType,
				Filename: "voice.ogg",
			})
		}
		if p.media.AutoVisionPhotos && len(upd.Message.Photo) > 0 {
			largest := upd.Message.Photo[len(upd.Message.Photo)-1]
			data, err := p.client.DownloadFileChecked(ctx, largest.FileID, maxBytes)
			if err != nil {
				return nil, fmt.Errorf("download telegram photo %s: %w", largest.FileID, err)
			}
			inbound.Media = append(inbound.Media, core.Media{
				Type:     "photo",
				Data:     data,
				MimeType: "image/jpeg",
				Filename: "photo.jpg",
			})
		}
		if doc := upd.Message.Document; doc != nil {
			if mediaType, filename, ok := normalizeSupportedDocument(doc, p.media); ok {
				data, err := p.client.DownloadFileChecked(ctx, doc.FileID, maxBytes)
				if err != nil {
					return nil, fmt.Errorf("download telegram document %s: %w", doc.FileID, err)
				}
				inbound.Media = append(inbound.Media, core.Media{
					Type:     "document",
					Data:     data,
					MimeType: mediaType,
					Filename: filename,
				})
			}
		}
	}
	return inbound, nil
}

func NormalizeMessage(msg *Message) *core.InboundMessage {
	if msg == nil || msg.Chat == nil || msg.Chat.Type != "private" {
		return nil
	}
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" && msg.Voice == nil && len(msg.Photo) == 0 && !supportedDocumentMessage(msg.Document) {
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

func supportedDocumentMessage(doc *Document) bool {
	if doc == nil {
		return false
	}
	if mediaType, _, ok := normalizeSupportedDocument(doc, config.TelegramMediaConfig{
		AutoVisionDocs: true,
		ExtractPDFText: true,
	}); ok && mediaType != "" {
		return true
	}
	return false
}

func normalizeSupportedDocument(doc *Document, cfg config.TelegramMediaConfig) (string, string, bool) {
	if doc == nil {
		return "", "", false
	}
	mediaType := strings.ToLower(strings.TrimSpace(doc.MimeType))
	filename := strings.TrimSpace(doc.FileName)
	ext := strings.ToLower(filepath.Ext(filename))
	switch {
	case cfg.AutoVisionDocs && (strings.HasPrefix(mediaType, "image/") || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp"):
		if mediaType == "" {
			switch ext {
			case ".png":
				mediaType = "image/png"
			case ".webp":
				mediaType = "image/webp"
			default:
				mediaType = "image/jpeg"
			}
		}
		if filename == "" {
			filename = "document-image"
		}
		return mediaType, filename, true
	case cfg.ExtractPDFText && (mediaType == "application/pdf" || ext == ".pdf"):
		if filename == "" {
			filename = "document.pdf"
		}
		return "application/pdf", filename, true
	default:
		return "", "", false
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
