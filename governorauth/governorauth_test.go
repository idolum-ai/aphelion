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
	raw := `{"tokens":{"access_token":"acc","refresh_token":"ref"}}`
	if err := os.WriteFile(authPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	creds, err := detectCodexCLICredentials(dir, defaultLookups())
	if err != nil {
		t.Fatalf("detectCodexCLICredentials() err = %v, want nil", err)
	}
	if creds.AccessToken != "acc" || creds.RefreshToken != "ref" {
		t.Fatalf("credentials = %#v, want access+refresh", creds)
	}
}

func TestIgnoreMalformedCodexCLIAuthFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	raw := `{"tokens":{"access_token":"acc"}}`
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
	raw := `{"tokens":{"access_token":"acc","refresh_token":"ref"}}`
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
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"acc","refresh_token":"ref"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	got, err := LoadCodexCLIAuth(authPath)
	if err != nil {
		t.Fatalf("LoadCodexCLIAuth() err = %v", err)
	}
	if got.AccessToken != "acc" || got.RefreshToken != "ref" {
		t.Fatalf("tokens = %#v, want acc/ref", got)
	}
}

func TestSaveCodexCLIAuthPreservesUnknownFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"auth_mode":"chatgpt","extra":{"k":"v"},"tokens":{"access_token":"old","refresh_token":"old"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	refreshedAt := time.Date(2026, time.April, 9, 12, 34, 56, 0, time.UTC)
	if err := SaveCodexCLIAuth(authPath, CodexTokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
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
	if tokens["access_token"] != "new-access" || tokens["refresh_token"] != "new-refresh" {
		t.Fatalf("tokens = %#v, want updated tokens", tokens)
	}
}
