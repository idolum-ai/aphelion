//go:build linux

package tool

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
)

func requireAdminTool(p principal.Principal, toolName string) error {
	if p.Role == "" || p.Role == principal.RoleAdmin {
		return nil
	}
	return fmt.Errorf("%s is admin-only", toolName)
}

func nonEmptyRoots(roots ...string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		out = append(out, root)
	}
	return out
}

func pathWithinAnyRoot(target string, roots []string) bool {
	for _, root := range roots {
		base, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, target)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
