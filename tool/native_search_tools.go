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

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"golang.org/x/sys/unix"
)

func (r *Registry) searchFiles(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (out string, err error) {
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
	target, roots, err := r.resolveNativeScopedTargetForOperation(ctx, scope, p, key, pathRaw, nativePathRead, "search")
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
			err = r.recordNativeFileAccessInvocation(audit, p, "search", err)
		}
	}()
	matches := make([]string, 0, limit)
	needle := strings.ToLower(query)
	var skips nativeTraversalSkips
	err = walkSearchRoot(ctx, target, hidden, maxBytes, limit, needle, &matches, &skips)
	if err != nil {
		return "", r.recordNativeResourcePreflight(ctx, key, pathRaw, err)
	}
	var b strings.Builder
	b.WriteString("[SEARCH]\n")
	fmt.Fprintf(&b, "path: %s\nquery: %s\nmatches: %d\npartial: %t\nskipped_count: %d\n", target.Path, query, len(matches), skips.partial(), skips.skippedCount())
	if summary := skips.summary(); summary != "" {
		fmt.Fprintf(&b, "skipped_reasons: %s\n", summary)
		fmt.Fprintf(&b, "partial_reasons: %s\n", summary)
	}
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

func walkSearchRoot(ctx context.Context, target nativeScopedTarget, hidden []string, maxBytes, limit int, needle string, matches *[]string, skips *nativeTraversalSkips) error {
	rootFD, err := nativeOpenRootNoFollow(target.Root, false)
	if err != nil {
		return nativeScopedOpenError("search open root", target.Root, err)
	}
	defer unix.Close(rootFD)
	fd, err := nativeOpenRelNoFollow(rootFD, target.Rel, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nativeScopedOpenError("search open", target.Path, err)
	}
	file, err := nativeFileFromFD(fd, target.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	return walkSearchOpened(ctx, file, target.Path, hidden, maxBytes, limit, needle, matches, skips, true)
}

func walkSearchOpened(ctx context.Context, file *os.File, displayPath string, hidden []string, maxBytes, limit int, needle string, matches *[]string, skips *nativeTraversalSkips, root bool) error {
	if len(*matches) >= limit {
		skips.recordOnce("result_limit")
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	info, err := file.Stat()
	if err != nil {
		if !root {
			skips.record("stat_failed")
			return nil
		}
		return fmt.Errorf("search stat %q: %w", displayPath, err)
	}
	if info.IsDir() {
		return walkSearchDir(ctx, file, displayPath, hidden, maxBytes, limit, needle, matches, skips, root)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := searchOneOpenFile(file, displayPath, maxBytes, limit, needle, matches, skips); err != nil {
		if !root {
			skips.record("read_failed")
			return nil
		}
		return err
	}
	return nil
}

func walkSearchDir(ctx context.Context, dir *os.File, displayPath string, hidden []string, maxBytes, limit int, needle string, matches *[]string, skips *nativeTraversalSkips, root bool) error {
	entries, err := nativeSortedDirEntries(dir)
	if err != nil {
		if !root {
			skips.record("read_dir_failed")
			return nil
		}
		return fmt.Errorf("search read directory %q: %w", displayPath, err)
	}
	for _, entry := range entries {
		if len(*matches) >= limit {
			skips.recordOnce("result_limit")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		name := entry.Name()
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\x00") {
			skips.record("invalid_name")
			continue
		}
		childPath := filepath.Join(displayPath, name)
		if nativePathHiddenByTraversalPolicy(childPath, hidden) {
			skips.record("hidden_policy")
			continue
		}
		fd, err := nativeOpenChildNoFollow(int(dir.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		if err != nil {
			skips.record(nativeTraversalOpenFailureReason(err))
			continue
		}
		child, err := nativeFileFromFD(fd, childPath)
		if err != nil {
			skips.record("open_failed")
			continue
		}
		err = walkSearchOpened(ctx, child, childPath, hidden, maxBytes, limit, needle, matches, skips, false)
		closeErr := child.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("search close %q: %w", childPath, closeErr)
		}
	}
	return nil
}

const nativeSearchScannerMaxTokenBytes = 64 * 1024

func searchOneOpenFile(file io.Reader, path string, maxBytes, limit int, needle string, matches *[]string, skips *nativeTraversalSkips) error {
	data, truncated, err := readBounded(file, maxBytes)
	if err != nil {
		return fmt.Errorf("search read %q: %w", path, err)
	}
	if truncated {
		skips.record("content_byte_limit")
	}
	if bytes.Contains(data, []byte{0}) {
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, nativeSearchScannerMaxTokenBytes), nativeSearchScannerMaxTokenBytes)
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
				skips.recordOnce("result_limit")
				return nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		skips.record("scanner_error")
	}
	return nil
}
