//go:build linux

package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const (
	maxNativeReadLines             = 10000
	defaultNativeReadMaxBytes      = 64 * 1024
	maxNativeReadBytes             = 512 * 1024
	defaultNativeFetchMaxBytes     = 128 * 1024
	maxNativeFetchBytes            = 1024 * 1024
	defaultNativeFetchExcerptBytes = 2048
	maxNativeFetchExcerptBytes     = 64 * 1024
	defaultNativeListLimit         = 100
	maxNativeListLimit             = 500
	defaultNativeSearchLimit       = 25
	maxNativeSearchLimit           = 100
	defaultNativeSearchMaxBytes    = 128 * 1024
	maxNativeSearchMaxBytes        = 512 * 1024

	DefaultNativeFetchUserAgent = "aphelion-fetch-url/1"
)

type readFileInput struct {
	Path     string `json:"path"`
	Offset   *int   `json:"offset,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
	Full     bool   `json:"full,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

type writeFileInput struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Append     bool   `json:"append,omitempty"`
	CreateDirs bool   `json:"create_dirs,omitempty"`
}

type listDirInput struct {
	Path  string `json:"path,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type searchFilesInput struct {
	Query    string `json:"query"`
	Path     string `json:"path,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

type fetchURLInput struct {
	URL          string `json:"url"`
	MaxBytes     int    `json:"max_bytes,omitempty"`
	ExcerptBytes int    `json:"excerpt_bytes,omitempty"`
}

type nativePathAccess string

const (
	nativePathRead  nativePathAccess = "read"
	nativePathWrite nativePathAccess = "write"
)

func nativeFileToolDefinitions() []agent.ToolDef {
	return []agent.ToolDef{
		{
			Name:        "read_file",
			Description: "Parallel-safe. Read a bounded text file through the current sandbox profile. Prefer this over exec cat/sed/head/tail/nl for scoped file inspection. Requires offset+limit unless full=true is set; independent file reads should be emitted together in one response so the runtime can execute the parallel-safe batch.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to read. Relative paths are scoped to the current working root."},
					"offset": {"type": "integer", "minimum": 0, "description": "Zero-based line offset. Required with limit unless full=true."},
					"limit": {"type": "integer", "minimum": 1, "maximum": 10000, "description": "Maximum lines to return. Required with offset unless full=true."},
					"full": {"type": "boolean", "description": "Explicitly read from the beginning without offset/limit, still bounded by max_bytes."},
					"max_bytes": {"type": "integer", "minimum": 1, "maximum": 524288, "description": "Maximum bytes to return; defaults to 65536."}
				},
				"required": ["path"]
			}`),
		},
		{
			Name:        "write_file",
			Description: "Write or append a bounded text file through the current sandbox profile. The target must be under a writable scoped root.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to write. Relative paths are scoped to the current working root."},
					"content": {"type": "string", "description": "Text content to write."},
					"append": {"type": "boolean", "description": "Append instead of replacing the file."},
					"create_dirs": {"type": "boolean", "description": "Create missing parent directories under the writable root."}
				},
				"required": ["path", "content"]
			}`),
		},
		{
			Name:        "list_dir",
			Description: "Parallel-safe. List a scoped directory through the current sandbox profile. Prefer this over exec ls/tree/find for basic directory inspection. For independent listings, issue multiple read_file/list_dir/search calls together in one response.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Directory path to list. Defaults to the current working root."},
					"limit": {"type": "integer", "minimum": 1, "maximum": 500, "description": "Maximum entries to return; defaults to 100."}
				}
			}`),
		},
		{
			Name:        "search",
			Description: "Parallel-safe. Search text files under a scoped path with literal matching. Prefer this over exec rg/grep/find for ordinary literal repository searches. For independent searches, issue multiple read_file/list_dir/search calls together in one response.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Literal search query."},
					"path": {"type": "string", "description": "File or directory to search. Defaults to the current working root."},
					"limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "Maximum matches to return; defaults to 25."},
					"max_bytes": {"type": "integer", "minimum": 1, "maximum": 524288, "description": "Maximum bytes inspected per file; defaults to 131072."}
				},
				"required": ["query"]
			}`),
		},
		{
			Name:        "system_log_read",
			Description: "Read bounded Aphelion/systemd journal lines for one unit without raw shell. Admin diagnostic tool; prefer this over exec journalctl/grep/tail for service log inspection. Results are literal-filtered, byte-limited, and redacted before display.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"unit": {"type": "string", "description": "systemd unit name, for example aphelion.service"},
					"system": {"type": "boolean", "description": "Read the system journal instead of the user journal"},
					"since": {"type": "string", "description": "Optional journalctl --since value, for example '2 hours ago'"},
					"until": {"type": "string", "description": "Optional journalctl --until value"},
					"priority": {"type": "string", "description": "Optional journalctl priority filter such as warning, err, or 3"},
					"include": {"type": "array", "items": {"type": "string"}, "description": "Optional case-insensitive literal substrings; keep lines matching any value"},
					"exclude": {"type": "array", "items": {"type": "string"}, "description": "Optional case-insensitive literal substrings; drop lines matching any value"},
					"limit": {"type": "integer", "minimum": 1, "maximum": 500, "description": "Maximum returned lines after filtering; defaults to 120"},
					"max_bytes": {"type": "integer", "minimum": 1, "maximum": 262144, "description": "Maximum returned bytes after filtering; defaults to 65536"}
				},
				"required": ["unit"]
			}`),
		},
		{
			Name:        "fetch_url",
			Description: "Fetch a bounded HTTP(S) URL digest when the current sandbox profile allows network access. Network-denied profiles cannot use this tool. max_bytes controls bytes read and hashed; excerpt_bytes controls the visible excerpt returned to the model.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "HTTP or HTTPS URL to fetch."},
					"max_bytes": {"type": "integer", "minimum": 1, "maximum": 1048576, "description": "Maximum response body bytes to read and hash; defaults to 131072."},
					"excerpt_bytes": {"type": "integer", "minimum": 1, "maximum": 65536, "description": "Maximum visible response bytes to include in excerpt; defaults to 2048 and is capped by max_bytes."}
				},
				"required": ["url"]
			}`),
		},
	}
}

