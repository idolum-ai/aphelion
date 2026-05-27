//go:build linux

package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

// ConfiguredCapabilityVisibilityOptions describes configured integrations that
// are useful to show in the prompt even when they are not exposed as invokable
// tools for the current principal. It must never contain secret material.
type ConfiguredCapabilityVisibilityOptions struct {
	GitHub     GitHubCapabilityVisibilityOptions
	SkillFiles []string
}

type GitHubCapabilityVisibilityOptions struct {
	Enabled    bool
	APIBaseURL string
	APIVersion string
	Apps       []GitHubAppCapabilityVisibility
}

type GitHubAppCapabilityVisibility struct {
	Name                 string
	AppID                int64
	InstallationID       int64
	PrivateKeyFile       string
	Repositories         []string
	Permissions          []string
	AllowAllRepositories bool
	AllowAllPermissions  bool
}

func (r *Registry) WithConfiguredCapabilityVisibility(opts ConfiguredCapabilityVisibilityOptions) *Registry {
	if r == nil {
		return r
	}
	r.configuredVisibility = normalizeConfiguredCapabilityVisibilityOptions(opts)
	return r
}

func normalizeConfiguredCapabilityVisibilityOptions(opts ConfiguredCapabilityVisibilityOptions) ConfiguredCapabilityVisibilityOptions {
	opts.GitHub.APIBaseURL = strings.TrimSpace(opts.GitHub.APIBaseURL)
	opts.GitHub.APIVersion = strings.TrimSpace(opts.GitHub.APIVersion)
	apps := make([]GitHubAppCapabilityVisibility, 0, len(opts.GitHub.Apps))
	for _, app := range opts.GitHub.Apps {
		app.Name = strings.TrimSpace(app.Name)
		app.PrivateKeyFile = strings.TrimSpace(app.PrivateKeyFile)
		app.Repositories = compactSortedStrings(app.Repositories)
		app.Permissions = compactSortedStrings(app.Permissions)
		if app.Name == "" && app.AppID == 0 && app.InstallationID == 0 && app.PrivateKeyFile == "" {
			continue
		}
		apps = append(apps, app)
	}
	opts.GitHub.Apps = apps
	opts.SkillFiles = compactSortedStrings(opts.SkillFiles)
	return opts
}

func (r *Registry) configuredCapabilityVisibilityForPrincipal(p principal.Principal, exposedDefs []agent.ToolDef, exposedExternal []ExternalToolManifest) string {
	if r == nil {
		return ""
	}
	lines := []string{}
	exposedNative := toolDefNameSet(exposedDefs)
	exposedExternalNames := externalManifestNameSet(exposedExternal)
	principalID := toolAuthorityPrincipalDisplay(p)

	if block := r.webSearchConfiguredVisibility(p, principalID, exposedNative); len(block) > 0 {
		lines = append(lines, block...)
	}
	if block := r.externalToolConfiguredVisibility(p, principalID, exposedExternalNames); len(block) > 0 {
		lines = append(lines, block...)
	}
	if block := r.githubConfiguredVisibility(p, principalID); len(block) > 0 {
		lines = append(lines, block...)
	}
	if block := r.skillConfiguredVisibility(); len(block) > 0 {
		lines = append(lines, block...)
	}
	if len(lines) == 0 {
		return ""
	}
	out := []string{
		"Configured capability visibility:",
		"- This section is read-only status, not invocation authority.",
		"- configured means present in config/runtime setup; exposed means listed as a callable tool for this principal; invokable still requires the relevant active grant/lease and runtime checks.",
	}
	out = append(out, lines...)
	return strings.Join(out, "\n")
}

