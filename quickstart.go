//go:build linux

package main

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/telegram"
	"golang.org/x/term"
)

//go:embed deploy/aphelion.service
var aphelionServiceTemplate string

const (
	defaultQuickstartDetectAdminTimeout = 60 * time.Second
	defaultQuickstartCommandTimeout     = 5 * time.Minute
	aphelionUserServiceName             = "aphelion"
)

type quickstartOptions struct {
	ConfigPath         string
	Force              bool
	NoInput            bool
	AllowPrompt        bool
	DetectAdmin        bool
	DetectAdminTimeout time.Duration
	InstallService     bool
	TelegramBotToken   string
	AdminUserID        int64
	Provider           string
	ProviderAPIKey     string
	ProviderModel      string
	ExecPath           string
	WorkDir            string
	In                 io.Reader
	Out                io.Writer
	Getenv             func(string) string
	NewTelegramClient  func(string) quickstartTelegramClient
	CommandRunner      quickstartCommandRunner
}

type quickstartTelegramClient interface {
	GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]telegram.Update, error)
}

type quickstartCommandRunner func(ctx context.Context, name string, args ...string) error

type quickstartSession struct {
	in          io.Reader
	out         io.Writer
	reader      *bufio.Reader
	getenv      func(string) string
	noInput     bool
	allowPrompt bool
}

type quickstartConfigValues struct {
	TelegramBotToken string
	AdminUserID      int64
	Provider         string
	ProviderAPIKey   string
	ProviderModel    string
}

type telegramAdminCandidate struct {
	UserID    int64
	Username  string
	FirstName string
	LastName  string
	ChatID    int64
	ChatType  string
}

type quickstartServiceOptions struct {
	ConfigPath    string
	ExecPath      string
	WorkDir       string
	Out           io.Writer
	CommandRunner quickstartCommandRunner
	Timeout       time.Duration
}

type quickstartServiceResult struct {
	ServicePath string
	ConfigPath  string
	ExecPath    string
	WorkDir     string
	Restarted   bool
}

func runQuickstartCommand(args []string) error {
	opts := defaultQuickstartOptions()
	fs := flag.NewFlagSet("quickstart", flag.ContinueOnError)
	fs.StringVar(&opts.ConfigPath, "config", "", "path to config.toml")
	fs.BoolVar(&opts.Force, "force", false, "overwrite an existing config file")
	fs.BoolVar(&opts.NoInput, "no-input", false, "fail instead of prompting for missing inputs")
	fs.BoolVar(&opts.DetectAdmin, "detect-admin", false, "discover the Telegram admin user id from a fresh bot message")
	fs.DurationVar(&opts.DetectAdminTimeout, "detect-admin-timeout", defaultQuickstartDetectAdminTimeout, "maximum time to wait for admin discovery")
	fs.BoolVar(&opts.InstallService, "install-service", false, "install, restart, and verify the user systemd service after writing config")
	fs.StringVar(&opts.TelegramBotToken, "telegram-bot-token", "", "Telegram bot token")
	fs.StringVar(&opts.TelegramBotToken, "bot-token", "", "alias for --telegram-bot-token")
	fs.Int64Var(&opts.AdminUserID, "admin-user-id", 0, "Telegram admin user id")
	fs.StringVar(&opts.Provider, "provider", "", "native provider: openai|anthropic|openrouter|gemini|ollama")
	fs.StringVar(&opts.ProviderAPIKey, "provider-api-key", "", "API key for the selected native provider")
	fs.StringVar(&opts.ProviderModel, "provider-model", "", "model override for the selected native provider")
	fs.StringVar(&opts.ExecPath, "exec", "", "binary path to write into the user service")
	fs.StringVar(&opts.WorkDir, "workdir", "", "working directory to write into the user service")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for quickstart", extra)
	}
	return runQuickstart(context.Background(), opts)
}

