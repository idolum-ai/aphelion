# Telegram UI Features

This document is the user-facing Telegram interface inventory for Aphelion.

## Slash Commands

Current command surface:

- `/start`
  - Shows a short intro plus the command list.
- `/help`
  - Shows command help.
- `/status`
  - Opens status output with inline status controls (no command arguments).
- `/stop`
  - Stops active work in the current chat and drops queued follow-up work.
- `/detach`
  - Stops active work, clears queued work, revokes continuation, and detaches pending decisions owned by this chat+sender.
- `/restart`
  - Admin-only forced gateway restart.
- `/reinstall`
  - Queues a rebuild/reinstall/restart request as normal routed work.
- `/set_persona_model`
  - Opens inline model selector buttons for Idolum.
- `/set_governor_effort`
  - Opens inline effort selector buttons for governor reasoning.

## Inline Buttons

### Design language

Binary decision prompts follow one consistent side rule:

- Left button: stop/deny/reject (negative or safer action)
- Right button: continue/approve/allow (affirmative action)

Non-binary selectors (for example `/status` navigation and model/effort pickers) are ordered by navigation intent or option list order, not by positive/negative polarity.

### `/status` controls

Always visible:

- `This Chat`
- `Pending Only`
- `Refresh`

Admin-only:

- `System Overview`
- `Hot Chats`
- `Find Chat`

`Find Chat` drill-down:

- `Chat <chat_id>` buttons for recent active/pending chats (up to 12 chats shown).

### `/status` content signals

Chat-scoped status now reports live work telemetry, not only router occupancy:

- `quick_read` one-line human summary (Haiku-backed when a native provider key is configured), prepended ahead of the raw status block.
- `turn_phase` for active in-flight stage (`face_proposal`, `brokerage`, `governor`, `render`, `persist`, `deliver`) when available.
- `operation` and `plan_step` from persisted session sidecars.
- `plan_progress` with completed/total steps and `fully_executed=true|false`.
- `hidden_inputs` categories plus provenance summary carried in floor metadata.
- `delivery` state that distinguishes in-flight, delivered, persisted-without-delivery, and delivery-failure paths.
- `detached_work` counters for pending decisions/continuations/recovery/stale-turn work.
- `current_signal` as a compact one-line machine signal (phase/tool/queue/blocked source).

### `/set_persona_model` selector

Buttons are generated from runtime options:

- `Sonnet 4.6` (`claude-sonnet-4-6`)
- `Opus 4.6` (`claude-opus-4-6`)
- `Opus 4.7` (`claude-opus-4-7`)

The currently active option is prefixed with `•`.

### `/set_governor_effort` selector

Buttons are generated from runtime options:

- `LOW`
- `MEDIUM`
- `HIGH`
- `XHIGH`

The currently active option is prefixed with `•`.

### Continuation approval prompt

When a turn offers continuation approval, an inline prompt is shown with:

- `Stop`
- `Continue`

### Runtime decision prompts

Decision prompts are shown with inline buttons. Depending on context, users can see:

- Busy interruption:
  - `🛑 Stop & reassess`
  - `⏳ Let it finish`
- Stop-word confirmation:
  - `Yes, stop`
  - `No, keep going`
- Proposal approval:
  - `Deny`
  - `Approve`
  - plus optional `Expand details` when summarized details are available.
- Artifact retention:
  - `This turn only`
  - `Keep for session`
  - `Save locally`

## Callback Behavior

- Status and selector callbacks edit the same Telegram message in place when possible.
- Status output can be chunked; extra chunks are sent as follow-up messages.
- Stale callback actions are acknowledged with a stale-message notice instead of applying old state.
- Non-admin access to admin-only status views is denied via callback acknowledgement.

## Operational UI Signals

- Typing indicator is emitted while active work is running in chats that support local reply delivery.
- Tool/progress updates are shown as concise status text in the chat.
- Restart and detach actions return explicit user-visible summaries.
