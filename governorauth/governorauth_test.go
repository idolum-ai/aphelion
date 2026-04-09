//go:build linux

package governorauth

import (
	"os"
	"path/filepath"
	"testing"

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

	creds, ok := detectCodexCLICredentials(dir, defaultLookups())
	if !ok {
		t.Fatal("detectCodexCLICredentials() = false, want true")
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

	if _, ok := detectCodexCLICredentials(dir, defaultLookups()); ok {
		t.Fatal("detectCodexCLICredentials() = true, want false for malformed payload")
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
	if err != ErrCodexAuthUnavailable {
		t.Fatalf("err = %v, want %v", err, ErrCodexAuthUnavailable)
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
