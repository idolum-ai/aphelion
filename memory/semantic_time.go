//go:build linux

package memory

import (
	"fmt"
	"strings"
	"time"
)

func normalizeSemanticScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "principal":
		return "principal"
	default:
		return "shared"
	}
}

func normalizePrincipalID(principalID string) string {
	return strings.TrimSpace(principalID)
}

func validateImportState(state SemanticImportState) error {
	switch state {
	case SemanticImportStateQuarantine, SemanticImportStateApproved, SemanticImportStateRejected:
		return nil
	default:
		return fmt.Errorf("invalid semantic import_state %q", state)
	}
}

func sameInstant(a time.Time, b time.Time) bool {
	if a.IsZero() && b.IsZero() {
		return true
	}
	return a.UTC().Equal(b.UTC())
}

func nullableTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return utcTimestamp(t)
}

func utcTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseOptionalTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unsupported time format %q", raw)
}
