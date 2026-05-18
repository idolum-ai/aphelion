//go:build linux

package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const (
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
			Description: "Read a bounded text file through the current sandbox profile. Relative paths resolve inside the current working root; hidden and out-of-scope paths are rejected.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to read. Relative paths are scoped to the current working root."},
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
			Description: "List a scoped directory through the current sandbox profile.",
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
			Description: "Search text files under a scoped path with literal matching. Hidden and out-of-scope paths are skipped or rejected.",
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
	data, truncated, err := readBoundedFile(path, maxBytes)
	if err != nil {
		return "", err
	}
	if bytes.Contains(data, []byte{0}) {
		return "", fmt.Errorf("read_file path %q appears to be binary", in.Path)
	}
	var b strings.Builder
	b.WriteString("[READ_FILE]\n")
	fmt.Fprintf(&b, "path: %s\nbytes: %d\ntruncated: %t\ncontent:\n", path, len(data), truncated)
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

func (r *Registry) searchFiles(ctx context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
	var in searchFilesInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode search input: %w", err)
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return "", fmt.Errorf("search query is required")
	}
	pathRaw := strings.TrimSpace(in.Path)
	if pathRaw == "" {
		pathRaw = "."
	}
	limit := clampNativeLimit(in.Limit, defaultNativeSearchLimit, maxNativeSearchLimit)
	maxBytes := clampNativeLimit(in.MaxBytes, defaultNativeSearchMaxBytes, maxNativeSearchMaxBytes)
	root, err := resolveNativeToolPath(scope, pathRaw, nativePathRead)
	if err != nil {
		return "", err
	}
	matches := make([]string, 0, limit)
	needle := strings.ToLower(query)
	err = walkSearchRoot(ctx, root, maxBytes, limit, needle, &matches, scope)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("[SEARCH]\n")
	fmt.Fprintf(&b, "path: %s\nquery: %s\nmatches: %d\n", root, query, len(matches))
	if len(matches) == 0 {
		b.WriteString("[/SEARCH]")
		return b.String(), nil
	}
	for _, match := range matches {
		b.WriteString(match)
		b.WriteByte('\n')
	}
	b.WriteString("[/SEARCH]")
	return b.String(), nil
}

func (r *Registry) fetchURL(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal) (string, error) {
	var in fetchURLInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode fetch_url input: %w", err)
	}
	raw := strings.TrimSpace(in.URL)
	if raw == "" {
		return "", fmt.Errorf("fetch_url url is required")
	}
	if scope.Profile.Network == sandbox.NetworkDeny {
		return "", fmt.Errorf("fetch_url denied by sandbox network policy")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("fetch_url parse url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("fetch_url only supports http and https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("fetch_url rejects URLs with embedded credentials")
	}
	if p.Role != principal.RoleAdmin && hostLooksLocal(parsed.Hostname()) {
		return "", fmt.Errorf("fetch_url rejects local/private hosts for non-admin principals")
	}
	transport := http.DefaultTransport
	var fetchPolicy *nativeFetchNetworkPolicy
	if scope.Profile.Mode == sandbox.ModeIsolated && scope.Profile.Network == sandbox.NetworkAllowlist {
		allowlistTransport, policy, err := r.fetchURLAllowlistTransport(ctx, scope.Profile, p.Role != principal.RoleAdmin)
		if err != nil {
			return "", err
		}
		transport = allowlistTransport
		fetchPolicy = policy
	}
	maxBytes := clampNativeLimit(in.MaxBytes, defaultNativeFetchMaxBytes, maxNativeFetchBytes)
	client := &http.Client{Timeout: 20 * time.Second, Transport: transport}
	if fetchPolicy != nil {
		client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
			return fetchPolicy.authorizeURL(req.Context(), req.URL)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("fetch_url create request: %w", err)
	}
	if userAgent := strings.TrimSpace(r.nativeFetchUserAgent); userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if fetchPolicy != nil {
		if err := fetchPolicy.authorizeURL(ctx, parsed); err != nil {
			return "", err
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch_url request: %w", err)
	}
	defer res.Body.Close()
	data, truncated, err := readBounded(res.Body, maxBytes)
	if err != nil {
		return "", fmt.Errorf("fetch_url read response: %w", err)
	}
	var b strings.Builder
	b.WriteString("[FETCH_URL]\n")
	fmt.Fprintf(&b, "url: %s\nstatus: %s\ncontent_type: %s\nbytes: %d\ntruncated: %t\nbody:\n",
		parsed.String(),
		res.Status,
		res.Header.Get("Content-Type"),
		len(data),
		truncated,
	)
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("[/FETCH_URL]")
	return b.String(), nil
}

func fetchURLPort(parsed *url.URL) (uint16, error) {
	if parsed == nil {
		return 0, fmt.Errorf("fetch_url url is required")
	}
	if raw := strings.TrimSpace(parsed.Port()); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port <= 0 || port > 65535 {
			return 0, fmt.Errorf("fetch_url port must be between 1 and 65535")
		}
		return uint16(port), nil
	}
	switch parsed.Scheme {
	case "http":
		return 80, nil
	case "https":
		return 443, nil
	default:
		return 0, fmt.Errorf("fetch_url only supports http and https")
	}
}

