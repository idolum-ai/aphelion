//go:build linux

package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) searchFiles(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (string, error) {
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
	roots, err := r.nativeFileAccessGrantRoots(ctx, scope, p, key, nativePathRead, "search")
	if err != nil {
		return "", err
	}
	root, err := resolveNativeToolPathWithReadRoots(scope, pathRaw, nativePathRead, roots)
	if err != nil {
		return "", err
	}
	matches := make([]string, 0, limit)
	needle := strings.ToLower(query)
	err = walkSearchRoot(ctx, root, maxBytes, limit, needle, &matches, scope, roots)
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

func walkSearchRoot(ctx context.Context, root string, maxBytes, limit int, needle string, matches *[]string, scope sandbox.Scope, extraReadRoots []string) error {
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
			resolved, err := resolveNativeToolPathWithReadRoots(scope, path, nativePathRead, extraReadRoots)
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
