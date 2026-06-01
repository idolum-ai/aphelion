//go:build linux

// Package githubapp is Aphelion's GitHub App credential membrane.
//
// It owns local key parsing, JWT signing, installation-token minting,
// repository/permission scope validation, and token redaction. It does not own
// PR workflows, git authority, tool invocation, runtime orchestration, session
// state, or Telegram presentation.
//
// Credential availability is not authority: callers must keep token material
// inside an active bounded grant/lease and should avoid printing tokens unless
// an explicit operator command requested that output.
package githubapp
