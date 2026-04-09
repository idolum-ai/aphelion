//go:build linux

package governorauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/config"
)

const (
	BackendAuto   = "auto"
	BackendCodex  = "codex"
	BackendNative = "native"

	AuthSourceAuto     = "auto"
	AuthSourceCodexCLI = "codex_cli"
	AuthSourceAphelion = "aphelion"

	DefaultCodexBaseURL = "https://chatgpt.com/backend-api/codex"
)

var (
	ErrUnsupportedBackend    = errors.New("unsupported governor backend")
	ErrCodexAuthUnavailable  = errors.New("codex credentials unavailable")
	ErrAphelionAuthStoreTODO = errors.New("aphelion governor auth store is not implemented")
)

type Bundle struct {
	Backend      string
	BaseURL      string
	AccessToken  string
	RefreshToken string
	Source       string
}

type lookups struct {
	getenv      func(string) string
	userHomeDir func() (string, error)
	readFile    func(string) ([]byte, error)
}

func defaultLookups() lookups {
	return lookups{
		getenv:      os.Getenv,
		userHomeDir: os.UserHomeDir,
		readFile:    os.ReadFile,
	}
}

func ResolveFromConfig(cfg config.GovernorConfig) (Bundle, error) {
	return resolveFromConfig(cfg, defaultLookups())
}

func resolveFromConfig(cfg config.GovernorConfig, l lookups) (Bundle, error) {
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == "" {
		backend = BackendAuto
	}

	switch backend {
	case BackendNative:
		return nativeBundle(cfg), nil
	case BackendAuto, BackendCodex:
		bundle, ok, err := resolveCodexBundle(cfg, l)
		if err != nil {
			return Bundle{}, err
		}
		if ok {
			return bundle, nil
		}
		if backend == BackendAuto {
			return nativeBundle(cfg), nil
		}
		return Bundle{}, ErrCodexAuthUnavailable
	default:
		return Bundle{}, fmt.Errorf("%w: %s", ErrUnsupportedBackend, backend)
	}
}

func resolveCodexBundle(cfg config.GovernorConfig, l lookups) (Bundle, bool, error) {
	authSource := strings.ToLower(strings.TrimSpace(cfg.Codex.AuthSource))
	if authSource == "" {
		authSource = AuthSourceAuto
	}

	switch authSource {
	case AuthSourceAuto, AuthSourceCodexCLI:
		creds, ok := detectCodexCLICredentials(cfg.Codex.CodexHome, l)
		if !ok {
			return Bundle{}, false, nil
		}
		baseURL := strings.TrimSpace(cfg.Codex.BaseURL)
		if baseURL == "" {
			baseURL = DefaultCodexBaseURL
		}
		return Bundle{
			Backend:      BackendCodex,
			BaseURL:      baseURL,
			AccessToken:  creds.AccessToken,
			RefreshToken: creds.RefreshToken,
			Source:       "codex-cli-auth-json",
		}, true, nil
	case AuthSourceAphelion:
		return Bundle{}, false, ErrAphelionAuthStoreTODO
	default:
		return Bundle{}, false, nil
	}
}

func nativeBundle(cfg config.GovernorConfig) Bundle {
	return Bundle{
		Backend: BackendNative,
		Source:  strings.TrimSpace(cfg.NativeProvider),
	}
}

type codexCLIAuth struct {
	Tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"tokens"`
}

type codexCredentials struct {
	AccessToken  string
	RefreshToken string
}

func detectCodexCLICredentials(codexHomeOverride string, l lookups) (codexCredentials, bool) {
	authPath, ok := resolveCodexAuthPath(codexHomeOverride, l)
	if !ok {
		return codexCredentials{}, false
	}

	raw, err := l.readFile(authPath)
	if err != nil {
		return codexCredentials{}, false
	}

	var parsed codexCLIAuth
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return codexCredentials{}, false
	}

	access := strings.TrimSpace(parsed.Tokens.AccessToken)
	refresh := strings.TrimSpace(parsed.Tokens.RefreshToken)
	if access == "" || refresh == "" {
		return codexCredentials{}, false
	}

	return codexCredentials{
		AccessToken:  access,
		RefreshToken: refresh,
	}, true
}

func resolveCodexAuthPath(codexHomeOverride string, l lookups) (string, bool) {
	codexHome := strings.TrimSpace(codexHomeOverride)
	if codexHome == "" {
		codexHome = strings.TrimSpace(l.getenv("CODEX_HOME"))
	}
	if codexHome == "" {
		home, err := l.userHomeDir()
		if err != nil {
			return "", false
		}
		codexHome = filepath.Join(home, ".codex")
	}
	if codexHome == "" {
		return "", false
	}
	return filepath.Join(codexHome, "auth.json"), true
}
