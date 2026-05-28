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
	"github.com/idolum-ai/aphelion/tool/sandbox"
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
	toolsOnly := strings.Split(manifest, "## Requestable Capabilities")[0]
	if strings.Contains(toolsOnly, "- web_search:") {
		t.Fatalf("tool manifest exposed ungranted web_search as callable:\n%s", manifest)
	}
	for _, want := range []string{
		"## Requestable Capabilities",
		"- capability.web_search: configured=true runtime_defined=true exposed=false active_grant=missing",
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
		"- capability.github_apps: configured=true runtime_tool=none maintenance_cli=github-app",
		"route_precedence=approval_before_manual_pr_fallback route_repair=stale_gh_auth_not_decisive,request_bounded_github_app_use",
		"until_granted=hide_app_details,no_github_api_call,no_token_output",
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
	if !strings.Contains(strings.Split(manifest, "## Requestable Capabilities")[0], "- web_search:") {
		t.Fatalf("callable tools missing granted web_search:\n%s", manifest)
	}
	if !strings.Contains(manifest, "- capability.web_search: configured=true runtime_defined=true exposed=true active_grant=active") {
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
	if !strings.Contains(manifest, "- capability.github_apps: configured=true active_external_account_grant=missing details=restricted request=capability_request kind=external_account target_resource=github action=read") {
		t.Fatalf("manifest missing coarse restricted GitHub line:\n%s", manifest)
	}
	if !strings.Contains(manifest, "route_precedence=approval_before_manual_pr_fallback route_repair=stale_gh_auth_not_decisive,request_bounded_github_app_use") || !strings.Contains(manifest, "no_token_output") {
		t.Fatalf("manifest missing safe GitHub route repair hint:\n%s", manifest)
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
	toolsOnly := strings.Split(out, "## Requestable Capabilities")[0]
	if strings.Contains(toolsOnly, "- browse_page:") {
		t.Fatalf("manifest exposed ungranted external tool as callable:\n%s", out)
	}
	if !strings.Contains(out, "- capability.external_tool_manifests:") || !strings.Contains(out, "manifest[browse_page]: configured=true") || !strings.Contains(out, "exposed=false") || !strings.Contains(out, "active_grant=missing") {
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
	if !strings.Contains(manifest, "- capability.web_search: configured=true") || !strings.Contains(manifest, "active_grant=missing") || !strings.Contains(manifest, "details=restricted") {
		t.Fatalf("manifest missing coarse web_search restriction:\n%s", manifest)
	}
	for _, forbidden := range []string{"providers_order", "provider.openai_hosted", "provider.brave", "BRAVE_TEST_KEY", "brave-key", "credential_present", "secret-token"} {
		if strings.Contains(manifest, forbidden) {
			t.Fatalf("manifest leaked web_search detail %q to ungranted durable agent:\n%s", forbidden, manifest)
		}
	}
}

func TestAvailableSkillsAreCompactBoundedAndSeparateFromTools(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second).
		WithConfiguredCapabilityVisibility(ConfiguredCapabilityVisibilityOptions{
			SkillFiles: []string{
				"/very/long/workspace/grimoire/practices/commit-archeology.md",
				"/very/long/workspace/grimoire/practices/review.md",
				"/very/long/workspace/grimoire/SKILLS.md",
				"relative/skills/scout.md",
				"relative/skills/extra.md",
			},
		})

	manifest := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	toolsOnly := strings.Split(manifest, "## Available Skills")[0]
	if strings.Contains(toolsOnly, "commit-archeology") || strings.Contains(toolsOnly, "skills_more") {
		t.Fatalf("tool manifest leaked skill affordances:\n%s", manifest)
	}
	for _, want := range []string{
		"## Available Skills",
		"- skill.commit-archeology: description=configured_skill_instructions location=.../practices/commit-archeology.md",
		"- skills_more: count=1",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing compact skill surface %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "/very/long/workspace") {
		t.Fatalf("manifest leaked raw long skill path:\n%s", manifest)
	}
}

func TestGitHubRequestableCapabilityListsAreCapped(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second).
		WithConfiguredCapabilityVisibility(ConfiguredCapabilityVisibilityOptions{
			GitHub: GitHubCapabilityVisibilityOptions{
				Enabled: true,
				Apps: []GitHubAppCapabilityVisibility{{
					Name:           "idolum-bot",
					InstallationID: 123,
					Repositories:   []string{"owner/a", "owner/b", "owner/c", "owner/d", "owner/e"},
					Permissions:    []string{"checks:read", "contents:write", "issues:write", "metadata:read", "pull_requests:write"},
				}},
			},
		})

	manifest := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	if !strings.Contains(manifest, "repos=owner/a,owner/b,owner/c,owner/d,+1_more") {
		t.Fatalf("manifest did not cap repositories:\n%s", manifest)
	}
	if !strings.Contains(manifest, "permissions=checks:read,contents:write,issues:write,metadata:read,+1_more") {
		t.Fatalf("manifest did not cap permissions:\n%s", manifest)
	}
	if strings.Contains(manifest, "owner/e") || strings.Contains(manifest, "pull_requests:write") {
		t.Fatalf("manifest leaked capped GitHub list details:\n%s", manifest)
	}
}

func TestCanonicalSkillFileUsesParentDirectoryName(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second).
		WithConfiguredCapabilityVisibility(ConfiguredCapabilityVisibilityOptions{
			SkillFiles: []string{"openai-docs/SKILL.md", "imagegen/SKILLS.md"},
		})

	manifest := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	for _, want := range []string{
		"- skill.imagegen: description=configured_skill_instructions location=imagegen/SKILLS.md",
		"- skill.openai-docs: description=configured_skill_instructions location=openai-docs/SKILL.md",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing canonical skill name %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "skill.skill:") || strings.Contains(manifest, "skill.skills:") {
		t.Fatalf("manifest used canonical SKILL.md basename instead of parent dir:\n%s", manifest)
	}
}

func TestExternalToolVisibilityUsesAliasAwareGrantStatus(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := []byte("#!/usr/bin/env bash\necho '{\"summary\":\"ok\"}'\n")
	if err := os.WriteFile(filepath.Join(registry.workspace, "run.sh"), script, 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "principal:1001")

	out := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	if !strings.Contains(strings.Split(out, "## Requestable Capabilities")[0], "- browse_page:") {
		t.Fatalf("callable manifest missing alias-granted external tool:\n%s", out)
	}
	if !strings.Contains(out, "manifest[browse_page]: configured=true") || !strings.Contains(out, "active_grant=active") {
		t.Fatalf("requestable visibility did not use alias-aware active grant status:\n%s", out)
	}
}
