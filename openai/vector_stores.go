//go:build linux

package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/memory"
)

var _ memory.RetrievalStore = (*VectorStoresClient)(nil)

// VectorStoresClient implements memory.RetrievalStore against OpenAI vector stores.
type VectorStoresClient struct {
	client *Client
}

// NewVectorStoresClient creates a vector-store client.
func NewVectorStoresClient(client *Client) (*VectorStoresClient, error) {
	if client == nil {
		return nil, fmt.Errorf("openai vector stores: client is required")
	}
	return &VectorStoresClient{client: client}, nil
}

// CreateStore creates a vector store by name.
func (c *VectorStoresClient) CreateStore(ctx context.Context, name string) (*memory.VectorStore, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("openai vector stores: name is required")
	}
	body, err := encodeJSON(map[string]any{"name": name})
	if err != nil {
		return nil, err
	}
	httpReq, err := c.client.newRequest(ctx, http.MethodPost, "/vector_stores", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	var resp vectorStoreObject
	if err := c.client.doJSON(httpReq, &resp); err != nil {
		return nil, err
	}
	return mapVectorStore(resp), nil
}

// AttachFile attaches an existing file object to a vector store.
func (c *VectorStoresClient) AttachFile(ctx context.Context, storeID string, fileID string) error {
	if strings.TrimSpace(storeID) == "" {
		return fmt.Errorf("openai vector stores: store id is required")
	}
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("openai vector stores: file id is required")
	}
	body, err := encodeJSON(map[string]any{"file_id": fileID})
	if err != nil {
		return err
	}
	httpReq, err := c.client.newRequest(
		ctx,
		http.MethodPost,
		"/vector_stores/"+url.PathEscape(storeID)+"/files",
		body,
	)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	var resp vectorStoreFileObject
	if err := c.client.doJSON(httpReq, &resp); err != nil {
		return err
	}
	if resp.ID == "" {
		return fmt.Errorf("openai vector stores: attach response missing id")
	}
	return nil
}

// Search queries an existing vector store.
func (c *VectorStoresClient) Search(ctx context.Context, storeID string, query string, limit int) ([]memory.RetrievalHit, error) {
	if strings.TrimSpace(storeID) == "" {
		return nil, fmt.Errorf("openai vector stores: store id is required")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("openai vector stores: query is required")
	}
	if limit <= 0 {
		limit = 10
	}

	body, err := encodeJSON(map[string]any{
		"query":           query,
		"max_num_results": limit,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := c.client.newRequest(
		ctx,
		http.MethodPost,
		"/vector_stores/"+url.PathEscape(storeID)+"/search",
		body,
	)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	var resp vectorStoreSearchResponse
	if err := c.client.doJSON(httpReq, &resp); err != nil {
		return nil, err
	}
	return mapRetrievalHits(resp.Data), nil
}

type vectorStoreObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

type vectorStoreFileObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	CreatedAt int64  `json:"created_at"`
}

type vectorStoreSearchResponse struct {
	Data []vectorStoreSearchHit `json:"data"`
}

type vectorStoreSearchHit struct {
	FileID   string            `json:"file_id"`
	Score    float64           `json:"score"`
	Content  []searchContent   `json:"content"`
	Metadata map[string]string `json:"metadata"`
}

type searchContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func mapVectorStore(in vectorStoreObject) *memory.VectorStore {
	store := &memory.VectorStore{
		ID:   in.ID,
		Name: in.Name,
	}
	if in.CreatedAt > 0 {
		store.CreatedAt = time.Unix(in.CreatedAt, 0).UTC()
	}
	return store
}

func mapRetrievalHits(data []vectorStoreSearchHit) []memory.RetrievalHit {
	out := make([]memory.RetrievalHit, 0, len(data))
	for _, hit := range data {
		out = append(out, memory.RetrievalHit{
			FileID:   hit.FileID,
			Score:    hit.Score,
			Content:  joinTextContent(hit.Content),
			Metadata: hit.Metadata,
		})
	}
	return out
}

func joinTextContent(items []searchContent) string {
	if len(items) == 0 {
		return ""
	}
	var builder strings.Builder
	for i, item := range items {
		if item.Type != "" && item.Type != "text" {
			continue
		}
		if item.Text == "" {
			continue
		}
		if builder.Len() > 0 && i > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(item.Text)
	}
	return builder.String()
}
