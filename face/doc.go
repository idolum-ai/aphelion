//go:build linux

// Package face owns operator-facing text renderers.
//
// Face code renders typed runtime/session/core records into Telegram and CLI
// presentation. It should not become an authority source; rendered text is a
// projection of records owned elsewhere.
package face
