//go:build linux

package governorauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

func TestDetectCodexCLIAuthFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	raw := `{"tokens":{"access_token":"acc","refresh_token":"ref","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	creds, err := detectCodexCLICredentials(dir, defaultLookups())
	if err != nil {
		t.Fatalf("detectCodexCLICredentials() err = %v, want nil", err)
	}
	if creds.AccessToken != "acc" || creds.RefreshToken != "ref" || creds.AccountID != "acct" {
		t.Fatalf("credentials = %#v, want access+refresh", creds)
	}
}

func TestIgnoreMalformedCodexCLIAuthFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	raw := `{"tokens":{"access_token":"acc","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	if _, err := detectCodexCLICredentials(dir, defaultLookups()); !errors.Is(err, ErrCodexAuthIncomplete) {
		t.Fatalf("detectCodexCLICredentials() err = %v, want ErrCodexAuthIncomplete", err)
	}
}

func TestGovernorBackendAutoPrefersCodexWhenCredentialsExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	raw := `{"tokens":{"access_token":"acc","refresh_token":"ref","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	bundle, err := ResolveFromConfig(config.GovernorConfig{
		Backend:        "auto",
		NativeProvider: "anthropic",
		Codex: config.GovernorCodexConfig{
			AuthSource: "codex_cli",
			CodexHome:  dir,
			BaseURL:    DefaultCodexBaseURL,
		},
	})
	if err != nil {
		t.Fatalf("ResolveFromConfig() err = %v", err)
	}
	if bundle.Backend != BackendCodex {
		t.Fatalf("backend = %q, want codex", bundle.Backend)
	}
	if bundle.Source != "codex-cli-auth-json" {
		t.Fatalf("source = %q, want codex-cli-auth-json", bundle.Source)
	}
	if bundle.AuthPath != authPath {
		t.Fatalf("auth path = %q, want %q", bundle.AuthPath, authPath)
	}
	if bundle.RefreshURL != DefaultCodexRefreshURL {
		t.Fatalf("refresh url = %q, want %q", bundle.RefreshURL, DefaultCodexRefreshURL)
	}
	if bundle.AccountID != "acct" {
		t.Fatalf("account id = %q, want acct", bundle.AccountID)
	}
}

func TestGovernorBackendAutoFallsBackNativeWhenCredentialsMissing(t *testing.T) {
	t.Parallel()

	bundle, err := ResolveFromConfig(config.GovernorConfig{
		Backend:        "auto",
		NativeProvider: "anthropic",
		Codex: config.GovernorCodexConfig{
			AuthSource: "codex_cli",
			CodexHome:  t.TempDir(),
			BaseURL:    DefaultCodexBaseURL,
		},
	})
	if err != nil {
		t.Fatalf("ResolveFromConfig() err = %v", err)
	}
	if bundle.Backend != BackendNative {
		t.Fatalf("backend = %q, want native", bundle.Backend)
	}
	if bundle.AccessToken != "" || bundle.RefreshToken != "" {
		t.Fatalf("native bundle leaked tokens: %#v", bundle)
	}
}

func TestGovernorBackendCodexFailsWithoutCredentials(t *testing.T) {
	t.Parallel()

	_, err := ResolveFromConfig(config.GovernorConfig{
		Backend:        "codex",
		NativeProvider: "anthropic",
		Codex: config.GovernorCodexConfig{
			AuthSource: "codex_cli",
			CodexHome:  t.TempDir(),
			BaseURL:    DefaultCodexBaseURL,
		},
	})
	if err == nil {
		t.Fatal("ResolveFromConfig() err = nil, want codex auth unavailable")
	}
	if !errors.Is(err, ErrCodexAuthUnavailable) {
		t.Fatalf("err = %v, want wrapped %v", err, ErrCodexAuthUnavailable)
	}
	if !errors.Is(err, ErrCodexAuthNotFound) {
		t.Fatalf("err = %v, want wrapped %v", err, ErrCodexAuthNotFound)
	}
}

func TestGovernorBackendCodexReportsMalformedAuth(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	_, err := ResolveFromConfig(config.GovernorConfig{
		Backend:        "codex",
		NativeProvider: "anthropic",
		Codex: config.GovernorCodexConfig{
			AuthSource: "codex_cli",
			CodexHome:  dir,
		},
	})
	if err == nil {
		t.Fatal("ResolveFromConfig() err = nil, want malformed auth failure")
	}
	if !errors.Is(err, ErrCodexAuthUnavailable) || !errors.Is(err, ErrCodexAuthMalformed) {
		t.Fatalf("err = %v, want unavailable+malformed classification", err)
	}
}

func TestResolveCodexAuthPathPrefersCODEXHOME(t *testing.T) {
	t.Parallel()

	dotCodex := t.TempDir()
	codeHome := t.TempDir()
	l := defaultLookups()
	l.getenv = func(key string) string {
		if key == "CODEX_HOME" {
			return codeHome
		}
		return ""
	}
	l.userHomeDir = func() (string, error) {
		return dotCodex, nil
	}

	got, ok := resolveCodexAuthPath("", l)
	if !ok {
		t.Fatal("resolveCodexAuthPath() ok = false, want true")
	}
	want := filepath.Join(codeHome, "auth.json")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestLoadCodexCLIAuth(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"acc","refresh_token":"ref","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	got, err := LoadCodexCLIAuth(authPath)
	if err != nil {
		t.Fatalf("LoadCodexCLIAuth() err = %v", err)
	}
	if got.AccessToken != "acc" || got.RefreshToken != "ref" || got.AccountID != "acct" {
		t.Fatalf("tokens = %#v, want acc/ref/acct", got)
	}
}

func TestGovernorBackendCodexLoadsAphelionAuthStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "codex-auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"app-acc","refresh_token":"app-ref","account_id":"app-acct"}}`), 0o600); err != nil {
		t.Fatalf("write codex auth store: %v", err)
	}

	bundle, err := ResolveFromConfig(config.GovernorConfig{
		Backend: "codex",
		Codex: config.GovernorCodexConfig{
			AuthSource: "aphelion",
			AuthPath:   authPath,
			BaseURL:    DefaultCodexBaseURL,
		},
	})
	if err != nil {
		t.Fatalf("ResolveFromConfig() err = %v", err)
	}
	if bundle.Backend != BackendCodex {
		t.Fatalf("backend = %q, want codex", bundle.Backend)
	}
	if bundle.Source != "aphelion-auth-json" {
		t.Fatalf("source = %q, want aphelion-auth-json", bundle.Source)
	}
	if bundle.AuthPath != authPath {
		t.Fatalf("auth path = %q, want %q", bundle.AuthPath, authPath)
	}
	if bundle.AccessToken != "app-acc" || bundle.RefreshToken != "app-ref" || bundle.AccountID != "app-acct" {
		t.Fatalf("bundle = %#v, want tokens from aphelion auth store", bundle)
	}
}

