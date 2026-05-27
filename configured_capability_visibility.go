//go:build linux

package main

import (
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/tool"
)

func configuredCapabilityVisibilityFromConfig(cfg *config.Config) tool.ConfiguredCapabilityVisibilityOptions {
	if cfg == nil {
		return tool.ConfiguredCapabilityVisibilityOptions{}
	}
	apps := make([]tool.GitHubAppCapabilityVisibility, 0, len(cfg.GitHub.Apps))
	for _, app := range cfg.GitHub.Apps {
		apps = append(apps, tool.GitHubAppCapabilityVisibility{
			Name:                 app.Name,
			AppID:                app.AppID,
			InstallationID:       app.InstallationID,
			PrivateKeyFile:       app.PrivateKeyFile,
			Repositories:         append([]string(nil), app.Repositories...),
			Permissions:          append([]string(nil), app.Permissions...),
			AllowAllRepositories: app.AllowAllRepositories,
			AllowAllPermissions:  app.AllowAllPermissions,
		})
	}
	return tool.ConfiguredCapabilityVisibilityOptions{
		GitHub: tool.GitHubCapabilityVisibilityOptions{
			Enabled:    cfg.GitHub.Enabled,
			APIBaseURL: cfg.GitHub.APIBaseURL,
			APIVersion: cfg.GitHub.APIVersion,
			Apps:       apps,
		},
		SkillFiles: configuredSkillFiles(cfg.Agent.DynamicFiles),
	}
}

func configuredSkillFiles(files []string) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		normalized := filepath.ToSlash(file)
		base := filepath.Base(normalized)
		lower := strings.ToLower(normalized)
		if strings.EqualFold(base, "SKILLS.md") || strings.EqualFold(base, "SKILL.md") || strings.HasPrefix(lower, "skills/") || strings.Contains(lower, "/skills/") {
			out = append(out, file)
		}
	}
	return out
}
