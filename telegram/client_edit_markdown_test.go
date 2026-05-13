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

func TestEditMessageTextWithInlineKeyboardPayload(t *testing.T) {
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

	if err := client.EditMessageTextWithInlineKeyboard(context.Background(), 5, 42, "Status", "", [][]InlineButton{
		{
			{Text: "Refresh", CallbackData: "status:chat"},
		},
	}); err != nil {
		t.Fatalf("EditMessageTextWithInlineKeyboard() err = %v", err)
	}
	replyMarkup, ok := requestBody["reply_markup"].(map[string]interface{})
	if !ok {
		t.Fatalf("reply_markup = %#v, want object", requestBody["reply_markup"])
	}
	rows, ok := replyMarkup["inline_keyboard"].([]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("inline_keyboard = %#v, want one row", replyMarkup["inline_keyboard"])
	}
}

func TestEditMessageTextWithInlineKeyboardRejectsLongButtonLabels(t *testing.T) {
	client := NewClient("TOKEN")
	err := client.EditMessageTextWithInlineKeyboard(context.Background(), 5, 42, "Status", "", [][]InlineButton{
		{
			{Text: "Open full status", CallbackData: "status:system"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "at most 2 words") {
		t.Fatalf("EditMessageTextWithInlineKeyboard() err = %v, want compact button-label rejection", err)
	}
}

func TestEditMessageTextWithoutInlineKeyboardPayload(t *testing.T) {
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

	if err := client.EditMessageTextWithoutInlineKeyboard(context.Background(), 5, 42, "Done.", ""); err != nil {
		t.Fatalf("EditMessageTextWithoutInlineKeyboard() err = %v", err)
	}
	replyMarkup, ok := requestBody["reply_markup"].(map[string]interface{})
	if !ok {
		t.Fatalf("reply_markup = %#v, want object", requestBody["reply_markup"])
	}
	rows, ok := replyMarkup["inline_keyboard"].([]interface{})
	if !ok || len(rows) != 0 {
		t.Fatalf("inline_keyboard = %#v, want empty rows", replyMarkup["inline_keyboard"])
	}
}

func TestEditMessageTextTruncatesOversizedText(t *testing.T) {
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

	longText := strings.Repeat("x", telegramTextChunkLimit+200)
	if err := client.EditMessageText(context.Background(), 5, 42, longText, ""); err != nil {
		t.Fatalf("EditMessageText() err = %v", err)
	}
	text, _ := requestBody["text"].(string)
	if runeCount(text) > telegramTextChunkLimit {
		t.Fatalf("edited text length = %d, want <= %d", runeCount(text), telegramTextChunkLimit)
	}
	if !strings.HasSuffix(text, "…") {
		t.Fatalf("edited text = %q, want truncation ellipsis", text)
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

func TestSendMessageAutoFormatsLineMarkdownForTelegram(t *testing.T) {
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
			resp.Result.MessageID = 127
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	text := strings.Join([]string{
		"### 1. Ecological perception",
		"",
		"> What behavior does this discontinuity make available?",
		"",
		"---",
		"",
		"Read [docs](https://core.telegram.org/bots/api#formatting-options).",
	}, "\n")
	_, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID: 5,
		Text:   text,
	})
	if err != nil {
		t.Fatalf("SendMessage() err = %v", err)
	}
	if requestBody["parse_mode"] != ParseModeHTML {
		t.Fatalf("parse_mode = %v, want %s", requestBody["parse_mode"], ParseModeHTML)
	}
	got, _ := requestBody["text"].(string)
	for _, want := range []string{
		"<b>1. Ecological perception</b>",
		"<blockquote>What behavior does this discontinuity make available?</blockquote>",
		`<a href="https://core.telegram.org/bots/api#formatting-options">docs</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("text = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "###") || strings.Contains(got, "---") {
		t.Fatalf("text = %q, still contains raw structural Markdown", got)
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
