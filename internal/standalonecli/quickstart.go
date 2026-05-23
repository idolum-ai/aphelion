//go:build linux

package standalonecli

import (
	"bufio"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/telegram"
)

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

type quickstartSession struct {
	in          io.Reader
	out         io.Writer
	reader      *bufio.Reader
	getenv      func(string) string
	noInput     bool
	allowPrompt bool
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
