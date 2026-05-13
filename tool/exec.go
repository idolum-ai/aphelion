//go:build linux

package tool

import (
	"time"
)

const (
	defaultMaxOutputBytes          = 32 * 1024
	capabilityGrantObserverTimeout = 10 * time.Second
)
