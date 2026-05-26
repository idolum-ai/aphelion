//go:build linux

package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

func validateGitHubConfig(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	parsedGitHubAPI, err := url.Parse(strings.TrimSpace(cfg.GitHub.APIBaseURL))
	if err != nil {
		return fmt.Errorf("github.api_base_url must be a valid URL: %w", err)
	}
	if parsedGitHubAPI.Scheme == "" || parsedGitHubAPI.Host == "" {
		return fmt.Errorf("github.api_base_url must be an absolute URL")
	}
	if parsedGitHubAPI.Scheme != "https" {
		return fmt.Errorf("github.api_base_url must use https")
	}
	if strings.TrimSpace(cfg.GitHub.APIVersion) == "" {
		return fmt.Errorf("github.api_version is required")
	}
	if cfg.GitHub.Enabled && len(cfg.GitHub.Apps) == 0 {
		return fmt.Errorf("github.apps must contain at least one app when github.enabled is true")
	}
	seenNames := map[string]struct{}{}
	for i, app := range cfg.GitHub.Apps {
		prefix := fmt.Sprintf("github.apps[%d]", i)
		nameKey := strings.ToLower(strings.TrimSpace(app.Name))
		if nameKey == "" {
			return fmt.Errorf("%s.name is required", prefix)
		}
		if _, ok := seenNames[nameKey]; ok {
			return fmt.Errorf("%s.name duplicates another GitHub App name", prefix)
		}
		seenNames[nameKey] = struct{}{}
		if app.AppID <= 0 {
			return fmt.Errorf("%s.app_id must be > 0", prefix)
		}
		if app.InstallationID <= 0 {
			return fmt.Errorf("%s.installation_id must be > 0", prefix)
		}
		if strings.TrimSpace(app.PrivateKeyFile) == "" {
			return fmt.Errorf("%s.private_key_file is required", prefix)
		}
		if err := validateGitHubPrivateKeyFile(app.PrivateKeyFile); err != nil {
			return fmt.Errorf("%s.private_key_file: %w", prefix, err)
		}
		if !app.AllowAllRepositories && len(app.Repositories) == 0 {
			return fmt.Errorf("%s.repositories must not be empty unless allow_all_repositories is true", prefix)
		}
		for _, repo := range app.Repositories {
			if !validGitHubRepository(repo) {
				return fmt.Errorf("%s.repositories contains invalid repository %q; use owner/name", prefix, repo)
			}
		}
		if !app.AllowAllPermissions && len(app.Permissions) == 0 {
			return fmt.Errorf("%s.permissions must not be empty unless allow_all_permissions is true", prefix)
		}
		for _, permission := range app.Permissions {
			if !validGitHubPermission(permission) {
				return fmt.Errorf("%s.permissions contains invalid permission %q; use permission:read|write|admin", prefix, permission)
			}
		}
	}
	return nil
}

func validateGitHubPrivateKeyFile(path string) error {
	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil {
		return fmt.Errorf("stat key file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("must not be readable, writable, or executable by group/other")
	}
	return nil
}

func validGitHubRepository(repo string) bool {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) != 2 {
		return false
	}
	return validGitHubOwner(parts[0]) && validGitHubRepositoryName(parts[1])
}

func validGitHubOwner(owner string) bool {
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

func validGitHubRepositoryName(name string) bool {
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

func validGitHubPermission(raw string) bool {
	name, level, ok := splitGitHubPermission(raw)
	if !ok {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	switch level {
	case "read", "write", "admin":
		return true
	default:
		return false
	}
}
