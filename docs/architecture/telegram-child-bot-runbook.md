# Telegram Child Bot Runbook

This runbook covers the generic Telegram child-bot runner. A child bot is a
narrow runtime lane for one durable child agent to receive Telegram group
traffic without starting the parent service loops.

## Boundary

- The command is `telegram-child-bot`.
- The runner is generic but narrow: one durable agent, one Telegram bot token
  file, one configured chat route.
- Token presence is not authority. Message handling still depends on the durable
  agent policy, child-local bootstrap, grants, review targets, and configured
  route.
- Preflight and status checks use metadata where possible and must not print bot
  tokens.

## Checks

Use these checks before starting a child bot service:

```bash
aphelion telegram-child-bot --config ~/.aphelion/aphelion.toml --agent sample-child --status
aphelion telegram-child-bot --config ~/.aphelion/aphelion.toml --agent sample-child --preflight
aphelion telegram-child-bot --config ~/.aphelion/aphelion.toml --agent sample-child --get-me-smoke
aphelion telegram-child-bot --config ~/.aphelion/aphelion.toml --agent sample-child --dry-start
```

## Service Shape

Run the child bot as a separate user service from the parent Aphelion service.
The child runner must not start parent-only loops such as admin commands,
heartbeat, cron, startup recovery, durable wake loops, or tailnet parent
servers.
