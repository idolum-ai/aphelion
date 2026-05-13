//go:build linux

// Package openai owns narrow OpenAI service clients used outside chat turns.
//
// It covers files, vector stores, and transcription helpers. Chat-completion
// provider behavior belongs in package provider.
package openai