func defaultQuickstartOptions() quickstartOptions {
	return quickstartOptions{
		DetectAdminTimeout: defaultQuickstartDetectAdminTimeout,
		In:                 os.Stdin,
		Out:                os.Stdout,
		Getenv:             os.Getenv,
		AllowPrompt:        isTerminalReader(os.Stdin),
		NewTelegramClient: func(token string) quickstartTelegramClient {
			return telegram.NewClient(token, telegram.WithHTTPClient(&http.Client{Timeout: 90 * time.Second}))
		},
		CommandRunner: execQuickstartCommand,
	}
}

func runQuickstart(ctx context.Context, opts quickstartOptions) error {
	opts = normalizeQuickstartOptions(opts)
	configPath, err := config.ResolveConfigPath(opts.ConfigPath)
	if err != nil {
		return err
	}
	if !opts.Force {
		if _, err := os.Stat(configPath); err == nil {
			if opts.InstallService && !quickstartHasConfigInputs(opts) {
				return runQuickstartInstallExisting(ctx, opts, configPath)
			}
			return fmt.Errorf("config %s already exists; pass --force to overwrite it", configPath)
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("stat config %s: %w", configPath, err)
		}
	}

	session := quickstartSession{
		in:          opts.In,
		out:         opts.Out,
		reader:      bufio.NewReader(opts.In),
		getenv:      opts.Getenv,
		noInput:     opts.NoInput,
		allowPrompt: opts.AllowPrompt,
	}

	token, err := session.resolveString("telegram bot token", opts.TelegramBotToken, []string{"APHELION_TELEGRAM_BOT_TOKEN"}, "Telegram bot token: ", true)
	if err != nil {
		return err
	}

	adminID := opts.AdminUserID
	if adminID <= 0 {
		adminID, err = parsePositiveInt64FromEnv(opts.Getenv, "APHELION_ADMIN_USER_ID", "TELEGRAM_ADMIN_USER_ID")
		if err != nil {
			return err
		}
	}
	if adminID <= 0 && opts.DetectAdmin {
		adminID, err = detectTelegramAdminForQuickstart(ctx, opts.NewTelegramClient(token), session, opts.DetectAdminTimeout)
		if err != nil {
			return err
		}
	}
	if adminID <= 0 {
		raw, err := session.resolveString("Telegram admin user id", "", nil, "Telegram admin user id: ", false)
		if err != nil {
			return err
		}
		adminID, err = parsePositiveInt64(raw, "Telegram admin user id")
		if err != nil {
			return err
		}
	}

	provider, err := session.resolveProvider(opts.Provider)
	if err != nil {
		return err
	}
	providerKey := strings.TrimSpace(opts.ProviderAPIKey)
	if providerRequiresAPIKey(provider) {
		providerKey, err = session.resolveString(provider+" API key", providerKey, providerAPIKeyEnvNames(provider), providerPrompt(provider), true)
		if err != nil {
			return err
		}
	}
	providerModel := strings.TrimSpace(opts.ProviderModel)
	if providerModel == "" {
		providerModel = strings.TrimSpace(opts.Getenv("APHELION_PROVIDER_MODEL"))
	}

	rawConfig := renderQuickstartConfig(quickstartConfigValues{
		TelegramBotToken: token,
		AdminUserID:      adminID,
		Provider:         provider,
		ProviderAPIKey:   providerKey,
		ProviderModel:    providerModel,
	})
	if err := writeValidatedQuickstartConfig(configPath, rawConfig, opts.Force); err != nil {
		return err
	}

	fmt.Fprintf(opts.Out, "action: quickstart\n")
	fmt.Fprintf(opts.Out, "config_path: %s\n", configPath)
	fmt.Fprintf(opts.Out, "admin_user_id: %d\n", adminID)
	fmt.Fprintf(opts.Out, "provider: %s\n", provider)

	if !opts.InstallService {
		fmt.Fprintf(opts.Out, "service_installed: false\n")
		fmt.Fprintf(opts.Out, "next: aphelion quickstart --config %s --install-service\n", configPath)
		return nil
	}

	return runQuickstartServiceInstall(ctx, opts, configPath)
}

