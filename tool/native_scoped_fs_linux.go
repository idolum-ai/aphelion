//go:build linux

package tool

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/tool/sandbox"
	"golang.org/x/sys/unix"
)

type nativeScopedTarget struct {
	Root string
	Rel  string
	Path string
}

func resolveNativeScopedTarget(scope sandbox.Scope, raw string, access nativePathAccess, extraRoots []string) (nativeScopedTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nativeScopedTarget{}, fmt.Errorf("path is required")
	}
	root := strings.TrimSpace(scope.WorkingRoot)
	if root == "" {
		return nativeScopedTarget{}, fmt.Errorf("working root is not configured")
	}
	var target string
	if filepath.IsAbs(raw) {
		target = filepath.Clean(raw)
	} else {
		target = filepath.Join(root, raw)
	}
	target, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return nativeScopedTarget{}, fmt.Errorf("resolve path %q: %w", raw, err)
	}
	allowed, err := nativeAllowedRoots(scope, access)
	if err != nil {
		return nativeScopedTarget{}, err
	}
	if len(extraRoots) > 0 {
		allowed = append(allowed, extraRoots...)
		allowed, err = normalizeNativeRoots(allowed)
		if err != nil {
			return nativeScopedTarget{}, err
		}
	}
	if !pathWithinAnyRoot(target, allowed) {
		return nativeScopedTarget{}, fmt.Errorf("path %q is outside the %s roots for this sandbox profile", raw, access)
	}
	hidden, err := nativeHiddenPaths(scope)
	if err != nil {
		return nativeScopedTarget{}, err
	}
	if pathWithinAnyRoot(target, hidden) {
		return nativeScopedTarget{}, fmt.Errorf("path %q is hidden by the sandbox profile", raw)
	}
	root, ok := nativeBestContainingRoot(target, allowed)
	if !ok {
		return nativeScopedTarget{}, fmt.Errorf("path %q is outside the %s roots for this sandbox profile", raw, access)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return nativeScopedTarget{}, fmt.Errorf("resolve path %q relative to scoped root: %w", raw, err)
	}
	cleanRel, err := cleanSafeRelativePath(filepath.ToSlash(rel))
	if err != nil {
		return nativeScopedTarget{}, err
	}
	return nativeScopedTarget{Root: root, Rel: cleanRel, Path: target}, nil
}

func nativeBestContainingRoot(target string, roots []string) (string, bool) {
	target, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", false
	}
	best := ""
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		root, err = filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		if !pathWithinAnyRoot(target, []string{root}) {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	return best, best != ""
}

func nativeOpenScopedReadFile(target nativeScopedTarget) (*os.File, error) {
	rootFD, err := nativeOpenRootNoFollow(target.Root, false)
	if err != nil {
		return nil, nativeScopedOpenError("read_file open root", target.Root, err)
	}
	defer unix.Close(rootFD)
	fd, err := nativeOpenRelNoFollow(rootFD, target.Rel, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nativeScopedOpenError("read_file open", target.Path, err)
	}
	file, err := nativeFileFromFD(fd, target.Path)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func nativeOpenScopedListDir(target nativeScopedTarget) (*os.File, error) {
	rootFD, err := nativeOpenRootNoFollow(target.Root, false)
	if err != nil {
		return nil, nativeScopedOpenError("list_dir open root", target.Root, err)
	}
	defer unix.Close(rootFD)
	fd, err := nativeOpenRelNoFollow(rootFD, target.Rel, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nativeScopedOpenError("list_dir open", target.Path, err)
	}
	file, err := nativeFileFromFD(fd, target.Path)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func nativeHiddenPathsForAuthorityRoot(scope sandbox.Scope, target nativeScopedTarget) ([]string, error) {
	hidden, err := nativeHiddenPaths(scope)
	if err != nil {
		return nil, err
	}
	if len(hidden) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(hidden))
	for _, root := range hidden {
		if pathWithinAnyRoot(root, []string{target.Root}) || pathWithinAnyRoot(target.Root, []string{root}) {
			out = append(out, root)
		}
	}
	return out, nil
}

func nativePathHiddenByTraversalPolicy(path string, hidden []string) bool {
	return len(hidden) > 0 && pathWithinAnyRoot(path, hidden)
}

type nativeTraversalSkips struct {
	count   int
	reasons map[string]int
}

func (s *nativeTraversalSkips) record(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	if s.reasons == nil {
		s.reasons = make(map[string]int)
	}
	s.count++
	s.reasons[reason]++
}

func (s *nativeTraversalSkips) recordOnce(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	if s.reasons != nil && s.reasons[reason] > 0 {
		return
	}
	s.record(reason)
}

func (s nativeTraversalSkips) partial() bool {
	return s.count > 0
}

func (s nativeTraversalSkips) skippedCount() int {
	return s.count
}

func (s nativeTraversalSkips) summary() string {
	if len(s.reasons) == 0 {
		return ""
	}
	keys := make([]string, 0, len(s.reasons))
	for key := range s.reasons {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, s.reasons[key]))
	}
	return strings.Join(parts, ",")
}