func (r *Registry) webSearchConfiguredVisibility(p principal.Principal, principalID string, exposed map[string]struct{}) []string {
	if !r.webSearchOptions.Enabled && !r.webSearchOptions.OpenAIHosted.Enabled && !r.webSearchOptions.Brave.Enabled {
		return nil
	}
	_, defined := r.webSearchToolDefinition()
	_, exposedNow := exposed[webSearchToolName]
	grantStatus := "not_checkable"
	if r.store != nil && principalID != "" {
		if _, ok, err := r.capabilityGrantAllowsAuthorityToolAccess(webSearchToolName, principalFromDisplay(principalID)); err == nil && ok {
			grantStatus = "active"
		} else if err != nil {
			grantStatus = "error"
		} else {
			grantStatus = "missing"
		}
	}
	lines := []string{
		fmt.Sprintf("- web_search: configured=%t runtime_defined=%t exposed=%t active_grant=%s invocation_requires=active_tool_grant+active_turn_or_operation_lease", r.webSearchOptions.Enabled, defined, exposedNow, grantStatus),
	}
	canSeeDetails := p.Role == principal.RoleAdmin || grantStatus == "active"
	if !canSeeDetails {
		return append(lines, "  details=restricted reason=request_web_search_authority")
	}
	providers := r.webSearchProviderMap()
	lines = append(lines, fmt.Sprintf("  providers_order=%s default_count=%d max_count=%d", strings.Join(r.webSearchOptions.ProviderOrder, ","), r.webSearchOptions.DefaultCount, r.webSearchOptions.MaxCount))
	if r.webSearchOptions.OpenAIHosted.Enabled || stringSliceContains(r.webSearchOptions.ProviderOrder, "openai_hosted") {
		_, wired := providers["openai_hosted"]
		lines = append(lines, fmt.Sprintf("  provider.openai_hosted: configured=%t registered_provider=%t context_size=%s credential_status=provider_level_not_exposed", r.webSearchOptions.OpenAIHosted.Enabled, wired, firstNonEmpty(r.webSearchOptions.OpenAIHosted.ContextSize, "medium")))
	}
	if r.webSearchOptions.Brave.Enabled || stringSliceContains(r.webSearchOptions.ProviderOrder, "brave") {
		_, wired := providers["brave"]
		source, present := secretSourceStatus(r.webSearchOptions.Brave.APIKeyEnv, r.webSearchOptions.Brave.APIKeyFile)
		lines = append(lines, fmt.Sprintf("  provider.brave: configured=%t registered_provider=%t endpoint_configured=%t credential_source=%s credential_present=%s", r.webSearchOptions.Brave.Enabled, wired, strings.TrimSpace(r.webSearchOptions.Brave.Endpoint) != "", source, present))
	}
	return lines
}

func (r *Registry) externalToolConfiguredVisibility(p principal.Principal, principalID string, exposed map[string]struct{}) []string {
	if len(r.externalManifests) == 0 || p.Role != principal.RoleAdmin {
		return nil
	}
	lines := []string{"- external_tool_manifests:"}
	manifests := append([]ExternalToolManifest(nil), r.externalManifests...)
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Name < manifests[j].Name })
	for _, manifest := range manifests {
		manifest = NormalizeExternalToolManifest(manifest)
		registered := "not_checkable"
		if r.store != nil {
			if row, ok, err := r.store.RegisteredTool(manifest.Name); err == nil && ok && row.Registered {
				registered = "true"
			} else if err != nil {
				registered = "error"
			} else {
				registered = "false"
			}
		}
		grantStatus := "not_checkable"
		if r.store != nil && principalID != "" {
			if _, ok, err := r.store.ActiveCapabilityGrant(session.CapabilityKindTool, manifest.Name, principalID, "invoke"); err == nil && ok {
				grantStatus = "active"
			} else if err != nil {
				grantStatus = "error"
			} else {
				grantStatus = "missing"
			}
		}
		_, exposedNow := exposed[manifest.Name]
		lines = append(lines, fmt.Sprintf("  manifest[%s]: configured=true owner=%s registered=%s exposed=%t active_grant=%s executable=%t", manifest.Name, firstNonEmpty(manifest.Owner, "unknown"), registered, exposedNow, grantStatus, r.externalExecutor != nil && r.externalExecutor.Supports(manifest)))
	}
	return lines
}

