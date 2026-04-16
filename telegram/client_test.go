//go:build linux

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
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

func encodeHTTPJSONResponse(t *testing.T, status int, v interface{}) *http.Response {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
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

func TestNormalizeMessagePhotoOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 12,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Photo:     []PhotoSize{{FileID: "p1", FileSize: 123}, {FileID: "p2", FileSize: 456}},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected photo message to be normalized")
	}
	if got.Text != "" {
		t.Fatalf("text = %q, want empty for photo-only input", got.Text)
	}
}

func TestNormalizeMessagePDFDocumentOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 13,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Document:  &Document{FileID: "doc1", FileName: "notes.pdf", MimeType: "application/pdf"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected pdf document message to be normalized")
	}
}

func TestNormalizeMessageAudioOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 14,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Audio:     &Audio{FileID: "audio1", FileName: "memo.mp3", MimeType: "audio/mpeg"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected audio message to be normalized")
	}
	if got.Text != "" {
		t.Fatalf("text = %q, want empty for audio-only input", got.Text)
	}
}

func TestNormalizeMessageVideoOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 15,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Video:     &Video{FileID: "video1", FileName: "clip.mp4", MimeType: "video/mp4"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected video message to be normalized")
	}
	if got.Text != "" {
		t.Fatalf("text = %q, want empty for video-only input", got.Text)
	}
}

func TestNormalizeMessageStickerOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 16,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Sticker:   &Sticker{FileID: "sticker1", MimeType: "image/webp"},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected sticker message to be normalized")
	}
}

func TestNormalizeMessageLocationOnly(t *testing.T) {
	now := time.Now().Unix()
	msg := &Message{
		MessageID: 17,
		Date:      now,
		Chat:      &Chat{ID: 7, Type: "private"},
		From:      &User{ID: 3, Username: "alice"},
		Location:  &Location{Latitude: 40.0, Longitude: -73.0},
	}

	got := NormalizeMessage(msg)
	if got == nil {
		t.Fatal("expected location message to be normalized")
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
	if _, ok := requestBody["parse_mode"]; ok {
		t.Fatalf("parse_mode = %v, want omitted", requestBody["parse_mode"])
	}
}

func TestSendInlineKeyboardPayload(t *testing.T) {
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
			resp.Result.MessageID = 222
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	replyTo := int64(99)
	got, err := client.SendInlineKeyboard(context.Background(), 5, "Choose", [][]InlineButton{
		{
			{Text: "Approve", CallbackData: "decision:1:approve"},
			{Text: "Deny", CallbackData: "decision:1:deny"},
		},
	}, &replyTo)
	if err != nil {
		t.Fatalf("SendInlineKeyboard() err = %v", err)
	}
	if got != 222 {
		t.Fatalf("message id = %d, want 222", got)
	}
	if requestBody["text"] != "Choose" {
		t.Fatalf("text = %v, want Choose", requestBody["text"])
	}
	if requestBody["reply_to_message_id"] != float64(99) {
		t.Fatalf("reply_to_message_id = %v, want 99", requestBody["reply_to_message_id"])
	}
	replyMarkup, ok := requestBody["reply_markup"].(map[string]interface{})
	if !ok {
		t.Fatalf("reply_markup = %#v, want object", requestBody["reply_markup"])
	}
	rows, ok := replyMarkup["inline_keyboard"].([]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("inline_keyboard = %#v, want 1 row", replyMarkup["inline_keyboard"])
	}
}

func TestAnswerCallbackQueryPayload(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/answerCallbackQuery" {
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

	if err := client.AnswerCallbackQuery(context.Background(), "cb-1", "ok"); err != nil {
		t.Fatalf("AnswerCallbackQuery() err = %v", err)
	}
	if requestBody["callback_query_id"] != "cb-1" {
		t.Fatalf("callback_query_id = %v, want cb-1", requestBody["callback_query_id"])
	}
	if requestBody["text"] != "ok" {
		t.Fatalf("text = %v, want ok", requestBody["text"])
	}
}

func TestGetUpdatesRequestsCallbackQueries(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/getUpdates" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &requestBody); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			return encodeJSONResponse(t, getUpdatesResponse{Ok: true, Result: []Update{}}), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	if _, err := client.GetUpdates(context.Background(), 10, 15); err != nil {
		t.Fatalf("GetUpdates() err = %v", err)
	}

	allowed, ok := requestBody["allowed_updates"].([]interface{})
	if !ok {
		t.Fatalf("allowed_updates = %#v, want array", requestBody["allowed_updates"])
	}
	foundMessage := false
	foundCallback := false
	for _, raw := range allowed {
		if raw == "message" {
			foundMessage = true
		}
		if raw == "callback_query" {
			foundCallback = true
		}
	}
	if !foundMessage || !foundCallback {
		t.Fatalf("allowed_updates = %#v, want message and callback_query", allowed)
	}
}