func walkSearchRoot(ctx context.Context, root string, maxBytes, limit int, needle string, matches *[]string, scope sandbox.Scope) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("search stat %q: %w", root, err)
	}
	if !info.IsDir() {
		return searchOneFile(root, maxBytes, limit, needle, matches)
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(*matches) >= limit {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if path != root {
			resolved, err := resolveNativeToolPath(scope, path, nativePathRead)
			if err != nil {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			path = resolved
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		return searchOneFile(path, maxBytes, limit, needle, matches)
	})
}

func searchOneFile(path string, maxBytes, limit int, needle string, matches *[]string) error {
	if len(*matches) >= limit {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, _, err := readBounded(file, maxBytes)
	if err != nil || bytes.Contains(data, []byte{0}) {
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), needle) {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) > 400 {
				trimmed = truncate(trimmed, 400)
			}
			*matches = append(*matches, fmt.Sprintf("%s:%d: %s", path, lineNo, trimmed))
			if len(*matches) >= limit {
				return nil
			}
		}
	}
	return nil
}

func resolveNativeToolPath(scope sandbox.Scope, raw string, access nativePathAccess) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}
	root := strings.TrimSpace(scope.WorkingRoot)
	if root == "" {
		return "", fmt.Errorf("working root is not configured")
	}
	var target string
	if filepath.IsAbs(raw) {
		target = filepath.Clean(raw)
	} else {
		target = filepath.Join(root, raw)
	}
	target, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", raw, err)
	}
	allowed, err := nativeAllowedRoots(scope, access)
	if err != nil {
		return "", err
	}
	if !pathWithinAnyRoot(target, allowed) {
		return "", fmt.Errorf("path %q is outside the %s roots for this sandbox profile", raw, access)
	}
	if hidden, _ := nativeHiddenPaths(scope); pathWithinAnyRoot(target, hidden) {
		return "", fmt.Errorf("path %q is hidden by the sandbox profile", raw)
	}
	if realTarget, err := filepath.EvalSymlinks(target); err == nil {
		realTarget, err = filepath.Abs(filepath.Clean(realTarget))
		if err != nil {
			return "", fmt.Errorf("resolve symlink target %q: %w", raw, err)
		}
		if !pathWithinAnyRoot(realTarget, allowed) {
			return "", fmt.Errorf("path %q resolves outside the %s roots for this sandbox profile", raw, access)
		}
		if hidden, _ := nativeHiddenPaths(scope); pathWithinAnyRoot(realTarget, hidden) {
			return "", fmt.Errorf("path %q resolves to a hidden sandbox path", raw)
		}
		target = realTarget
	}
	return target, nil
}

func validateNativeWriteParent(scope sandbox.Scope, parent string) error {
	allowed, err := nativeAllowedRoots(scope, nativePathWrite)
	if err != nil {
		return err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("write_file parent %q is not available; set create_dirs when it should be created: %w", parent, err)
	}
	realParent, err = filepath.Abs(filepath.Clean(realParent))
	if err != nil {
		return fmt.Errorf("resolve write_file parent %q: %w", parent, err)
	}
	if !pathWithinAnyRoot(realParent, allowed) {
		return fmt.Errorf("write_file parent %q resolves outside writable sandbox roots", parent)
	}
	if hidden, _ := nativeHiddenPaths(scope); pathWithinAnyRoot(realParent, hidden) {
		return fmt.Errorf("write_file parent %q is hidden by the sandbox profile", parent)
	}
	return nil
}

func validateNativeWriteParentForCreate(scope sandbox.Scope, parent string) error {
	if _, err := os.Stat(parent); err == nil {
		return validateNativeWriteParent(scope, parent)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("write_file stat parent %q: %w", parent, err)
	}
	allowed, err := nativeAllowedRoots(scope, nativePathWrite)
	if err != nil {
		return err
	}
	hidden, _ := nativeHiddenPaths(scope)
	ancestor := filepath.Clean(parent)
	for {
		if info, err := os.Stat(ancestor); err == nil {
			if !info.IsDir() {
				return fmt.Errorf("write_file parent ancestor %q is not a directory", ancestor)
			}
			return validateNativeWriteAncestorForCreate(parent, ancestor, allowed, hidden)
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("write_file stat parent ancestor %q: %w", ancestor, err)
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return fmt.Errorf("write_file parent %q has no existing writable ancestor", parent)
		}
		ancestor = next
	}
}