func runQuickstartInstallExisting(ctx context.Context, opts quickstartOptions, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return &configStartupError{Path: configPath, Err: err}
	}
	fmt.Fprintf(opts.Out, "action: quickstart\n")
	fmt.Fprintf(opts.Out, "config_path: %s\n", configPath)
	if len(cfg.Principals.Telegram.AdminUserIDs) == 1 {
		fmt.Fprintf(opts.Out, "admin_user_id: %d\n", cfg.Principals.Telegram.AdminUserIDs[0])
	}
	if provider := config.EffectiveNativeProvider(cfg); provider != "" {
		fmt.Fprintf(opts.Out, "provider: %s\n", provider)
	}
	return runQuickstartServiceInstall(ctx, opts, configPath)
}

func runQuickstartServiceInstall(ctx context.Context, opts quickstartOptions, configPath string) error {
	serviceResult, err := installQuickstartUserService(ctx, quickstartServiceOptions{
		ConfigPath:    configPath,
		ExecPath:      opts.ExecPath,
		WorkDir:       opts.WorkDir,
		Out:           opts.Out,
		CommandRunner: opts.CommandRunner,
		Timeout:       defaultQuickstartCommandTimeout,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "service_installed: true\n")
	fmt.Fprintf(opts.Out, "service_path: %s\n", serviceResult.ServicePath)
	fmt.Fprintf(opts.Out, "service_name: %s\n", aphelionUserServiceName)
	fmt.Fprintf(opts.Out, "service_restarted: %t\n", serviceResult.Restarted)
	fmt.Fprintf(opts.Out, "exec_path: %s\n", serviceResult.ExecPath)
	fmt.Fprintf(opts.Out, "workdir: %s\n", serviceResult.WorkDir)
	fmt.Fprintf(opts.Out, "status: verified\n")
	return nil
}

func quickstartHasConfigInputs(opts quickstartOptions) bool {
	return strings.TrimSpace(opts.TelegramBotToken) != "" ||
		opts.AdminUserID > 0 ||
		opts.DetectAdmin ||
		strings.TrimSpace(opts.Provider) != "" ||
		strings.TrimSpace(opts.ProviderAPIKey) != "" ||
		strings.TrimSpace(opts.ProviderModel) != ""
}

func normalizeQuickstartOptions(opts quickstartOptions) quickstartOptions {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.DetectAdminTimeout <= 0 {
		opts.DetectAdminTimeout = defaultQuickstartDetectAdminTimeout
	}
	if opts.NewTelegramClient == nil {
		opts.NewTelegramClient = defaultQuickstartOptions().NewTelegramClient
	}
	if opts.CommandRunner == nil {
		opts.CommandRunner = execQuickstartCommand
	}
	return opts
}

func (s *quickstartSession) resolveString(label string, explicit string, envNames []string, prompt string, secret bool) (string, error) {
	if value := strings.TrimSpace(explicit); value != "" {
		return value, nil
	}
	for _, name := range envNames {
		if value := strings.TrimSpace(s.getenv(name)); value != "" {
			return value, nil
		}
	}
	return s.promptRequired(label, prompt, secret)
}

func (s *quickstartSession) resolveProvider(explicit string) (string, error) {
	if provider := normalizeQuickstartProvider(explicit); provider != "" {
		return provider, nil
	}
	if provider := normalizeQuickstartProvider(s.getenv("APHELION_PROVIDER")); provider != "" {
		return provider, nil
	}
	if provider := inferQuickstartProviderFromEnv(s.getenv); provider != "" {
		return provider, nil
	}
	if s.noInput {
		return "", fmt.Errorf("provider is required in --no-input mode; pass --provider or set APHELION_PROVIDER")
	}
	if !s.allowPrompt {
		return "", fmt.Errorf("provider is required; pass --provider or set APHELION_PROVIDER")
	}
	fmt.Fprint(s.out, "Provider [openai]: ")
	raw, err := s.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	provider := normalizeQuickstartProvider(raw)
	if provider == "" {
		provider = "openai"
	}
	if !isQuickstartProvider(provider) {
		return "", fmt.Errorf("provider must be one of openai|anthropic|openrouter|gemini|ollama")
	}
	return provider, nil
}

func (s *quickstartSession) promptRequired(label string, prompt string, secret bool) (string, error) {
	if s.noInput {
		return "", fmt.Errorf("%s is required in --no-input mode", label)
	}
	if !s.allowPrompt {
		return "", fmt.Errorf("%s is required; pass a flag or set the matching environment variable", label)
	}
	if prompt == "" {
		prompt = label + ": "
	}
	fmt.Fprint(s.out, prompt)
	if secret {
		if file, ok := s.in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			raw, err := term.ReadPassword(int(file.Fd()))
			fmt.Fprintln(s.out)
			if err != nil {
				return "", err
			}
			if value := strings.TrimSpace(string(raw)); value != "" {
				return value, nil
			}
			return "", fmt.Errorf("%s is required", label)
		}
	}
	raw, err := s.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if value := strings.TrimSpace(raw); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("%s is required", label)
}

