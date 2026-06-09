//go:build linux

package runtime

import (
	"regexp"
	"strings"
)

var runtimeSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(token|api[_-]?key|secret|password)\s*[:=]\s*[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)\b[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY)[A-Z0-9_]*=[^\s]+`),
	regexp.MustCompile(`(?i)authorization\s*[:=]\s*bearer\s+[^\s,;"}]+`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{12,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{12,}`),
	regexp.MustCompile(`/home/[^/\s]+/\.aphelion/secrets/[^\s]+`),
}

func redactRuntimeText(value string, limit int) string {
	value = strings.TrimSpace(value)
	for _, pattern := range runtimeSecretPatterns {
		value = pattern.ReplaceAllString(value, "[redacted]")
	}
	if limit > 0 && len(value) > limit {
		value = strings.TrimSpace(value[:limit]) + " [truncated]"
	}
	return value
}
