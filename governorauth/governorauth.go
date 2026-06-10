//go:build linux

package governorauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

const (
	BackendAuto   = "auto"
	BackendCodex  = "codex"
	BackendNative = "native"

	AuthSourceAuto     = "auto"
	AuthSourceCodexCLI = "codex_cli"
	AuthSourceAphelion = "aphelion"

	DefaultCodexBaseURL                  = "https://chatgpt.com/backend-api"
	DefaultCodexRefreshURL               = "https://auth.openai.com/oauth/token"
	defaultAphelionCodexAuthRelativePath = ".aphelion/state/codex-auth.json"
)

var (
	ErrUnsupportedBackend    = errors.New("unsupported governor backend")
	ErrCodexAuthUnavailable  = errors.New("codex credentials unavailable")
	ErrCodexAuthNotFound     = errors.New("codex auth file not found")
	ErrCodexAuthMalformed    = errors.New("codex auth file is malformed")
	ErrCodexAuthIncomplete   = errors.New("codex auth payload is incomplete")
	ErrUnsupportedAuthSource = errors.New("unsupported governor auth source")
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
	AccountID    string
	AuthPath     string
	RefreshURL   string
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
	case AuthSourceAuto:
		baseURL := strings.TrimSpace(cfg.Codex.BaseURL)
		if baseURL == "" {
			baseURL = DefaultCodexBaseURL
		}
		aphelionPath, ok := resolveAphelionCodexAuthPath(cfg.Codex.AuthPath, l)
		if ok {
			tokens, err := LoadAphelionCodexAuth(aphelionPath)
			if err == nil {
				return Bundle{
					Backend:      BackendCodex,
					BaseURL:      baseURL,
					AccessToken:  tokens.AccessToken,
					RefreshToken: tokens.RefreshToken,
					AccountID:    tokens.AccountID,
					AuthPath:     aphelionPath,
					RefreshURL:   DefaultCodexRefreshURL,
					Source:       "aphelion-auth-json",
				}, true, nil, nil
			}
		}
		creds, err := detectCodexCLICredentials(cfg.Codex.CodexHome, l)
		if err != nil {
			if ok {
				if _, aphelionErr := LoadAphelionCodexAuth(aphelionPath); aphelionErr != nil {
					return Bundle{}, false, aphelionErr, nil
				}
			}
			return Bundle{}, false, err, nil
		}
		return Bundle{
			Backend:      BackendCodex,
			BaseURL:      baseURL,
			AccessToken:  creds.AccessToken,
			RefreshToken: creds.RefreshToken,
			AccountID:    creds.AccountID,
			AuthPath:     creds.AuthPath,
			RefreshURL:   DefaultCodexRefreshURL,
			Source:       "codex-cli-auth-json",
		}, true, nil, nil
	case AuthSourceCodexCLI:
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
			AccountID:    creds.AccountID,
			AuthPath:     creds.AuthPath,
			RefreshURL:   DefaultCodexRefreshURL,
			Source:       "codex-cli-auth-json",
		}, true, nil, nil
	case AuthSourceAphelion:
		authPath, ok := resolveAphelionCodexAuthPath(cfg.Codex.AuthPath, l)
		if !ok {
			return Bundle{}, false, ErrCodexAuthNotFound, nil
		}
		tokens, err := LoadAphelionCodexAuth(authPath)
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
			AccessToken:  tokens.AccessToken,
			RefreshToken: tokens.RefreshToken,
			AccountID:    tokens.AccountID,
			AuthPath:     authPath,
			RefreshURL:   DefaultCodexRefreshURL,
			Source:       "aphelion-auth-json",
		}, true, nil, nil
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
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

type codexCredentials struct {
	AccessToken  string
	RefreshToken string
	AccountID    string
	AuthPath     string
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
	accountID := strings.TrimSpace(parsed.Tokens.AccountID)
	if access == "" || refresh == "" || accountID == "" {
		return codexCredentials{}, ErrCodexAuthIncomplete
	}

	return codexCredentials{
		AccessToken:  access,
		RefreshToken: refresh,
		AccountID:    accountID,
		AuthPath:     authPath,
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

func resolveAphelionCodexAuthPath(authPathOverride string, l lookups) (string, bool) {
	authPath := strings.TrimSpace(authPathOverride)
	if authPath != "" {
		return authPath, true
	}
	home, err := l.userHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", false
	}
	return filepath.Join(home, defaultAphelionCodexAuthRelativePath), true
}

type CodexTokens struct {
	AccessToken  string
	RefreshToken string
	AccountID    string
}

func LoadCodexCLIAuth(path string) (CodexTokens, error) {
	return loadCodexAuthFile(path)
}

func LoadAphelionCodexAuth(path string) (CodexTokens, error) {
	return loadCodexAuthFile(path)
}

func loadCodexAuthFile(path string) (CodexTokens, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CodexTokens{}, ErrCodexAuthNotFound
		}
		return CodexTokens{}, ErrCodexAuthMalformed
	}

	var parsed codexCLIAuth
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CodexTokens{}, ErrCodexAuthMalformed
	}

	access := strings.TrimSpace(parsed.Tokens.AccessToken)
	refresh := strings.TrimSpace(parsed.Tokens.RefreshToken)
	accountID := strings.TrimSpace(parsed.Tokens.AccountID)
	if access == "" || refresh == "" || accountID == "" {
		return CodexTokens{}, ErrCodexAuthIncomplete
	}
	return CodexTokens{
		AccessToken:  access,
		RefreshToken: refresh,
		AccountID:    accountID,
	}, nil
}

func SaveCodexCLIAuth(path string, tokens CodexTokens, refreshedAt time.Time) error {
	return saveCodexAuthFile(path, tokens, refreshedAt)
}

func SaveAphelionCodexAuth(path string, tokens CodexTokens, refreshedAt time.Time) error {
	return saveCodexAuthFile(path, tokens, refreshedAt)
}

func saveCodexAuthFile(path string, tokens CodexTokens, refreshedAt time.Time) error {
	access := strings.TrimSpace(tokens.AccessToken)
	refresh := strings.TrimSpace(tokens.RefreshToken)
	accountID := strings.TrimSpace(tokens.AccountID)
	if access == "" || refresh == "" {
		return ErrCodexAuthIncomplete
	}

	payload := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return ErrCodexAuthMalformed
		}
	} else if !os.IsNotExist(err) {
		return ErrCodexAuthMalformed
	}

	tokenPayload := map[string]any{}
	if existingTokens, ok := payload["tokens"].(map[string]any); ok {
		for key, value := range existingTokens {
			tokenPayload[key] = value
		}
		if accountID == "" {
			if existingAccountID, ok := existingTokens["account_id"].(string); ok {
				accountID = strings.TrimSpace(existingAccountID)
			}
		}
	}
	if accountID == "" {
		return ErrCodexAuthIncomplete
	}

	if payload == nil {
		payload = map[string]any{}
	}
	tokenPayload["access_token"] = access
	tokenPayload["refresh_token"] = refresh
	tokenPayload["account_id"] = accountID
	payload["tokens"] = tokenPayload
	payload["last_refresh"] = refreshedAt.UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal codex auth: %w", err)
	}
	data = append(data, '\n')

	if err := writeCodexAuthFile(path, data); err != nil {
		return err
	}
	return nil
}

func writeCodexAuthFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir codex auth dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod codex auth dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".codex-auth-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp codex auth: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp codex auth: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp codex auth: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp codex auth: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp codex auth: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename codex auth: %w", err)
	}
	removeTemp = false
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod codex auth: %w", err)
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
