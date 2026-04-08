//go:build linux

package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Identity  IdentityConfig
	Telegram  TelegramConfig
	Providers ProvidersConfig
	Sessions  SessionsConfig
	Agent     AgentConfig
}

type IdentityConfig struct {
	UserAgent string
}

type TelegramConfig struct {
	BotToken    string
	PollTimeout int
}

type ProvidersConfig struct {
	Default   string
	Anthropic AnthropicConfig
}

type AnthropicConfig struct {
	APIKey    string
	Model     string
	MaxTokens int
}

type SessionsConfig struct {
	DBPath string
}

type AgentConfig struct {
	Workspace     string
	MaxIterations int
	ToolTimeout   int
}

func Default() Config {
	return Config{
		Telegram: TelegramConfig{
			PollTimeout: 30,
		},
		Providers: ProvidersConfig{
			Default: "anthropic",
			Anthropic: AnthropicConfig{
				Model:     "claude-sonnet-4-6",
				MaxTokens: 4096,
			},
		},
		Sessions: SessionsConfig{
			DBPath: "~/.config/aphelion/sessions.db",
		},
		Agent: AgentConfig{
			Workspace:     ".",
			MaxIterations: 50,
			ToolTimeout:   300,
		},
	}
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := parse(&cfg, string(raw)); err != nil {
		return nil, err
	}

	cfg.Sessions.DBPath, err = expandPath(cfg.Sessions.DBPath)
	if err != nil {
		return nil, fmt.Errorf("expand sessions.db_path: %w", err)
	}
	cfg.Agent.Workspace, err = expandPath(cfg.Agent.Workspace)
	if err != nil {
		return nil, fmt.Errorf("expand agent.workspace: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func parse(cfg *Config, raw string) error {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	section := ""
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("config line %d: expected key = value", lineNo)
		}

		if err := assign(cfg, section, strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("config line %d: %w", lineNo, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan config: %w", err)
	}
	return nil
}

func assign(cfg *Config, section, key, value string) error {
	switch section {
	case "identity":
		switch key {
		case "user_agent":
			v, err := parseString(value)
			if err != nil {
				return err
			}
			cfg.Identity.UserAgent = v
			return nil
		}
	case "telegram":
		switch key {
		case "bot_token":
			v, err := parseString(value)
			if err != nil {
				return err
			}
			cfg.Telegram.BotToken = v
			return nil
		case "poll_timeout":
			v, err := parseInt(value)
			if err != nil {
				return err
			}
			cfg.Telegram.PollTimeout = v
			return nil
		}
	case "providers":
		switch key {
		case "default":
			v, err := parseString(value)
			if err != nil {
				return err
			}
			cfg.Providers.Default = v
			return nil
		}
	case "providers.anthropic":
		switch key {
		case "api_key":
			v, err := parseString(value)
			if err != nil {
				return err
			}
			cfg.Providers.Anthropic.APIKey = v
			return nil
		case "model":
			v, err := parseString(value)
			if err != nil {
				return err
			}
			cfg.Providers.Anthropic.Model = v
			return nil
		case "max_tokens":
			v, err := parseInt(value)
			if err != nil {
				return err
			}
			cfg.Providers.Anthropic.MaxTokens = v
			return nil
		}
	case "sessions":
		switch key {
		case "db_path":
			v, err := parseString(value)
			if err != nil {
				return err
			}
			cfg.Sessions.DBPath = v
			return nil
		}
	case "agent":
		switch key {
		case "workspace":
			v, err := parseString(value)
			if err != nil {
				return err
			}
			cfg.Agent.Workspace = v
			return nil
		case "max_iterations":
			v, err := parseInt(value)
			if err != nil {
				return err
			}
			cfg.Agent.MaxIterations = v
			return nil
		case "tool_timeout":
			v, err := parseInt(value)
			if err != nil {
				return err
			}
			cfg.Agent.ToolTimeout = v
			return nil
		}
	}

	return nil
}

func stripComment(line string) string {
	var b strings.Builder
	inQuote := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '"' && (i == 0 || line[i-1] != '\\') {
			inQuote = !inQuote
		}
		if ch == '#' && !inQuote {
			break
		}
		b.WriteByte(ch)
	}

	return b.String()
}

func parseString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "\"") {
		v, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("parse string %q: %w", raw, err)
		}
		return v, nil
	}
	return raw, nil
}

func parseInt(raw string) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("parse int %q: %w", raw, err)
	}
	return v, nil
}

func validate(cfg *Config) error {
	if strings.TrimSpace(cfg.Telegram.BotToken) == "" {
		return fmt.Errorf("telegram.bot_token is required")
	}
	if strings.TrimSpace(cfg.Providers.Anthropic.APIKey) == "" {
		return fmt.Errorf("providers.anthropic.api_key is required")
	}
	if cfg.Telegram.PollTimeout <= 0 {
		return fmt.Errorf("telegram.poll_timeout must be > 0")
	}
	if cfg.Agent.MaxIterations <= 0 {
		return fmt.Errorf("agent.max_iterations must be > 0")
	}
	if cfg.Agent.ToolTimeout <= 0 {
		return fmt.Errorf("agent.tool_timeout must be > 0")
	}
	if strings.TrimSpace(cfg.Sessions.DBPath) == "" {
		return fmt.Errorf("sessions.db_path is required")
	}
	if strings.TrimSpace(cfg.Agent.Workspace) == "" {
		return fmt.Errorf("agent.workspace is required")
	}
	return nil
}

func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	return filepath.Abs(path)
}
