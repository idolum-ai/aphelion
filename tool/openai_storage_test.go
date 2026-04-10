//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type fakeFileStore struct {
	putPath    string
	putPurpose string
	files      []memstore.StoredFile
	meta       *memstore.StoredFile
	deletedID  string
}

func (f *fakeFileStore) Put(_ context.Context, localPath string, purpose string) (*memstore.StoredFile, error) {
	f.putPath = localPath
	f.putPurpose = purpose
	return &memstore.StoredFile{
		ID:        "file_123",
		Filename:  filepath.Base(localPath),
		Bytes:     5,
		Purpose:   purpose,
		CreatedAt: time.Unix(1710000000, 0).UTC(),
	}, nil
}

func (f *fakeFileStore) Get(_ context.Context, fileID string) (io.ReadCloser, *memstore.StoredFile, error) {
	if f.meta == nil {
		f.meta = &memstore.StoredFile{ID: fileID, Filename: "memo.txt", Bytes: 12, Purpose: "assistants"}
	}
	return io.NopCloser(strings.NewReader("ignored")), f.meta, nil
}

func (f *fakeFileStore) Delete(_ context.Context, fileID string) error {
	f.deletedID = fileID
	return nil
}

func (f *fakeFileStore) List(_ context.Context, _ string) ([]memstore.StoredFile, error) {
	return append([]memstore.StoredFile(nil), f.files...), nil
}

type fakeRetrievalStore struct {
	createdName string
	attachStore string
	attachFile  string
	searchStore string
	searchQuery string
	searchLimit int
}

func (f *fakeRetrievalStore) CreateStore(_ context.Context, name string) (*memstore.VectorStore, error) {
	f.createdName = name
	return &memstore.VectorStore{ID: "vs_123", Name: name, CreatedAt: time.Unix(1710000010, 0).UTC()}, nil
}

func (f *fakeRetrievalStore) AttachFile(_ context.Context, storeID string, fileID string) error {
	f.attachStore = storeID
	f.attachFile = fileID
	return nil
}

func (f *fakeRetrievalStore) Search(_ context.Context, storeID string, query string, limit int) ([]memstore.RetrievalHit, error) {
	f.searchStore = storeID
	f.searchQuery = query
	f.searchLimit = limit
	return []memstore.RetrievalHit{{
		FileID:   "file_123",
		Score:    0.92,
		Content:  "Step 1\nStep 2",
		Metadata: map[string]string{"source": "runbook"},
	}}, nil
}

func TestDefinitionsIncludeOpenAIStorageToolsWhenConfigured(t *testing.T) {
	t.Parallel()

	registry := NewRegistry("/tmp", time.Second).
		WithFileStore(&fakeFileStore{}, "assistants").
		WithRetrievalStore(&fakeRetrievalStore{}, "vs_default")

	defs := registry.Definitions()
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	if !containsString(names, "openai_file") {
		t.Fatalf("definitions = %#v, want openai_file", names)
	}
	if !containsString(names, "openai_vector_store") {
		t.Fatalf("definitions = %#v, want openai_vector_store", names)
	}
}

func TestOpenAIFileToolPutUsesConfiguredPurposeAndReadablePath(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	sharedRoot := filepath.Join(tmp, "shared-memory")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  sharedRoot,
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	notePath := filepath.Join(sharedRoot, "memory", "knowledge.md")
	if err := ensureDir(filepath.Dir(notePath)); err != nil {
		t.Fatalf("ensureDir() err = %v", err)
	}
	if err := ensureFile(notePath, "hello"); err != nil {
		t.Fatalf("ensureFile() err = %v", err)
	}

	store := &fakeFileStore{}
	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).WithFileStore(store, "assistants")
	setFakeBubblewrapRunner(t, registry)

	out, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"openai_file",
		json.RawMessage(`{"action":"put","path":"memory/knowledge.md"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal(openai_file) err = %v", err)
	}
	if !strings.Contains(out, "openai_file_put_ok") || !strings.Contains(out, "file_id=file_123") {
		t.Fatalf("output = %q, want upload success", out)
	}
	if store.putPurpose != "assistants" {
		t.Fatalf("putPurpose = %q, want assistants", store.putPurpose)
	}
	if store.putPath != notePath {
		t.Fatalf("putPath = %q, want %q", store.putPath, notePath)
	}
}

func TestOpenAIFileToolApprovedUserIsDenied(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).WithFileStore(&fakeFileStore{}, "assistants")
	setFakeBubblewrapRunner(t, registry)

	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"openai_file",
		json.RawMessage(`{"action":"list"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForPrincipal(openai_file) err = nil, want admin-only denial")
	}
	if !strings.Contains(err.Error(), "admin-only") {
		t.Fatalf("err = %v, want admin-only denial", err)
	}
}

func TestOpenAIVectorStoreSearchUsesDefaultStore(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	store := &fakeRetrievalStore{}
	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).WithRetrievalStore(store, "vs_default")
	setFakeBubblewrapRunner(t, registry)

	out, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"openai_vector_store",
		json.RawMessage(`{"action":"search","query":"release process","limit":3}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal(openai_vector_store) err = %v", err)
	}
	if store.searchStore != "vs_default" || store.searchQuery != "release process" || store.searchLimit != 3 {
		t.Fatalf("search call = %#v, want default store/query/limit", store)
	}
	if !strings.Contains(out, "[VECTOR_SEARCH]") || !strings.Contains(out, "file_id=file_123") {
		t.Fatalf("output = %q, want formatted vector search results", out)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func ensureFile(path string, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
