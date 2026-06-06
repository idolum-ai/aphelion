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
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const (
	maxNativeReadLines          = 10000
	defaultNativeReadMaxBytes   = 64 * 1024
	maxNativeReadBytes          = 512 * 1024
	defaultNativeFetchMaxBytes  = 128 * 1024
	maxNativeFetchBytes         = 1024 * 1024
	defaultNativeListLimit      = 100
	maxNativeListLimit          = 500
	defaultNativeSearchLimit    = 25
	maxNativeSearchLimit        = 100
	defaultNativeSearchMaxBytes = 128 * 1024
	maxNativeSearchMaxBytes     = 512 * 1024

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
	URL      string `json:"url"`
	MaxBytes int    `json:"max_bytes,omitempty"`
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
			Name:        "fetch_url",
			Description: "Fetch a bounded HTTP(S) URL when the current sandbox profile allows network access. Network-denied profiles cannot use this tool.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "HTTP or HTTPS URL to fetch."},
					"max_bytes": {"type": "integer", "minimum": 1, "maximum": 1048576, "description": "Maximum response body bytes to return; defaults to 131072."}
				},
				"required": ["url"]
			}`),
		},
	}
}

func (r *Registry) readFile(_ context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
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
	path, err := resolveNativeToolPath(scope, in.Path, nativePathRead)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read_file stat %q: %w", in.Path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("read_file path %q is a directory", in.Path)
	}
	data, lines, truncated, err := readBoundedFileWindow(path, offset, limit, maxBytes)
	if err != nil {
		return "", err
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
	fmt.Fprintf(&b, "path: %s\noffset: %d\nlimit: %s\nlines: %d\nbytes: %d\ntruncated: %t\nfull: %t\ncontent:\n", path, offset, limitLabel, lines, len(data), truncated, in.Full)
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("[/READ_FILE]")
	return b.String(), nil
}

func (r *Registry) writeFile(_ context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
	var in writeFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode write_file input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return "", fmt.Errorf("write_file path is required")
	}
	path, err := resolveNativeToolPath(scope, in.Path, nativePathWrite)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(path)
	if in.CreateDirs {
		if err := validateNativeWriteParentForCreate(scope, parent); err != nil {
			return "", err
		}
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", fmt.Errorf("write_file create parent %q: %w", parent, err)
		}
	}
	if err := validateNativeWriteParent(scope, parent); err != nil {
		return "", err
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return "", fmt.Errorf("write_file path %q is a directory", in.Path)
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("write_file stat %q: %w", in.Path, err)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if in.Append {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return "", fmt.Errorf("write_file open %q: %w", in.Path, err)
	}
	defer file.Close()
	if _, err := file.WriteString(in.Content); err != nil {
		return "", fmt.Errorf("write_file write %q: %w", in.Path, err)
	}
	return fmt.Sprintf("write_file_ok path=%s bytes=%d append=%t", path, len([]byte(in.Content)), in.Append), nil
}

func (r *Registry) listDir(_ context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
	var in listDirInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode list_dir input: %w", err)
	}
	pathRaw := strings.TrimSpace(in.Path)
	if pathRaw == "" {
		pathRaw = "."
	}
	limit := clampNativeLimit(in.Limit, defaultNativeListLimit, maxNativeListLimit)
	path, err := resolveNativeToolPath(scope, pathRaw, nativePathRead)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("list_dir read %q: %w", pathRaw, err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var b strings.Builder
	b.WriteString("[LIST_DIR]\n")
	fmt.Fprintf(&b, "path: %s\nentries: %d", path, len(entries))
	if len(entries) > limit {
		fmt.Fprintf(&b, "\ntruncated: true")
	}
	b.WriteString("\n")
	for i, entry := range entries {
		if i >= limit {
			break
		}
		info, err := entry.Info()
		if err != nil {
			fmt.Fprintf(&b, "- %s unknown\n", entry.Name())
			continue
		}
		kind := "file"
		if entry.IsDir() {
			kind = "dir"
		} else if info.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
		}
		fmt.Fprintf(&b, "- %s %s bytes=%d\n", entry.Name(), kind, info.Size())
	}
	b.WriteString("[/LIST_DIR]")
	return b.String(), nil
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