func nativeTraversalOpenFailureReason(err error) string {
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
		return "permission_denied"
	}
	return "open_failed"
}

func nativeOpenScopedWriteFile(target nativeScopedTarget, createDirs, appendFile bool) (*os.File, error) {
	dirRel, base, _, err := cleanSafeRelativeFilePath(target.Rel)
	if err != nil {
		return nil, err
	}
	rootFD, err := nativeOpenRootNoFollow(target.Root, createDirs)
	if err != nil {
		return nil, nativeScopedOpenError("write_file open root", target.Root, err)
	}
	defer unix.Close(rootFD)
	parentFD, err := nativeOpenDirBeneathNoFollow(rootFD, dirRel, createDirs)
	if err != nil {
		parent := filepath.Dir(target.Path)
		if !createDirs && errors.Is(err, unix.ENOENT) {
			return nil, fmt.Errorf("write_file parent %q is not available; set create_dirs when it should be created: %w", parent, err)
		}
		return nil, nativeScopedOpenError("write_file open parent", parent, err)
	}
	defer unix.Close(parentFD)

	fd, err := nativeOpenWritableRegularChild(parentFD, base, target.Path, appendFile)
	if err != nil {
		return nil, nativeScopedOpenError("write_file open", target.Path, err)
	}
	file, err := nativeFileFromFD(fd, target.Path)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func nativeOpenWritableRegularChild(parentFD int, base string, displayPath string, appendFile bool) (int, error) {
	flags := unix.O_WRONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if appendFile {
		flags |= unix.O_APPEND
	}
	fd, err := nativeOpenChildNoFollow(parentFD, base, flags, 0)
	if errors.Is(err, unix.ENOENT) {
		fd, err = nativeOpenChildNoFollow(parentFD, base, flags|unix.O_CREAT|unix.O_EXCL, 0o600)
		if errors.Is(err, unix.EEXIST) {
			fd, err = nativeOpenChildNoFollow(parentFD, base, flags, 0)
		}
	}
	if err != nil {
		return -1, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("stat scoped write target %s: %w", displayPath, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("write_file target %s is not a regular file", displayPath)
	}
	if !appendFile {
		if err := unix.Ftruncate(fd, 0); err != nil {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("truncate scoped write target %s: %w", displayPath, err)
		}
		if _, err := unix.Seek(fd, 0, io.SeekStart); err != nil {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("seek scoped write target %s: %w", displayPath, err)
		}
	}
	return fd, nil
}

func nativeOpenRootNoFollow(root string, create bool) (int, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return -1, fmt.Errorf("scoped root is required")
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return -1, fmt.Errorf("resolve scoped root %q: %w", root, err)
	}
	if !filepath.IsAbs(rootAbs) {
		return -1, fmt.Errorf("scoped root %q resolved to non-absolute path", root)
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open filesystem root: %w", err)
	}
	trimmed := strings.TrimPrefix(rootAbs, string(filepath.Separator))
	if trimmed == "" {
		return fd, nil
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("scoped root %q has invalid component %q", rootAbs, part)
		}
		next, err := nativeOpenChildDirNoFollow(fd, part)
		if errors.Is(err, unix.ENOENT) && create {
			if mkErr := unix.Mkdirat(fd, part, 0o755); mkErr != nil && !errors.Is(mkErr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, fmt.Errorf("create scoped root component %q under %q: %w", part, current, mkErr)
			}
			next, err = nativeOpenChildDirNoFollow(fd, part)
		}
		if err != nil {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("open scoped root component %q under %q: %w", part, current, err)
		}
		_ = unix.Close(fd)
		fd = next
		current = filepath.Join(current, part)
	}
	return fd, nil
}

