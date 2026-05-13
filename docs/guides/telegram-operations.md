# Telegram Operations

Telegram is the radio link to Aphelion. Use it for live work, approvals,
status, recovery, and evidence. Use the CLI for install, local service control,
and deeper repair.

The full command and button reference is
[Telegram UI Features](../telegram-ui-features.md).

## Start

```text
/start
/help
/health
/status
```

`/help` shows the current command menu. `/health` gives the compact system
panel. `/status` shows chat state, pending work, active runs, and admin status
views.

## Inspect Health

Use `/health` first when you need to know whether Aphelion is ready.

Admin health controls include status, trace, diagnosis, service restart, and
reinstall panels. `/health trace` starts with a compact quick read and can expand
into deeper evidence. `/health diagnose` runs read-only diagnosis from a private
admin chat.

Use `/status` when the question is about active work, pending approvals,
durable-agent state, or a specific chat.

## Stop Or Reset A Chat

```text
/stop
/new
/detach
```

`/stop` cancels current work and clears queued follow-up messages for the chat.
`/new` starts a fresh chat session without clearing memory. `/detach` clears
active, queued, continuation, and approval state for you in the chat.

If the service itself needs attention, use `/health` first so the next action is
grounded in current state.

## Grant Bounded Automation

```text
/auto
```

Use `/auto` for automation policy and approval controls. The panels are
button-driven, so command parameters are optional. Keep automation bounded by
duration, scope, use count, and reason.

`/auto policy` shows the configured default, ceiling, live override setting, and
active override state. `/auto approvals` shows the current bounded
approval-prompt grant.

## Manage Work Surfaces

Use `/agents` to inspect durable agents and open chat controls.

Use `/memory` to review curated memory, inspect suggestions, and approve or
reject changes.

Use `/mission` to review objectives and mission candidates. Mission state
preserves intent and review material; it does not grant new authority on its
own.

Use `/tailnet` to inspect declared Tailnet surfaces and grant bindings.

## Service Actions

Admin service actions are exposed through `/health`, `/restart`, and
`/reinstall`.

`/restart` restarts the gateway and parks active work for startup recovery.
`/reinstall` queues a rebuild, install, restart, and post-restart verification
request as normal routed work.

After either path, check `/health` and `/status`.

## Model Controls

Use `/model` to inspect and change model routing through the admin-only model
slot controls when the configured provider surface allows it.

## Read Evidence

Operator panels should show current status, why it matters, the next action,
and labeled details. Raw IDs and deeper traces belong in trace output, logs, or
machine-readable mirrors.

When a panel and a trace disagree, prefer the canonical records named by the
trace and then check `/health diagnose`.