func TestSendMessageChunksLongReplies(t *testing.T) {
	var bodies []map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			bodies = append(bodies, body)
			resp := sendMessageResponse{Ok: true}
			resp.Result.MessageID = int64(100 + len(bodies))
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	longText := strings.Repeat("chunk words ", 500)
	replyTo := int64(77)
	got, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID:  5,
		Text:    longText,
		ReplyTo: &replyTo,
	})
	if err != nil {
		t.Fatalf("SendMessage() err = %v", err)
	}
	if got != 101 {
		t.Fatalf("message id = %d, want first chunk id 101", got)
	}
	if len(bodies) < 2 {
		t.Fatalf("request count = %d, want multiple chunks", len(bodies))
	}
	if bodies[0]["reply_to_message_id"] != float64(77) {
		t.Fatalf("first chunk reply_to_message_id = %v, want 77", bodies[0]["reply_to_message_id"])
	}
	for i := 1; i < len(bodies); i++ {
		if _, ok := bodies[i]["reply_to_message_id"]; ok {
			t.Fatalf("chunk %d unexpectedly carried reply_to_message_id", i+1)
		}
	}
	for i, body := range bodies {
		text, _ := body["text"].(string)
		if runeCount(text) > telegramTextChunkLimit {
			t.Fatalf("chunk %d length = %d, want <= %d", i+1, runeCount(text), telegramTextChunkLimit)
		}
	}
}

func TestSendMessagePreservesTelegramDescriptionOnHTTPError(t *testing.T) {
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			return encodeHTTPJSONResponse(t, http.StatusBadRequest, map[string]interface{}{
				"ok":          false,
				"description": "Bad Request: message is too long",
			}), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	_, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID: 5,
		Text:   "hello",
	})
	if err == nil {
		t.Fatal("SendMessage() err = nil, want error")
	}
	if !strings.Contains(err.Error(), "message is too long") {
		t.Fatalf("err = %v, want Telegram description", err)
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

	if err := client.EditMessageText(context.Background(), 5, 42, "working...", ""); err != nil {
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
	if _, ok := requestBody["parse_mode"]; ok {
		t.Fatalf("parse_mode = %v, want omitted", requestBody["parse_mode"])
	}
}

func TestSendMessageAutoFormatsMarkdownSubset(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &requestBody); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			resp := sendMessageResponse{Ok: true}
			resp.Result.MessageID = 124
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	_, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID: 5,
		Text:   "try *this* and `that`",
	})
	if err != nil {
		t.Fatalf("SendMessage() err = %v", err)
	}
	if requestBody["parse_mode"] != ParseModeHTML {
		t.Fatalf("parse_mode = %v, want %s", requestBody["parse_mode"], ParseModeHTML)
	}
	if requestBody["text"] != "try <i>this</i> and <code>that</code>" {
		t.Fatalf("text = %v, want transformed HTML", requestBody["text"])
	}
}

func TestSendMessageFallsBackToPlainTextOnParseError(t *testing.T) {
	call := 0
	var bodies []map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			bodies = append(bodies, body)
			if call == 1 {
				return encodeJSONResponse(t, sendMessageResponse{Ok: false, Description: "Bad Request: can't parse entities"}), nil
			}
			resp := sendMessageResponse{Ok: true}
			resp.Result.MessageID = 125
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	_, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID: 5,
		Text:   "try *this*",
	})
	if err != nil {
		t.Fatalf("SendMessage() err = %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want 2", len(bodies))
	}
	if _, ok := bodies[0]["parse_mode"]; !ok {
		t.Fatal("first request missing parse_mode")
	}
	if _, ok := bodies[1]["parse_mode"]; ok {
		t.Fatal("fallback request should omit parse_mode")
	}
	if bodies[1]["text"] != "try *this*" {
		t.Fatalf("fallback text = %v, want original plain text", bodies[1]["text"])
	}
}

func TestSendMessageFallsBackToPlainTextOnParseErrorHTTP400(t *testing.T) {
	call := 0
	var bodies []map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			bodies = append(bodies, body)
			if call == 1 {
				return encodeHTTPJSONResponse(t, http.StatusBadRequest, sendMessageResponse{Ok: false, Description: "Bad Request: can't parse entities"}), nil
			}
			resp := sendMessageResponse{Ok: true}
			resp.Result.MessageID = 126
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	_, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID: 5,
		Text:   "try *this*",
	})
	if err != nil {
		t.Fatalf("SendMessage() err = %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want 2", len(bodies))
	}
	if _, ok := bodies[0]["parse_mode"]; !ok {
		t.Fatal("first request missing parse_mode")
	}
	if _, ok := bodies[1]["parse_mode"]; ok {
		t.Fatal("fallback request should omit parse_mode")
	}
}

