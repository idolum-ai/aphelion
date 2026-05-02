//go:build linux

package core

import "errors"

var (
	ErrContinuationExpired    = errors.New("continuation expired")
	ErrContinuationNotPending = errors.New("continuation is not pending")
	ErrContinuationNoTurns    = errors.New("continuation has no remaining turns")
	ErrContinuationStale      = errors.New("continuation is stale")
)
