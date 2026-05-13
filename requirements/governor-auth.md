# Governor Auth — Codex Credential Sourcing, Ownership, and Fallback

## Overview

Governor authentication is separate from provider API keys.

Aphelion supports two broad governor paths:

- `native`: uses the configured inference provider and its API credentials
- `codex`: uses Codex-style governor credentials tied to ChatGPT/Codex access

This spec defines how Aphelion discovers, uses, and eventually owns Codex credentials.

## Scope

### Required

- detect external Codex CLI credentials
- support `governor.backend = "auto"` choosing Codex when valid credentials exist
- support falling back to native governor when Codex credentials are missing or unusable
- never require Codex for the system to run

### Deferred

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

### Aphelion-owned source

Aphelion now supports its own Codex auth store separate from Codex CLI.

This gives the runtime a stable, refreshable credential home while still allowing external Codex CLI credentials when they are the active source.

## Credential Strategy

Aphelion should support both:

- Codex CLI interoperability
- Aphelion-owned Codex credential persistence

That means:

- detect existing Codex CLI credentials
- use them for governor selection when valid
- support an Aphelion-owned auth file as an explicit source of truth
- refresh and persist tokens to the active owner store
- fall back cleanly to native governor when Codex credentials are unavailable

The intended posture is:

- OpenClaw-style interoperability
- Hermes-style ownership and refresh resilience

The attribution and departure record for these references lives in
[`docs/architecture/influences-and-departures.md`](../docs/architecture/influences-and-departures.md).

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
    AccountID string
    Source    string
}
```

For Codex, the source may be:

- `codex-cli-auth-json`
- `aphelion-auth-json`

The exact type may evolve, but the rest of the governor path should depend on a normalized bundle rather than directly reading `auth.json`.

For Codex CLI auth, the normalized bundle should carry:

- access token
- refresh token
- ChatGPT account id

If `account_id` is missing, Codex auth should be treated as incomplete and should not be selected in `auto`.

## Expiry and Refresh

The runtime should:

- use credentials that appear valid
- if they are missing, malformed, or expired, fall back to native in `auto`
- fail explicitly in `codex`
- refresh access tokens when needed
- persist refreshed tokens to the active owner store
- resync from the active auth file before requests so externally rotated tokens do not strand the process

Ownership rules still matter:

- if Aphelion is borrowing Codex CLI credentials, it should treat that file as the active owner store for refresh persistence
- if Aphelion is using its own auth store, it should refresh there without depending on Codex CLI as the sole source of truth

## Security Rules

Governor auth is secret material.

The system must ensure:

- credential files are never injected into prompts
- `exec` cannot casually expose them to non-admin sessions
- `Idolum` never receives raw governor credentials
- logs do not print tokens
- malformed auth sources fail closed

See `security.md`.

## Durable Child Bootstrap

Durable children that use `codex` should follow the same auth law, but through child-local bootstrap rather than parent governor config.

That means:

- a child may use `codex` as its own node backend
- the child should resolve Codex auth from its own configured `codex_home` / auth source
- the child must not inherit the parent's Codex credentials merely because it runs on the same host
- parent-authored live policy may choose within the child bootstrap ceiling, but it must not inject new Codex secrets into live policy

This keeps Codex auth ownership aligned with the durable-agent bootstrap law: secret-bearing bootstrap is local, policy is downward and non-secret.

## Config Surface

See `config.md`, but the intended shape includes:

```toml
[governor]
backend = "auto"                # "auto" | "codex" | "native"
native_provider = ""            # Empty lets providers.selection choose from configured providers.

[governor.codex]
auth_source = "auto"            # "auto" | "codex_cli" | "aphelion"
auth_path = ""                  # Empty = ~/.aphelion/state/codex-auth.json
codex_home = ""                 # empty = CODEX_HOME or ~/.codex
base_url = "https://chatgpt.com/backend-api"
model = "gpt-5.5"
store_responses = true            # Auto-falls back to store=false when the Codex endpoint rejects stored responses.
max_continuations = 3
transport_retries = 1
```

`auth_source = "auto"` means:

- prefer Aphelion-owned credentials when that store exists
- otherwise use external Codex CLI credentials when available

- `auth_source = "aphelion"` means use Aphelion-owned credential persistence explicitly
- `auth_source = "codex_cli"` means use the external Codex CLI store explicitly

## Decisions

- **Codex auth is not the same as OpenAI API-key auth.**
- **External Codex CLI credentials are a valid current implementation source.**
- **Aphelion should interoperate with Codex before it is Codex-self-hosting.**
- **Aphelion may now own Codex auth while still accepting the Codex CLI credential source.**
- **`auto` prefers Codex when valid credentials exist.**
- **Fallback to native is required for practicality.**
- **Governor credentials must never leak into prompts or non-admin tool surfaces.**

## Test Plan

- **TestDetectCodexCLIAuthFile**: valid `CODEX_HOME/auth.json` is detected
- **TestIgnoreMalformedCodexCLIAuthFile**: malformed or incomplete auth file is ignored
- **TestGovernorBackendAutoPrefersCodexWhenCredentialsExist**: `auto` selects Codex when valid external credentials exist
- **TestGovernorBackendAutoPrefersAphelionAuthStoreBeforeCLI**: `auto` prefers the Aphelion-owned auth store when both sources exist
- **TestGovernorBackendAutoFallsBackNativeWhenCredentialsMissing**: `auto` selects native when Codex credentials are absent
- **TestGovernorBackendCodexFailsWithoutCredentials**: explicit Codex mode fails clearly when auth is unavailable
- **TestGovernorBackendCodexLoadsAphelionAuthStore**: explicit Aphelion auth source resolves a stored credential bundle
- **TestGovernorAuthNeverInjectedIntoPrompt**: access tokens do not appear in governor or face prompt text