func nativeOpenDirBeneathNoFollow(rootFD int, rel string, create bool) (int, error) {
	cleanRel, err := cleanSafeRelativePath(rel)
	if err != nil {
		return -1, err
	}
	if !create {
		return nativeOpenRelNoFollow(rootFD, cleanRel, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	}
	fd, err := nativeOpenRelNoFollow(rootFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	if cleanRel == "." {
		return fd, nil
	}
	for _, part := range strings.Split(cleanRel, "/") {
		next, err := nativeOpenChildDirNoFollow(fd, part)
		if errors.Is(err, unix.ENOENT) {
			if mkErr := unix.Mkdirat(fd, part, 0o755); mkErr != nil && !errors.Is(mkErr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, fmt.Errorf("create scoped directory %q: %w", part, mkErr)
			}
			next, err = nativeOpenChildDirNoFollow(fd, part)
		}
		if err != nil {
			_ = unix.Close(fd)
			return -1, err
		}
		_ = unix.Close(fd)
		fd = next
	}
	return fd, nil
}

func nativeOpenChildDirNoFollow(parentFD int, name string) (int, error) {
	return nativeOpenChildNoFollow(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}

func nativeOpenChildNoFollow(parentFD int, name string, flags int, mode uint32) (int, error) {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\x00") {
		return -1, fmt.Errorf("scoped path component %q is invalid", name)
	}
	return nativeOpenRelNoFollow(parentFD, name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
}

func nativeOpenRelNoFollow(rootFD int, rel string, flags int, mode uint32) (int, error) {
	cleanRel, err := cleanSafeRelativePath(rel)
	if err != nil {
		return -1, err
	}
	flags |= unix.O_CLOEXEC
	how := &unix.OpenHow{
		Flags:   uint64(flags),
		Mode:    uint64(mode),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS,
	}
	fd, err := unix.Openat2(rootFD, cleanRel, how)
	if err == nil {
		return fd, nil
	}
	if !nativeOpenat2Unsupported(err) {
		return -1, err
	}
	return nativeOpenRelNoFollowFallback(rootFD, cleanRel, flags, mode)
}

func nativeOpenat2Unsupported(err error) bool {
	return errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL)
}

func nativeOpenRelNoFollowFallback(rootFD int, rel string, flags int, mode uint32) (int, error) {
	cleanRel, err := cleanSafeRelativePath(rel)
	if err != nil {
		return -1, err
	}
	if cleanRel == "." {
		return unix.Openat(rootFD, ".", flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
	}
	dirRel, base, _, err := cleanSafeRelativeFilePath(cleanRel)
	if err != nil {
		return -1, err
	}
	parentFD := rootFD
	closeParent := false
	if dirRel != "." {
		parentFD, err = nativeOpenDirBeneathNoFollow(rootFD, dirRel, false)
		if err != nil {
			return -1, err
		}
		closeParent = true
	}
	if closeParent {
		defer unix.Close(parentFD)
	}
	return unix.Openat(parentFD, base, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
}

func nativeFileFromFD(fd int, name string) (*os.File, error) {
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open scoped file %s: invalid file descriptor", name)
	}
	return file, nil
}

func nativeScopedOpenError(operation string, path string, err error) error {
	if errors.Is(err, unix.ELOOP) {
		return fmt.Errorf("%s %q refused symlink component: %w", operation, path, err)
	}
	if errors.Is(err, unix.ENOTDIR) {
		return fmt.Errorf("%s %q refused non-directory or symlink component: %w", operation, path, err)
	}
	return fmt.Errorf("%s %q: %w", operation, path, err)
}

func readBoundedFileWindowFromReader(reader io.Reader, label string, offset, limit, maxBytes int) ([]byte, int, bool, error) {
	buffered := bufio.NewReader(reader)
	var out bytes.Buffer
	lineNo, lines := 0, 0
	for {
		line, err := buffered.ReadBytes('\n')
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
		if errors.Is(err, io.EOF) {
			return out.Bytes(), lines, false, nil
		}
		if err != nil {
			return nil, lines, false, fmt.Errorf("read_file read %q: %w", label, err)
		}
	}
}

func nativeSortedDirEntries(dir *os.File) ([]os.DirEntry, error) {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}
