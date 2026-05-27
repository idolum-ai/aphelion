//go:build linux

package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
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

func TestManifestRestrictsGitHubAppDetailsForDurableAgentWithoutGrant(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	keyPath := filepath.Join(root, "github-app.pem")
	if err := os.WriteFile(keyPath, []byte("not-a-real-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) err = %v", err)
	}
	registry := NewRegistry(t.TempDir(), time.Second).
		WithSessionStore(newToolTestStore(t)).
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

	manifest := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"})
	if !strings.Contains(manifest, "- github_apps: configured=true details=restricted reason=request_external_account_authority") {
		t.Fatalf("manifest missing coarse restricted GitHub line:\n%s", manifest)
	}
	for _, forbidden := range []string{"idolum-bot", "installation_id", "github-app.pem", "key_present", "idolum-ai/aphelion", "contents:write", "pull_requests:write", root, "not-a-real-key"} {
		if strings.Contains(manifest, forbidden) {
			t.Fatalf("manifest leaked %q to durable agent without grant:\n%s", forbidden, manifest)
		}
	}
}

func TestGitHubDetailsRequireExactActiveExternalAccountGrant(t *testing.T) {
	t.Parallel()

	store := newToolTestStore(t)
	registry := NewRegistry(t.TempDir(), time.Second).
		WithSessionStore(store).
		WithConfiguredCapabilityVisibility(ConfiguredCapabilityVisibilityOptions{
			GitHub: GitHubCapabilityVisibilityOptions{
				Enabled: true,
				Apps: []GitHubAppCapabilityVisibility{{
					Name:           "idolum-bot",
					InstallationID: 123,
					Repositories:   []string{"idolum-ai/aphelion"},
					Permissions:    []string{"pull_requests:write"},
				}},
			},
		})
	actor := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}

	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-github-granted-by-false-positive",
		GrantedBy:      "child-alpha",
		GrantedTo:      "other-child",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github",
		AllowedActions: []string{"read"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(granted_by false positive) err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-github-wrong-action",
		GrantedBy:      "test",
		GrantedTo:      "child-alpha",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github",
		AllowedActions: []string{"write"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(wrong action) err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-github-wrong-target",
		GrantedBy:      "test",
		GrantedTo:      "child-alpha",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github-enterprise-other",
		AllowedActions: []string{"read"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(wrong target) err = %v", err)
	}

	manifest := registry.ManifestForPrincipal(actor)
	if !strings.Contains(manifest, "details=restricted") {
		t.Fatalf("manifest should restrict details without exact grant:\n%s", manifest)
	}
	if strings.Contains(manifest, "idolum-bot") || strings.Contains(manifest, "installation_id") || strings.Contains(manifest, "pull_requests:write") {
		t.Fatalf("manifest leaked GitHub app details without exact grant:\n%s", manifest)
	}

	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-github-exact-read",
		GrantedBy:      "test",
		GrantedTo:      "durable_agent:child-alpha",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github",
		AllowedActions: []string{"read"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(exact canonical durable agent) err = %v", err)
	}
	manifest = registry.ManifestForPrincipal(actor)
	if !strings.Contains(manifest, "active_external_account_grant=active_external_account_grant") || !strings.Contains(manifest, "app.idolum-bot: installation_id=123") {
		t.Fatalf("manifest did not show details with exact canonical durable-agent grant:\n%s", manifest)
	}
}

func TestConfiguredExternalToolVisibilityIsReachableWithoutCallableExposure(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	out := registry.ManifestForPrincipal(actor)
	toolsOnly := strings.Split(out, "Configured capability visibility:")[0]
	if strings.Contains(toolsOnly, "- browse_page:") {
		t.Fatalf("manifest exposed ungranted external tool as callable:\n%s", out)
	}
	if !strings.Contains(out, "- external_tool_manifests:") || !strings.Contains(out, "manifest[browse_page]: configured=true") || !strings.Contains(out, "exposed=false") || !strings.Contains(out, "active_grant=missing") {
		t.Fatalf("manifest missing configured external tool visibility:\n%s", out)
	}
}

func TestWebSearchConfiguredVisibilityRestrictsUngranteddurableAgentDetails(t *testing.T) {
	store := newToolTestStore(t)
	registry := NewRegistry(t.TempDir(), time.Second).
		WithSessionStore(store).
		WithWebSearchOptions(WebSearchOptions{
			Enabled:       true,
			ProviderOrder: []string{"openai_hosted", "brave"},
			DefaultCount:  3,
			MaxCount:      7,
			OpenAIHosted:  WebSearchOpenAIOptions{Enabled: true, ContextSize: "high"},
			Brave:         WebSearchBraveOptions{Enabled: true, APIKeyEnv: "BRAVE_TEST_KEY", APIKeyFile: "/tmp/brave-key", Endpoint: "https://api.search.brave.com/res/v1/web/search"},
		})
	t.Setenv("BRAVE_TEST_KEY", "secret-token")

	manifest := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"})
	if !strings.Contains(manifest, "- web_search: configured=true") || !strings.Contains(manifest, "active_grant=missing") || !strings.Contains(manifest, "details=restricted reason=request_web_search_authority") {
		t.Fatalf("manifest missing coarse web_search restriction:\n%s", manifest)
	}
	for _, forbidden := range []string{"providers_order", "provider.openai_hosted", "provider.brave", "BRAVE_TEST_KEY", "brave-key", "credential_present", "secret-token"} {
		if strings.Contains(manifest, forbidden) {
			t.Fatalf("manifest leaked web_search detail %q to ungranted durable agent:\n%s", forbidden, manifest)
		}
	}
}