func (r *Registry) readFile(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (out string, err error) {
	var in readFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode read_file input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return "", fmt.Errorf("read_file path is required")
	}
	if !in.Full && (in.Offset == nil || in.Limit == nil) {
		return "", fmt.Errorf("read_file requires offset+limit or full=true")
	}
	offset := 0
	if in.Offset != nil {
		if *in.Offset < 0 {
			return "", fmt.Errorf("read_file offset must be >= 0")
		}
		offset = *in.Offset
	}
	limit := 0
	if in.Limit != nil {
		if *in.Limit <= 0 {
			return "", fmt.Errorf("read_file limit must be >= 1")
		}
		limit = *in.Limit
		if limit > maxNativeReadLines {
			limit = maxNativeReadLines
		}
	}
	maxBytes := clampNativeLimit(in.MaxBytes, defaultNativeReadMaxBytes, maxNativeReadBytes)
	roots, err := r.nativeFileAccessGrantRoots(ctx, scope, p, key, nativePathRead, "read_file")
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, err)
	}
	target, err := resolveNativeScopedTarget(scope, in.Path, nativePathRead, nativeFileAccessGrantRootPaths(roots))
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, err)
	}
	audit, auditOK := nativeFileAccessGrantRootForPath(target.Path, roots)
	defer func() {
		if auditOK {
			err = r.recordNativeFileAccessInvocation(audit, p, "read_file", err)
		}
	}()
	file, err := nativeOpenScopedReadFile(target)
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, fmt.Errorf("read_file stat %q: %w", in.Path, err))
	}
	if info.IsDir() {
		return "", fmt.Errorf("read_file path %q is a directory", in.Path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("read_file path %q is not a regular file", in.Path)
	}
	data, lines, truncated, err := readBoundedFileWindowFromReader(file, target.Path, offset, limit, maxBytes)
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, err)
	}
	if bytes.Contains(data, []byte{0}) {
		return "", fmt.Errorf("read_file path %q appears to be binary", in.Path)
	}
	limitLabel := "full"
	if limit > 0 {
		limitLabel = fmt.Sprintf("%d", limit)
	}
	var b strings.Builder
	b.WriteString("[READ_FILE]\n")
	fmt.Fprintf(&b, "path: %s\noffset: %d\nlimit: %s\nlines: %d\nbytes: %d\ntruncated: %t\nfull: %t\ncontent:\n", target.Path, offset, limitLabel, lines, len(data), truncated, in.Full)
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("[/READ_FILE]")
	return b.String(), nil
}

