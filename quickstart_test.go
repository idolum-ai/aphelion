//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestRunQuickstartWritesMinimalValidConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, "aphelion.toml")
	var out bytes.Buffer

	err := runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:       configPath,
		NoInput:          true,
		TelegramBotToken: "telegram-token",
		AdminUserID:      123456789,
		Provider:         "openai",
		ProviderAPIKey:   "openai-key",
		Out:              &out,
		Getenv:           emptyQuickstartEnv,
	})
	if err != nil {
		t.Fatalf("runQuickstart() err = %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("Stat(config) err = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %v, want 0600", got)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load() err = %v", err)
	}
	if cfg.Telegram.BotToken != "telegram-token" {
		t.Fatalf("telegram token = %q", cfg.Telegram.BotToken)
	}
	if !reflect.DeepEqual(cfg.Principals.Telegram.AdminUserIDs, []int64{123456789}) {
		t.Fatalf("admin ids = %#v", cfg.Principals.Telegram.AdminUserIDs)
	}
	if cfg.Providers.Default != "openai" || cfg.Providers.OpenAI.APIKey != "openai-key" {
		t.Fatalf("provider config = default %q openai key %q", cfg.Providers.Default, cfg.Providers.OpenAI.APIKey)
	}
	if cfg.Autonomy.DefaultMode != "ask_first" || cfg.Autonomy.Ceiling != "leased" || !cfg.Autonomy.AllowLiveOverrides {
		t.Fatalf("autonomy config = %#v, want explicit ask_first/leased defaults", cfg.Autonomy)
	}
	if !strings.Contains(out.String(), "service_installed: false") {
		t.Fatalf("output = %q, want service_installed false", out.String())
	}
}

func TestRunQuickstartRefusesExistingConfigWithoutForce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, "aphelion.toml")
	if err := os.WriteFile(configPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile(existing) err = %v", err)
	}

	err := runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:       configPath,
		NoInput:          true,
		TelegramBotToken: "telegram-token",
		AdminUserID:      1,
		Provider:         "ollama",
		Out:              ioDiscard{},
		Getenv:           emptyQuickstartEnv,
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("runQuickstart() err = %v, want existing config failure", err)
	}
}

func TestRunQuickstartNoInputRequiresMissingValues(t *testing.T) {
	t.Parallel()

	err := runQuickstart(context.Background(), quickstartOptions{
		ConfigPath: filepath.Join(t.TempDir(), "aphelion.toml"),
		NoInput:    true,
		Out:        ioDiscard{},
		Getenv:     emptyQuickstartEnv,
	})
	if err == nil || !strings.Contains(err.Error(), "telegram bot token is required") {
		t.Fatalf("runQuickstart() err = %v, want missing token failure", err)
	}
}

func TestRunQuickstartUsesFlagBeforeEnv(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, "aphelion.toml")
	env := map[string]string{
		"APHELION_TELEGRAM_BOT_TOKEN": "env-token",
		"APHELION_ADMIN_USER_ID":      "111",
		"APHELION_PROVIDER":           "openai",
		"OPENAI_API_KEY":              "env-key",
	}

	err := runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:       configPath,
		NoInput:          true,
		TelegramBotToken: "flag-token",
		AdminUserID:      222,
		Provider:         "anthropic",
		ProviderAPIKey:   "flag-key",
		Out:              ioDiscard{},
		Getenv:           mapQuickstartEnv(env),
	})
	if err != nil {
		t.Fatalf("runQuickstart() err = %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load() err = %v", err)
	}
	if cfg.Telegram.BotToken != "flag-token" || cfg.Principals.Telegram.AdminUserIDs[0] != 222 {
		t.Fatalf("config used env before flags: token=%q admins=%#v", cfg.Telegram.BotToken, cfg.Principals.Telegram.AdminUserIDs)
	}
	if cfg.Providers.Default != "anthropic" || cfg.Providers.Anthropic.APIKey != "flag-key" {
		t.Fatalf("provider config = default %q key %q", cfg.Providers.Default, cfg.Providers.Anthropic.APIKey)
	}
}

