# Governor Auth — Codex Credential Sourcing, Ownership, and Fallback

## Overview

Governor authentication is separate from provider API keys.

Aphelion supports two broad governor paths:

- `native`: uses the configured inference provider and its API credentials
- `codex`: uses Codex-style governor credentials tied to ChatGPT/Codex access

This spec defines how Aphelion discovers, uses, and eventually owns Codex credentials.

## Scope

### v0 required

- detect external Codex CLI credentials
- support `governor.backend = "auto"` choosing Codex when valid credentials exist
- support falling back to native governor when Codex credentials are missing or unusable
- never require Codex for the system to run

### Deferred after v0

- Aphelion-owned Codex auth store
- Aphelion-run OAuth login flow
- token refresh and persistence independent of Codex CLI
- multiple Codex accounts or profiles

## Credential Sources

### External Codex CLI source

Aphelion should detect Codex credentials from:

- `CODEX_HOME/auth.json`
- otherwise `~/.codex/auth.json`

The minimum usable payload is:

- `tokens.access_token`
- `tokens.refresh_token`

If those are missing or malformed, the source is ignored.

### Future Aphelion-owned source

Later, Aphelion may maintain its own governor auth store separate from Codex CLI.

That is the long-term ownership path, but it is not required for v0.

## v0 Credential Strategy

For v0, Aphelion should be interoperable first.

That means:

- detect existing Codex CLI credentials
- use them for governor selection when valid
- avoid taking ownership of those credentials yet
- fall back cleanly to native governor when unavailable

This is intentionally closer to OpenClaw’s interoperability posture than to Hermes’ immediate import-into-own-store posture.

## Desired Long-Term Strategy

Longer term, Aphelion should become capable of:

- importing Codex CLI credentials into an Aphelion-owned store
- refreshing them independently
- operating without depending on Codex CLI as the sole source of truth

That is conceptually closer to Hermes.

The intended end state is:

- OpenClaw-style interoperability
- Hermes-style ownership

## Backend Resolution

### `governor.backend = "auto"`

`auto` means:

1. if valid Codex credentials are available, use `codex`
2. otherwise use `native`

### `governor.backend = "codex"`

`codex` means:

- require valid Codex credentials
- fail clearly if they are unavailable

### `governor.backend = "native"`

`native` means:

- ignore Codex credentials entirely

## Runtime Credential Shape

The governor should receive a normalized runtime auth bundle, not raw file parsing details.

Example shape:

```go
type GovernorAuth struct {
    Backend   string
    BaseURL   string
    AccessKey string
    Source    string
}
```

For Codex, the source may be:

- `codex-cli-auth-json`
- later `aphelion-auth-store`

The exact type may evolve, but the rest of the governor path should depend on a normalized bundle rather than directly reading `auth.json`.

## Expiry and Refresh

### v0

v0 may stay conservative:

- use only credentials that appear valid
- if they are missing, malformed, or expired, fall back to native in `auto`
- fail explicitly in `codex`

### Later

Later versions may:

- detect imminent expiry
- refresh access tokens
- persist refreshed tokens to the appropriate owner store

Ownership rules matter here:

- if Aphelion is only borrowing Codex CLI credentials, it should be careful about mutating that store
- once Aphelion has its own auth store, it may refresh there without depending on Codex CLI

## Security Rules

Governor auth is secret material.

The system must ensure:

- credential files are never injected into prompts
- `exec` cannot casually expose them to non-admin sessions
- `Idolum` never receives raw governor credentials
- logs do not print tokens
- malformed auth sources fail closed

See `security.md`.

## Config Surface

See `config.md`, but the intended shape includes:

```toml
[governor]
backend = "auto"                # "auto" | "codex" | "native"
native_provider = "anthropic"

[governor.codex]
auth_source = "auto"            # "auto" | "codex_cli" | "aphelion"
codex_home = ""                 # empty = CODEX_HOME or ~/.codex
base_url = "https://chatgpt.com/backend-api/codex"
```

`auth_source = "auto"` means:

- prefer Aphelion-owned credentials when that store exists
- otherwise use external Codex CLI credentials when available

For v0, only the external path is required.

## Decisions

- **Codex auth is not the same as OpenAI API-key auth.**
- **External Codex CLI credentials are a valid v0 source.**
- **Aphelion should be Codex-compatible before it is Codex-self-hosting.**
- **`auto` prefers Codex when valid credentials exist.**
- **Fallback to native is required for practicality.**
- **Governor credentials must never leak into prompts or non-admin tool surfaces.**

## Test Plan

- **TestDetectCodexCLIAuthFile**: valid `CODEX_HOME/auth.json` is detected
- **TestIgnoreMalformedCodexCLIAuthFile**: malformed or incomplete auth file is ignored
- **TestGovernorBackendAutoPrefersCodexWhenCredentialsExist**: `auto` selects Codex when valid external credentials exist
- **TestGovernorBackendAutoFallsBackNativeWhenCredentialsMissing**: `auto` selects native when Codex credentials are absent
- **TestGovernorBackendCodexFailsWithoutCredentials**: explicit Codex mode fails clearly when auth is unavailable
- **TestGovernorAuthNeverInjectedIntoPrompt**: access tokens do not appear in governor or face prompt text
