//go:build linux

// Package media owns provider-neutral media contracts.
//
// It defines media interfaces and normalized request/response types used by
// runtime and transport code. Current contracts cover audio transcription and
// document text extraction. Concrete provider clients, Telegram UX, retention
// policy, prompt injection, and turn orchestration stay outside this package.
package media