func (r *Registry) writeFile(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (out string, err error) {
	var in writeFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode write_file input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return "", fmt.Errorf("write_file path is required")
	}
	writeGrantRoots, err := r.nativeFileAccessGrantRoots(ctx, scope, p, key, nativePathWrite, "write_file")
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, err)
	}
	writeRoots := nativeFileAccessGrantRootPaths(writeGrantRoots)
	target, err := resolveNativeScopedTarget(scope, in.Path, nativePathWrite, writeRoots)
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, err)
	}
	audit, auditOK := nativeFileAccessGrantRootForPath(target.Path, writeGrantRoots)
	defer func() {
		if auditOK {
			err = r.recordNativeFileAccessInvocation(audit, p, "write_file", err)
		}
	}()
	file, err := nativeOpenScopedWriteFile(target, in.CreateDirs, in.Append)
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, err)
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("write_file path %q is a directory", in.Path)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("write_file path %q is not a regular file", in.Path)
		}
	} else {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, fmt.Errorf("write_file stat %q: %w", in.Path, err))
	}
	if _, err := file.WriteString(in.Content); err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, in.Path, fmt.Errorf("write_file write %q: %w", target.Path, err))
	}
	return fmt.Sprintf("write_file_ok path=%s bytes=%d append=%t", target.Path, len([]byte(in.Content)), in.Append), nil
}

func (r *Registry) listDir(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (out string, err error) {
	var in listDirInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode list_dir input: %w", err)
	}
	pathRaw := strings.TrimSpace(in.Path)
	if pathRaw == "" {
		pathRaw = "."
	}
	limit := clampNativeLimit(in.Limit, defaultNativeListLimit, maxNativeListLimit)
	roots, err := r.nativeFileAccessGrantRoots(ctx, scope, p, key, nativePathRead, "list_dir")
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, pathRaw, err)
	}
	target, err := resolveNativeScopedTarget(scope, pathRaw, nativePathRead, nativeFileAccessGrantRootPaths(roots))
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, pathRaw, err)
	}
	hidden, err := nativeHiddenPathsForAuthorityRoot(scope, target)
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, pathRaw, err)
	}
	audit, auditOK := nativeFileAccessGrantRootForPath(target.Path, roots)
	defer func() {
		if auditOK {
			err = r.recordNativeFileAccessInvocation(audit, p, "list_dir", err)
		}
	}()
	dir, err := nativeOpenScopedListDir(target)
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, pathRaw, err)
	}
	defer dir.Close()
	entries, err := nativeSortedDirEntries(dir)
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, pathRaw, fmt.Errorf("list_dir read %q: %w", pathRaw, err))
	}
	var skips nativeTraversalSkips
	lines := make([]string, 0, min(limit, len(entries)))
	visibleEntries := 0
	for _, entry := range entries {
		name := entry.Name()
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\x00") {
			skips.record("invalid_name")
			continue
		}
		childPath := filepath.Join(target.Path, name)
		if nativePathHiddenByTraversalPolicy(childPath, hidden) {
			skips.record("hidden_policy")
			continue
		}
		info, err := entry.Info()
		if err != nil {
			skips.record("stat_failed")
			continue
		}
		visibleEntries++
		if len(lines) >= limit {
			continue
		}
		kind := "file"
		if entry.IsDir() {
			kind = "dir"
		} else if info.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
		}
		lines = append(lines, fmt.Sprintf("- %s %s bytes=%d\n", name, kind, info.Size()))
	}
	var b strings.Builder
	b.WriteString("[LIST_DIR]\n")
	fmt.Fprintf(&b, "path: %s\nentries: %d\npartial: %t\nskipped_count: %d", target.Path, visibleEntries, skips.partial(), skips.skippedCount())
	if summary := skips.summary(); summary != "" {
		fmt.Fprintf(&b, "\nskipped_reasons: %s", summary)
	}
	if visibleEntries > limit {
		fmt.Fprintf(&b, "\ntruncated: true")
	}
	b.WriteString("\n")
	for _, line := range lines {
		b.WriteString(line)
	}
	b.WriteString("[/LIST_DIR]")
	return b.String(), nil
}

