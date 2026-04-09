//go:build linux

package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestVectorStoresCreateStoreBuildsJSONAndMapsResponse(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/vector_stores" {
			t.Fatalf("path = %s, want /vector_stores", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["name"] != "project-docs" {
			t.Fatalf("name = %v, want project-docs", body["name"])
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"id":         "vs_123",
			"object":     "vector_store",
			"name":       "project-docs",
			"created_at": 1710000010,
		}), nil
	})
	vectorStores, err := NewVectorStoresClient(client)
	if err != nil {
		t.Fatalf("new vector stores client: %v", err)
	}

	got, err := vectorStores.CreateStore(context.Background(), "project-docs")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if got.ID != "vs_123" {
		t.Fatalf("id = %q, want vs_123", got.ID)
	}
	if !got.CreatedAt.Equal(time.Unix(1710000010, 0).UTC()) {
		t.Fatalf("created_at = %v", got.CreatedAt)
	}
}

func TestVectorStoresAttachFileBuildsJSON(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/vector_stores/vs_123/files" {
			t.Fatalf("path = %s, want /vector_stores/vs_123/files", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["file_id"] != "file_123" {
			t.Fatalf("file_id = %v, want file_123", body["file_id"])
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"id":         "vs_file_1",
			"object":     "vector_store.file",
			"created_at": 1710000011,
		}), nil
	})
	vectorStores, err := NewVectorStoresClient(client)
	if err != nil {
		t.Fatalf("new vector stores client: %v", err)
	}

	if err := vectorStores.AttachFile(context.Background(), "vs_123", "file_123"); err != nil {
		t.Fatalf("attach file: %v", err)
	}
}

func TestVectorStoresSearchBuildsJSONAndMapsHits(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/vector_stores/vs_123/search" {
			t.Fatalf("path = %s, want /vector_stores/vs_123/search", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["query"] != "release process" {
			t.Fatalf("query = %v, want release process", body["query"])
		}
		if body["max_num_results"] != float64(3) {
			t.Fatalf("max_num_results = %v, want 3", body["max_num_results"])
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"data": []map[string]any{
				{
					"file_id": "file_1",
					"score":   0.92,
					"content": []map[string]any{
						{"type": "text", "text": "Step 1"},
						{"type": "text", "text": "Step 2"},
					},
					"metadata": map[string]string{"source": "runbook"},
				},
			},
		}), nil
	})
	vectorStores, err := NewVectorStoresClient(client)
	if err != nil {
		t.Fatalf("new vector stores client: %v", err)
	}

	hits, err := vectorStores.Search(context.Background(), "vs_123", "release process", 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len = %d, want 1", len(hits))
	}
	if hits[0].FileID != "file_1" {
		t.Fatalf("file id = %q, want file_1", hits[0].FileID)
	}
	if hits[0].Content != "Step 1\nStep 2" {
		t.Fatalf("content = %q, want joined text", hits[0].Content)
	}
	if hits[0].Metadata["source"] != "runbook" {
		t.Fatalf("metadata[source] = %q, want runbook", hits[0].Metadata["source"])
	}
}
