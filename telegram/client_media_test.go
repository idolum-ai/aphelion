//go:build linux

package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

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
