//go:build linux

package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func resolveNativeToolPath(scope sandbox.Scope, raw string, access nativePathAccess) (string, error) {
	return resolveNativeToolPathWithExtraRoots(scope, raw, access, nil)
}

func resolveNativeToolPathWithReadRoots(scope sandbox.Scope, raw string, access nativePathAccess, extraReadRoots []string) (string, error) {
	if access != nativePathRead {
		extraReadRoots = nil
	}
	return resolveNativeToolPathWithExtraRoots(scope, raw, access, extraReadRoots)
}

func (r *Registry) resolveNativeReadToolPath(ctx context.Context, scope sandbox.Scope, p principal.Principal, key session.SessionKey, raw string) (string, error) {
	return r.resolveNativeReadToolPathForOperation(ctx, scope, p, key, raw, "read_file")
}

func (r *Registry) resolveNativeReadToolPathForOperation(ctx context.Context, scope sandbox.Scope, p principal.Principal, key session.SessionKey, raw string, operation string) (string, error) {
	roots, err := r.nativeFileAccessGrantRoots(ctx, scope, p, key, nativePathRead, operation)
	if err != nil {
		return "", err
	}
	return resolveNativeToolPathWithReadRoots(scope, raw, nativePathRead, roots)
}

func (r *Registry) resolveNativeWriteToolPath(ctx context.Context, scope sandbox.Scope, p principal.Principal, key session.SessionKey, raw string) (string, error) {
	roots, err := r.nativeFileAccessGrantRoots(ctx, scope, p, key, nativePathWrite, "write_file")
	if err != nil {
		return "", err
	}
	return resolveNativeToolPathWithExtraRoots(scope, raw, nativePathWrite, roots)
}

func resolveNativeToolPathWithExtraRoots(scope sandbox.Scope, raw string, access nativePathAccess, extraRoots []string) (string, error) {
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
	if len(extraRoots) > 0 {
		allowed = append(allowed, extraRoots...)
		allowed, err = normalizeNativeRoots(allowed)
		if err != nil {
			return "", err
		}
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

func (r *Registry) nativeFileAccessGrantRoots(ctx context.Context, scope sandbox.Scope, p principal.Principal, key session.SessionKey, access nativePathAccess, operation string) ([]string, error) {
	if r == nil || r.store == nil || !toolSessionKeyHasIdentity(key) {
		return nil, nil
	}
	if _, err := r.authorityUseRefForGrant(ctx, "file_access", key); err != nil {
		return nil, nil
	}

	now := time.Now().UTC()
	roots := make([]string, 0)
	seen := make(map[string]struct{})
	for _, principalID := range nativeFileAccessPrincipalIDs(p) {
		grants, err := r.store.CapabilityGrants(200, session.CapabilityGrantStatusActive, session.CapabilityKindFileAccess, principalID)
		if err != nil {
			return nil, err
		}
		for _, grant := range grants {
			grant = session.NormalizeCapabilityGrant(grant)
			if grant.GrantedTo != principalID || !nativeFileAccessGrantAllows(grant, access, operation) {
				continue
			}
			if !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now) {
				continue
			}
			root, err := nativeCapabilityFileAccessRoot(scope, grant.TargetResource)
			if err != nil {
				continue
			}
			if _, ok := seen[root]; ok {
				continue
			}
			seen[root] = struct{}{}
			roots = append(roots, root)
		}
	}
	return normalizeNativeRootsAllowEmpty(roots)
}

func nativeFileAccessPrincipalIDs(p principal.Principal) []string {
	candidates := append([]string{}, toolAuthorityPrincipalKeys(p)...)
	candidates = append(candidates, toolAuthorityPrincipalDisplay(p))
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func nativeFileAccessGrantAllows(grant session.CapabilityGrant, access nativePathAccess, operation string) bool {
	operation = normalizeToolFileAccessOperation(operation)
	for _, action := range session.NormalizeCapabilityActions(grant.AllowedActions) {
		if action == "*" {
			return true
		}
		if access == nativePathWrite {
			switch action {
			case "write":
				return true
			case "write_file":
				return operation == "write_file"
			default:
				continue
			}
		}
		switch action {
		case "read":
			return true
		case "read_file":
			if operation == "read_file" {
				return true
			}
		case "list", "list_dir":
			if operation == "list_dir" {
				return true
			}
		case "search":
			if operation == "search" {
				return true
			}
		case "inspect":
			if operation == "list_dir" || operation == "search" {
				return true
			}
		}
	}
	return false
}

func normalizeToolFileAccessOperation(operation string) string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	operation = strings.ReplaceAll(operation, "-", "_")
	switch operation {
	case "read_file", "list_dir", "search", "write_file":
		return operation
	default:
		return ""
	}
}

func nativeCapabilityFileAccessRoot(scope sandbox.Scope, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("file_access target_resource is required")
	}
	var root string
	var err error
	if filepath.IsAbs(value) || strings.HasPrefix(value, "~") || strings.Contains(value, "{") {
		root, err = nativeScopedPath(value, scope)
	} else {
		base := strings.TrimSpace(scope.WorkingRoot)
		if base == "" {
			return "", fmt.Errorf("working root is not configured")
		}
		root, err = filepath.Abs(filepath.Clean(filepath.Join(base, value)))
	}
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("file_access target_resource %q must not be a symlink", value)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		realRoot, err = filepath.Abs(filepath.Clean(realRoot))
		if err != nil {
			return "", err
		}
		root = realRoot
	}
	return root, nil
}

