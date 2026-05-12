# Principals — Telegram Admission & Authority

_Current status:_ Telegram admin admission is config-owned. Durable-child
access remains governed through child policy.

## Overview

The principal model is deliberately small:

- one Telegram user
- talking to the bot in an admitted private chat or through a configured
  durable-child/group route
- admitted explicitly through config
- assigned a fixed role at ingress

This spec intentionally avoids a generic identity system, approval workflow, or
in-band role mutation. The point of the principal layer is only to answer:

1. is this Telegram ingress allowed?
2. if allowed, is the user the configured `admin`?

## Scope

### Current required

- Telegram-only principals
- config-owned Telegram admission
- config-owned admin principal list
- explicit ingress role: `admin`
- unknown users are denied at ingress

### Deferred or research-only

- pending approval workflows
- bans/denylist as first-class persisted state
- runtime principal mutation
- cross-transport identity
- durable principal registry in SQLite

## Principal Key

The Telegram principal key is:

- `transport = "telegram"`
- `telegram_user_id`

Use Telegram `from.id` as the identity key.

Do **not** use DM `chat_id` as the principal key, even if private chat IDs often line up with user IDs in practice. Session routing may use the DM `chat_id`, but admission is based on the Telegram user.

## Roles

Two role types exist in the runtime:

- `admin` (single global operator)
- `durable_agent` (scoped child principal created by the admin pair)

### `admin`

- may operate on the global workspace
- may mutate shared memory and persona/bootstrap files
- may receive review digests from non-admin sessions

## Config Ownership

Principals are defined in config.

```toml
[principals.telegram]
admin_user_ids = [123456789]
```

Rules:

- IDs are Telegram `from.id` values
- at least one admin must exist
- users not present as admins are denied in admin DM scope
- non-admin user access is granted per durable agent (allowlist), not through global principal lists

Changing admin admission means editing config and restarting the daemon.
Durable-agent user access is changed through durable-agent governance actions.

## Resolution

Principal resolution happens before session creation.

```go
type Principal struct {
    TelegramUserID int64
    Role           string // "admin" at DM ingress
}

func ResolveTelegramPrincipal(userID int64, cfg *Config) *Principal {
    if contains(cfg.Principals.Telegram.AdminUserIDs, userID) {
        return &Principal{TelegramUserID: userID, Role: "admin"}
    }
    return nil
}
```

If resolution returns `nil`:

- do not create or resume a session
- do not expose tools
- do not assemble prompt context
- optionally send a fixed denial notice

## Relation to Sessions

Principals and sessions are different things:

- the **principal** says whether a user may talk to the bot and with what authority
- the **session** is the DM transcript once that user is admitted

In current implementation:

- principal resolution uses Telegram `user_id`
- session identity uses the DM `chat_id`

This split is intentional. Admission is about who the human is; the session ledger is about which DM thread the bot is continuing.

## Relation to Tools and Memory

The resolved principal role controls:

- which tool definitions are exposed
- which sandbox profile is used
- which roots are writable
- whether shared/global memory is writable or read-only

This must be enforced in code and config, not only described in prompt text.

## Non-Goals

current implementation principals do **not** provide:

- a pending state
- a bot-driven approval queue
- a persisted principal database
- a generic concept of users across transports

Those can be added later if needed. They are not required for the first correct system.

## Test Plan

- **TestResolveTelegramAdminPrincipal**: configured admin `user_id` resolves as `admin`
- **TestResolveTelegramUnknownPrincipal**: unknown `user_id` resolves to nil
- **TestConfigRejectsApprovedUserIDs**: `approved_user_ids` config is rejected
- **TestConfigRequiresAtLeastOneAdmin**: at least one admin is required
- **TestIngressRejectsUnknownDMBeforeSessionCreation**: unknown Telegram DM does not create or resume a session
- **TestIngressRoutesConfiguredPrincipal**: configured Telegram principal is allowed into the DM session path
