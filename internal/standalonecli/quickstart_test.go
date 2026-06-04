//go:build linux

package standalonecli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestQuickstartServiceTemplateMatchesDeployTemplate(t *testing.T) {
	rootTemplate, err := os.ReadFile("../../deploy/aphelion.service")
	if err != nil {
		t.Fatalf("read root deploy service template: %v", err)
	}
	if string(rootTemplate) != aphelionServiceTemplate {
		t.Fatalf("embedded quickstart service template differs from deploy/aphelion.service; update both together")
	}
}

type fakeQuickstartCommandRunner struct {
	calls     []string
	active    bool
	unitList  string
	unitFiles string
	show      string
	versions  map[string]string
}

func (r *fakeQuickstartCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	call := name
	if len(args) > 0 {
		call += " " + strings.Join(args, " ")
	}
	r.calls = append(r.calls, call)
	if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "is-active", "--quiet", "aphelion"}) {
		if r.active {
			return nil
		}
		return errors.New("inactive")
	}
	if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "enable", "--now", "aphelion"}) {
		r.active = true
	}
	return nil
}

func (r *fakeQuickstartCommandRunner) RunServiceGuardCommand(_ context.Context, name string, args ...string) ([]byte, error) {
	if len(args) == 2 && args[0] == "version" && args[1] == "--json" {
		if out, ok := r.versions[name]; ok {
			return []byte(out), nil
		}
	}
	if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "list-units", "--all", "--no-legend", "--plain"}) {
		return []byte(r.unitList), nil
	}
	if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "list-unit-files", "--no-legend", "--plain"}) {
		return []byte(r.unitFiles), nil
	}
	if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "show", "aphelion", "-p", "MainPID", "-p", "ExecStart", "--no-pager"}) {
		return []byte(r.show), nil
	}
	return nil, errors.New("unexpected service guard command: " + name + " " + strings.Join(args, " "))
}

func TestRunQuickstartInstallServiceUsesDeploySequence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	configPath := filepath.Join(root, "aphelion.toml")
	execPath := filepath.Join(root, "bin", "aphelion")
	workDir := filepath.Join(root, "work")
	runner := &fakeQuickstartCommandRunner{
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		show:      "MainPID=123\nExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }\n",
		versions:  map[string]string{execPath: `{"version":"v0.2.2","vcs_revision":"abc123"}`},
	}

	err := runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:           configPath,
		NoInput:              true,
		InstallService:       true,
		TelegramBotToken:     "telegram-token",
		AdminUserID:          123,
		Provider:             "ollama",
		ExecPath:             execPath,
		WorkDir:              workDir,
		Out:                  ioDiscard{},
		Getenv:               emptyQuickstartEnv,
		CommandRunner:        runner.Run,
		ServiceGuardRunner:   runner.RunServiceGuardCommand,
		ServiceGuardReadlink: func(string) (string, error) { return execPath, nil },
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
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
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

func TestInstallQuickstartUserServiceTargetVersionUsesTimeout(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	configPath := filepath.Join(root, "aphelion.toml")
	execPath := filepath.Join(root, "bin", "aphelion")
	calledRunner := false

	err := func() error {
		_, err := installQuickstartUserService(context.Background(), quickstartServiceOptions{
			ConfigPath: configPath,
			ExecPath:   execPath,
			WorkDir:    root,
			Out:        ioDiscard{},
			Timeout:    time.Nanosecond,
			CommandRunner: func(context.Context, string, ...string) error {
				calledRunner = true
				return nil
			},
			ServiceGuardRunner: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if _, ok := ctx.Deadline(); !ok {
					return nil, errors.New("service guard version probe missing deadline")
				}
				<-ctx.Done()
				return nil, ctx.Err()
			},
		})
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "read target executable version") || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("installQuickstartUserService() err = %v, want target version deadline", err)
	}
	if calledRunner {
		t.Fatal("CommandRunner was called after timed-out target version probe")
	}
}

