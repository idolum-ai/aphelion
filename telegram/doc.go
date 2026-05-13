//go:build linux

// Package telegram owns Telegram wire types and Bot API client behavior.
//
// The package normalizes Telegram updates into core transport records and sends
// Telegram API requests. It should stay transport-level and avoid importing
// runtime, turn, pipeline, or storage orchestration.
package telegram