func (r *Registry) recordNativeFileAccessInvocation(root nativeFileAccessGrantRoot, p principal.Principal, action string, operationErr error) error {
	if r == nil || r.store == nil || strings.TrimSpace(root.Grant.GrantID) == "" {
		return operationErr
	}
	status := "succeeded"
	errorText := ""
	if operationErr != nil {
		status = "failed"
		errorText = operationErr.Error()
	}
	principalID := strings.TrimSpace(root.Grant.GrantedTo)
	if principalID == "" {
		principalID = toolAuthorityPrincipalDisplay(p)
	}
	_, recordErr := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
		GrantID:   root.Grant.GrantID,
		Principal: principalID,
		Action:    normalizeToolFileAccessOperation(action),
		Status:    status,
		ErrorText: errorText,
	}, root.UseRef))
	if recordErr != nil && operationErr == nil {
		return recordErr
	}
	return operationErr
}

func (r *Registry) recordNativeResourcePreflight(ctx context.Context, key session.SessionKey, resource string, cause error) error {
	if r == nil || r.store == nil || cause == nil || !toolSessionKeyHasIdentity(key) {
		return cause
	}
	reason := "resource_denied"
	lower := strings.ToLower(cause.Error())
	switch {
	case strings.Contains(lower, "sandbox") || strings.Contains(lower, "outside") || strings.Contains(lower, "root"):
		reason = "host_mode_denied"
	case strings.Contains(lower, "permission"):
		reason = "host_permission_denied"
	case strings.Contains(lower, "symlink"):
		reason = "path_symlink_denied"
	}
	turnRunID := int64(0)
	if ref, ok := ToolInvocationRefFromContext(ctx); ok {
		turnRunID = ref.TurnRunID
	}
	if err := r.store.RecordResourcePreflight(key, turnRunID, resource, reason, cause.Error(), time.Now().UTC()); err != nil {
		return fmt.Errorf("%w (and failed to record native resource preflight: %v)", cause, err)
	}
	return cause
}

func readBoundedFileWindow(path string, offset, limit, maxBytes int) ([]byte, int, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read_file open %q: %w", path, err)
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var out bytes.Buffer
	lineNo, lines := 0, 0
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if lineNo >= offset {
				if limit > 0 && lines >= limit {
					return out.Bytes(), lines, true, nil
				}
				if out.Len()+len(line) > maxBytes {
					remaining := maxBytes - out.Len()
					if remaining > 0 {
						out.Write(line[:remaining])
					}
					return out.Bytes(), lines, true, nil
				}
				out.Write(line)
				lines++
			}
			lineNo++
		}
		if err == io.EOF {
			return out.Bytes(), lines, false, nil
		}
		if err != nil {
			return nil, lines, false, fmt.Errorf("read_file read %q: %w", path, err)
		}
	}
}

func readBoundedFile(path string, maxBytes int) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("read_file open %q: %w", path, err)
	}
	defer file.Close()
	data, truncated, err := readBounded(file, maxBytes)
	if err != nil {
		return nil, false, fmt.Errorf("read_file read %q: %w", path, err)
	}
	return data, truncated, nil
}

func readBounded(reader io.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		maxBytes = defaultNativeReadMaxBytes
	}
	limited := io.LimitReader(reader, int64(maxBytes)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return data, truncated, nil
}

func clampNativeLimit(value, def, max int) int {
	if value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}