func validateNativeWriteParent(scope sandbox.Scope, parent string, extraWriteRoots []string) error {
	allowed, err := nativeAllowedRoots(scope, nativePathWrite)
	if err != nil {
		return err
	}
	if len(extraWriteRoots) > 0 {
		allowed = append(allowed, extraWriteRoots...)
		allowed, err = normalizeNativeRoots(allowed)
		if err != nil {
			return err
		}
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

func validateNativeWriteParentForCreate(scope sandbox.Scope, parent string, extraWriteRoots []string) error {
	if _, err := os.Stat(parent); err == nil {
		return validateNativeWriteParent(scope, parent, extraWriteRoots)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("write_file stat parent %q: %w", parent, err)
	}
	allowed, err := nativeAllowedRoots(scope, nativePathWrite)
	if err != nil {
		return err
	}
	if len(extraWriteRoots) > 0 {
		allowed = append(allowed, extraWriteRoots...)
		allowed, err = normalizeNativeRoots(allowed)
		if err != nil {
			return err
		}
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
	rel, err := filepath.Rel(filepath.Clean(ancestor), filepath.Clean(parent))
	if err != nil {
		return fmt.Errorf("resolve write_file parent %q relative to ancestor %q: %w", parent, ancestor, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("write_file parent %q escapes existing ancestor %q", parent, ancestor)
	}
	intended := filepath.Join(realAncestor, rel)
	intended, err = filepath.Abs(filepath.Clean(intended))
	if err != nil {
		return fmt.Errorf("resolve write_file parent %q: %w", parent, err)
	}
	ancestorAllowed := pathWithinAnyRoot(realAncestor, allowed)
	if !ancestorAllowed && !nativeWriteCreateParentWithinGrantedRoot(realAncestor, intended, allowed) {
		return fmt.Errorf("write_file parent %q resolves outside writable sandbox roots", parent)
	}
	if pathWithinAnyRoot(realAncestor, hidden) {
		return fmt.Errorf("write_file parent %q is hidden by the sandbox profile", parent)
	}
	if rel == "." {
		return nil
	}
	if !pathWithinAnyRoot(intended, allowed) {
		return fmt.Errorf("write_file parent %q resolves outside writable sandbox roots", parent)
	}
	if pathWithinAnyRoot(intended, hidden) {
		return fmt.Errorf("write_file parent %q is hidden by the sandbox profile", parent)
	}
	return nil
}

func nativeWriteCreateParentWithinGrantedRoot(realAncestor string, intendedParent string, allowed []string) bool {
	for _, root := range allowed {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		root, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		if !pathWithinAnyRoot(root, []string{realAncestor}) {
			continue
		}
		if pathWithinAnyRoot(intendedParent, []string{root}) {
			return true
		}
	}
	return false
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
	roots, err := normalizeNativeRootsAllowEmpty(values)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("sandbox profile has no %s roots", "usable")
	}
	return roots, nil
}

func normalizeNativeRootsAllowEmpty(values []string) ([]string, error) {
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
	return roots, nil
}
