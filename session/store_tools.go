//go:build linux

package session

import (
	"time"
)

func nullableTimeRFC3339(ts time.Time) any {
	if ts.IsZero() {
		return nil
	}
	return ts.UTC().Format(time.RFC3339Nano)
}
