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
	ErrCodexAuthNotFound     = errors.New("codex auth file not found")
	ErrCodexAuthMalformed    = errors.New("codex auth file is malformed")
	ErrCodexAuthIncomplete   = errors.New("codex auth payload is incomplete")
	ErrUnsupportedAuthSource = errors.New("unsupported governor auth source")
	ErrAphelionAuthStoreTODO = errors.New("aphelion governor auth store is not implemented")
)

type codexUnavailableError struct {
	cause error
}

func (e codexUnavailableError) Error() string {
	if e.cause == nil {
		return ErrCodexAuthUnavailable.Error()
	}
	return ErrCodexAuthUnavailable.Error() + ": " + e.cause.Error()
}

func (e codexUnavailableError) Unwrap() error {
	return e.cause
}

func (e codexUnavailableError) Is(target error) bool {
	return target == ErrCodexAuthUnavailable
}

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
		bundle, ok, cause, err := resolveCodexBundle(cfg, l)
		if err != nil {
			return Bundle{}, err
		}
		if ok {
			return bundle, nil
		}
		if backend == BackendAuto {
			return nativeBundle(cfg), nil
		}
		if cause != nil {
			return Bundle{}, codexUnavailableError{cause: cause}
		}
		return Bundle{}, ErrCodexAuthUnavailable
	default:
		return Bundle{}, fmt.Errorf("%w: %s", ErrUnsupportedBackend, backend)
	}
}

func resolveCodexBundle(cfg config.GovernorConfig, l lookups) (Bundle, bool, error, error) {
	authSource := strings.ToLower(strings.TrimSpace(cfg.Codex.AuthSource))
	if authSource == "" {
		authSource = AuthSourceAuto
	}

	switch authSource {
	case AuthSourceAuto, AuthSourceCodexCLI:
		creds, err := detectCodexCLICredentials(cfg.Codex.CodexHome, l)
		if err != nil {
			return Bundle{}, false, err, nil
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
		}, true, nil, nil
	case AuthSourceAphelion:
		return Bundle{}, false, nil, ErrAphelionAuthStoreTODO
	default:
		return Bundle{}, false, fmt.Errorf("%w: %s", ErrUnsupportedAuthSource, authSource), nil
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

func detectCodexCLICredentials(codexHomeOverride string, l lookups) (codexCredentials, error) {
	authPath, ok := resolveCodexAuthPath(codexHomeOverride, l)
	if !ok {
		return codexCredentials{}, ErrCodexAuthNotFound
	}

	raw, err := l.readFile(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return codexCredentials{}, ErrCodexAuthNotFound
		}
		return codexCredentials{}, ErrCodexAuthMalformed
	}

	var parsed codexCLIAuth
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return codexCredentials{}, ErrCodexAuthMalformed
	}

	access := strings.TrimSpace(parsed.Tokens.AccessToken)
	refresh := strings.TrimSpace(parsed.Tokens.RefreshToken)
	if access == "" || refresh == "" {
		return codexCredentials{}, ErrCodexAuthIncomplete
	}

	return codexCredentials{
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
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
