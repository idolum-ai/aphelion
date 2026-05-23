//go:build linux

package maintenancecli

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/githubapp"
	"github.com/idolum-ai/aphelion/session"
)

func appendGitHubAppTokenEvent(store *session.SQLiteStore, action string, status string, app githubapp.App, token githubapp.InstallationToken, errText string) error {
	payload := map[string]any{
		"action":                 strings.TrimSpace(action),
		"app_name":               strings.TrimSpace(app.Name),
		"app_id":                 app.AppID,
		"installation_id":        app.InstallationID,
		"repositories":           append([]string(nil), app.Repositories...),
		"permissions":            append([]string(nil), app.Permissions...),
		"allow_all_repositories": app.AllowAllRepositories,
		"allow_all_permissions":  app.AllowAllPermissions,
		"token_redacted":         true,
	}
	if !token.ExpiresAt.IsZero() {
		payload["expires_at"] = token.ExpiresAt.Format(time.RFC3339)
	}
	if errText != "" {
		payload["error"] = githubapp.Redact(errText)
	}
	return appendMaintenanceExecutionEvent(store, maintenanceRepairKey(), core.ExecutionEventGitHubAppTokenMinted, "github_app", strings.TrimSpace(status), payload, time.Now().UTC())
}