func (r *Registry) githubConfiguredVisibility(p principal.Principal, principalID string) []string {
	cfg := r.configuredVisibility.GitHub
	if !cfg.Enabled && len(cfg.Apps) == 0 {
		return nil
	}
	grantStatus := r.githubExternalAccountGrantStatus(p, principalID)
	canSeeDetails := p.Role == principal.RoleAdmin || grantStatus == "active_external_account_grant"
	if !canSeeDetails {
		return []string{fmt.Sprintf("- github_apps: configured=%t details=restricted reason=request_external_account_authority", cfg.Enabled)}
	}
	lines := []string{fmt.Sprintf("- github_apps: configured=%t runtime_tool=none maintenance_cli=github-app active_external_account_grant=%s api_base_url=%s", cfg.Enabled, grantStatus, firstNonEmpty(cfg.APIBaseURL, "not_configured"))}
	apps := append([]GitHubAppCapabilityVisibility(nil), cfg.Apps...)
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	for _, app := range apps {
		keySource, keyPresent := keyFileStatus(app.PrivateKeyFile)
		repoScope := "configured"
		if app.AllowAllRepositories {
			repoScope = "installation"
		}
		permScope := "configured"
		if app.AllowAllPermissions {
			permScope = "installation"
		}
		lines = append(lines, fmt.Sprintf("  app.%s: installation_id=%d key_file=%s key_present=%s repos=%s repo_scope=%s permissions=%s permission_scope=%s", firstNonEmpty(app.Name, "unnamed"), app.InstallationID, keySource, keyPresent, listOrNone(app.Repositories), repoScope, listOrNone(app.Permissions), permScope))
	}
	return lines
}

func (r *Registry) githubExternalAccountGrantStatus(p principal.Principal, principalID string) string {
	if r == nil || r.store == nil {
		return "not_checkable"
	}
	candidates := append([]string{}, toolAuthorityPrincipalKeys(p)...)
	candidates = append(candidates, principalID)
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		for _, target := range []string{"github", "github_app"} {
			if _, ok, err := r.store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, target, candidate, "read"); err != nil {
				return "error"
			} else if ok {
				return "active_external_account_grant"
			}
		}
	}
	return "missing"
}

func (r *Registry) skillConfiguredVisibility() []string {
	if len(r.configuredVisibility.SkillFiles) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("- skills: prompt_file_backed=true configured_files=%s note=skills_are_instructions_not_tools", strings.Join(r.configuredVisibility.SkillFiles, ","))}
}

func toolDefNameSet(defs []agent.ToolDef) map[string]struct{} {
	out := map[string]struct{}{}
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

func externalManifestNameSet(manifests []ExternalToolManifest) map[string]struct{} {
	out := map[string]struct{}{}
	for _, manifest := range manifests {
		name := strings.TrimSpace(manifest.Name)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

func secretSourceStatus(envName, fileName string) (string, string) {
	sources := []string{}
	present := false
	configured := false
	if envName = strings.TrimSpace(envName); envName != "" {
		configured = true
		sources = append(sources, "env:"+envName)
		if value, ok := os.LookupEnv(envName); ok && strings.TrimSpace(value) != "" {
			present = true
		}
	}
	if fileName = strings.TrimSpace(fileName); fileName != "" {
		configured = true
		sources = append(sources, "file:"+filepath.Base(fileName))
		if info, err := os.Stat(fileName); err == nil && !info.IsDir() && info.Size() > 0 {
			present = true
		}
	}
	if !configured {
		return "not_configured", "not_configured"
	}
	if present {
		return strings.Join(sources, "+"), "true"
	}
	return strings.Join(sources, "+"), "false"
}

func keyFileStatus(fileName string) (string, string) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return "not_configured", "not_configured"
	}
	if info, err := os.Stat(fileName); err == nil && !info.IsDir() && info.Size() > 0 {
		return filepath.Base(fileName), "true"
	}
	return filepath.Base(fileName), "false"
}

func compactSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func stringSliceContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func principalFromDisplay(display string) principal.Principal {
	display = strings.TrimSpace(display)
	if display == "admin" {
		return principal.Principal{Role: principal.RoleAdmin}
	}
	if strings.HasPrefix(display, "telegram:") {
		// For grant lookup, capabilityGrantAllowsAuthorityToolAccess will derive the
		// same display/key set from this principal. If parsing fails, the display
		// fallback still gets checked below by callers that use the store directly.
		var id int64
		_, _ = fmt.Sscanf(strings.TrimPrefix(display, "telegram:"), "%d", &id)
		return principal.Principal{Role: principal.RoleAdmin, TelegramUserID: id}
	}
	return principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: display}
}
