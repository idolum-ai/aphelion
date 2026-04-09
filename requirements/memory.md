# Memory — Workspace Files, OpenAI Files & Retrieval Storage

## Overview

This spec covers memory-bearing storage surfaces:

- workspace files (`SOUL.md`, `MEMORY.md`, etc.)
- durable shared and per-user memory roots
- OpenAI file storage
- OpenAI vector stores / retrieval storage

This is not the inference-provider spec. OpenAI appears here because its file and retrieval APIs are storage primitives for Aphelion, not merely model backends.

Memory writes are governor-owned. The face may consume memory-derived context for rendering, but it must not commit durable memory on its own.

## Scope

### v0 required

- workspace bootstrap files
- dynamic workspace files
- shared vs per-user memory roots in the staged design

### v0.5

- isolated per-user writable memory
- shared memory as read-only for non-admins
- durable review-event inputs for admin awareness

### Deferred after v0.5

- OpenAI file storage integration
- OpenAI vector stores
- semantic retrieval and `file_search`
- embeddings-backed memory indexing

## Memory Layers

### Global persona/bootstrap

Examples:

- `SOUL.md`
- `IDENTITY.md`
- `USER.md`
- `AGENTS.md`
- `TOOLS.md`
- `BOOTSTRAP.md`

These are identity-bearing files and should be treated as global/admin-controlled.

### Shared memory

This is long-lived shared state for the system.

- writable by admin
- read-only for non-admins

Examples:

- `MEMORY.md`
- shared daily notes
- promoted decisions/facts

### Per-user memory

Per-user memory is writable only by the corresponding non-admin principal.

Examples:

- isolated notes
- local state snapshots
- private working summaries

### Review flow as memory input

`review_events` are not shared memory themselves. They are bounded cross-session inputs into the admin conversation. They may later influence shared memory, but they do not automatically become it.

## OpenAI File Storage

OpenAI files should be treated as external durable objects usable by higher-level memory workflows.

Intended uses:

- upload source documents for retrieval
- stage files for later vector-store attachment
- keep external copies of memory-related documents when useful

### Interface

```go
type FileStore interface {
    Put(ctx context.Context, localPath string, purpose string) (*StoredFile, error)
    Get(ctx context.Context, fileID string) (io.ReadCloser, *StoredFile, error)
    Delete(ctx context.Context, fileID string) error
    List(ctx context.Context, purpose string) ([]StoredFile, error)
}

type StoredFile struct {
    ID        string
    Filename  string
    Bytes     int64
    Purpose   string
    CreatedAt time.Time
}
```

### Notes

- OpenAI `files` is general object storage for platform workflows
- this is not a replacement for the local workspace
- local workspace remains the source of truth for persona files and live writable state

## OpenAI Vector Stores

Vector stores belong here because they are retrieval storage, not inference.

Use cases:

- attach uploaded files to retrieval indexes
- store parsed/chunked document representations
- later power search/retrieval over approved corpora

### Interface

```go
type RetrievalStore interface {
    CreateStore(ctx context.Context, name string) (*VectorStore, error)
    AttachFile(ctx context.Context, storeID string, fileID string) error
    Search(ctx context.Context, storeID string, query string, limit int) ([]RetrievalHit, error)
}

type VectorStore struct {
    ID        string
    Name      string
    CreatedAt time.Time
}

type RetrievalHit struct {
    FileID   string
    Score    float64
    Content  string
    Metadata map[string]string
}
```

## Ownership Rules

- local workspace is the live truth
- shared memory is admin-owned
- per-user memory is principal-owned
- OpenAI files/vector stores are auxiliary storage/services
- no OpenAI storage object should silently replace the local workspace as the source of truth

## Config

```toml
[sessions.isolation]
global_root = "~/.config/aphelion/workspace"
shared_memory_root = "~/.config/aphelion/memory/shared"
user_memory_root = "~/.config/aphelion/memory/users"

[openai.files]
enabled = false
purpose = "assistants"

[openai.vector_stores]
enabled = false
default_store = ""
```

## Tests

### v0

- **TestSharedMemoryReadOnlyForNonAdmin**
- **TestPerUserMemoryWritable**
- **TestWorkspacePromptFilesLoadInExpectedOrder**

### Deferred OpenAI storage

- **TestOpenAIFilePut**
- **TestOpenAIFileGet**
- **TestOpenAIFileDelete**
- **TestOpenAIVectorStoreCreate**
- **TestOpenAIVectorStoreAttachFile**
- **TestOpenAIVectorStoreSearch**