func TestEditMessageTextFallsBackToPlainTextOnParseError(t *testing.T) {
	call := 0
	var bodies []map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			bodies = append(bodies, body)
			if call == 1 {
				return encodeJSONResponse(t, editMessageResponse{Ok: false, Description: "Bad Request: can't parse entities"}), nil
			}
			return encodeJSONResponse(t, editMessageResponse{Ok: true}), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	if err := client.EditMessageText(context.Background(), 5, 42, "try `this`", ""); err != nil {
		t.Fatalf("EditMessageText() err = %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want 2", len(bodies))
	}
	if _, ok := bodies[0]["parse_mode"]; !ok {
		t.Fatal("first request missing parse_mode")
	}
	if _, ok := bodies[1]["parse_mode"]; ok {
		t.Fatal("fallback request should omit parse_mode")
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

func TestSendMessageUploadsPhotoMedia(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "chart.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", path, err)
	}

	var contentType string
	var payload string
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/sendPhoto" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			contentType = req.Header.Get("Content-Type")
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			payload = string(data)
			resp := sendMessageResponse{Ok: true}
			resp.Result.MessageID = 600
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	got, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID: 5,
		Text:   "Here is the chart",
		Media: []core.Media{{
			Type:     "image",
			Path:     path,
			Filename: "chart.png",
		}},
	})
	if err != nil {
		t.Fatalf("SendMessage() err = %v", err)
	}
	if got != 600 {
		t.Fatalf("message id = %d, want 600", got)
	}
	if !strings.Contains(contentType, "multipart/form-data") {
		t.Fatalf("content-type = %q, want multipart", contentType)
	}
	if !strings.Contains(payload, "png-bytes") {
		t.Fatalf("payload missing media bytes: %s", payload)
	}
	if !strings.Contains(payload, "Here is the chart") {
		t.Fatalf("payload missing caption: %s", payload)
	}
}

func TestSendMessageUploadsVoiceMedia(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "reply.ogg")
	if err := os.WriteFile(path, []byte("voice-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", path, err)
	}

	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/sendVoice" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if !strings.Contains(string(data), "voice-bytes") {
				t.Fatalf("payload missing voice bytes: %s", string(data))
			}
			resp := sendMessageResponse{Ok: true}
			resp.Result.MessageID = 601
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	got, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID: 5,
		Media: []core.Media{{
			Type:     "voice",
			Path:     path,
			Filename: "reply.ogg",
		}},
	})
	if err != nil {
		t.Fatalf("SendMessage() err = %v", err)
	}
	if got != 601 {
		t.Fatalf("message id = %d, want 601", got)
	}
}

func TestSendMessageSplitsCaptionOverflowForMedia(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "chart.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", path, err)
	}

	firstCaption := strings.Repeat("a", 1024)
	overflow := strings.Repeat("b", 40)
	var (
		paths   []string
		payload []string
		bodies  []map[string]interface{}
	)
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			paths = append(paths, req.URL.Path)
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			payload = append(payload, string(data))
			if strings.HasSuffix(req.URL.Path, "/sendMessage") {
				var body map[string]interface{}
				if err := json.Unmarshal(data, &body); err != nil {
					t.Fatalf("unmarshal body: %v", err)
				}
				bodies = append(bodies, body)
			}
			resp := sendMessageResponse{Ok: true}
			resp.Result.MessageID = int64(700 + len(paths))
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	got, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID: 5,
		Text:   firstCaption + " " + overflow,
		Media: []core.Media{{
			Type:     "image",
			Path:     path,
			Filename: "chart.png",
		}},
	})
	if err != nil {
		t.Fatalf("SendMessage() err = %v", err)
	}
	if got != 701 {
		t.Fatalf("message id = %d, want 701", got)
	}
	if len(paths) != 2 {
		t.Fatalf("request count = %d, want 2", len(paths))
	}
	if paths[0] != "/botTOKEN/sendPhoto" || paths[1] != "/botTOKEN/sendMessage" {
		t.Fatalf("paths = %#v, want sendPhoto then sendMessage", paths)
	}
	if !strings.Contains(payload[0], firstCaption) {
		t.Fatalf("media payload missing first caption chunk")
	}
	if len(bodies) != 1 {
		t.Fatalf("sendMessage bodies = %d, want 1", len(bodies))
	}
	if bodies[0]["text"] != overflow {
		t.Fatalf("overflow text = %v, want %q", bodies[0]["text"], overflow)
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
						FileSize int64  `json:"file_size"`
					}{FilePath: "voice/file.ogg", FileSize: 11},
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

