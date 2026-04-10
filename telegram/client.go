//go:build linux

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/idolum-ai/aphelion/core"
)

const defaultPollTimeoutSeconds = 30
const telegramTextChunkLimit = 3800

type Client struct {
	token       string
	baseURL     string
	httpClient  *http.Client
	pollTimeout int
}

type FileInfo struct {
	Path string
	Size int64
}

type ClientOption func(*Client)

func NewClient(token string, opts ...ClientOption) *Client {
	base := fmt.Sprintf("https://api.telegram.org/bot%s/", token)
	c := &Client{
		token:       token,
		baseURL:     base,
		httpClient:  http.DefaultClient,
		pollTimeout: defaultPollTimeoutSeconds,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithBaseURL(base string) ClientOption {
	return func(c *Client) {
		if base != "" {
			c.baseURL = base
		}
	}
}

func WithPollTimeout(seconds int) ClientOption {
	return func(c *Client) {
		if seconds > 0 {
			c.pollTimeout = seconds
		}
	}
}

func (c *Client) endpoint(method string) string {
	return fmt.Sprintf("%s%s", c.baseURL, method)
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = c.pollTimeout
	}
	payload := map[string]interface{}{
		"offset":          offset,
		"timeout":         timeoutSeconds,
		"allowed_updates": []string{"message"},
	}
	var resp getUpdatesResponse
	if err := c.post(ctx, "getUpdates", payload, &resp); err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, fmt.Errorf("telegram getUpdates failed: %s", resp.Description)
	}
	return resp.Result, nil
}

func (c *Client) SendMessage(ctx context.Context, msg core.OutboundMessage) (int64, error) {
	if msg.ChatID == 0 {
		return 0, errors.New("chat_id is required")
	}
	chunks := splitTelegramTextChunks(msg.Text, telegramTextChunkLimit)
	if len(chunks) == 0 {
		chunks = []string{""}
	}

	firstMessageID := int64(0)
	for i, chunk := range chunks {
		replyTo := (*int64)(nil)
		if i == 0 {
			replyTo = msg.ReplyTo
		}
		messageID, err := c.sendMessageChunk(ctx, msg.ChatID, chunk, msg.ParseMode, replyTo)
		if err != nil {
			return 0, err
		}
		if firstMessageID == 0 {
			firstMessageID = messageID
		}
	}
	return firstMessageID, nil
}

