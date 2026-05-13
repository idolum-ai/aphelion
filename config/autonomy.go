//go:build linux

package config

import (
	"fmt"
	"strings"
	"time"
)

func normalizeWorkExecutor(value string) string {
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.ReplaceAll(name, "-", "_")
	switch name {
	case "", "auto":
		return "auto"
	case "codex", "native":
		return name
	default:
		return name
	}
}

func normalizeWorkExecutorList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		name := normalizeWorkExecutor(raw)
		if name == "" || name == "auto" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func NormalizeAutonomyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		return "ask_first"
	case "review", "review-only", "review_only":
		return "review_only"
	case "ask", "ask-first", "ask_first":
		return "ask_first"
	case "lease", "leased":
		return "leased"
	case "mission", "mission_owned", "mission-owned":
		return "mission"
	case "off":
		return "off"
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func AutonomyModeRank(mode string) (int, bool) {
	switch NormalizeAutonomyMode(mode) {
	case "off":
		return 0, true
	case "review_only":
		return 1, true
	case "ask_first":
		return 2, true
	case "leased":
		return 3, true
	case "mission":
		return 4, true
	default:
		return 0, false
	}
}

func EffectiveAutonomyPolicy(cfg *Config) AutonomyPolicy {
	policy := AutonomyPolicy{
		DefaultMode:         "ask_first",
		Ceiling:             "leased",
		AllowLiveOverrides:  true,
		MaxOverrideDuration: 4 * time.Hour,
	}
	if cfg == nil {
		return policy
	}
	policy.DefaultMode = NormalizeAutonomyMode(cfg.Autonomy.DefaultMode)
	policy.Ceiling = NormalizeAutonomyMode(cfg.Autonomy.Ceiling)
	policy.AllowLiveOverrides = cfg.Autonomy.AllowLiveOverrides
	if parsed, err := time.ParseDuration(strings.TrimSpace(cfg.Autonomy.MaxOverrideDuration)); err == nil && parsed > 0 {
		policy.MaxOverrideDuration = parsed
	}
	return policy
}

func validateMemoryWritePolicy(policy MemoryWritePolicyConfig) error {
	for name, value := range map[string]string{
		"memory.write_policy.direct_user_writes": strings.TrimSpace(policy.DirectUserWrites),
		"memory.write_policy.reflection_writes":  strings.TrimSpace(policy.ReflectionWrites),
		"memory.write_policy.aggressive_writes":  strings.TrimSpace(policy.AggressiveWrites),
	} {
		switch strings.ToLower(value) {
		case "apply", "propose":
		default:
			return fmt.Errorf("%s must be apply or propose", name)
		}
	}
	return nil
}

func validateAutonomyConfig(policy AutonomyConfig) error {
	defaultMode := NormalizeAutonomyMode(policy.DefaultMode)
	ceiling := NormalizeAutonomyMode(policy.Ceiling)
	defaultRank, ok := AutonomyModeRank(defaultMode)
	if !ok {
		return fmt.Errorf("autonomy.default_mode must be one of off|review_only|ask_first|leased|mission")
	}
	ceilingRank, ok := AutonomyModeRank(ceiling)
	if !ok {
		return fmt.Errorf("autonomy.ceiling must be one of off|review_only|ask_first|leased|mission")
	}
	if defaultRank > ceilingRank {
		return fmt.Errorf("autonomy.default_mode must not exceed autonomy.ceiling")
	}
	duration, err := time.ParseDuration(strings.TrimSpace(policy.MaxOverrideDuration))
	if err != nil {
		return fmt.Errorf("autonomy.max_override_duration must be a valid duration: %w", err)
	}
	if duration <= 0 {
		return fmt.Errorf("autonomy.max_override_duration must be > 0")
	}
	if policy.AllowLiveOverrides && duration > 24*time.Hour {
		return fmt.Errorf("autonomy.max_override_duration must be <= 24h when live overrides are enabled")
	}
	return nil
}
