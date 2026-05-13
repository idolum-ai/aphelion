//go:build linux

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func (c *Client) SendVoiceMessage(ctx context.Context, chatID int64, media core.Media, replyTo *int64) (int64, error) {
	return c.sendMultipartMediaMessage(ctx, "sendVoice", "voice", chatID, media, "", replyTo)
}

func (c *Client) SendPhotoMessage(ctx context.Context, chatID int64, media core.Media, caption string, replyTo *int64) (int64, error) {
	return c.sendMultipartMediaMessage(ctx, "sendPhoto", "photo", chatID, media, caption, replyTo)
}

func (c *Client) SendDocumentMessage(ctx context.Context, chatID int64, media core.Media, caption string, replyTo *int64) (int64, error) {
	return c.sendMultipartMediaMessage(ctx, "sendDocument", "document", chatID, media, caption, replyTo)
}

func (c *Client) SendVideoMessage(ctx context.Context, chatID int64, media core.Media, caption string, replyTo *int64) (int64, error) {
	return c.sendMultipartMediaMessage(ctx, "sendVideo", "video", chatID, media, caption, replyTo)
}

func (c *Client) SendAudioMessage(ctx context.Context, chatID int64, media core.Media, caption string, replyTo *int64) (int64, error) {
	return c.sendMultipartMediaMessage(ctx, "sendAudio", "audio", chatID, media, caption, replyTo)
}

func (c *Client) SendAnimationMessage(ctx context.Context, chatID int64, media core.Media, caption string, replyTo *int64) (int64, error) {
	return c.sendMultipartMediaMessage(ctx, "sendAnimation", "animation", chatID, media, caption, replyTo)
}

func (c *Client) sendMediaItem(ctx context.Context, chatID int64, media core.Media, caption string, replyTo *int64) (int64, error) {
	method, fieldName := classifyTelegramMedia(media)
	switch method {
	case "sendPhoto":
		return c.SendPhotoMessage(ctx, chatID, media, caption, replyTo)
	case "sendVideo":
		return c.SendVideoMessage(ctx, chatID, media, caption, replyTo)
	case "sendAudio":
		return c.SendAudioMessage(ctx, chatID, media, caption, replyTo)
	case "sendVoice":
		return c.sendMultipartMediaMessage(ctx, method, fieldName, chatID, media, caption, replyTo)
	case "sendAnimation":
		return c.SendAnimationMessage(ctx, chatID, media, caption, replyTo)
	default:
		return c.SendDocumentMessage(ctx, chatID, media, caption, replyTo)
	}
}

func (c *Client) sendMultipartMediaMessage(ctx context.Context, method string, fieldName string, chatID int64, media core.Media, caption string, replyTo *int64) (int64, error) {
	if chatID == 0 {
		return 0, errors.New("chat_id is required")
	}
	data, err := readOutboundMediaBytes(media)
	if err != nil {
		return 0, err
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", fmt.Sprintf("%d", chatID)); err != nil {
		return 0, fmt.Errorf("write chat_id: %w", err)
	}
	if replyTo != nil {
		if err := writer.WriteField("reply_to_message_id", fmt.Sprintf("%d", *replyTo)); err != nil {
			return 0, fmt.Errorf("write reply_to_message_id: %w", err)
		}
	}
	if strings.TrimSpace(caption) != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return 0, fmt.Errorf("write caption: %w", err)
		}
	}
	filename := mediaFilename(media, fieldName)
	part, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		return 0, fmt.Errorf("create %s form file: %w", fieldName, err)
	}
	if _, err := part.Write(data); err != nil {
		return 0, fmt.Errorf("write %s data: %w", fieldName, err)
	}
	if err := writer.Close(); err != nil {
		return 0, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), &body)
	if err != nil {
		return 0, fmt.Errorf("create %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s request failed: %w", method, c.redactError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, telegramHTTPError(method, resp)
	}

	var decoded sendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, fmt.Errorf("decode %s response: %w", method, err)
	}
	if !decoded.Ok {
		return 0, fmt.Errorf("telegram %s failed: %s", method, decoded.Description)
	}
	return decoded.Result.MessageID, nil
}

func splitTelegramCaption(text string) (string, string) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if trimmed == "" {
		return "", ""
	}
	runes := []rune(trimmed)
	if len(runes) <= telegramCaptionLimit {
		return trimmed, ""
	}
	return string(runes[:telegramCaptionLimit]), strings.TrimSpace(string(runes[telegramCaptionLimit:]))
}

func readOutboundMediaBytes(media core.Media) ([]byte, error) {
	if len(media.Data) > 0 {
		return media.Data, nil
	}
	path := strings.TrimSpace(media.Path)
	if path == "" {
		return nil, errors.New("media data or path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read media file %q: %w", path, err)
	}
	return data, nil
}

func mediaFilename(media core.Media, fieldName string) string {
	filename := strings.TrimSpace(media.Filename)
	if filename != "" {
		return filename
	}
	if path := strings.TrimSpace(media.Path); path != "" {
		return filepath.Base(path)
	}
	switch fieldName {
	case "voice":
		return "reply.ogg"
	case "photo":
		return "reply.png"
	case "video":
		return "reply.mp4"
	case "audio":
		return "reply.mp3"
	case "animation":
		return "reply.gif"
	default:
		return "reply.bin"
	}
}

func classifyTelegramMedia(media core.Media) (string, string) {
	kind := strings.ToLower(strings.TrimSpace(media.Type))
	if kind == "" {
		kind = classifyTelegramMediaByFile(media)
	}
	switch kind {
	case "image", "photo":
		return "sendPhoto", "photo"
	case "video":
		return "sendVideo", "video"
	case "audio":
		return "sendAudio", "audio"
	case "voice":
		return "sendVoice", "voice"
	case "animation":
		return "sendAnimation", "animation"
	default:
		return "sendDocument", "document"
	}
}

func classifyTelegramMediaByFile(media core.Media) string {
	mimeType := strings.ToLower(strings.TrimSpace(media.MimeType))
	if mimeType == "" {
		name := mediaFilename(media, "document")
		mimeType = strings.ToLower(strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))))
	}
	ext := strings.ToLower(filepath.Ext(mediaFilename(media, "document")))
	switch {
	case ext == ".gif" || mimeType == "image/gif":
		return "animation"
	case strings.HasPrefix(mimeType, "image/"), isTelegramImageExtension(ext):
		return "image"
	case strings.HasPrefix(mimeType, "video/"), isTelegramVideoExtension(ext):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"), isTelegramAudioExtension(ext):
		return "audio"
	default:
		return "document"
	}
}

func isTelegramImageExtension(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp":
		return true
	default:
		return false
	}
}

func isTelegramVideoExtension(ext string) bool {
	switch ext {
	case ".mp4", ".mov", ".avi", ".mkv", ".webm", ".3gp":
		return true
	default:
		return false
	}
}

func isTelegramAudioExtension(ext string) bool {
	switch ext {
	case ".ogg", ".opus", ".mp3", ".wav", ".m4a", ".flac":
		return true
	default:
		return false
	}
}