func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	if len(commands) == 0 {
		return errors.New("commands are required")
	}

	body := map[string]interface{}{
		"commands": commands,
	}
	var resp setMyCommandsResponse
	if err := c.post(ctx, "setMyCommands", body, &resp); err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("telegram setMyCommands failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return errors.New("action is required")
	}

	body := map[string]interface{}{
		"chat_id": chatID,
		"action":  action,
	}
	var resp telegramOKResponse
	if err := c.post(ctx, "sendChatAction", body, &resp); err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("telegram sendChatAction failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error {
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	if messageID == 0 {
		return errors.New("message_id is required")
	}
	formatted := prepareFormattedText(text, parseMode)
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       formatted.Text,
	}
	if formatted.ParseMode != "" {
		body["parse_mode"] = formatted.ParseMode
	}
	resp, err := c.editMessageTextRequest(ctx, body)
	if err != nil {
		return err
	}
	if !resp.Ok {
		if isTelegramMessageNotModified(resp.Description) {
			return nil
		}
		if formatted.ParseMode != "" && isTelegramParseError(resp.Description) {
			return c.editMessageTextFallback(ctx, chatID, messageID, formatted.PlainText)
		}
		return fmt.Errorf("telegram editMessageText failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) DeleteMessage(ctx context.Context, chatID int64, messageID int64) error {
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	if messageID == 0 {
		return errors.New("message_id is required")
	}

	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
	}
	var resp telegramOKResponse
	if err := c.post(ctx, "deleteMessage", body, &resp); err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("telegram deleteMessage failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) SendVoiceMessage(ctx context.Context, chatID int64, media core.Media, replyTo *int64) (int64, error) {
	if chatID == 0 {
		return 0, errors.New("chat_id is required")
	}
	if len(media.Data) == 0 {
		return 0, errors.New("voice data is required")
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
	filename := strings.TrimSpace(media.Filename)
	if filename == "" {
		filename = "reply.ogg"
	}
	part, err := writer.CreateFormFile("voice", filename)
	if err != nil {
		return 0, fmt.Errorf("create voice form file: %w", err)
	}
	if _, err := part.Write(media.Data); err != nil {
		return 0, fmt.Errorf("write voice data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return 0, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("sendVoice"), &body)
	if err != nil {
		return 0, fmt.Errorf("create sendVoice request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("sendVoice request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, telegramHTTPError("sendVoice", resp)
	}

	var decoded sendVoiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, fmt.Errorf("decode sendVoice response: %w", err)
	}
	if !decoded.Ok {
		return 0, fmt.Errorf("telegram sendVoice failed: %s", decoded.Description)
	}
	return decoded.Result.MessageID, nil
}

func (c *Client) GetFileInfo(ctx context.Context, fileID string) (*FileInfo, error) {
	if strings.TrimSpace(fileID) == "" {
		return nil, errors.New("file_id is required")
	}

	var meta getFileResponse
	if err := c.post(ctx, "getFile", map[string]any{"file_id": fileID}, &meta); err != nil {
		return nil, err
	}
	if !meta.Ok || strings.TrimSpace(meta.Result.FilePath) == "" {
		return nil, fmt.Errorf("telegram getFile failed: %s", meta.Description)
	}
	return &FileInfo{
		Path: strings.TrimSpace(meta.Result.FilePath),
		Size: meta.Result.FileSize,
	}, nil
}

func (c *Client) DownloadFile(ctx context.Context, fileID string) ([]byte, error) {
	return c.DownloadFileChecked(ctx, fileID, 0)
}

func (c *Client) DownloadFileChecked(ctx context.Context, fileID string, maxBytes int64) ([]byte, error) {
	info, err := c.GetFileInfo(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && info.Size > 0 && info.Size > maxBytes {
		return nil, fmt.Errorf("telegram file exceeds configured size limit: %d > %d", info.Size, maxBytes)
	}

	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.token, info.Path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create file download request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, telegramHTTPError("downloadFile", resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read downloaded file: %w", err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("telegram downloaded file exceeds configured size limit: %d > %d", len(data), maxBytes)
	}
	return data, nil
}

func (c *Client) post(ctx context.Context, method string, body interface{}, out interface{}) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", method, err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", method, err)
	}
	if err := json.Unmarshal(bodyBytes, out); err != nil {
		if resp.StatusCode != http.StatusOK {
			return telegramHTTPErrorFromBody(method, resp.StatusCode, bodyBytes)
		}
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	return nil
}

func (c *Client) sendMessageRequest(ctx context.Context, body map[string]interface{}) (*sendMessageResponse, error) {
	var resp sendMessageResponse
	if err := c.post(ctx, "sendMessage", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) sendMessageChunk(ctx context.Context, chatID int64, text string, parseMode string, replyTo *int64) (int64, error) {
	formatted := prepareFormattedText(text, parseMode)
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    formatted.Text,
	}
	if formatted.ParseMode != "" {
		body["parse_mode"] = formatted.ParseMode
	}
	if replyTo != nil {
		body["reply_to_message_id"] = *replyTo
	}
	resp, err := c.sendMessageRequest(ctx, body)
	if err != nil {
		return 0, err
	}
	if !resp.Ok {
		if formatted.ParseMode != "" && isTelegramParseError(resp.Description) {
			return c.sendMessageFallback(ctx, chatID, formatted.PlainText, replyTo)
		}
		return 0, fmt.Errorf("telegram sendMessage failed: %s", resp.Description)
	}
	return resp.Result.MessageID, nil
}

func (c *Client) sendMessageFallback(ctx context.Context, chatID int64, text string, replyTo *int64) (int64, error) {
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if replyTo != nil {
		body["reply_to_message_id"] = *replyTo
	}
	resp, err := c.sendMessageRequest(ctx, body)
	if err != nil {
		return 0, err
	}
	if !resp.Ok {
		return 0, fmt.Errorf("telegram sendMessage failed: %s", resp.Description)
	}
	return resp.Result.MessageID, nil
}

func (c *Client) editMessageTextRequest(ctx context.Context, body map[string]interface{}) (*editMessageResponse, error) {
	var resp editMessageResponse
	if err := c.post(ctx, "editMessageText", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) editMessageTextFallback(ctx context.Context, chatID int64, messageID int64, text string) error {
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	resp, err := c.editMessageTextRequest(ctx, body)
	if err != nil {
		return err
	}
	if !resp.Ok && !isTelegramMessageNotModified(resp.Description) {
		return fmt.Errorf("telegram editMessageText failed: %s", resp.Description)
	}
	return nil
}

func buildSenderName(user *User) string {
	if user == nil {
		return ""
	}
	if user.Username != "" {
		return user.Username
	}
	name := user.FirstName
	if user.LastName != "" {
		if name != "" {
			name += " "
		}
		name += user.LastName
	}
	return name
}

func splitTelegramTextChunks(text string, limit int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if limit <= 0 {
		limit = telegramTextChunkLimit
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}

	var chunks []string
	for len(runes) > 0 {
		if len(runes) <= limit {
			chunk := strings.TrimSpace(string(runes))
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}

		split := bestTelegramChunkBoundary(runes, limit)
		if split <= 0 || split > len(runes) {
			split = limit
		}
		chunk := strings.TrimSpace(string(runes[:split]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = trimLeadingTelegramChunkRunes(runes[split:])
	}
	return chunks
}

func bestTelegramChunkBoundary(runes []rune, limit int) int {
	if limit <= 0 || len(runes) <= limit {
		return len(runes)
	}
	for i := limit; i > 0; i-- {
		if i >= 2 && runes[i-2] == '\n' && runes[i-1] == '\n' {
			return i
		}
	}
	for i := limit; i > 0; i-- {
		if runes[i-1] == '\n' {
			return i
		}
	}
	for i := limit; i > 0; i-- {
		if runes[i-1] == ' ' {
			return i
		}
	}
	return limit
}

func trimLeadingTelegramChunkRunes(runes []rune) []rune {
	start := 0
	for start < len(runes) {
		if runes[start] == '\n' || runes[start] == ' ' || runes[start] == '\t' {
			start++
			continue
		}
		break
	}
	return runes[start:]
}

func telegramHTTPError(method string, resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s unexpected status %d (read body: %v)", method, resp.StatusCode, err)
	}
	return telegramHTTPErrorFromBody(method, resp.StatusCode, body)
}

func telegramHTTPErrorFromBody(method string, status int, body []byte) error {
	description := telegramErrorDescription(body)
	if description == "" {
		description = truncateTelegramErrorBody(body)
	}
	if description == "" {
		return fmt.Errorf("%s unexpected status %d", method, status)
	}
	return fmt.Errorf("%s unexpected status %d: %s", method, status, description)
}

func telegramErrorDescription(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Description)
}

func truncateTelegramErrorBody(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= 240 {
		return trimmed
	}
	return string(runes[:239]) + "…"
}

func runeCount(text string) int {
	return utf8.RuneCountInString(text)
}
