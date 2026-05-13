//go:build linux

// Package provider owns model-provider adapters.
//
// It converts agent contracts to concrete provider APIs, handles streaming, and
// coordinates failover. It should not own turn policy, Telegram UI, or durable
// storage.
package provider
