//go:build linux

package memory

import (
	"context"
	"io"
	"time"
)

// FileStore abstracts durable file-like storage for higher-level memory flows.
type FileStore interface {
	Put(ctx context.Context, localPath string, purpose string) (*StoredFile, error)
	Get(ctx context.Context, fileID string) (io.ReadCloser, *StoredFile, error)
	Delete(ctx context.Context, fileID string) error
	List(ctx context.Context, purpose string) ([]StoredFile, error)
}

// StoredFile describes a durable file object.
type StoredFile struct {
	ID        string
	Filename  string
	Bytes     int64
	Purpose   string
	CreatedAt time.Time
}

// RetrievalStore abstracts vector-search style storage.
type RetrievalStore interface {
	CreateStore(ctx context.Context, name string) (*VectorStore, error)
	AttachFile(ctx context.Context, storeID string, fileID string) error
	Search(ctx context.Context, storeID string, query string, limit int) ([]RetrievalHit, error)
}

// VectorStore identifies a retrieval index.
type VectorStore struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// RetrievalHit is a single retrieval result.
type RetrievalHit struct {
	FileID   string
	Score    float64
	Content  string
	Metadata map[string]string
}