func detectTelegramAdminForQuickstart(ctx context.Context, client quickstartTelegramClient, session quickstartSession, timeout time.Duration) (int64, error) {
	if client == nil {
		return 0, fmt.Errorf("telegram client is required for admin discovery")
	}
	if session.noInput {
		return 0, fmt.Errorf("--detect-admin requires confirmation and cannot run with --no-input")
	}
	if !session.allowPrompt {
		return 0, fmt.Errorf("--detect-admin requires an interactive terminal")
	}
	if timeout <= 0 {
		timeout = defaultQuickstartDetectAdminTimeout
	}

	offset, err := nextTelegramUpdateOffset(ctx, client)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(session.out, "Message the Telegram bot now from the admin account.\n")
	candidates, err := collectTelegramAdminCandidates(ctx, client, offset, timeout)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, fmt.Errorf("timed out waiting for a Telegram admin message")
	}
	if len(candidates) == 1 {
		candidate := candidates[0]
		fmt.Fprintf(session.out, "Detected %s.\n", formatTelegramAdminCandidate(candidate))
		ok, err := session.confirm("Use this Telegram user as admin? [y/N]: ")
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, fmt.Errorf("admin discovery was rejected")
		}
		return candidate.UserID, nil
	}

	fmt.Fprintf(session.out, "Detected multiple Telegram users:\n")
	for i, candidate := range candidates {
		fmt.Fprintf(session.out, "  %d. %s\n", i+1, formatTelegramAdminCandidate(candidate))
	}
	fmt.Fprintf(session.out, "Choose admin number: ")
	raw, err := session.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	choice, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || choice < 1 || choice > len(candidates) {
		return 0, fmt.Errorf("invalid admin selection")
	}
	return candidates[choice-1].UserID, nil
}

func (s *quickstartSession) confirm(prompt string) (bool, error) {
	fmt.Fprint(s.out, prompt)
	raw, err := s.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func nextTelegramUpdateOffset(ctx context.Context, client quickstartTelegramClient) (int64, error) {
	updates, err := client.GetUpdates(ctx, 0, 0)
	if err != nil {
		return 0, err
	}
	var offset int64
	for _, update := range updates {
		if update.UpdateID >= offset {
			offset = update.UpdateID + 1
		}
	}
	return offset, nil
}

func collectTelegramAdminCandidates(ctx context.Context, client quickstartTelegramClient, offset int64, timeout time.Duration) ([]telegramAdminCandidate, error) {
	deadline := time.Now().Add(timeout)
	seen := map[int64]struct{}{}
	var candidates []telegramAdminCandidate
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		pollSeconds := int(remaining / time.Second)
		if pollSeconds < 1 {
			pollSeconds = 1
		}
		if pollSeconds > 5 {
			pollSeconds = 5
		}
		updates, err := client.GetUpdates(ctx, offset, pollSeconds)
		if err != nil {
			return nil, err
		}
		for _, update := range updates {
			if update.UpdateID < offset {
				continue
			}
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			candidate, ok := telegramAdminCandidateFromUpdate(update)
			if !ok {
				continue
			}
			if _, exists := seen[candidate.UserID]; exists {
				continue
			}
			seen[candidate.UserID] = struct{}{}
			candidates = append(candidates, candidate)
		}
		if len(candidates) > 0 {
			return candidates, nil
		}
	}
	return nil, nil
}

