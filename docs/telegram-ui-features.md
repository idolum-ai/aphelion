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
- `/debug`
  - Starts with a collapsed `quick_read` summary plus a `Read More` button.
  - `Read More` expands in place to the full detailed debug snapshot for the current chat.
  - Admin users get system and durable-agent sections in the expanded view.
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

### `/debug` content signals

`/debug` starts as a collapsed command reply with `quick_read`, then expands via `Read More`.
It is intended for operational diagnosis when `/status` is too compressed.

- prepends `quick_read` summary when the readable-summary provider is available
- includes the full chat status block (`status_scope=chat`)
- adds `debug_chat` detail lines with latest turn internals:
  - `latest_request`
  - `last_tool_preview`
  - decoded `last_exec_command` when available
  - `last_tool_result`, `last_tool_error`, `turn_error`
- admin users additionally receive:
  - full `status_scope=system`
  - `debug_system` (pending-kind counters + latest turn rollups per chat)
  - full `status_scope=durables`
- output is chunked when needed to fit Telegram message size limits

### Natural-language durable setup trigger

For admin users, natural language requests to create an email durable child are auto-normalized into a safe wizard-driving instruction before the turn reaches the model.

Examples that should trigger:

- “Create a durable email agent”
- “I want to give you your own email address”

Behavior:

- rewrite favors `durable_agent` wizard actions
- explicitly blocks `exec`/`go run` style paths for this workflow
- tells the assistant to ask one concise question at a time for missing wizard fields
- preserves the original user sentence in the rewritten instruction
- if an email address is present in the user text, it is passed as known wizard context

### Durable wizard inline controls

When a response contains a machine-readable email-wizard card (`action: durable-agent wizard show`), Telegram auto-attaches inline buttons for the active step.

Step answer buttons are predefined for structured fields such as:

- autonomy mode
- wakeup mode
- summarize PDFs yes/no
- cadence and poll-interval presets
- charter/capability/retention presets

Control row layout follows the same left/right language used elsewhere:

- in-progress wizard: `Cancel` (left) and `Refresh` (right)
- ready wizard: `Cancel` (left) and `Finalize` (right)

Callback behavior:

- buttons are admin-only
- stale/mismatched callbacks are acknowledged and ignored
- valid callbacks run deterministic `durable_agent` wizard actions (`wizard_answer`, `wizard_show`, `wizard_finalize`, `wizard_cancel`) and edit the same message in place

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
  - `Reassess`
  - `Stop`
- `Reassess` stops active work, clears queue, revokes continuation, and detaches sender-owned pending decisions.
- `Stop` stops active work for the chat and revokes continuation.
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
