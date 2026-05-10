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
  - Starts with a collapsed `Quick Read:` summary plus a `Read More` button.
  - `Read More` expands in place to the full detailed debug snapshot for the current chat.
  - Admin users get system and durable-agent sections in the expanded view.
- `/agents`
  - Admin-only durable-agent launcher.
  - Lists durable agents with inline `Start Chat` buttons.
  - Starts a background parent-child conversation kickoff for the selected durable agent.
- `/memory`
  - Opens memory review with inline controls across session history and semantic memory views.
  - Lets the user set an active memory focus from a candidate item (`Focus 1/2/3`).
  - Active focus is injected as bounded turn context on subsequent non-command chat messages until cleared.
- `/mission`
  - Shows the current working objective and the caller-owned Mission Ledger entries.
  - Supports manual `list`, `show`, `create`, `pin`, `unpin`, `activate`, `pause`, `block`, `complete`, `archive`, `summon`, and admin `health` actions.
  - Self-summon is review-only; Mission Ledger state does not grant self-continuation or new capabilities.
- `/autonomy`
  - Admin-only policy report and live override control.
  - Shows the configured default mode, ceiling, live-override setting, maximum override duration, and active override state.
  - Supports `status`, `off`, and `leased <duration> [all|workspace|deploy] [uses=N] [reason]`.
  - `leased` uses the same bounded approval-lease substrate as `/autoapprove` and cannot exceed the configured ceiling or maximum duration.
  - If config is tightened later, old live overrides are ignored and `/doctor` reports the precedence block.
- `/autoapprove`
  - Admin-only short lease for eligible approval prompts.
  - Also obeys the configured autonomy ceiling and maximum live-override duration.
- `/stop`
  - Stops active work in the current chat and drops queued follow-up work.
  - When `memory.aggressive.flush_on_session_boundary` is enabled, it also runs a bounded memory flush first.
- `/new`
  - Starts a fresh chat session context (same chat), preserving memories.
  - When `memory.aggressive.flush_on_session_boundary` is enabled, it flushes recent session context before resetting.
- `/detach`
  - Stops active work, clears queued work, revokes continuation, and detaches pending decisions owned by this chat+sender.
- `/restart`
  - Admin-only forced gateway restart.
  - When `memory.aggressive.flush_on_session_boundary` is enabled, it flushes recent session context before restart.
- `/reinstall`
  - Queues a rebuild/reinstall/restart request as normal routed work.
- `/set_persona_model`
  - Opens inline model selector buttons for Idolum.
- `/set_governor_effort`
  - Opens inline effort selector buttons for system reasoning.

Visibility notes:

- `/start` and `/help` are role-aware.
  - Admin users see `/autonomy`, `/autoapprove`, and `/restart`.
  - Non-admin users do not see those admin commands.

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

- `Quick Read:` one-line human summary (Haiku-backed when a native provider key is configured), prepended ahead of the status block.
- `Quick Read:` is grounded against the rendered status tokens; contradictory generated summaries are replaced with deterministic snapshot text.
- Telemetry labels are rendered as human-readable labels with colons (including snake_case and known status/debug lead labels; for example, `status_scope=chat` appears as `Status Scope: chat`, and `summary state=idle` appears as `Summary: State: idle`).
- Bracketed machine envelopes are humanized in Telegram-facing status/debug output (for example, `[PLAN_UPDATED]` renders as `Plan Updated:` and closing tags are removed).
- `turn_phase` for active in-flight stage (`face_proposal`, `brokerage`, `governor`, `render`, `persist`, `deliver`) when available.
- `operation` and `plan_step` from persisted session sidecars.
- `plan_progress` with completed/total steps and `fully_executed=true|false`.
- `hidden_inputs` categories plus provenance summary carried in floor metadata.
- `delivery` state that distinguishes in-flight, delivered, persisted-without-delivery, and delivery-failure paths.
- `detached_work` counters for pending decisions/continuations/recovery/stale-turn work.
- `current_signal` as a compact one-line machine signal (phase/tool/queue/blocked source).

Durables status (`Durables` button, admin-only):

- `Status Scope: durables` with aggregate counts (`total`, `active`, `dormant`, `degraded`, `inactive`).
- Per-agent health cards with:
  - identity and topology (`agent_id`, `channel`, `status`, `health`, `review_chat`)
  - policy posture (`policy_version`, `policy_hash`, `outbound`, `drift`, `capabilities`)
  - delegation posture (`capability_request` and `capability_grant` status when delegated permissions are active)
  - runtime pulse (`last_wake`, `last_review`, `dormant_at`, apply status/error)
  - remote enrollment pulse (`enrollment status`, `last_seen`, `last_seq`, revocation state)

### `/debug` content signals

`/debug` starts as a collapsed command reply with `Quick Read:`, then expands via `Read More`.
It is intended for operational diagnosis when `/status` is too compressed.

