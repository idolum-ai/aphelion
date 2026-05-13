//go:build linux

package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

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

func TestSendInlineKeyboardPayloadSupportsURLButtons(t *testing.T) {
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
			resp.Result.MessageID = 223
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	_, err := client.SendInlineKeyboard(context.Background(), 5, "Status", [][]InlineButton{
		{
			{Text: "Open Status", URL: "https://aphelion.example.ts.net/status"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("SendInlineKeyboard() err = %v", err)
	}
	replyMarkup, ok := requestBody["reply_markup"].(map[string]interface{})
	if !ok {
		t.Fatalf("reply_markup = %#v, want object", requestBody["reply_markup"])
	}
	rows, ok := replyMarkup["inline_keyboard"].([]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("inline_keyboard = %#v, want 1 row", replyMarkup["inline_keyboard"])
	}
	row, ok := rows[0].([]interface{})
	if !ok || len(row) != 1 {
		t.Fatalf("inline_keyboard[0] = %#v, want 1 button", rows[0])
	}
	urlButton, ok := row[0].(map[string]interface{})
	if !ok {
		t.Fatalf("url button = %#v, want object", row[0])
	}
	if urlButton["url"] != "https://aphelion.example.ts.net/status" {
		t.Fatalf("url button = %#v, want URL", urlButton)
	}
	if _, ok := urlButton["callback_data"]; ok {
		t.Fatalf("callback_data = %#v, want omitted for URL button", urlButton["callback_data"])
	}
}

func TestSendInlineKeyboardRejectsLongButtonLabels(t *testing.T) {
	client := NewClient("TOKEN")
	_, err := client.SendInlineKeyboard(context.Background(), 5, "Choose", [][]InlineButton{
		{
			{Text: "Approve this action", CallbackData: "decision:1:approve"},
		},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "at most 2 words") {
		t.Fatalf("SendInlineKeyboard() err = %v, want compact button-label rejection", err)
	}
}

func TestSendInlineKeyboardSplitsLongTextIntoFollowUpMessages(t *testing.T) {
	var bodies []map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/sendMessage" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
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
			resp.Result.MessageID = int64(300 + len(bodies))
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	longText := strings.Repeat("pending approval details ", 250)
	replyTo := int64(88)
	got, err := client.SendInlineKeyboard(context.Background(), 5, longText, [][]InlineButton{
		{{Text: "Approve", CallbackData: "decision:1:approve"}},
	}, &replyTo)
	if err != nil {
		t.Fatalf("SendInlineKeyboard() err = %v", err)
	}
	if got != 301 {
		t.Fatalf("message id = %d, want first chunk id 301", got)
	}
	if len(bodies) < 2 {
		t.Fatalf("request count = %d, want multiple chunks", len(bodies))
	}
	if _, ok := bodies[0]["reply_markup"]; !ok {
		t.Fatalf("first chunk missing reply_markup: %#v", bodies[0])
	}
	if bodies[0]["reply_to_message_id"] != float64(88) {
		t.Fatalf("first chunk reply_to_message_id = %v, want 88", bodies[0]["reply_to_message_id"])
	}
	for i := 1; i < len(bodies); i++ {
		if _, ok := bodies[i]["reply_markup"]; ok {
			t.Fatalf("chunk %d unexpectedly includes reply_markup", i+1)
		}
		if _, ok := bodies[i]["reply_to_message_id"]; ok {
			t.Fatalf("chunk %d unexpectedly includes reply_to_message_id", i+1)
		}
	}
	for i, body := range bodies {
		text, _ := body["text"].(string)
		if runeCount(text) > telegramTextChunkLimit {
			t.Fatalf("chunk %d length = %d, want <= %d", i+1, runeCount(text), telegramTextChunkLimit)
		}
	}
}

func TestSendInlineKeyboardAutoFormatsMarkdownSubset(t *testing.T) {
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
			resp.Result.MessageID = 223
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	got, err := client.SendInlineKeyboard(context.Background(), 5, "try *this* and `that`", [][]InlineButton{
		{
			{Text: "Approve", CallbackData: "decision:1:approve"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("SendInlineKeyboard() err = %v", err)
	}
	if got != 223 {
		t.Fatalf("message id = %d, want 223", got)
	}
	if requestBody["parse_mode"] != ParseModeHTML {
		t.Fatalf("parse_mode = %v, want %s", requestBody["parse_mode"], ParseModeHTML)
	}
	if requestBody["text"] != "try <i>this</i> and <code>that</code>" {
		t.Fatalf("text = %v, want transformed HTML", requestBody["text"])
	}
}

func TestSendInlineKeyboardFallsBackToPlainTextOnParseError(t *testing.T) {
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
			resp.Result.MessageID = 224
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	got, err := client.SendInlineKeyboard(context.Background(), 5, "try *this*", [][]InlineButton{
		{
			{Text: "Approve", CallbackData: "decision:1:approve"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("SendInlineKeyboard() err = %v", err)
	}
	if got != 224 {
		t.Fatalf("message id = %d, want 224", got)
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
	if _, ok := bodies[0]["reply_markup"]; !ok {
		t.Fatal("first request missing reply_markup")
	}
	if _, ok := bodies[1]["reply_markup"]; !ok {
		t.Fatal("fallback request missing reply_markup")
	}
	if bodies[1]["text"] != "try *this*" {
		t.Fatalf("fallback text = %v, want original plain text", bodies[1]["text"])
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
	foundReaction := false
	for _, raw := range allowed {
		if raw == "message" {
			foundMessage = true
		}
		if raw == "callback_query" {
			foundCallback = true
		}
		if raw == "message_reaction" {
			foundReaction = true
		}
	}
	if !foundMessage || !foundCallback || !foundReaction {
		t.Fatalf("allowed_updates = %#v, want message, callback_query, and message_reaction", allowed)
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

func TestSetMessageReactionPayload(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/setMessageReaction" {
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

	if err := client.SetMessageReaction(context.Background(), 5, 42, "👍"); err != nil {
		t.Fatalf("SetMessageReaction() err = %v", err)
	}
	if requestBody["chat_id"] != float64(5) {
		t.Fatalf("chat_id = %v, want 5", requestBody["chat_id"])
	}
	if requestBody["message_id"] != float64(42) {
		t.Fatalf("message_id = %v, want 42", requestBody["message_id"])
	}
	reactions, ok := requestBody["reaction"].([]interface{})
	if !ok || len(reactions) != 1 {
		t.Fatalf("reaction = %#v, want one reaction", requestBody["reaction"])
	}
	reaction, ok := reactions[0].(map[string]interface{})
	if !ok || reaction["type"] != "emoji" || reaction["emoji"] != "👍" {
		t.Fatalf("reaction[0] = %#v, want emoji thumbs up", reactions[0])
	}
}

func TestSendMessageReactionOnlyUsesReplyTarget(t *testing.T) {
	var requestBody map[string]interface{}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/setMessageReaction" {
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

	replyTo := int64(42)
	got, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID:    5,
		ReplyTo:   &replyTo,
		Reactions: []string{"🔥"},
	})
	if err != nil {
		t.Fatalf("SendMessage() err = %v", err)
	}
	if got != 42 {
		t.Fatalf("message id = %d, want reacted message id 42", got)
	}
	reactions, ok := requestBody["reaction"].([]interface{})
	if !ok || len(reactions) != 1 {
		t.Fatalf("reaction = %#v, want one reaction", requestBody["reaction"])
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
