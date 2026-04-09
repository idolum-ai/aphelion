# Principals — DM Admission & Authority

## Overview

For v0, a principal is deliberately simple:

- one Telegram user
- talking to the bot in a private chat
- admitted explicitly through config
- assigned a fixed role in config

This spec intentionally avoids a generic identity system, approval workflow, or in-band role mutation. The point of the principal layer in v0 is only to answer:

1. is this Telegram DM allowed?
2. if allowed, is the user `admin` or `approved_user`?

## Scope

### v0 required

- Telegram-only principals
- DM-only admission
- config-owned principal list
- explicit roles: `admin` and `approved_user`
- unknown users are denied at ingress

### Deferred after v0

- pending approval workflows
- bans/denylist as first-class persisted state
- runtime principal mutation
- cross-transport identity
- durable principal registry in SQLite

## Principal Key

The v0 principal key is:

- `transport = "telegram"`
- `telegram_user_id`

Use Telegram `from.id` as the identity key.

Do **not** use DM `chat_id` as the principal key, even if private chat IDs often line up with user IDs in practice. Session routing may use the DM `chat_id`, but admission is based on the Telegram user.

## Roles

Two roles exist in v0:

- `admin`
- `approved_user`

### `admin`

- may operate on the global workspace
- may mutate shared memory and persona/bootstrap files
- may receive review digests from non-admin sessions

### `approved_user`

- may use the bot in DMs
- must remain isolated from global mutable state
- may later produce bounded review digests for the admin DM

## Config Ownership

Principals are defined in config.

```toml
[principals.telegram]
admin_user_ids = [123456789]
approved_user_ids = [234567890, 345678901]
```

Rules:

- IDs are Telegram `from.id` values
- at least one admin must exist
- a user ID may appear in only one role list
- users not present in either list are denied

For v0, changing admission means editing config and restarting the daemon.

## Resolution

Principal resolution happens before session creation.

```go
type Principal struct {
    TelegramUserID int64
    Role           string // "admin" | "approved_user"
}

func ResolveTelegramPrincipal(userID int64, cfg *Config) *Principal {
    if contains(cfg.Principals.Telegram.AdminUserIDs, userID) {
        return &Principal{TelegramUserID: userID, Role: "admin"}
    }
    if contains(cfg.Principals.Telegram.ApprovedUserIDs, userID) {
        return &Principal{TelegramUserID: userID, Role: "approved_user"}
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

In v0:

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

v0 principals do **not** provide:

- a pending state
- a bot-driven approval queue
- a persisted principal database
- a generic concept of users across transports

Those can be added later if needed. They are not required for the first correct system.

## Test Plan

- **TestResolveTelegramAdminPrincipal**: configured admin `user_id` resolves as `admin`
- **TestResolveTelegramApprovedPrincipal**: configured approved `user_id` resolves as `approved_user`
- **TestResolveTelegramUnknownPrincipal**: unknown `user_id` resolves to nil
- **TestConfigRejectsDuplicatePrincipalRoleAssignment**: same `user_id` cannot appear in both role lists
- **TestIngressRejectsUnknownDMBeforeSessionCreation**: unknown Telegram DM does not create or resume a session
- **TestIngressRoutesConfiguredPrincipal**: configured Telegram principal is allowed into the DM session path
