//go:build linux

package standalonecli

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

//go:embed deploy/aphelion.service
var aphelionServiceTemplate string

type quickstartCommandRunner func(ctx context.Context, name string, args ...string) error

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

func runQuickstartInstallExisting(ctx context.Context, opts quickstartOptions, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return &ConfigStartupError{Path: configPath, Err: err}
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
	if err := runner(runCtx, execPath, "verify-deploy", "--config", opts.ConfigPath, "--format=kv"); err != nil {
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
