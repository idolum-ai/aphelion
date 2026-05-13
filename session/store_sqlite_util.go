//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func defaultChatType(chatType string) string {
	if strings.TrimSpace(chatType) == "" {
		return "dm"
	}
	return chatType
}

func nullToString(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}

func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func marshalStringSlice(values []string) ([]byte, error) {
	if len(values) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(values)
}

func marshalInt64Slice(values []int64) ([]byte, error) {
	values = core.NormalizeDurableAgentAllowedTelegramUserIDs(values)
	if len(values) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(values)
}

func mustParseSQLiteTime(raw string) time.Time {
	t, err := parseSQLiteTime(raw)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nonZeroTimeOrNow(t, now time.Time) time.Time {
	if t.IsZero() {
		return now
	}
	return t
}

func maxInt64(a int64, b int64) int64 {
	if a >= b {
		return a
	}
	return b
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func sqliteNegativeDuration(d time.Duration) string {
	seconds := int64(d.Seconds())
	if seconds < 0 {
		seconds = -seconds
	}
	return fmt.Sprintf("-%d seconds", seconds)
}

func parseSQLiteTime(raw string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format %q", raw)
}
