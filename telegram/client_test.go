//go:build linux

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
)

type testTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (t testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.roundTrip(req)
}

func encodeJSONResponse(t *testing.T, v interface{}) *http.Response {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     http.Header{"Content-Type": {"application/json"}},
	}
}

func TestNormalizeMessagePrivate(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 10,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice", FirstName: "Alice"},
		Text:      "hello",
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected message to be normalized")
	}
	if got.ChatID != 7 {
		t.Fatalf("chat id = %d, want 7", got.ChatID)
	}
	if got.SenderName != "alice" {
		t.Fatalf("sender name = %q, want %q", got.SenderName, "alice")
	}
	if got.Text != "hello" {
		t.Fatalf("text = %q, want %q", got.Text, "hello")
	}
	if got.MessageID != 10 {
		t.Fatalf("message id = %d, want 10", got.MessageID)
	}
	if got.Timestamp.Unix() != now {
		t.Fatalf("timestamp = %v, want unix %d", got.Timestamp, now)
	}
}

func TestNormalizeMessageSkipsNonPrivate(t *testing.T) {
	msg := &Message{
		Chat: &Chat{ID: 1, Type: "group"},
		Text: "hi",
	}
	if NormalizeMessage(msg) != nil {
		t.Fatal("expected non-private message to be ignored")
	}
}

func TestNormalizeMessageVoiceOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 11,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Voice:     &Voice{FileID: "voice-file", MimeType: "audio/ogg"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected voice message to be normalized")
	}
	if got.Text != "" {
		t.Fatalf("text = %q, want empty for voice-only input", got.Text)
	}
}

func TestSendMessagePayload(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/sendMessage" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &requestBody); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			resp := sendMessageResponse{Ok: true}
			resp.Result.MessageID = 123
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	val := int64(11)
	reply := core.OutboundMessage{
		ChatID:  5,
		Text:    "reply",
		ReplyTo: &val,
	}
	got, err := client.SendMessage(context.Background(), reply)
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if got != 123 {
		t.Fatalf("message id = %d, want 123", got)
	}
	if requestBody["chat_id"] != float64(5) {
		t.Fatalf("chat_id = %v, want 5", requestBody["chat_id"])
	}
	if requestBody["text"] != "reply" {
		t.Fatalf("text = %v, want reply", requestBody["text"])
	}
	if _, ok := requestBody["reply_to_message_id"]; !ok {
		t.Fatal("missing reply_to_message_id")
	}
}

func TestSendChatActionPayload(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/sendChatAction" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &requestBody); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			return encodeJSONResponse(t, telegramOKResponse{Ok: true}), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	if err := client.SendChatAction(context.Background(), 5, "typing"); err != nil {
		t.Fatalf("SendChatAction() err = %v", err)
	}
	if requestBody["chat_id"] != float64(5) {
		t.Fatalf("chat_id = %v, want 5", requestBody["chat_id"])
	}
	if requestBody["action"] != "typing" {
		t.Fatalf("action = %v, want typing", requestBody["action"])
	}
}

func TestSetMyCommandsPayload(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/setMyCommands" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &requestBody); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			return encodeJSONResponse(t, setMyCommandsResponse{Ok: true}), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	err := client.SetMyCommands(context.Background(), []BotCommand{
		{Command: "start", Description: "Show intro"},
		{Command: "stop", Description: "Stop current work"},
	})
	if err != nil {
		t.Fatalf("SetMyCommands() err = %v", err)
	}

	rawCommands, ok := requestBody["commands"].([]interface{})
	if !ok || len(rawCommands) != 2 {
		t.Fatalf("commands = %#v, want two commands", requestBody["commands"])
	}
	first, ok := rawCommands[0].(map[string]interface{})
	if !ok {
		t.Fatalf("first command = %#v, want object", rawCommands[0])
	}
	if first["command"] != "start" {
		t.Fatalf("first command = %v, want start", first["command"])
	}
}

func TestEditMessageTextPayload(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/editMessageText" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &requestBody); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			return encodeJSONResponse(t, editMessageResponse{Ok: true}), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	if err := client.EditMessageText(context.Background(), 5, 42, "working..."); err != nil {
		t.Fatalf("EditMessageText() err = %v", err)
	}
	if requestBody["chat_id"] != float64(5) {
		t.Fatalf("chat_id = %v, want 5", requestBody["chat_id"])
	}
	if requestBody["message_id"] != float64(42) {
		t.Fatalf("message_id = %v, want 42", requestBody["message_id"])
	}
	if requestBody["text"] != "working..." {
		t.Fatalf("text = %v, want working...", requestBody["text"])
	}
}

