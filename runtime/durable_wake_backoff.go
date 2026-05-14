//go:build linux

package runtime

import "time"

func durableWakeBackoffUntil(now time.Time, failures int) time.Time {
	if failures <= 0 {
		failures = 1
	}
	d := 30 * time.Minute
	for i := 1; i < failures && d < 6*time.Hour; i++ {
		d *= 2
	}
	if d > 6*time.Hour {
		d = 6 * time.Hour
	}
	return now.UTC().Add(d)
}