- prepends `Quick Read:` summary when the readable-summary provider is available
- includes the full chat status block (`Status Scope: chat`)
- adds `Debug Chat:` detail lines with latest turn internals:
  - `latest_request`
  - `last_tool_preview`
  - decoded `last_exec_command` when available
  - `last_tool_result`, `last_tool_error`, `turn_error`
- admin users additionally receive:
  - full `Status Scope: system`
  - `Debug System:` (pending-kind counters + latest turn rollups per chat)
  - full `Status Scope: durables`
- output is chunked when needed to fit Telegram message size limits

Review digest deliveries to admin chat are rendered with labeled metadata lines (`Source Chat:`, `Source User:`, `Source Role:`, optional scope/agent lines) plus a `Summary:` section.

### Natural-language durable setup trigger

For admin users, natural language requests to create a durable child are auto-normalized into a safe wizard-driving instruction before the turn reaches the model.

Examples that should trigger:

- “Create a durable child agent”
- “Create a durable external-channel agent”
- “I want to give you your own external channel address”

Behavior:

- rewrite favors `durable_agent` wizard actions
- explicitly blocks `exec`/`go run` style paths for this workflow
- tells the assistant to ask one concise question at a time for missing wizard fields
- preserves the original user sentence in the rewritten instruction
- if an external channel address is present in the user text, it is passed as known context for the external channel adapter profile

### Durable wizard inline controls

When a response contains a machine-readable durable-wizard card (`action: durable-agent wizard show`), Telegram auto-attaches inline buttons for the active step.

Step answer buttons are predefined for structured fields such as:

- bootstrap profile (`inherit_parent` vs `child_custom`)
- bootstrap model pin (shown when `child_custom` is selected)
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

Bootstrap nuance:

- when the effective bootstrap backend is `codex`, bootstrap profile controls collapse to `Inherit parent` only and no `bootstrap_model` pin step is shown

### Durable child relay syntax

Telegram DM can route a single message directly to an active durable Telegram child without a slash command:

- `agent:<agent_id> <message>`

Examples:

- `agent:ops-child summarize today’s incidents`
- `agent:ops-child should we escalate this to review?`

Behavior:

- bypasses normal slash-command handling for that message
- routes the turn as `durable_agent` scoped execution
- delivers the child reply in the same chat when channel policy allows local reply
- sender must still be authorized by the child (`allowed_telegram_user_ids` or admin role)

### `/set_persona_model` selector

Buttons are generated from runtime options:

- `Sonnet 4.6` (`claude-sonnet-4-6`)
- `Opus 4.6` (`claude-opus-4-6`)
- `Opus 4.7` (`claude-opus-4-7`)
- `GPT-5.5` (`gpt-5.5`)

The currently active option is prefixed with `•`.

### `/set_governor_effort` selector

Buttons are generated from runtime options:

- `LOW`
- `MEDIUM`
- `HIGH`
- `XHIGH`

The currently active option is prefixed with `•`.

### `/memory` review controls

- Source selectors:
  - `Session`
  - `Semantic Shared`
  - `Semantic Local`
- Candidate selectors:
  - `Focus 1`
  - `Focus 2`
  - `Focus 3`
- Control row:
  - `Clear Focus`
  - `Refresh`

Behavior:

- panel includes:
  - source id
  - query seed
  - active focus summary (or `none`)
  - candidate items with labels and excerpts
- focus applies to subsequent non-command inbound messages by prepending a machine-only `MEMORY_FOCUS_CONTEXT` block.
- slash commands and durable relay payloads are not rewritten by memory-focus injection.

### Continuation approval prompt

When a turn offers continuation approval, an inline prompt is shown with:

- `Start`
- `Details`
- `Change`
- `Pause`
- `Stop`

Telegram button labels stay short because the chat surface is narrow. Scope,
phase names, and stop conditions belong in the prompt body, not in button text.

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
- Prompt delivery is TES-grounded: the displayed continuation prompt must match a live continuation decision event (`continuation.offered`) for the same `decision_id`; otherwise prompt text falls back to deterministic copy.
- When handshake fails, continuation state is persisted as idle with an explicit blocked reason and a first-person blocked notice is sent in chat (persona-rendered with deterministic fallback).
- Deploy/restart work is not bundled into ordinary development approvals. A
  deploy prompt must ask for a fresh standalone lease whose body names commit,
  build, install, restart, and post-restart verification.

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
- Durable-agent launcher callbacks are admin-only and run-id agnostic:
  - `Start Chat: <agent_id>` triggers a background durable `conversation_send` kickoff.
  - `Refresh` reloads the durable-agent list in place.

## Operational UI Signals

- Typing indicator is emitted while active work is running in chats that support local reply delivery.
- Tool/progress updates are emitted as a single live `Thinking...` card per turn.
- Restart and detach actions return explicit user-visible summaries.
