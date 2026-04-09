//go:build linux

package openai

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUploadsCreateBuildsJSONAndMapsResponse(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/uploads" {
			t.Fatalf("path = %s, want /uploads", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["filename"] != "large.bin" {
			t.Fatalf("filename = %v", body["filename"])
		}
		if body["purpose"] != "assistants" {
			t.Fatalf("purpose = %v", body["purpose"])
		}
		if body["mime_type"] != "application/octet-stream" {
			t.Fatalf("mime_type = %v", body["mime_type"])
		}
		if body["bytes"] != float64(1048577) {
			t.Fatalf("bytes = %v", body["bytes"])
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"id":         "upl_123",
			"object":     "upload",
			"filename":   "large.bin",
			"bytes":      1048577,
			"purpose":    "assistants",
			"mime_type":  "application/octet-stream",
			"status":     "pending",
			"expires_at": 1710000005,
		}), nil
	})
	uploads, err := NewUploadsClient(client)
	if err != nil {
		t.Fatalf("new uploads client: %v", err)
	}

	got, err := uploads.Create(context.Background(), &CreateUploadRequest{
		Filename: "large.bin",
		Bytes:    1048577,
		Purpose:  "assistants",
		MIMEType: "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if got.ID != "upl_123" {
		t.Fatalf("id = %q, want upl_123", got.ID)
	}
	if got.Status != "pending" {
		t.Fatalf("status = %q, want pending", got.Status)
	}
	if !got.ExpiresAt.Equal(time.Unix(1710000005, 0).UTC()) {
		t.Fatalf("expires_at = %v", got.ExpiresAt)
	}
}

func TestUploadsAddPartBuildsMultipartRequestAndMapsResponse(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/uploads/upl_123/parts" {
			t.Fatalf("path = %s, want /uploads/upl_123/parts", r.URL.Path)
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse media type: %v", err)
		}
		if mediaType != "multipart/form-data" {
			t.Fatalf("media type = %s, want multipart/form-data", mediaType)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		if part.FormName() != "data" {
			t.Fatalf("form name = %s, want data", part.FormName())
		}
		payload, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if string(payload) != "chunk-1" {
			t.Fatalf("payload = %q, want chunk-1", payload)
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"id":         "part_1",
			"object":     "upload.part",
			"upload_id":  "upl_123",
			"bytes":      7,
			"created_at": 1710000006,
		}), nil
	})
	uploads, err := NewUploadsClient(client)
	if err != nil {
		t.Fatalf("new uploads client: %v", err)
	}

	got, err := uploads.AddPart(context.Background(), "upl_123", strings.NewReader("chunk-1"))
	if err != nil {
		t.Fatalf("add part: %v", err)
	}
	if got.ID != "part_1" {
		t.Fatalf("id = %q, want part_1", got.ID)
	}
	if got.UploadID != "upl_123" {
		t.Fatalf("upload_id = %q, want upl_123", got.UploadID)
	}
}

func TestUploadsCompleteBuildsJSONAndMapsFile(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/uploads/upl_123/complete" {
			t.Fatalf("path = %s, want /uploads/upl_123/complete", r.URL.Path)
		}
		var body map[string][]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(body["part_ids"]) != 2 || body["part_ids"][0] != "part_a" || body["part_ids"][1] != "part_b" {
			t.Fatalf("part_ids = %#v", body["part_ids"])
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"file": map[string]any{
				"id":         "file_123",
				"object":     "file",
				"bytes":      "12",
				"created_at": 1710000007,
				"filename":   "large.bin",
				"purpose":    "assistants",
			},
		}), nil
	})
	uploads, err := NewUploadsClient(client)
	if err != nil {
		t.Fatalf("new uploads client: %v", err)
	}

	got, err := uploads.Complete(context.Background(), "upl_123", []string{"part_a", "part_b"})
	if err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	if got.ID != "file_123" {
		t.Fatalf("id = %q, want file_123", got.ID)
	}
	if got.Bytes != 12 {
		t.Fatalf("bytes = %d, want 12", got.Bytes)
	}
}

func TestUploadsRejectsUnwiredEmptyPartList(t *testing.T) {
	t.Parallel()

	client, err := NewClient(ClientOptions{APIKey: "test-key", BaseURL: "https://example.invalid"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	uploads, err := NewUploadsClient(client)
	if err != nil {
		t.Fatalf("new uploads client: %v", err)
	}

	_, err = uploads.Complete(context.Background(), "upl_123", nil)
	if err == nil {
		t.Fatal("expected error for empty part list")
	}
}