func TestGovernorBackendAutoPrefersAphelionAuthStoreBeforeCLI(t *testing.T) {
	t.Parallel()

	cliDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cliDir, "auth.json"), []byte(`{"tokens":{"access_token":"cli-acc","refresh_token":"cli-ref","account_id":"cli-acct"}}`), 0o600); err != nil {
		t.Fatalf("write cli auth.json: %v", err)
	}
	aphelionDir := t.TempDir()
	authPath := filepath.Join(aphelionDir, "codex-auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"app-acc","refresh_token":"app-ref","account_id":"app-acct"}}`), 0o600); err != nil {
		t.Fatalf("write aphelion auth store: %v", err)
	}

	bundle, err := ResolveFromConfig(config.GovernorConfig{
		Backend: "auto",
		Codex: config.GovernorCodexConfig{
			AuthSource: "auto",
			AuthPath:   authPath,
			CodexHome:  cliDir,
			BaseURL:    DefaultCodexBaseURL,
		},
	})
	if err != nil {
		t.Fatalf("ResolveFromConfig() err = %v", err)
	}
	if bundle.Source != "aphelion-auth-json" {
		t.Fatalf("source = %q, want aphelion-auth-json", bundle.Source)
	}
	if bundle.AccessToken != "app-acc" || bundle.RefreshToken != "app-ref" || bundle.AccountID != "app-acct" {
		t.Fatalf("bundle = %#v, want aphelion auth store tokens", bundle)
	}
}