func telegramAdminCandidateFromUpdate(update telegram.Update) (telegramAdminCandidate, bool) {
	if update.Message == nil || update.Message.From == nil || update.Message.From.ID <= 0 || update.Message.From.IsBot {
		return telegramAdminCandidate{}, false
	}
	candidate := telegramAdminCandidate{
		UserID:    update.Message.From.ID,
		Username:  strings.TrimSpace(update.Message.From.Username),
		FirstName: strings.TrimSpace(update.Message.From.FirstName),
		LastName:  strings.TrimSpace(update.Message.From.LastName),
	}
	if update.Message.Chat != nil {
		candidate.ChatID = update.Message.Chat.ID
		candidate.ChatType = strings.TrimSpace(update.Message.Chat.Type)
	}
	return candidate, true
}

func formatTelegramAdminCandidate(candidate telegramAdminCandidate) string {
	parts := []string{fmt.Sprintf("user_id=%d", candidate.UserID)}
	if candidate.Username != "" {
		parts = append(parts, "username=@"+candidate.Username)
	}
	name := strings.TrimSpace(strings.Join([]string{candidate.FirstName, candidate.LastName}, " "))
	if name != "" {
		parts = append(parts, "name="+name)
	}
	if candidate.ChatID != 0 {
		parts = append(parts, fmt.Sprintf("chat_id=%d", candidate.ChatID))
	}
	if candidate.ChatType != "" {
		parts = append(parts, "chat_type="+candidate.ChatType)
	}
	return strings.Join(parts, " ")
}

func renderQuickstartConfig(values quickstartConfigValues) string {
	provider := normalizeQuickstartProvider(values.Provider)
	var b strings.Builder
	fmt.Fprintf(&b, "[telegram]\n")
	fmt.Fprintf(&b, "bot_token = %s\n\n", strconv.Quote(strings.TrimSpace(values.TelegramBotToken)))
	fmt.Fprintf(&b, "[principals.telegram]\n")
	fmt.Fprintf(&b, "admin_user_ids = [%d]\n\n", values.AdminUserID)
	fmt.Fprintf(&b, "[autonomy]\n")
	fmt.Fprintf(&b, "default_mode = \"ask_first\"\n")
	fmt.Fprintf(&b, "ceiling = \"ask_first\"\n")
	fmt.Fprintf(&b, "allow_live_overrides = false\n")
	fmt.Fprintf(&b, "max_override_duration = \"4h\"\n\n")
	fmt.Fprintf(&b, "[providers]\n")
	fmt.Fprintf(&b, "selection = \"manual\"\n")
	fmt.Fprintf(&b, "auto_order = [%s]\n", strconv.Quote(provider))
	fmt.Fprintf(&b, "default = %s\n", strconv.Quote(provider))
	fmt.Fprintf(&b, "fallback_chain = []\n\n")
	switch provider {
	case "anthropic":
		fmt.Fprintf(&b, "[providers.anthropic]\n")
		fmt.Fprintf(&b, "api_key = %s\n", strconv.Quote(strings.TrimSpace(values.ProviderAPIKey)))
	case "openai":
		fmt.Fprintf(&b, "[providers.openai]\n")
		fmt.Fprintf(&b, "api_key = %s\n", strconv.Quote(strings.TrimSpace(values.ProviderAPIKey)))
	case "openrouter":
		fmt.Fprintf(&b, "[providers.openrouter]\n")
		fmt.Fprintf(&b, "api_key = %s\n", strconv.Quote(strings.TrimSpace(values.ProviderAPIKey)))
	case "gemini":
		fmt.Fprintf(&b, "[providers.gemini]\n")
		fmt.Fprintf(&b, "api_key = %s\n", strconv.Quote(strings.TrimSpace(values.ProviderAPIKey)))
	case "ollama":
		fmt.Fprintf(&b, "[providers.ollama]\n")
	}
	if model := strings.TrimSpace(values.ProviderModel); model != "" {
		fmt.Fprintf(&b, "model = %s\n", strconv.Quote(model))
	}
	return b.String()
}

