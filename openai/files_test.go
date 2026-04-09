//go:build linux

package openai

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilesPutBuildsMultipartRequestAndMapsResponse(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/files" {
			t.Fatalf("path = %s, want /files", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse media type: %v", err)
		}
		if mediaType != "multipart/form-data" {
			t.Fatalf("media type = %s, want multipart/form-data", mediaType)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		var purpose string
		var filename string
		var fileBody string
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read part: %v", err)
			}
			switch part.FormName() {
			case "purpose":
				purpose = string(data)
			case "file":
				filename = part.FileName()
				fileBody = string(data)
			}
		}
		if purpose != "assistants" {
			t.Fatalf("purpose = %q, want assistants", purpose)
		}
		if filename != "note.txt" {
			t.Fatalf("filename = %q, want note.txt", filename)
		}
		if fileBody != "hello" {
			t.Fatalf("file body = %q, want hello", fileBody)
		}

		return jsonResponse(t, http.StatusOK, map[string]any{
			"id":         "file_123",
			"object":     "file",
			"bytes":      5,
			"created_at": 1710000000,
			"filename":   "note.txt",
			"purpose":    "assistants",
		}), nil
	})
	files, err := NewFilesClient(client)
	if err != nil {
		t.Fatalf("new files client: %v", err)
	}

	got, err := files.Put(context.Background(), path, "assistants")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if got.ID != "file_123" {
		t.Fatalf("id = %q, want file_123", got.ID)
	}
	if got.Bytes != 5 {
		t.Fatalf("bytes = %d, want 5", got.Bytes)
	}
	if !got.CreatedAt.Equal(time.Unix(1710000000, 0).UTC()) {
		t.Fatalf("created_at = %v", got.CreatedAt)
	}
}

func TestFilesGetFetchesMetadataAndContent(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/files/file_123":
			return jsonResponse(t, http.StatusOK, map[string]any{
				"id":         "file_123",
				"object":     "file",
				"bytes":      "12",
				"created_at": 1710000001,
				"filename":   "memo.txt",
				"purpose":    "assistants",
			}), nil
		case "/files/file_123/content":
			return textResponse(http.StatusOK, "hello world!"), nil
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
			return nil, nil
		}
	})
	files, err := NewFilesClient(client)
	if err != nil {
		t.Fatalf("new files client: %v", err)
	}

	body, meta, err := files.Get(context.Background(), "file_123")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(content) != "hello world!" {
		t.Fatalf("content = %q", content)
	}
	if meta.Filename != "memo.txt" {
		t.Fatalf("filename = %q, want memo.txt", meta.Filename)
	}
	if meta.Bytes != 12 {
		t.Fatalf("bytes = %d, want 12", meta.Bytes)
	}
}

func TestFilesListFiltersPurpose(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/files" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"data": []map[string]any{
				{"id": "file_1", "filename": "a.txt", "bytes": 1, "purpose": "assistants"},
				{"id": "file_2", "filename": "b.txt", "bytes": 2, "purpose": "vision"},
			},
		}), nil
	})
	files, err := NewFilesClient(client)
	if err != nil {
		t.Fatalf("new files client: %v", err)
	}

	listed, err := files.List(context.Background(), "assistants")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("len = %d, want 1", len(listed))
	}
	if listed[0].ID != "file_1" {
		t.Fatalf("id = %q, want file_1", listed[0].ID)
	}
}

func TestFilesDeleteChecksDeleteResponse(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/file_123") {
			t.Fatalf("path = %s, want /files/file_123", r.URL.Path)
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"id":      "file_123",
			"object":  "file",
			"deleted": true,
		}), nil
	})
	files, err := NewFilesClient(client)
	if err != nil {
		t.Fatalf("new files client: %v", err)
	}

	if err := files.Delete(context.Background(), "file_123"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
