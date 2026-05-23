//go:build linux

package standalonecli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

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
