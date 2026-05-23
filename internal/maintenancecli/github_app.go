//go:build linux

package maintenancecli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/githubapp"
	"github.com/idolum-ai/aphelion/session"
)

const (
	githubAppTokenFormatRaw           = "raw"
	githubAppTokenFormatJSON          = "json"
	githubAppTokenFormatGitCredential = "git-credential"
)

var githubAppHTTPClient = &http.Client{Timeout: 30 * time.Second}

func RunGitHubAppCommand(args []string) error {
	return runGitHubAppCommand(args)
}

func runGitHubAppCommand(args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "status" {
		if len(args) > 0 {
			args = args[1:]
		}
		return runGitHubAppStatusCommand(args)
	}
	switch strings.TrimSpace(args[0]) {
	case "token":
		return runGitHubAppTokenCommand(args[1:])
	default:
		return fmt.Errorf("github-app subcommand must be one of status|token")
	}
}

func runGitHubAppStatusCommand(args []string) error {
	fs := flag.NewFlagSet("github-app status", flag.ContinueOnError)
	configPathFlag := fs.String("config", "", "path to config.toml")
	appFlag := fs.String("app", "", "GitHub App name")
	onlineFlag := fs.Bool("online", false, "mint and discard an installation token to verify online auth")
	formatFlag := fs.String("format", commandOutputHuman, "output format: human, kv, or json")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for github-app status", extra)
	}
	format, err := normalizeCommandOutputFormat(*formatFlag, *jsonOutput)
	if err != nil {
		return err
	}
	cfg, resolvedPath, err := loadConfigForCommand(*configPathFlag)
	if err != nil {
		return err
	}
	apps, err := selectGitHubApps(cfg, *appFlag)
	if err != nil {
		return err
	}
	report := buildGitHubAppStatusReport(cfg, resolvedPath, apps)
	if *onlineFlag {
		if !cfg.GitHub.Enabled {
			return fmt.Errorf("github.enabled is false")
		}
		store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
		if err != nil {
			return fmt.Errorf("open sessions store before github app token check: %w", err)
		}
		defer func() { _ = store.Close() }()
		client := githubAppClientFromConfig(cfg)
		for i, appCfg := range apps {
			app := githubAppFromConfig(appCfg)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			token, mintErr := client.MintInstallationToken(ctx, app)
			cancel()
			status := "verified"
			errText := ""
			if mintErr != nil {
				status = "failed"
				errText = githubapp.Redact(mintErr.Error())
			}
			if err := appendGitHubAppTokenEvent(store, "status_online", status, app, token, errText); err != nil {
				return err
			}
			report.Apps[i].OnlineStatus = status
			if !token.ExpiresAt.IsZero() {
				report.Apps[i].TokenExpiresAt = token.ExpiresAt.Format(time.RFC3339)
			}
			report.Apps[i].LastError = errText
			if mintErr != nil {
				return mintErr
			}
		}
		report.Status = "verified"
	}
	return renderGitHubAppCommandReport(os.Stdout, report, format)
}

