//go:build linux

// Package continuation owns private, deterministic continuation mechanics for
// the runtime shell.
//
// This package is not a public API. It may classify continuation authority,
// derive bounded work modes, and validate pure continuation state transitions
// from explicit session records. It must not own ingress, session locking,
// store mutation, Telegram delivery, provider/work-executor selection,
// background lifecycle, or broad runtime orchestration.
package continuation