func TestDownloadFileCheckedHonorsGetFileSize(t *testing.T) {
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://api.telegram.org/botTOKEN/getFile":
				return encodeJSONResponse(t, getFileResponse{
					Ok: true,
					Result: struct {
						FilePath string `json:"file_path"`
						FileSize int64  `json:"file_size"`
					}{FilePath: "docs/file.pdf", FileSize: 30},
				}), nil
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

	if _, err := client.DownloadFileChecked(context.Background(), "file123", 20); err == nil {
		t.Fatal("expected size-limit error")
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

func TestPollerIgnoresGroupMessagesWithoutDurableAdmission(t *testing.T) {
	now := time.Now().Unix()
	updates := []Update{
		{
			UpdateID: 1,
			Message: &Message{
				MessageID: 10,
				Chat:      &Chat{ID: -100, Type: "group", Title: "family"},
				From:      &User{ID: 555, Username: "alice"},
				Text:      "@aphelion hello",
				Date:      now,
				Entities: []MessageEntity{
					{Type: "mention", Offset: 0, Length: len([]rune("@aphelion"))},
				},
			},
		},
	}

	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
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
	poller := NewPoller(client, handler, WithPollerTimeout(1), WithBotIdentity(&User{ID: 99, Username: "aphelion"}))
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_ = poller.Run(ctx)
	if len(handled) != 0 {
		t.Fatalf("handled %d group messages without durable admission, want 0", len(handled))
	}
}

func TestPollerRoutesAdmittedDurableGroupMentions(t *testing.T) {
	now := time.Now().Unix()
	mention := "@aphelion"
	updates := []Update{
		{
			UpdateID: 1,
			Message: &Message{
				MessageID: 10,
				Chat:      &Chat{ID: -100, Type: "supergroup", Title: "family"},
				From:      &User{ID: 555, Username: "alice"},
				Text:      mention + " hello there",
				Date:      now,
				Entities: []MessageEntity{
					{Type: "mention", Offset: 0, Length: len([]rune(mention))},
				},
			},
		},
	}

	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
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
	poller := NewPoller(
		client,
		handler,
		WithPollerTimeout(1),
		WithDurableGroups([]config.TelegramDurableGroupConfig{{
			ChatID:    -100,
			AgentID:   "family-group",
			Charter:   "Help in the family group.",
			RespondOn: "mentions",
		}}),
		WithBotIdentity(&User{ID: 99, Username: "aphelion"}),
	)
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("poller failed: %v", err)
	}
	if len(handled) != 1 {
		t.Fatalf("handled %d admitted group messages, want 1", len(handled))
	}
	if handled[0].DurableAgentID != "family-group" {
		t.Fatalf("durable agent id = %q, want family-group", handled[0].DurableAgentID)
	}
	if handled[0].ChatType != "supergroup" {
		t.Fatalf("chat type = %q, want supergroup", handled[0].ChatType)
	}
	if handled[0].ChatTitle != "family" {
		t.Fatalf("chat title = %q, want family", handled[0].ChatTitle)
	}
}

func TestNormalizeArtifactsEmitsMetadataOnlyRemoteDescriptors(t *testing.T) {
	p := &Poller{}
	artifacts, err := p.normalizeArtifacts(context.Background(), &Message{
		Caption:  "look at this",
		Photo:    []PhotoSize{{FileID: "p1", FileSize: 123}, {FileID: "p2", FileSize: 456}},
		Voice:    &Voice{FileID: "voice-file", MimeType: "audio/ogg", FileSize: 11},
		Document: &Document{FileID: "doc1", FileName: "notes.pdf", MimeType: "application/pdf", FileSize: 99},
	})
	if err != nil {
		t.Fatalf("normalizeArtifacts() err = %v", err)
	}
	if len(artifacts) != 3 {
		t.Fatalf("artifacts len = %d, want 3", len(artifacts))
	}
	for _, artifact := range artifacts {
		if len(artifact.Data) != 0 {
			t.Fatalf("artifact %s has eager data bytes, want metadata-only descriptor", artifact.ID)
		}
		if artifact.RemoteID == "" && artifact.Kind != "structured" {
			t.Fatalf("artifact %#v missing remote id", artifact)
		}
	}
	if artifacts[0].RemoteID != "voice-file" {
		t.Fatalf("voice remote id = %q, want voice-file", artifacts[0].RemoteID)
	}
}