func writeValidatedQuickstartConfig(path string, raw string, force bool) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is required")
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config %s already exists; pass --force to overwrite it", path)
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("stat config %s: %w", path, err)
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".aphelion-quickstart-*.toml")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temporary config: %w", err)
	}
	if _, err := tmp.WriteString(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if _, err := config.Load(tmpPath); err != nil {
		return fmt.Errorf("generated config did not validate: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install config %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod config %s: %w", path, err)
	}
	return nil
}

func installQuickstartUserService(ctx context.Context, opts quickstartServiceOptions) (quickstartServiceResult, error) {
	if strings.TrimSpace(opts.ConfigPath) == "" {
		return quickstartServiceResult{}, fmt.Errorf("config path is required")
	}
	runner := opts.CommandRunner
	if runner == nil {
		runner = execQuickstartCommand
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultQuickstartCommandTimeout
	}
	execPath := strings.TrimSpace(opts.ExecPath)
	if execPath == "" {
		path, err := os.Executable()
		if err != nil {
			return quickstartServiceResult{}, fmt.Errorf("resolve current executable: %w", err)
		}
		execPath = path
	}
	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return quickstartServiceResult{}, fmt.Errorf("resolve home directory: %w", err)
		}
		workDir = home
	}
	servicePath, err := aphelionUserServicePath()
	if err != nil {
		return quickstartServiceResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := runner(runCtx, execPath, "--config", opts.ConfigPath, "--check-config"); err != nil {
		return quickstartServiceResult{}, fmt.Errorf("check config: %w", err)
	}
	if err := runner(runCtx, execPath, "init", "--config", opts.ConfigPath); err != nil {
		return quickstartServiceResult{}, fmt.Errorf("init: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		return quickstartServiceResult{}, fmt.Errorf("create service directory: %w", err)
	}
	unit := renderAphelionServiceUnit(opts.ConfigPath, execPath, workDir)
	if err := os.WriteFile(servicePath, []byte(unit), 0o644); err != nil {
		return quickstartServiceResult{}, fmt.Errorf("write service unit: %w", err)
	}

	if err := runner(runCtx, "systemctl", "--user", "daemon-reload"); err != nil {
		return quickstartServiceResult{}, fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	restarted := false
	if err := runner(runCtx, "systemctl", "--user", "is-active", "--quiet", aphelionUserServiceName); err == nil {
		if err := runner(runCtx, execPath, "park-restart", "--config", opts.ConfigPath, "--source", "quickstart_install_service"); err != nil {
			return quickstartServiceResult{}, fmt.Errorf("park restart: %w", err)
		}
		if err := runner(runCtx, "systemctl", "--user", "restart", aphelionUserServiceName); err != nil {
			return quickstartServiceResult{}, fmt.Errorf("systemctl restart: %w", err)
		}
		restarted = true
	} else {
		if err := runner(runCtx, "systemctl", "--user", "enable", "--now", aphelionUserServiceName); err != nil {
			return quickstartServiceResult{}, fmt.Errorf("systemctl enable: %w", err)
		}
	}
	if err := waitForAphelionUserService(runCtx, runner); err != nil {
		return quickstartServiceResult{}, err
	}
	if err := runner(runCtx, execPath, "verify-deploy", "--config", opts.ConfigPath); err != nil {
		return quickstartServiceResult{}, fmt.Errorf("verify deploy: %w", err)
	}
	return quickstartServiceResult{
		ServicePath: servicePath,
		ConfigPath:  opts.ConfigPath,
		ExecPath:    execPath,
		WorkDir:     workDir,
		Restarted:   restarted,
	}, nil
}

func waitForAphelionUserService(ctx context.Context, runner quickstartCommandRunner) error {
	var lastErr error
	for i := 0; i < 10; i++ {
		if err := runner(ctx, "systemctl", "--user", "is-active", "--quiet", aphelionUserServiceName); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("service %s did not become active: %w", aphelionUserServiceName, lastErr)
}

func renderAphelionServiceUnit(configPath string, execPath string, workDir string) string {
	unit := strings.ReplaceAll(aphelionServiceTemplate, "@WORKDIR@", workDir)
	unit = strings.ReplaceAll(unit, "@EXEC_PATH@", execPath)
	unit = strings.ReplaceAll(unit, "@CONFIG_PATH@", configPath)
	return unit
}

func aphelionUserServicePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(configDir, "systemd", "user", aphelionUserServiceName+".service"), nil
}

func execQuickstartCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func inferQuickstartProviderFromEnv(getenv func(string) string) string {
	for _, entry := range []struct {
		Provider string
		EnvNames []string
	}{
		{Provider: "openai", EnvNames: []string{"OPENAI_API_KEY", "APHELION_OPENAI_API_KEY"}},
		{Provider: "anthropic", EnvNames: []string{"ANTHROPIC_API_KEY", "APHELION_ANTHROPIC_API_KEY"}},
		{Provider: "openrouter", EnvNames: []string{"OPENROUTER_API_KEY", "APHELION_OPENROUTER_API_KEY"}},
		{Provider: "gemini", EnvNames: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "APHELION_GEMINI_API_KEY"}},
	} {
		for _, name := range entry.EnvNames {
			if strings.TrimSpace(getenv(name)) != "" {
				return entry.Provider
			}
		}
	}
	return ""
}

func providerAPIKeyEnvNames(provider string) []string {
	envs := []string{"APHELION_PROVIDER_API_KEY"}
	switch normalizeQuickstartProvider(provider) {
	case "anthropic":
		return append(envs, "ANTHROPIC_API_KEY", "APHELION_ANTHROPIC_API_KEY")
	case "openai":
		return append(envs, "OPENAI_API_KEY", "APHELION_OPENAI_API_KEY")
	case "openrouter":
		return append(envs, "OPENROUTER_API_KEY", "APHELION_OPENROUTER_API_KEY")
	case "gemini":
		return append(envs, "GEMINI_API_KEY", "GOOGLE_API_KEY", "APHELION_GEMINI_API_KEY")
	default:
		return envs
	}
}

func providerPrompt(provider string) string {
	switch normalizeQuickstartProvider(provider) {
	case "anthropic":
		return "Anthropic API key: "
	case "openai":
		return "OpenAI API key: "
	case "openrouter":
		return "OpenRouter API key: "
	case "gemini":
		return "Gemini API key: "
	default:
		return provider + " API key: "
	}
}

func providerRequiresAPIKey(provider string) bool {
	switch normalizeQuickstartProvider(provider) {
	case "anthropic", "openai", "openrouter", "gemini":
		return true
	default:
		return false
	}
}

func normalizeQuickstartProvider(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = strings.ReplaceAll(name, "-", "")
	name = strings.ReplaceAll(name, "_", "")
	switch name {
	case "":
		return ""
	case "anthropic", "claude":
		return "anthropic"
	case "openai":
		return "openai"
	case "openrouter":
		return "openrouter"
	case "gemini", "google":
		return "gemini"
	case "ollama", "local":
		return "ollama"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func isQuickstartProvider(provider string) bool {
	switch normalizeQuickstartProvider(provider) {
	case "anthropic", "openai", "openrouter", "gemini", "ollama":
		return true
	default:
		return false
	}
}

func parsePositiveInt64FromEnv(getenv func(string) string, names ...string) (int64, error) {
	for _, name := range names {
		raw := strings.TrimSpace(getenv(name))
		if raw == "" {
			continue
		}
		return parsePositiveInt64(raw, name)
	}
	return 0, nil
}

func parsePositiveInt64(raw string, label string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer: %w", label, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", label)
	}
	return value, nil
}

func isTerminalReader(in io.Reader) bool {
	file, ok := in.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
