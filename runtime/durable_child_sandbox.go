//go:build linux

package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableChildSandboxAccess struct {
	readonlyPaths []string
	readonlyBinds []sandbox.BindPath
	env           map[string]string
}

func durableChildSandboxAccessFor(binaryPath string, agent core.DurableAgent) durableChildSandboxAccess {
	access := durableChildSandboxAccess{readonlyPaths: []string{strings.TrimSpace(binaryPath)}}
	access.readonlyPaths = compactNonEmptyStrings(access.readonlyPaths)

	bootstrap := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	if bootstrap.Backend == "codex" && strings.TrimSpace(bootstrap.CodexHome) != "" {
		access.readonlyPaths = append(access.readonlyPaths, strings.TrimSpace(bootstrap.CodexHome))
	}
	if durableAgentUsesGogCLI(agent) {
		access.addGogCLI()
	}
	access.readonlyPaths = compactNonEmptyStrings(access.readonlyPaths)
	return access
}

func (a *durableChildSandboxAccess) addGogCLI() {
	if a == nil {
		return
	}
	if gogPath, err := exec.LookPath("gog"); err == nil {
		if dir := strings.TrimSpace(filepath.Dir(gogPath)); dir != "" {
			a.readonlyBinds = append(a.readonlyBinds, sandbox.BindPath{Source: dir, Target: "/usr/local/bin"})
		}
	}
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			configHome = filepath.Join(home, ".config")
		}
	}
	if configHome != "" {
		gogConfigDir := filepath.Join(configHome, "gogcli")
		if info, err := os.Stat(gogConfigDir); err == nil && info.IsDir() {
			a.readonlyPaths = append(a.readonlyPaths, gogConfigDir)
			a.ensureEnv()["XDG_CONFIG_HOME"] = configHome
		}
	}
	if password := strings.TrimSpace(os.Getenv("GOG_KEYRING_PASSWORD")); password != "" {
		a.ensureEnv()["GOG_KEYRING_PASSWORD"] = password
	}
}

func (a *durableChildSandboxAccess) ensureEnv() map[string]string {
	if a.env == nil {
		a.env = make(map[string]string)
	}
	return a.env
}

func durableAgentUsesGogCLI(agent core.DurableAgent) bool {
	if !strings.EqualFold(strings.TrimSpace(agent.ChannelKind), "email") || agent.ChannelConfig.Email == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(agent.ChannelConfig.Email.Adapter), "gog_cli")
}

func compactNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
