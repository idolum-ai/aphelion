//go:build linux

package githubapp

import (
	"fmt"
	"strings"
)

func PermissionMap(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, raw := range values {
		name, level, ok := strings.Cut(strings.TrimSpace(raw), ":")
		if !ok {
			return nil, fmt.Errorf("invalid github app permission %q", raw)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		level = strings.ToLower(strings.TrimSpace(level))
		if name == "" || level == "" {
			return nil, fmt.Errorf("invalid github app permission %q", raw)
		}
		if !validPermissionName(name) || !validPermissionLevel(level) {
			return nil, fmt.Errorf("invalid github app permission %q", raw)
		}
		out[name] = level
	}
	return out, nil
}

func SelectRepository(app App, repo string) (App, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return app, nil
	}
	if app.AllowAllRepositories {
		if !validRepository(repo) {
			return App{}, fmt.Errorf("invalid github app repository %q", repo)
		}
		app.AllowAllRepositories = false
		app.Repositories = []string{repo}
		return app, nil
	}
	for _, allowed := range app.Repositories {
		if strings.EqualFold(strings.TrimSpace(allowed), repo) {
			app.Repositories = []string{allowed}
			return app, nil
		}
	}
	return App{}, fmt.Errorf("repository %q is outside configured github app repository scope", repo)
}

func RepositoryName(repo string) string {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(repo)
}

func validRepository(repo string) bool {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) != 2 {
		return false
	}
	return validRepositoryOwner(parts[0]) && validRepositoryName(parts[1])
}

func validRepositoryOwner(owner string) bool {
	owner = strings.TrimSpace(owner)
	if owner == "" || strings.HasPrefix(owner, "-") || strings.HasSuffix(owner, "-") {
		return false
	}
	for _, r := range owner {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validRepositoryName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validPermissionName(name string) bool {
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return strings.TrimSpace(name) != ""
}

func validPermissionLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "read", "write", "admin":
		return true
	default:
		return false
	}
}
