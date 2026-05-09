//go:build linux

package tool

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

func safeDirectoryUnderRootNoSymlink(root string, rel string) (string, error) {
	_, dirPath, dirFD, err := openSafeDirectoryUnderRoot(root, rel)
	if err != nil {
		return "", err
	}
	if err := syscall.Close(dirFD); err != nil {
		return "", fmt.Errorf("close safe directory %s: %w", dirPath, err)
	}
	return dirPath, nil
}

func safeWriteFileUnderRootNoSymlink(root string, rel string, data []byte, perm os.FileMode) (string, error) {
	dirRel, base, cleanRel, err := cleanSafeRelativeFilePath(rel)
	if err != nil {
		return "", err
	}
	rootAbs, _, dirFD, err := openSafeDirectoryUnderRoot(root, dirRel)
	if err != nil {
		return "", err
	}
	defer syscall.Close(dirFD)

	targetPath := filepath.Join(rootAbs, filepath.FromSlash(cleanRel))
	if !pathWithinAnyRoot(targetPath, []string{rootAbs}) {
		return "", fmt.Errorf("safe write target %q escapes root %q", cleanRel, rootAbs)
	}
	fd, err := syscall.Openat(dirFD, base, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, uint32(perm.Perm()))
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return "", fmt.Errorf("refuse to write through symlink: %s", targetPath)
		}
		return "", fmt.Errorf("open safe write target %s: %w", targetPath, err)
	}
	file := os.NewFile(uintptr(fd), targetPath)
	if file == nil {
		_ = syscall.Close(fd)
		return "", fmt.Errorf("open safe write target %s: invalid file descriptor", targetPath)
	}

	n, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return "", fmt.Errorf("write safe file %s: %w", targetPath, writeErr)
	}
	if n != len(data) {
		return "", fmt.Errorf("write safe file %s: %w", targetPath, io.ErrShortWrite)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close safe file %s: %w", targetPath, closeErr)
	}
	return targetPath, nil
}

func openSafeDirectoryUnderRoot(root string, rel string) (string, string, int, error) {
	cleanRel, err := cleanSafeRelativePath(rel)
	if err != nil {
		return "", "", -1, err
	}
	rootAbs, dirFD, err := openSafeRootDirectory(root)
	if err != nil {
		return "", "", -1, err
	}
	dirPath := rootAbs
	if cleanRel == "." {
		return rootAbs, dirPath, dirFD, nil
	}
	for _, part := range strings.Split(cleanRel, "/") {
		nextFD, err := openOrCreateSafeChildDirectory(dirFD, part)
		if err != nil {
			_ = syscall.Close(dirFD)
			return "", "", -1, err
		}
		_ = syscall.Close(dirFD)
		dirFD = nextFD
		dirPath = filepath.Join(dirPath, part)
		if !pathWithinAnyRoot(dirPath, []string{rootAbs}) {
			_ = syscall.Close(dirFD)
			return "", "", -1, fmt.Errorf("safe directory %q escapes root %q", cleanRel, rootAbs)
		}
	}
	return rootAbs, dirPath, dirFD, nil
}

func openSafeRootDirectory(root string) (string, int, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", -1, fmt.Errorf("safe write root is required")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", -1, fmt.Errorf("resolve safe write root: %w", err)
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return "", -1, fmt.Errorf("create safe write root %s: %w", rootAbs, err)
	}
	fd, err := syscall.Open(rootAbs, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return "", -1, fmt.Errorf("refuse to use symlink root: %s", rootAbs)
		}
		return "", -1, fmt.Errorf("open safe write root %s: %w", rootAbs, err)
	}
	return rootAbs, fd, nil
}

func openOrCreateSafeChildDirectory(parentFD int, name string) (int, error) {
	fd, err := syscall.Openat(parentFD, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err == nil {
		return fd, nil
	}
	if errors.Is(err, syscall.ENOENT) {
		if mkErr := syscall.Mkdirat(parentFD, name, 0o755); mkErr != nil && !errors.Is(mkErr, syscall.EEXIST) {
			return -1, fmt.Errorf("create safe directory %q: %w", name, mkErr)
		}
		fd, err = syscall.Openat(parentFD, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if err == nil {
			return fd, nil
		}
	}
	if errors.Is(err, syscall.ELOOP) {
		return -1, fmt.Errorf("refuse to use symlink directory %q", name)
	}
	return -1, fmt.Errorf("open safe directory %q: %w", name, err)
}

func cleanSafeRelativeFilePath(raw string) (string, string, string, error) {
	cleanRel, err := cleanSafeRelativePath(raw)
	if err != nil {
		return "", "", "", err
	}
	if cleanRel == "." {
		return "", "", "", fmt.Errorf("safe write file path is required")
	}
	base := path.Base(cleanRel)
	if base == "." || base == ".." || base == "" {
		return "", "", "", fmt.Errorf("safe write file path %q is invalid", raw)
	}
	return path.Dir(cleanRel), base, cleanRel, nil
}

func cleanSafeRelativePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ".", nil
	}
	if strings.Contains(raw, "\x00") || strings.Contains(raw, "\\") {
		return "", fmt.Errorf("safe path %q is not allowed", raw)
	}
	if strings.HasPrefix(raw, "/") || filepath.IsAbs(raw) {
		return "", fmt.Errorf("safe path %q must be relative", raw)
	}
	cleanRel := path.Clean(filepath.ToSlash(raw))
	if cleanRel == "." {
		return ".", nil
	}
	if cleanRel == ".." || strings.HasPrefix(cleanRel, "../") {
		return "", fmt.Errorf("safe path %q must stay under root", raw)
	}
	for _, part := range strings.Split(cleanRel, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("safe path %q is invalid", raw)
		}
	}
	return cleanRel, nil
}