func TestDeleteMessagePayload(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/deleteMessage" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &requestBody); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			return encodeJSONResponse(t, telegramOKResponse{Ok: true}), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	if err := client.DeleteMessage(context.Background(), 5, 42); err != nil {
		t.Fatalf("DeleteMessage() err = %v", err)
	}
	if requestBody["chat_id"] != float64(5) {
		t.Fatalf("chat_id = %v, want 5", requestBody["chat_id"])
	}
	if requestBody["message_id"] != float64(42) {
		t.Fatalf("message_id = %v, want 42", requestBody["message_id"])
	}
}

func TestSendVoiceMessagePayload(t *testing.T) {
	var contentType string
	var payload string
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/sendVoice" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			contentType = req.Header.Get("Content-Type")
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			payload = string(data)
			resp := sendVoiceResponse{Ok: true}
			resp.Result.MessageID = 456
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	got, err := client.SendVoiceMessage(context.Background(), 5, core.Media{
		Type:     "voice",
		Data:     []byte("voice-bytes"),
		MimeType: "audio/mpeg",
		Filename: "reply.mp3",
	}, nil)
	if err != nil {
		t.Fatalf("SendVoiceMessage() err = %v", err)
	}
	if got != 456 {
		t.Fatalf("message id = %d, want 456", got)
	}
	if !strings.Contains(contentType, "multipart/form-data") {
		t.Fatalf("content-type = %q, want multipart", contentType)
	}
	if !strings.Contains(payload, "voice-bytes") {
		t.Fatalf("payload missing voice bytes: %s", payload)
	}
}

func TestDownloadFile(t *testing.T) {
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://api.telegram.org/botTOKEN/getFile":
				return encodeJSONResponse(t, getFileResponse{
					Ok: true,
					Result: struct {
						FilePath string `json:"file_path"`
					}{FilePath: "voice/file.ogg"},
				}), nil
			case "https://api.telegram.org/file/botTOKEN/voice/file.ogg":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("voice-bytes")),
				}, nil
			default:
				t.Fatalf("unexpected url %s", req.URL.String())
				return nil, nil
			}
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	data, err := client.DownloadFile(context.Background(), "file123")
	if err != nil {
		t.Fatalf("DownloadFile() err = %v", err)
	}
	if string(data) != "voice-bytes" {
		t.Fatalf("data = %q, want voice-bytes", string(data))
	}
}

func TestPollerProcessesPrivateMessagesOnly(t *testing.T) {
	now := time.Now().Unix()
	updates := []Update{
		{
			UpdateID: 5,
			Message: &Message{
				MessageID: 2,
				Chat:      &Chat{ID: 1, Type: "private"},
				From:      &User{ID: 1, Username: "keeper"},
				Text:      "private",
				Date:      now,
			},
		},
		{
			UpdateID: 6,
			Message: &Message{
				MessageID: 3,
				Chat:      &Chat{ID: 1, Type: "group"},
				Text:      "group",
				Date:      now + 1,
			},
		},
	}

	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/getUpdates" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			call++
			resp := getUpdatesResponse{Ok: true}
			if call == 1 {
				resp.Result = updates
			}
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handled := make([]core.InboundMessage, 0, 1)
	handler := func(ctx context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg)
		cancel()
		return nil
	}

	poller := NewPoller(client, handler, WithPollerTimeout(1))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("poller failed: %v", err)
	}

	if len(handled) != 1 {
		t.Fatalf("handled %d messages, want 1", len(handled))
	}
	if handled[0].Text != "private" {
		t.Fatalf("handled text = %q, want %q", handled[0].Text, "private")
	}
}

func TestPollerDropsUnknownPrincipalMessages(t *testing.T) {
	now := time.Now().Unix()
	updates := []Update{
		{
			UpdateID: 1,
			Message: &Message{
				MessageID: 10,
				Chat:      &Chat{ID: 11, Type: "private"},
				From:      &User{ID: 999, Username: "unknown"},
				Text:      "blocked",
				Date:      now,
			},
		},
		{
			UpdateID: 2,
			Message: &Message{
				MessageID: 20,
				Chat:      &Chat{ID: 21, Type: "private"},
				From:      &User{ID: 123, Username: "admin"},
				Text:      "allowed",
				Date:      now + 1,
			},
		},
	}

	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/getUpdates" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			call++
			resp := getUpdatesResponse{Ok: true}
			if call == 1 {
				resp.Result = updates
			}
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	resolver := principal.NewResolver([]int64{123}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handled := make([]core.InboundMessage, 0, 1)
	handler := func(ctx context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg)
		cancel()
		return nil
	}

	poller := NewPoller(
		client,
		handler,
		WithPollerTimeout(1),
		WithPrincipalResolver(resolver),
	)
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("poller failed: %v", err)
	}

	if len(handled) != 1 {
		t.Fatalf("handled %d messages, want 1", len(handled))
	}
	if handled[0].SenderID != 123 {
		t.Fatalf("sender id = %d, want 123", handled[0].SenderID)
	}
	if handled[0].Text != "allowed" {
		t.Fatalf("text = %q, want allowed", handled[0].Text)
	}
}
