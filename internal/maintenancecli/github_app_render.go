//go:build linux

package maintenancecli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/githubapp"
)

type githubAppCommandReport struct {
	Action     string                  `json:"action"`
	Status     string                  `json:"status"`
	ConfigPath string                  `json:"config_path,omitempty"`
	Enabled    bool                    `json:"enabled"`
	APIBaseURL string                  `json:"api_base_url,omitempty"`
	APIVersion string                  `json:"api_version,omitempty"`
	Apps       []githubAppStatusReport `json:"apps,omitempty"`
	Token      *githubAppTokenOutput   `json:"token,omitempty"`
}

type githubAppStatusReport struct {
	Name            string   `json:"name"`
	AppID           int64    `json:"app_id,omitempty"`
	InstallationID  int64    `json:"installation_id,omitempty"`
	PrivateKeyFile  string   `json:"private_key_file,omitempty"`
	RepositoryScope string   `json:"repository_scope,omitempty"`
	Repositories    []string `json:"repositories,omitempty"`
	PermissionScope string   `json:"permission_scope,omitempty"`
	Permissions     []string `json:"permissions,omitempty"`
	OnlineStatus    string   `json:"online_status,omitempty"`
	TokenExpiresAt  string   `json:"token_expires_at,omitempty"`
	LastError       string   `json:"last_error,omitempty"`
}

type githubAppTokenOutput struct {
	AppName        string            `json:"app_name"`
	InstallationID int64             `json:"installation_id"`
	Repository     string            `json:"repository,omitempty"`
	ExpiresAt      string            `json:"expires_at,omitempty"`
	Permissions    map[string]string `json:"permissions,omitempty"`
	Repositories   []string          `json:"repositories,omitempty"`
	Token          string            `json:"token,omitempty"`
}

func buildGitHubAppStatusReport(cfg *config.Config, configPath string, apps []config.GitHubAppConfig) githubAppCommandReport {
	status := "disabled"
	if cfg.GitHub.Enabled {
		status = "configured"
		if len(apps) == 0 {
			status = "missing_apps"
		}
	}
	report := githubAppCommandReport{
		Action:     "github-app status",
		Status:     status,
		ConfigPath: configPath,
		Enabled:    cfg.GitHub.Enabled,
		APIBaseURL: cfg.GitHub.APIBaseURL,
		APIVersion: cfg.GitHub.APIVersion,
		Apps:       make([]githubAppStatusReport, 0, len(apps)),
	}
	for _, app := range apps {
		report.Apps = append(report.Apps, githubAppStatusReport{
			Name:            app.Name,
			AppID:           app.AppID,
			InstallationID:  app.InstallationID,
			PrivateKeyFile:  filepath.Base(app.PrivateKeyFile),
			RepositoryScope: githubAppRepositoryScope(app),
			Repositories:    append([]string(nil), app.Repositories...),
			PermissionScope: githubAppPermissionScope(app),
			Permissions:     append([]string(nil), app.Permissions...),
		})
	}
	return report
}

func githubAppRepositoryScope(app config.GitHubAppConfig) string {
	if app.AllowAllRepositories {
		return "installation"
	}
	return "configured"
}

func githubAppPermissionScope(app config.GitHubAppConfig) string {
	if app.AllowAllPermissions {
		return "installation"
	}
	return "configured"
}

func renderGitHubAppCommandReport(w io.Writer, report githubAppCommandReport, format string) error {
	switch format {
	case commandOutputJSON:
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case commandOutputKV:
		fmt.Fprintf(w, "action=%s\n", report.Action)
		fmt.Fprintf(w, "status=%s\n", report.Status)
		fmt.Fprintf(w, "enabled=%t\n", report.Enabled)
		fmt.Fprintf(w, "api_base_url=%s\n", report.APIBaseURL)
		fmt.Fprintf(w, "api_version=%s\n", report.APIVersion)
		for i, app := range report.Apps {
			prefix := fmt.Sprintf("apps.%d.", i)
			fmt.Fprintf(w, "%sname=%s\n", prefix, app.Name)
			fmt.Fprintf(w, "%sapp_id=%d\n", prefix, app.AppID)
			fmt.Fprintf(w, "%sinstallation_id=%d\n", prefix, app.InstallationID)
			fmt.Fprintf(w, "%sprivate_key_file=%s\n", prefix, app.PrivateKeyFile)
			fmt.Fprintf(w, "%srepository_scope=%s\n", prefix, app.RepositoryScope)
			fmt.Fprintf(w, "%spermission_scope=%s\n", prefix, app.PermissionScope)
			if app.OnlineStatus != "" {
				fmt.Fprintf(w, "%sonline_status=%s\n", prefix, app.OnlineStatus)
			}
			if app.TokenExpiresAt != "" {
				fmt.Fprintf(w, "%stoken_expires_at=%s\n", prefix, app.TokenExpiresAt)
			}
		}
		return nil
	default:
		fmt.Fprintln(w, renderGitHubAppStatusHuman(report))
		return nil
	}
}

func renderGitHubAppStatusHuman(report githubAppCommandReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "GitHub App credentials: %s\n", report.Status)
	fmt.Fprintf(&b, "Enabled: %t\n", report.Enabled)
	if report.APIBaseURL != "" {
		fmt.Fprintf(&b, "API: %s\n", report.APIBaseURL)
	}
	if len(report.Apps) == 0 {
		fmt.Fprintf(&b, "Apps: none")
		return b.String()
	}
	fmt.Fprintf(&b, "Apps:\n")
	for _, app := range report.Apps {
		fmt.Fprintf(&b, "- %s: installation=%d key=%s repos=%s permissions=%s", app.Name, app.InstallationID, app.PrivateKeyFile, app.RepositoryScope, app.PermissionScope)
		if app.OnlineStatus != "" {
			fmt.Fprintf(&b, " online=%s", app.OnlineStatus)
		}
		if app.TokenExpiresAt != "" {
			fmt.Fprintf(&b, " expires=%s", app.TokenExpiresAt)
		}
		if app.LastError != "" {
			fmt.Fprintf(&b, " error=%s", app.LastError)
		}
		fmt.Fprintln(&b)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderGitHubAppTokenOutput(w io.Writer, out githubAppTokenOutput, format string, apiBaseURL string) error {
	switch format {
	case githubAppTokenFormatJSON:
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case githubAppTokenFormatGitCredential:
		fmt.Fprintf(w, "protocol=https\n")
		fmt.Fprintf(w, "host=%s\n", githubapp.GitCredentialHost(apiBaseURL))
		fmt.Fprintf(w, "username=x-access-token\n")
		fmt.Fprintf(w, "password=%s\n\n", out.Token)
	default:
		fmt.Fprintln(w, out.Token)
	}
	return nil
}
