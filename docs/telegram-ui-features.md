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

Visibility notes:

- `/start` and `/help` are role-aware.
  - Admin users see `/restart`.
  - Non-admin users do not see `/restart`.

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
- `Durables`

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

Durables status (`Durables` button, admin-only):

- `status_scope=durables` with aggregate counts (`total`, `active`, `dormant`, `degraded`, `inactive`).
- Per-agent health cards with:
  - identity and topology (`agent_id`, `channel`, `status`, `health`, `review_chat`)
  - policy posture (`policy_version`, `policy_hash`, `outbound`, `drift`, `capabilities`)
  - runtime pulse (`last_wake`, `last_review`, `dormant_at`, apply status/error)
  - remote enrollment pulse (`enrollment status`, `last_seen`, `last_seq`, revocation state)

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

Offer conditions:

- Persona proposal note must include explicit continuation contract fields:
  - `CONTINUATION_SCHEMA_VERSION: 1`
  - `CONTINUATION_INTENT: continue|hold|stop`
  - `CONTINUATION_RATIONALE: ...`
  - `CONTINUATION_NEXT_STEP: ...`
  - `CONTINUATION_CONFIDENCE: low|medium|high`
- Governor ratification artifact must include explicit continuation contract fields:
  - `CONTINUATION_SCHEMA_VERSION: 1`
  - `CONTINUATION_INTENT: continue|hold|stop`
  - `CONTINUATION_RATIONALE: ...`
  - `CONTINUATION_RATIFIED: yes|no`
  - `CONTINUATION_NEXT_STEP: ...`
  - `CONTINUATION_CONSTRAINTS: ...`
  - `CONTINUATION_CONFIDENCE: low|medium|high`
- Prompt is shown only when both intents are `continue`, both rationales are non-empty, and governor is ratified.
- Prompt text is rendered as one first-person system voice (Haiku/face render when available, deterministic fallback otherwise), not as a split `Persona`/`Governor` dialogue block.
- When handshake fails, continuation state is persisted as idle with an explicit blocked reason and a first-person blocked notice is sent in chat (persona-rendered with deterministic fallback).

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

### Deliberation thinking card controls

When a turn enters long-running deliberation/tool execution, Telegram shows one auto-updating card:

- Header starts with `Thinking...` and includes elapsed time while active.
- Card includes inline controls:
  - `Stop & Clean Up`
  - `Stop Turn`
- `Stop & Clean Up` stops active work, clears queue, revokes continuation, and detaches sender-owned pending decisions.
- `Stop Turn` stops active work for the chat and revokes continuation.
- When deliberation ends, controls are removed from the card (or the card is deleted when `telegram.tool_progress_cleanup=true`).

## Callback Behavior

- Status and selector callbacks edit the same Telegram message in place when possible.
- Status output can be chunked; extra chunks are sent as follow-up messages.
- Stale callback actions are acknowledged with a stale-message notice instead of applying old state.
- Non-admin access to admin-only status views is denied via callback acknowledgement.
- Deliberation control callbacks are run-id scoped; stale controls are ignored with a stale notice.

## Operational UI Signals

- Typing indicator is emitted while active work is running in chats that support local reply delivery.
- Tool/progress updates are emitted as a single live `Thinking...` card per turn.
- Restart and detach actions return explicit user-visible summaries.