func runGitHubAppTokenCommand(args []string) error {
	fs := flag.NewFlagSet("github-app token", flag.ContinueOnError)
	configPathFlag := fs.String("config", "", "path to config.toml")
	appFlag := fs.String("app", "", "GitHub App name")
	repositoryFlag := fs.String("repository", "", "optional owner/repo subset")
	formatFlag := fs.String("format", githubAppTokenFormatRaw, "output format: raw, json, or git-credential")
	showTokenFlag := fs.Bool("show-token", false, "allow token material on stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for github-app token", extra)
	}
	if !*showTokenFlag {
		return fmt.Errorf("github-app token refuses to print token material without --show-token")
	}
	tokenFormat, err := normalizeGitHubAppTokenFormat(*formatFlag)
	if err != nil {
		return err
	}
	cfg, _, err := loadConfigForCommand(*configPathFlag)
	if err != nil {
		return err
	}
	if !cfg.GitHub.Enabled {
		return fmt.Errorf("github.enabled is false")
	}
	appCfg, err := selectGitHubApp(cfg, *appFlag)
	if err != nil {
		return err
	}
	app, err := githubapp.SelectRepository(githubAppFromConfig(appCfg), *repositoryFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return fmt.Errorf("open sessions store before github app token mint: %w", err)
	}
	defer func() { _ = store.Close() }()
	client := githubAppClientFromConfig(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	token, mintErr := client.MintInstallationToken(ctx, app)
	cancel()
	status := "minted"
	errText := ""
	if mintErr != nil {
		status = "failed"
		errText = githubapp.Redact(mintErr.Error())
	}
	if err := appendGitHubAppTokenEvent(store, "token", status, app, token, errText); err != nil {
		return err
	}
	if mintErr != nil {
		return mintErr
	}
	out := githubAppTokenOutput{
		AppName:        app.Name,
		InstallationID: app.InstallationID,
		Repository:     strings.TrimSpace(*repositoryFlag),
		ExpiresAt:      token.ExpiresAt.Format(time.RFC3339),
		Permissions:    token.Permissions,
		Repositories:   token.Repositories,
		Token:          token.Token,
	}
	return renderGitHubAppTokenOutput(os.Stdout, out, tokenFormat, cfg.GitHub.APIBaseURL)
}

func githubAppClientFromConfig(cfg *config.Config) *githubapp.Client {
	return githubapp.NewClient(githubapp.Client{
		HTTPClient: githubAppHTTPClient,
		APIBaseURL: cfg.GitHub.APIBaseURL,
		APIVersion: cfg.GitHub.APIVersion,
		UserAgent:  config.EffectiveUserAgent(cfg, githubapp.DefaultUserAgent),
	})
}

func githubAppFromConfig(app config.GitHubAppConfig) githubapp.App {
	return githubapp.App{
		Name:                 app.Name,
		AppID:                app.AppID,
		InstallationID:       app.InstallationID,
		PrivateKeyFile:       app.PrivateKeyFile,
		Repositories:         append([]string(nil), app.Repositories...),
		Permissions:          append([]string(nil), app.Permissions...),
		AllowAllRepositories: app.AllowAllRepositories,
		AllowAllPermissions:  app.AllowAllPermissions,
	}
}

func selectGitHubApps(cfg *config.Config, name string) ([]config.GitHubAppConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return append([]config.GitHubAppConfig(nil), cfg.GitHub.Apps...), nil
	}
	app, err := selectGitHubApp(cfg, name)
	if err != nil {
		return nil, err
	}
	return []config.GitHubAppConfig{app}, nil
}

func selectGitHubApp(cfg *config.Config, name string) (config.GitHubAppConfig, error) {
	if cfg == nil {
		return config.GitHubAppConfig{}, fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		if len(cfg.GitHub.Apps) == 1 {
			return cfg.GitHub.Apps[0], nil
		}
		if len(cfg.GitHub.Apps) == 0 {
			return config.GitHubAppConfig{}, fmt.Errorf("no github apps are configured")
		}
		return config.GitHubAppConfig{}, fmt.Errorf("--app is required when multiple github apps are configured")
	}
	for _, app := range cfg.GitHub.Apps {
		if strings.EqualFold(strings.TrimSpace(app.Name), name) {
			return app, nil
		}
	}
	return config.GitHubAppConfig{}, fmt.Errorf("github app %q is not configured", name)
}

func normalizeGitHubAppTokenFormat(raw string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(raw))
	if format == "" {
		return githubAppTokenFormatRaw, nil
	}
	switch format {
	case githubAppTokenFormatRaw, githubAppTokenFormatJSON, githubAppTokenFormatGitCredential:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported github-app token output format %q; use raw, json, or git-credential", raw)
	}
}
