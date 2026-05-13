//go:build linux

package telegram

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
)

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

func TestDownloadFileCheckedBoundsDownloadRead(t *testing.T) {
	body := &failAfterReadCloser{data: []byte(strings.Repeat("x", 25)), limit: 21}
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://api.telegram.org/botTOKEN/getFile":
				return encodeJSONResponse(t, getFileResponse{
					Ok: true,
					Result: struct {
						FilePath string `json:"file_path"`
						FileSize int64  `json:"file_size"`
					}{FilePath: "docs/file.pdf", FileSize: 0},
				}), nil
			case "https://api.telegram.org/file/botTOKEN/docs/file.pdf":
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
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

	_, err := client.DownloadFileChecked(context.Background(), "file123", 20)
	if err == nil {
		t.Fatal("DownloadFileChecked() err = nil, want size-limit error")
	}
	if !strings.Contains(err.Error(), "downloaded file exceeds configured size limit") {
		t.Fatalf("DownloadFileChecked() err = %v, want downloaded size-limit context", err)
	}
	if body.offset != 21 {
		t.Fatalf("download body read %d bytes, want max+1", body.offset)
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