func TestRunQuickstartInstallServiceDefaultGuardRunnerCapturesOutput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(binDir) err = %v", err)
	}
	configPath := filepath.Join(root, "aphelion.toml")
	execPath := filepath.Join(binDir, "aphelion")
	systemctlPath := filepath.Join(binDir, "systemctl")
	versionJSON := `{"version":"v0.2.2","vcs_revision":"abc123"}`
	aphelionScript := "#!/bin/sh\n" +
		"if [ \"$1\" = version ] && [ \"$2\" = --json ]; then printf '%s\\n' '" + versionJSON + "'; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(execPath, []byte(aphelionScript), 0o755); err != nil {
		t.Fatalf("WriteFile(aphelion) err = %v", err)
	}
	systemctlScript := "#!/bin/sh\n" +
		"if [ \"$1 $2 $3\" = '--user is-active --quiet' ]; then exit 0; fi\n" +
		"if [ \"$1 $2 $3 $4 $5\" = '--user list-units --all --no-legend --plain' ]; then printf '%s\\n' 'aphelion.service loaded active running Aphelion'; exit 0; fi\n" +
		"if [ \"$1 $2 $3 $4\" = '--user list-unit-files --no-legend --plain' ]; then printf '%s\\n' 'aphelion.service enabled'; exit 0; fi\n" +
		"if [ \"$1 $2 $3 $4\" = '--user show aphelion -p' ]; then printf '%s\\n' 'MainPID=123' 'ExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }'; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(systemctlPath, []byte(systemctlScript), 0o755); err != nil {
		t.Fatalf("WriteFile(systemctl) err = %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:           configPath,
		NoInput:              true,
		InstallService:       true,
		TelegramBotToken:     "telegram-token",
		AdminUserID:          123,
		Provider:             "ollama",
		ExecPath:             execPath,
		WorkDir:              root,
		Out:                  ioDiscard{},
		Getenv:               emptyQuickstartEnv,
		ServiceGuardReadlink: func(string) (string, error) { return execPath, nil },
	})
	if err != nil {
		t.Fatalf("runQuickstart(default service guard runner) err = %v", err)
	}
}

func TestRunQuickstartInstallServiceFailsOnDuplicatePrimaryUnits(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	configPath := filepath.Join(root, "aphelion.toml")
	execPath := filepath.Join(root, "bin", "aphelion")
	runner := &fakeQuickstartCommandRunner{
		unitList:  "aphelion.service loaded active running Aphelion\naphelion-v013-deploy.service loaded failed failed old\n",
		unitFiles: "aphelion.service enabled\n",
		show:      "MainPID=123\nExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }\n",
		versions:  map[string]string{execPath: `{"version":"v0.2.2","vcs_revision":"abc123"}`},
	}

	err := runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:           configPath,
		NoInput:              true,
		InstallService:       true,
		TelegramBotToken:     "telegram-token",
		AdminUserID:          123,
		Provider:             "ollama",
		ExecPath:             execPath,
		WorkDir:              root,
		Out:                  ioDiscard{},
		Getenv:               emptyQuickstartEnv,
		CommandRunner:        runner.Run,
		ServiceGuardRunner:   runner.RunServiceGuardCommand,
		ServiceGuardReadlink: func(string) (string, error) { return execPath, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate/stale Aphelion primary unit") {
		t.Fatalf("runQuickstart() err = %v, want duplicate/stale primary unit", err)
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

	execPath := filepath.Join(root, "bin", "aphelion")
	runner := &fakeQuickstartCommandRunner{
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		show:      "MainPID=123\nExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }\n",
		versions:  map[string]string{execPath: `{"version":"v0.2.2","vcs_revision":"abc123"}`},
	}

	err = runQuickstart(context.Background(), quickstartOptions{
		ConfigPath:           configPath,
		NoInput:              true,
		InstallService:       true,
		ExecPath:             execPath,
		WorkDir:              root,
		Out:                  ioDiscard{},
		Getenv:               emptyQuickstartEnv,
		CommandRunner:        runner.Run,
		ServiceGuardRunner:   runner.RunServiceGuardCommand,
		ServiceGuardReadlink: func(string) (string, error) { return execPath, nil },
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
