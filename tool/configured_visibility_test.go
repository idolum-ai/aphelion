//go:build linux

package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
)

func TestManifestShowsConfiguredButUngrantableWebSearchWithoutExposingTool(t *testing.T) {

	store := newToolTestStore(t)
	registry := NewRegistry(t.TempDir(), time.Second).
		WithSessionStore(store).
		WithWebSearchOptions(WebSearchOptions{
			Enabled:       true,
			ProviderOrder: []string{"openai_hosted", "brave"},
			DefaultCount:  3,
			MaxCount:      7,
			OpenAIHosted:  WebSearchOpenAIOptions{Enabled: true, ContextSize: "high"},
			Brave:         WebSearchBraveOptions{Enabled: true, APIKeyEnv: "BRAVE_TEST_KEY", Endpoint: "https://api.search.brave.com/res/v1/web/search"},
		})
	t.Setenv("BRAVE_TEST_KEY", "secret-token")

	manifest := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	toolsOnly := strings.Split(manifest, "Configured capability visibility:")[0]
	if strings.Contains(toolsOnly, "- web_search:") {
		t.Fatalf("tool manifest exposed ungranted web_search as callable:\n%s", manifest)
	}
	for _, want := range []string{
		"Configured capability visibility:",
		"- web_search: configured=true runtime_defined=true exposed=false active_grant=missing",
		"provider.openai_hosted: configured=true",
		"provider.brave: configured=true",
		"credential_source=env:BRAVE_TEST_KEY credential_present=true",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "secret-token") {
		t.Fatalf("manifest leaked secret token:\n%s", manifest)
	}
}

func TestManifestShowsConfiguredGitHubAppsWithoutSecretPathsOrRuntimeTool(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	keyPath := filepath.Join(root, "github-app.pem")
	if err := os.WriteFile(keyPath, []byte("not-a-real-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) err = %v", err)
	}
	registry := NewRegistry(t.TempDir(), time.Second).
		WithConfiguredCapabilityVisibility(ConfiguredCapabilityVisibilityOptions{
			GitHub: GitHubCapabilityVisibilityOptions{
				Enabled:    true,
				APIBaseURL: "https://api.github.com",
				Apps: []GitHubAppCapabilityVisibility{{
					Name:           "idolum-bot",
					InstallationID: 123,
					PrivateKeyFile: keyPath,
					Repositories:   []string{"idolum-ai/aphelion"},
					Permissions:    []string{"contents:write", "pull_requests:write"},
				}},
			},
		})

	manifest := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	for _, want := range []string{
		"- github_apps: configured=true runtime_tool=none maintenance_cli=github-app",
		"app.idolum-bot: installation_id=123 key_file=github-app.pem key_present=true",
		"repos=idolum-ai/aphelion",
		"permissions=contents:write,pull_requests:write",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, root) || strings.Contains(manifest, "not-a-real-key") {
		t.Fatalf("manifest leaked key path or contents:\n%s", manifest)
	}
}

func TestManifestShowsGrantedWebSearchAsExposedInConfiguredVisibility(t *testing.T) {
	t.Parallel()

	store := newToolTestStore(t)
	registry := NewRegistry(t.TempDir(), time.Second).
		WithSessionStore(store).
		WithWebSearchOptions(WebSearchOptions{Enabled: true, OpenAIHosted: WebSearchOpenAIOptions{Enabled: true}})
	grantToolInvoke(t, store, webSearchToolName, "telegram:1001")

	manifest := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	if !strings.Contains(strings.Split(manifest, "Configured capability visibility:")[0], "- web_search:") {
		t.Fatalf("callable tools missing granted web_search:\n%s", manifest)
	}
	if !strings.Contains(manifest, "- web_search: configured=true runtime_defined=true exposed=true active_grant=active") {
		t.Fatalf("configured visibility did not show exposed grant:\n%s", manifest)
	}
}
