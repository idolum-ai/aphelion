//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) openAIFile(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal) (string, error) {
	if r.fileStore == nil {
		return "", fmt.Errorf("openai file storage is not configured")
	}
	if err := requireAdminTool(p, "openai_file"); err != nil {
		return "", err
	}

	var in openAIFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode openai_file input: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "put":
		localPath, err := resolveUploadPath(scope, in.Path)
		if err != nil {
			return "", err
		}
		purpose := firstNonEmpty(strings.TrimSpace(in.Purpose), r.filePurpose)
		if purpose == "" {
			return "", fmt.Errorf("openai_file purpose is required")
		}
		stored, err := r.fileStore.Put(ctx, localPath, purpose)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("openai_file_put_ok file_id=%s filename=%s bytes=%d purpose=%s", stored.ID, stored.Filename, stored.Bytes, stored.Purpose), nil
	case "list":
		purpose := strings.TrimSpace(in.Purpose)
		if purpose == "" {
			purpose = r.filePurpose
		}
		files, err := r.fileStore.List(ctx, purpose)
		if err != nil {
			return "", err
		}
		return renderOpenAIFileList(purpose, files), nil
	case "get_metadata":
		fileID := strings.TrimSpace(in.FileID)
		if fileID == "" {
			return "", fmt.Errorf("openai_file file_id is required for get_metadata")
		}
		body, meta, err := r.fileStore.Get(ctx, fileID)
		if err != nil {
			return "", err
		}
		if body != nil {
			_, _ = io.Copy(io.Discard, body)
			_ = body.Close()
		}
		return renderOpenAIFileMetadata(meta), nil
	case "delete":
		fileID := strings.TrimSpace(in.FileID)
		if fileID == "" {
			return "", fmt.Errorf("openai_file file_id is required for delete")
		}
		if err := r.fileStore.Delete(ctx, fileID); err != nil {
			return "", err
		}
		return fmt.Sprintf("openai_file_delete_ok file_id=%s", fileID), nil
	default:
		return "", fmt.Errorf("openai_file action must be one of put|list|get_metadata|delete")
	}
}

func (r *Registry) openAIVectorStore(ctx context.Context, input json.RawMessage, p principal.Principal) (string, error) {
	if r.retrievalStore == nil {
		return "", fmt.Errorf("openai vector store is not configured")
	}
	if err := requireAdminTool(p, "openai_vector_store"); err != nil {
		return "", err
	}

	var in openAIVectorStoreInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode openai_vector_store input: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "create":
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return "", fmt.Errorf("openai_vector_store name is required for create")
		}
		store, err := r.retrievalStore.CreateStore(ctx, name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("openai_vector_store_create_ok store_id=%s name=%s", store.ID, store.Name), nil
	case "attach":
		storeID, err := r.resolveVectorStoreID(in.StoreID)
		if err != nil {
			return "", err
		}
		fileID := strings.TrimSpace(in.FileID)
		if fileID == "" {
			return "", fmt.Errorf("openai_vector_store file_id is required for attach")
		}
		if err := r.retrievalStore.AttachFile(ctx, storeID, fileID); err != nil {
			return "", err
		}
		return fmt.Sprintf("openai_vector_store_attach_ok store_id=%s file_id=%s", storeID, fileID), nil
	case "search":
		storeID, err := r.resolveVectorStoreID(in.StoreID)
		if err != nil {
			return "", err
		}
		query := strings.TrimSpace(in.Query)
		if query == "" {
			return "", fmt.Errorf("openai_vector_store query is required for search")
		}
		hits, err := r.retrievalStore.Search(ctx, storeID, query, in.Limit)
		if err != nil {
			return "", err
		}
		return renderOpenAIVectorSearchResults(storeID, query, hits), nil
	default:
		return "", fmt.Errorf("openai_vector_store action must be one of create|attach|search")
	}
}

func resolveUploadPath(scope sandbox.Scope, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("openai_file path is required for put")
	}
	candidates := make([]string, 0, 3)
	if filepath.IsAbs(raw) {
		candidates = append(candidates, filepath.Clean(raw))
	} else {
		for _, root := range []string{scope.WorkingRoot, scope.SharedMemoryRoot, scope.UserMemory} {
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			candidates = append(candidates, filepath.Join(root, raw))
		}
	}
	allowedRoots := nonEmptyRoots(scope.WorkingRoot, scope.SharedMemoryRoot, scope.UserMemory)
	for _, candidate := range candidates {
		resolved, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if !pathWithinAnyRoot(resolved, allowedRoots) {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return "", fmt.Errorf("openai_file path %q is a directory", raw)
		}
		return resolved, nil
	}
	return "", fmt.Errorf("openai_file path %q is not readable within the current roots", raw)
}

func renderOpenAIFileList(purpose string, files []memstore.StoredFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[OPENAI_FILES]\npurpose: %s\n", strings.TrimSpace(purpose))
	if len(files) == 0 {
		b.WriteString("no_files\n[/OPENAI_FILES]")
		return b.String()
	}
	for i, file := range files {
		fmt.Fprintf(&b, "\n%d. id=%s filename=%s bytes=%d purpose=%s", i+1, file.ID, file.Filename, file.Bytes, file.Purpose)
		if !file.CreatedAt.IsZero() {
			fmt.Fprintf(&b, " created_at=%s", file.CreatedAt.UTC().Format(time.RFC3339))
		}
		b.WriteString("\n")
	}
	b.WriteString("[/OPENAI_FILES]")
	return b.String()
}

func renderOpenAIFileMetadata(meta *memstore.StoredFile) string {
	if meta == nil {
		return "[OPENAI_FILE]\nmissing_metadata\n[/OPENAI_FILE]"
	}
	var b strings.Builder
	b.WriteString("[OPENAI_FILE]\n")
	fmt.Fprintf(&b, "id: %s\nfilename: %s\nbytes: %d\npurpose: %s\n", meta.ID, meta.Filename, meta.Bytes, meta.Purpose)
	if !meta.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "created_at: %s\n", meta.CreatedAt.UTC().Format(time.RFC3339))
	}
	b.WriteString("[/OPENAI_FILE]")
	return b.String()
}

func (r *Registry) resolveVectorStoreID(raw string) (string, error) {
	storeID := firstNonEmpty(raw, r.defaultStore)
	if storeID == "" {
		return "", fmt.Errorf("openai_vector_store store_id is required when no default store is configured")
	}
	return storeID, nil
}

func renderOpenAIVectorSearchResults(storeID string, query string, hits []memstore.RetrievalHit) string {
	var b strings.Builder
	b.WriteString("[VECTOR_SEARCH]\n")
	fmt.Fprintf(&b, "store_id: %s\nquery: %s\n", storeID, strings.TrimSpace(query))
	if len(hits) == 0 {
		b.WriteString("no_hits\n[/VECTOR_SEARCH]")
		return b.String()
	}
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n%d. file_id=%s score=%.3f\n", i+1, hit.FileID, hit.Score)
		if strings.TrimSpace(hit.Content) != "" {
			b.WriteString("content: ")
			b.WriteString(truncate(strings.TrimSpace(hit.Content), 600))
			b.WriteString("\n")
		}
		if len(hit.Metadata) > 0 {
			b.WriteString("metadata: ")
			first := true
			for key, value := range hit.Metadata {
				if !first {
					b.WriteString(", ")
				}
				first = false
				b.WriteString(key)
				b.WriteByte('=')
				b.WriteString(value)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("[/VECTOR_SEARCH]")
	return b.String()
}
