//go:build linux

package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

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