func TestRunQuickstartDetectsTelegramAdmin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, "aphelion.toml")
	client := &fakeQuickstartTelegramClient{
		batches: [][]telegram.Update{
			{{UpdateID: 10, Message: &telegram.Message{From: &telegram.User{ID: 7, FirstName: "Old"}}}},
			{{UpdateID: 11, Message: &telegram.Message{
				From: &telegram.User{ID: 42, Username: "admin", FirstName: "Ada"},
				Chat: &telegram.Chat{ID: 42, Type: "private"},
			}}},
		},
	}
	var out bytes.Buffer

	err := runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:       configPath,
		TelegramBotToken: "telegram-token",
		Provider:         "ollama",
		DetectAdmin:      true,
		AllowPrompt:      true,
		In:               strings.NewReader("y\n"),
		Out:              &out,
		Getenv:           emptyQuickstartEnv,
		NewTelegramClient: func(string) quickstartTelegramClient {
			return client
		},
	})
	if err != nil {
		t.Fatalf("runQuickstart() err = %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load() err = %v", err)
	}
	if got := cfg.Principals.Telegram.AdminUserIDs[0]; got != 42 {
		t.Fatalf("admin id = %d, want 42", got)
	}
	if !strings.Contains(out.String(), "Detected user_id=42") {
		t.Fatalf("output = %q, want detected user", out.String())
	}
}

func TestRunQuickstartInstallServiceUsesDeploySequence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	configPath := filepath.Join(root, "aphelion.toml")
	execPath := filepath.Join(root, "bin", "aphelion")
	workDir := filepath.Join(root, "work")
	var calls []string
	active := false
	runner := func(_ context.Context, name string, args ...string) error {
		call := name
		if len(args) > 0 {
			call += " " + strings.Join(args, " ")
		}
		calls = append(calls, call)
		if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "is-active", "--quiet", "aphelion"}) {
			if active {
				return nil
			}
			return errors.New("inactive")
		}
		if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "enable", "--now", "aphelion"}) {
			active = true
		}
		return nil
	}

	err := runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:       configPath,
		NoInput:          true,
		InstallService:   true,
		TelegramBotToken: "telegram-token",
		AdminUserID:      123,
		Provider:         "ollama",
		ExecPath:         execPath,
		WorkDir:          workDir,
		Out:              ioDiscard{},
		Getenv:           emptyQuickstartEnv,
		CommandRunner:    runner,
	})
	if err != nil {
		t.Fatalf("runQuickstart() err = %v", err)
	}

	want := []string{
		execPath + " --config " + configPath + " --check-config",
		execPath + " init --config " + configPath,
		"systemctl --user daemon-reload",
		"systemctl --user is-active --quiet aphelion",
		"systemctl --user enable --now aphelion",
		"systemctl --user is-active --quiet aphelion",
		execPath + " verify-deploy --config " + configPath + " --format=kv",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}

	servicePath := filepath.Join(root, "xdg", "systemd", "user", "aphelion.service")
	raw, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatalf("ReadFile(service) err = %v", err)
	}
	text := string(raw)
	for _, needle := range []string{
		"WorkingDirectory=" + workDir,
		"ExecStart=" + execPath + " --config " + configPath,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("service = %q, want %q", text, needle)
		}
	}
}

func TestRunQuickstartInstallServiceReusesExistingConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	configPath := filepath.Join(root, "aphelion.toml")
	rawConfig := renderQuickstartConfig(quickstartConfigValues{
		TelegramBotToken: "telegram-token",
		AdminUserID:      123,
		Provider:         "ollama",
	})
	if err := writeValidatedQuickstartConfig(configPath, rawConfig, false); err != nil {
		t.Fatalf("writeValidatedQuickstartConfig() err = %v", err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(before) err = %v", err)
	}

	active := false
	runner := func(_ context.Context, name string, args ...string) error {
		if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "is-active", "--quiet", "aphelion"}) {
			if active {
				return nil
			}
			return errors.New("inactive")
		}
		if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "enable", "--now", "aphelion"}) {
			active = true
		}
		return nil
	}

	err = runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:     configPath,
		NoInput:        true,
		InstallService: true,
		ExecPath:       filepath.Join(root, "bin", "aphelion"),
		WorkDir:        root,
		Out:            ioDiscard{},
		Getenv:         emptyQuickstartEnv,
		CommandRunner:  runner,
	})
	if err != nil {
		t.Fatalf("runQuickstart(existing install) err = %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(after) err = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config changed during install existing\nbefore=%q\nafter=%q", before, after)
	}
}

type fakeQuickstartTelegramClient struct {
	batches [][]telegram.Update
	calls   int
}

func (c *fakeQuickstartTelegramClient) GetUpdates(_ context.Context, _ int64, _ int) ([]telegram.Update, error) {
	if c.calls >= len(c.batches) {
		return nil, nil
	}
	batch := c.batches[c.calls]
	c.calls++
	return batch, nil
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

func emptyQuickstartEnv(string) string {
	return ""
}

func mapQuickstartEnv(values map[string]string) func(string) string {
	return func(name string) string {
		return values[name]
	}
}
