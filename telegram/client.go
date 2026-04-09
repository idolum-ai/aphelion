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

	"github.com/idolum-ai/aphelion/core"
)

const defaultPollTimeoutSeconds = 30

type Client struct {
	token       string
	baseURL     string
	httpClient  *http.Client
	pollTimeout int
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
	body := map[string]interface{}{
		"chat_id": msg.ChatID,
		"text":    msg.Text,
	}
	if msg.ParseMode != "" {
		body["parse_mode"] = msg.ParseMode
	}
	if msg.ReplyTo != nil {
		body["reply_to_message_id"] = *msg.ReplyTo
	}
	var resp sendMessageResponse
	if err := c.post(ctx, "sendMessage", body, &resp); err != nil {
		return 0, err
	}
	if !resp.Ok {
		return 0, fmt.Errorf("telegram sendMessage failed: %s", resp.Description)
	}
	return resp.Result.MessageID, nil
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
		return 0, fmt.Errorf("sendVoice unexpected status %d", resp.StatusCode)
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

func (c *Client) DownloadFile(ctx context.Context, fileID string) ([]byte, error) {
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

	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.token, strings.TrimSpace(meta.Result.FilePath))
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
		return nil, fmt.Errorf("download file unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read downloaded file: %w", err)
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
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s unexpected status %d", method, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
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