func validateNativeWriteAncestorForCreate(parent string, ancestor string, allowed []string, hidden []string) error {
	realAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("resolve write_file parent ancestor %q: %w", ancestor, err)
	}
	realAncestor, err = filepath.Abs(filepath.Clean(realAncestor))
	if err != nil {
		return fmt.Errorf("resolve write_file parent ancestor %q: %w", ancestor, err)
	}
	if !pathWithinAnyRoot(realAncestor, allowed) {
		return fmt.Errorf("write_file parent %q resolves outside writable sandbox roots", parent)
	}
	if pathWithinAnyRoot(realAncestor, hidden) {
		return fmt.Errorf("write_file parent %q is hidden by the sandbox profile", parent)
	}
	rel, err := filepath.Rel(filepath.Clean(ancestor), filepath.Clean(parent))
	if err != nil {
		return fmt.Errorf("resolve write_file parent %q relative to ancestor %q: %w", parent, ancestor, err)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("write_file parent %q escapes existing ancestor %q", parent, ancestor)
	}
	intended := filepath.Join(realAncestor, rel)
	intended, err = filepath.Abs(filepath.Clean(intended))
	if err != nil {
		return fmt.Errorf("resolve write_file parent %q: %w", parent, err)
	}
	if !pathWithinAnyRoot(intended, allowed) {
		return fmt.Errorf("write_file parent %q resolves outside writable sandbox roots", parent)
	}
	if pathWithinAnyRoot(intended, hidden) {
		return fmt.Errorf("write_file parent %q is hidden by the sandbox profile", parent)
	}
	return nil
}

func nativeAllowedRoots(scope sandbox.Scope, access nativePathAccess) ([]string, error) {
	writable, err := nativeScopedPaths(scope.Profile.WritablePaths, scope)
	if err != nil {
		return nil, err
	}
	writable = append(writable, scope.WorkingRoot)
	if scope.Profile.Mode == sandbox.ModeTrusted || scope.Principal.Role == principal.RoleAdmin || scope.Profile.Mode == "" {
		writable = append(writable, scope.WorkingRoot)
	}
	if access == nativePathWrite {
		return normalizeNativeRoots(writable)
	}
	readonly, err := nativeScopedPaths(scope.Profile.ReadonlyPaths, scope)
	if err != nil {
		return nil, err
	}
	readonly = append(readonly, writable...)
	if scope.Profile.Mode == sandbox.ModeTrusted || scope.Principal.Role == principal.RoleAdmin || scope.Profile.Mode == "" {
		readonly = append(readonly, scope.GlobalRoot, scope.SharedMemoryRoot, scope.UserWorkspace, scope.UserMemory)
	}
	return normalizeNativeRoots(readonly)
}

func nativeHiddenPaths(scope sandbox.Scope) ([]string, error) {
	hidden, err := nativeScopedPaths(scope.Profile.HiddenPaths, scope)
	if err != nil {
		return nil, err
	}
	return normalizeNativeRoots(hidden)
}

func nativeScopedPaths(values []string, scope sandbox.Scope) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		path, err := nativeScopedPath(value, scope)
		if err != nil {
			return nil, err
		}
		if path != "" {
			out = append(out, path)
		}
	}
	return out, nil
}

func nativeScopedPath(value string, scope sandbox.Scope) (string, error) {
	p := strings.TrimSpace(value)
	if p == "" {
		return "", nil
	}
	replacements := map[string]string{
		"{global_root}":        scope.GlobalRoot,
		"{shared_memory_root}": scope.SharedMemoryRoot,
		"{user_workspace}":     scope.UserWorkspace,
		"{user_memory}":        scope.UserMemory,
		"{working_root}":       scope.WorkingRoot,
	}
	for token, target := range replacements {
		if strings.Contains(p, token) {
			target = strings.TrimSpace(target)
			if target == "" {
				return "", nil
			}
			p = strings.ReplaceAll(p, token, target)
		}
	}
	if p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~: %w", err)
		}
		p = home
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~/ path %q: %w", value, err)
		}
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("sandbox path %q resolved to non-absolute %q", value, p)
	}
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", fmt.Errorf("resolve sandbox path %q: %w", value, err)
	}
	return abs, nil
}

func normalizeNativeRoots(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	roots := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		abs, err := filepath.Abs(filepath.Clean(value))
		if err != nil {
			return nil, fmt.Errorf("resolve sandbox root %q: %w", value, err)
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("sandbox profile has no %s roots", "usable")
	}
	return roots, nil
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

func hostLooksLocal(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