func TestLoadAphelionCodexAuth(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "codex-auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"acc","refresh_token":"ref","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatalf("write auth store: %v", err)
	}

	got, err := LoadAphelionCodexAuth(authPath)
	if err != nil {
		t.Fatalf("LoadAphelionCodexAuth() err = %v", err)
	}
	if got.AccessToken != "acc" || got.RefreshToken != "ref" || got.AccountID != "acct" {
		t.Fatalf("tokens = %#v, want acc/ref/acct", got)
	}
}

func TestSaveAphelionCodexAuthPreservesUnknownFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "codex-auth.json")
	if err := os.WriteFile(authPath, []byte(`{"store":"aphelion","extra":{"k":"v"},"tokens":{"access_token":"old","refresh_token":"old","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatalf("write auth store: %v", err)
	}

	refreshedAt := time.Date(2026, time.April, 14, 1, 2, 3, 0, time.UTC)
	if err := SaveAphelionCodexAuth(authPath, CodexTokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		AccountID:    "acct",
	}, refreshedAt); err != nil {
		t.Fatalf("SaveAphelionCodexAuth() err = %v", err)
	}

	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth store: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal saved auth store: %v", err)
	}
	if payload["store"] != "aphelion" {
		t.Fatalf("store = %#v, want aphelion", payload["store"])
	}
	if payload["last_refresh"] != refreshedAt.Format(time.RFC3339) {
		t.Fatalf("last_refresh = %#v, want %q", payload["last_refresh"], refreshedAt.Format(time.RFC3339))
	}
	tokens, ok := payload["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("tokens = %#v, want object", payload["tokens"])
	}
	if tokens["access_token"] != "new-access" || tokens["refresh_token"] != "new-refresh" || tokens["account_id"] != "acct" {
		t.Fatalf("tokens = %#v, want updated tokens", tokens)
	}
}

func TestSaveCodexCLIAuthPreservesUnknownFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"auth_mode":"chatgpt","extra":{"k":"v"},"tokens":{"access_token":"old","refresh_token":"old","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	refreshedAt := time.Date(2026, time.April, 9, 12, 34, 56, 0, time.UTC)
	if err := SaveCodexCLIAuth(authPath, CodexTokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		AccountID:    "acct",
	}, refreshedAt); err != nil {
		t.Fatalf("SaveCodexCLIAuth() err = %v", err)
	}

	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal saved auth: %v", err)
	}
	if payload["auth_mode"] != "chatgpt" {
		t.Fatalf("auth_mode = %#v, want chatgpt", payload["auth_mode"])
	}
	if payload["last_refresh"] != refreshedAt.Format(time.RFC3339) {
		t.Fatalf("last_refresh = %#v, want %q", payload["last_refresh"], refreshedAt.Format(time.RFC3339))
	}
	tokens, ok := payload["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("tokens = %#v, want object", payload["tokens"])
	}
	if tokens["access_token"] != "new-access" || tokens["refresh_token"] != "new-refresh" || tokens["account_id"] != "acct" {
		t.Fatalf("tokens = %#v, want updated tokens", tokens)
	}
}

func TestSaveCodexCLIAuthDoesNotClobberMalformedExistingJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	original := []byte(`{"tokens":`)
	if err := os.WriteFile(authPath, original, 0o600); err != nil {
		t.Fatalf("write malformed auth.json: %v", err)
	}

	err := SaveCodexCLIAuth(authPath, CodexTokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		AccountID:    "acct",
	}, time.Date(2026, time.April, 9, 12, 34, 56, 0, time.UTC))
	if !errors.Is(err, ErrCodexAuthMalformed) {
		t.Fatalf("SaveCodexCLIAuth() err = %v, want ErrCodexAuthMalformed", err)
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	if string(raw) != string(original) {
		t.Fatalf("auth file was clobbered: got %q want %q", string(raw), string(original))
	}
}

func TestSaveCodexCLIAuthFallsBackToExistingAccountIDAndPreservesTokenMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"old","refresh_token":"old","account_id":"acct-existing","metadata":{"device":"laptop"},"scope":"openid"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	refreshedAt := time.Date(2026, time.April, 10, 1, 2, 3, 0, time.UTC)
	if err := SaveCodexCLIAuth(authPath, CodexTokens{
		AccessToken:  "fresh-access",
		RefreshToken: "fresh-refresh",
	}, refreshedAt); err != nil {
		t.Fatalf("SaveCodexCLIAuth() err = %v", err)
	}

	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal saved auth: %v", err)
	}
	tokens, ok := payload["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("tokens = %#v, want object", payload["tokens"])
	}
	if tokens["access_token"] != "fresh-access" || tokens["refresh_token"] != "fresh-refresh" || tokens["account_id"] != "acct-existing" {
		t.Fatalf("tokens = %#v, want refreshed pair with existing account id", tokens)
	}
	if tokens["scope"] != "openid" {
		t.Fatalf("tokens.scope = %#v, want preserved scope", tokens["scope"])
	}
	metadata, ok := tokens["metadata"].(map[string]any)
	if !ok || metadata["device"] != "laptop" {
		t.Fatalf("tokens.metadata = %#v, want preserved nested metadata", tokens["metadata"])
	}
	if payload["last_refresh"] != refreshedAt.Format(time.RFC3339) {
		t.Fatalf("last_refresh = %#v, want %q", payload["last_refresh"], refreshedAt.Format(time.RFC3339))
	}
}

func TestSaveCodexCLIAuthRequiresAccountIDBeforeCreatingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "missing", "auth.json")
	err := SaveCodexCLIAuth(authPath, CodexTokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
	}, time.Date(2026, time.April, 11, 1, 2, 3, 0, time.UTC))
	if !errors.Is(err, ErrCodexAuthIncomplete) {
		t.Fatalf("SaveCodexCLIAuth() err = %v, want ErrCodexAuthIncomplete", err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("auth file exists or stat err = %v, want not exist", err)
	}
}

func TestSaveCodexCLIAuthDoesNotRewriteExistingFileWhenIncomplete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	original := []byte(`{"tokens":{"access_token":"old","refresh_token":"old","account_id":"acct"}}`)
	if err := os.WriteFile(authPath, original, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	err := SaveCodexCLIAuth(authPath, CodexTokens{
		AccessToken: "new-access",
		AccountID:   "acct",
	}, time.Date(2026, time.April, 11, 1, 2, 3, 0, time.UTC))
	if !errors.Is(err, ErrCodexAuthIncomplete) {
		t.Fatalf("SaveCodexCLIAuth() err = %v, want ErrCodexAuthIncomplete", err)
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	if string(raw) != string(original) {
		t.Fatalf("auth file changed: got %q want %q", string(raw), string(original))
	}
}

func TestSaveCodexCLIAuthCreatesRestrictiveAuthFileModes(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "codex")
	authPath := filepath.Join(dir, "auth.json")
	if err := SaveCodexCLIAuth(authPath, CodexTokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		AccountID:    "acct",
	}, time.Date(2026, time.April, 12, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatalf("SaveCodexCLIAuth() err = %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat auth dir: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("auth dir mode = %#o, want 0700", mode)
	}
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("auth file mode = %#o, want 0600", mode)
	}
}

func TestSaveCodexCLIAuthTightensExistingPermissiveModes(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll auth dir: %v", err)
	}
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"old","refresh_token":"old","account_id":"acct"}}`), 0o644); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	if err := SaveCodexCLIAuth(authPath, CodexTokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		AccountID:    "acct",
	}, time.Date(2026, time.April, 12, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatalf("SaveCodexCLIAuth() err = %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat auth dir: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("auth dir mode = %#o, want tightened 0700", mode)
	}
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("auth file mode = %#o, want tightened 0600", mode)
	}
}

func TestSaveCodexCLIAuthLeavesNoTemporaryAuthFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := SaveCodexCLIAuth(authPath, CodexTokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		AccountID:    "acct",
	}, time.Date(2026, time.April, 13, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatalf("SaveCodexCLIAuth() err = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, ".codex-auth-*.tmp"))
	if err != nil {
		t.Fatalf("Glob temp auth files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary auth files = %#v, want none", matches)
	}
}
