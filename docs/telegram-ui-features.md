# Telegram UI Features

This document is the user-facing Telegram interface inventory for Aphelion.

## Operator Presentation Contract

Default human-facing panels use the same shape across Telegram and CLI:

- title
- current status
- why the state matters
- next action
- details and labeled evidence

Raw `key=value` telemetry, long IDs, hashes, and enum-heavy records belong in
`/health trace`, explicit evidence sections, logs, or machine-readable mirrors. Text and
buttons are projections only; authority still lives in typed leases, grants,
decisions, and TES records.

Telegram renders most operator panels in compact form by default: status, why,
next action, and a bounded set of details/evidence. Full CLI output, `/health trace`,
logs, and machine-readable mirrors carry the longer diagnostic record.

## Slash Commands

Current command surface:

- `/start`
  - Shows a grouped, role-aware intro plus command sections and a no-argument command menu.
- `/help`
  - Shows grouped, role-aware command help and a no-argument command menu.
- `/status`
  - Opens status output with inline status controls (no command arguments).
  - When explicitly targeted with `(thread N) /status` or by replying to a side-thread message, reports that side thread's session state.
- `/health`
  - Opens status, trace, and diagnosis controls without requiring arguments.
  - `/health status` opens the live status view.
  - `/health trace` starts with a collapsed `Quick Read:` summary plus a `Read More` button.
  - `Read More` expands in place to the full trace snapshot for the current chat.
  - `/health diagnose` queues a read-only admin diagnosis from a private admin chat.
  - Diagnosis launched from a button is durable callback work: the callback is terminally recorded and the diagnosis request is queued on its own replay surface.
  - Admin users get system and durable-agent sections in the expanded trace view.
- `/agents`
  - Admin-only durable-agent launcher.
  - Lists durable agents with compact health cards and inline `Chat` buttons.
  - Starts a background parent-child conversation kickoff for the selected durable agent.
- `/memory`
  - Opens memory review with current focus, candidate count, source evidence, and inline controls across session history and semantic memory views.
  - Lets the user set an active memory focus from a candidate item (`Focus 1/2/3`).
  - Active focus is stored per session lane and injected as bounded turn context on subsequent non-command messages in that lane until cleared.
- `/thread`
  - Creates an empty per-chat side thread and shows a compact guide when called without arguments.
  - Starts a side thread and routes the first turn from `/thread <message>`.
  - Side-thread replies and progress messages are visibly prefixed as `(thread N)`.
  - Replies to known side-thread messages route back into that thread.
  - Thread identity is stored as a typed session scope; the prefix is presentation, not transcript text.
- `/threads`
  - Shows open and recently closed side threads for the chat.
  - Provides `Absorb N` buttons for open threads.
  - Provides a `Summarize` button that queues ordinary main-chat work to produce one short status across open threads.
- `/absorb`
  - Closes a side thread with `/absorb N` and appends a compact bookkeeping note to the main chat session.
  - Stops the target side-thread lane before closing so active or queued side-thread work cannot append after absorb.
  - Absorb does not write curated memory by itself; `/memory` remains the review surface for durable memory changes.
- `/tailnet`
  - Admin-only Tailnet declaration, private-surface, grant-binding, drift, and rollback evidence.
  - Shows local registry readiness, private parent status, durable-child control-plane evidence, and issue evidence without mutating live Tailscale policy.
  - Provides button navigation for status, surfaces, grants, refresh, private status URL, and per-surface local revoke confirmation.
- `/mission`
  - Shows the current working objective and the caller-owned Mission Ledger entries.
  - Provides buttons for home/list, show, propose, pin/unpin, activate, pause, complete, archive, refresh, and admin health.
  - Supports manual `create`, `block`, and `summon` actions when typed input is the natural carrier for the new objective or reason.
  - Self-summon is review-only; Mission Ledger state does not grant self-continuation, autonomous continuation, new capabilities, or external authority.
- Approval windows
  - Admin-only inline controls shown after an approval succeeds.
  - The approved message offers `Approve 15m` and `Close`.
  - `Approve 15m` opens the temporary automation gate and matching approval grant together for the current chat or side thread.
  - Active windows offer `Double time` and `Cancel approvals`.
  - Each `Double time` press doubles the current window duration within the configured live-override ceiling. `Cancel approvals` revokes both records.
  - If config is tightened later, live mode overrides outside the new ceiling are ignored and `/health diagnose` reports the precedence block.
- `/stop`
  - Stops active work in the current chat and drops queued follow-up work.
  - When explicitly targeted with `(thread N) /stop` or by replying to a side-thread message, stops only that side-thread lane.
  - When `memory.aggressive.flush_on_session_boundary` is enabled, it also runs a bounded memory flush first.
- `/new`
  - Starts a fresh chat session context (same chat), preserving memories.
  - When explicitly targeted with `(thread N) /new` or by replying to a side-thread message, resets only that side-thread session context.
  - When `memory.aggressive.flush_on_session_boundary` is enabled, it flushes recent session context before resetting.
- `/detach`
  - Stops active work, clears queued work, revokes continuation, and detaches pending decisions owned by this chat+sender.
  - When explicitly targeted with `(thread N) /detach` or by replying to a side-thread message, detaches only that side-thread lane.
- `/restart`
  - Admin-only forced gateway restart.
  - When `memory.aggressive.flush_on_session_boundary` is enabled, it flushes recent session context before restart.
- `/reinstall`
  - Queues a rebuild/reinstall/restart request as normal routed work after marking the Telegram ingress row queued.

Visibility notes:

- `/start` and `/help` are role-aware.
  - Admin users see `/restart`.
  - Non-admin users do not see those admin commands.
- All listed slash commands are usable without typing parameters. When a command
  has a safe finite option set, Telegram presents buttons. Free-form creation or
  reason text remains typed input.
- Ordinary replies route to the thread of the replied-to message when Aphelion
  has durable ingress, outbound, progress-card, thread-guide, or thread-created
  evidence for that message. Unknown replies route to the main chat session.
- A reply that begins with `(thread N)` routes to that open side thread and
  stores the stripped message text in that thread session.
- Operator/global commands keep their global command meaning: `/health`,
  `/tailnet`, `/model`, `/agents`, `/thread`, `/threads`, `/absorb`,
  `/restart`, `/reinstall`, and mission/durable-agent controls are not
  side-thread work-lane commands. Approval-window callbacks use the scope of the
  approval message they are attached to.
- Work-lane commands can be explicitly scoped to a side thread: `/status`,
  `/memory`, `/stop`, `/new`, and `/detach` target a side thread when the
  command is written after `(thread N)` or sent as a reply to a known
  side-thread message. Bare commands still target the main chat-level view.

## Inline Buttons

### Design language

Binary decision prompts follow one consistent side rule:

- Left button: stop/deny/reject (negative or safer action)
- Right button: continue/approve/allow (affirmative action)

Non-binary selectors (for example `/status` navigation and model controls) are ordered by navigation intent or option list order, not by positive/negative polarity.

Inline button labels are delivery-validated at the Telegram client boundary:
labels must be non-empty and use at most two words. Longer explanations belong
in the prompt body.

### Command menu

`/start` and `/help` attach role-scoped command buttons. Public buttons include
status, health, memory, mission, threads, stop, new, and detach. Admin buttons
add models, agents, tailnet, reinstall, and restart.

Menu callbacks route through the same command dispatcher as typed slash commands;
the button is not a new authority path.

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
- Telemetry labels are rendered as human-readable labels with colons inside trace/evidence contexts. Operator `/status` panels use direct titles such as `Chat Status`, `System Status`, and `Durable Agents` instead of surfacing raw status-scope markers.
- Bracketed machine envelopes are humanized in Telegram-facing status/trace output (for example, `[PLAN_UPDATED]` renders as `Plan Updated:` and closing tags are removed).
- `turn_phase` for active in-flight stage (`face_proposal`, `brokerage`, `governor`, `render`, `persist`, `deliver`) when available.
- `operation` and `plan_step` from persisted session sidecars.
- `plan_progress` with completed/total steps and `fully_executed=true|false`.
- `hidden_inputs` categories plus provenance summary carried in floor metadata.
- `delivery` state that distinguishes in-flight, delivered, persisted-without-delivery, and delivery-failure paths.
- `detached_work` counters for pending decisions/continuations/recovery/stale-turn work.
- `provider_health` on system health/status views, summarizing recent provider
  failures, retries, failovers, successes, and the latest failure reason.
- `sandbox_readiness` warnings when an execution profile cannot currently enforce its configured isolation or network policy.
- `watchdog` recovery state. Stale-turn recovery interrupts the exact stale
  turn rows and matching Telegram ingress rows before surfacing
  `watchdog.recovered`; it does not restart the process as the first repair.
- `current_signal` as a compact one-line machine signal (phase/tool/queue/blocked source).

Durables status (`Durables` button, admin-only):

- `Status Scope: durables` with aggregate counts (`total`, `active`, `dormant`, `degraded`, `inactive`).
- Per-agent health cards with:
  - identity and topology (`agent_id`, `channel`, `status`, `health`, `review_chat`)
  - policy posture (`policy_version`, `policy_hash`, `outbound`, `drift`, `capabilities`)
  - delegation posture (`capability_request` and `capability_grant` status when delegated permissions are active)
  - runtime pulse (`last_wake`, `last_review`, `dormant_at`, apply status/error)
  - remote/control-plane pulse when present (`last_seen`, enrollment status, policy sequence, error evidence)

### `/health trace` content signals

`/health trace` starts as a collapsed command reply with `Quick Read:`, then expands via `Read More`.
It is intended for operational diagnosis when `/status` is too compressed.

- prepends `Quick Read:` summary when the readable-summary provider is available
- includes the full chat status block (`Status Scope: chat`)
- adds `Trace Chat:` detail lines with latest turn internals:
  - `latest_request`
  - `last_tool_preview`
  - decoded `last_exec_command` when available
  - `last_tool_result`, `last_tool_error`, `turn_error`
- admin users additionally receive:
  - full `Status Scope: system`
  - `Trace System:` (pending-kind counters + latest turn rollups per chat)
  - sandbox readiness warnings when present
  - full `Status Scope: durables`
- output is chunked when needed to fit Telegram message size limits

Review digest deliveries to admin chat are rendered with labeled metadata lines (`Source Chat:`, `Source User:`, `Source Role:`, optional scope/agent lines) plus a `Summary:` section.

### `/tailnet` controls

Tailnet buttons keep private networking as a diagnostic/control projection:

- `Refresh`
- `Surfaces`
- `Grants`
- `Open Status` when the private parent status URL is known
- `Revoke <n>` on visible registered surfaces

Surface revoke buttons use short callback tokens, re-resolve the live surface
registry on click, and require a second confirmation before writing the local
registry revoke event.

### `/mission` controls

Mission Ledger buttons expose the finite review actions without requiring copied
IDs:

- home/list refresh
- show mission details
- propose bounded action
- pin/unpin
- activate, pause, complete, archive
- admin health

Callbacks resolve short mission tokens against the current authorized mission
view before applying any state change. Mission actions update ledger records; they
do not create continuation authority or capability grants.

### Approval Windows

Approval-window buttons keep automation contextual to the request that was just
approved:

- `Approve 15m` creates a temporary automation gate and matching approval
  grant for the current chat or side thread.
- `Close` removes the offer buttons without changing runtime state.
- `Double time` doubles the current approval window within the configured
  live-override ceiling.
- `Cancel approvals` revokes both the approval grant and its matching temporary
  automation gate.

Duration, scope, live-override ceiling, admin checks, and spendability remain
typed runtime checks, not UI convention.

### Side threads

Side threads are lightweight per-chat work lanes for keeping simultaneous
requests apart without creating another operator channel.

- `/thread` creates the next numeric thread and shows a guide.
- `/thread <message>` creates the next numeric thread and routes the message
  there immediately. The command only selects the side-thread lane; the first
  turn still goes through the same busy, artifact-retention, durable-ingress,
  and recovery gates as any other Telegram work.
- `(thread N) <message>` routes a later message to an existing open thread.
  Prefix targeting also only selects the lane, so it cannot bypass interrupt or
  artifact-retention prompts.
- Replies to side-thread messages route back to that thread when the reply
  target is present in the durable Telegram ledger, including guide cards,
  progress cards, thread-created messages, and ordinary outbound replies.
- `/threads` lists threads and shows summarize/absorb buttons for open ones.
- `/absorb N` closes the thread and records a compact note in the main chat.

The main chat remains thread `0`. Thread sessions have independent transcript,
plan, progress, and recovery state, so three child-agent setup requests in three
threads do not share the same turn plan or router queue. Absorb is bookkeeping:
it closes the side lane and carries the outcome back to the main transcript, but
it does not merge every thread message into thread `0` and does not automatically
approve memory writes. Summarize is also bookkeeping: its callback is recorded
as recoverable ingress, then it queues a normal thread-0 turn with bounded
evidence from open side threads and does not close or absorb them.

Thread-scoped work-lane controls follow the same typed session scope as the
turns themselves. Continuation approvals, progress-card `Stop`/`Reassess`,
startup recovery prompts, busy/interrupt decisions, artifact retention prompts,
and `/memory` focus are keyed to the side-thread session when the work came from
that side thread. Deferred busy and artifact decisions resume through their own
recoverable Telegram ingress surfaces before the pending decision is cleared.
Global operator surfaces stay global so authority and service state do not
become ambiguous.

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
  - `Stop`
  - `Finish`
- Stop-word confirmation:
  - `Yes, stop`
  - `Keep going`
- Proposal approval:
  - `Deny`
  - `Approve`
  - plus optional `Expand details` when summarized details are available.
- Artifact retention:
  - `Turn only`
  - `Session`
  - `Save locally`

### Live progress card controls

When a turn enters long-running activity/tool execution, Telegram shows one auto-updating progress card:

- Header starts with `Working...` while active, so the card reports activity/progress rather than claiming to expose private reasoning.
- Card includes inline controls:
  - `Reassess`
  - `Details` / `Summary`
  - `Stop`
- `Reassess` stops active work, clears queue, revokes continuation, and detaches sender-owned pending decisions.
- `Details` is presentation-only: it re-renders the same run-scoped progress card from TES with safe tool/update detail for the whole retained visible window. It does not affect authority, execution, queueing, or continuation.
- `Details` stays selected for that active run until `Summary` is clicked; later progress edits keep using details mode.
- `Summary` returns the whole retained visible window to the semantic summarized projection.
- If a live projection cannot be rebuilt immediately, the card keeps the last rendered summary/details text instead of blanking or replacing real detail with an empty-state line.
- Thread progress cards keep their `(thread N)` presentation prefix when toggled
  between `Details` and `Summary`.
- Entries outside `telegram.tool_progress_window` stay represented only by the omitted-count line.
- `Stop` stops the active run's session lane and revokes continuation for that
  lane. A thread progress card stops its side thread, not unrelated main-chat
  work.
- When deliberation ends, controls are removed from the card (or the card is deleted when `telegram.tool_progress_cleanup=true`).

## Callback Behavior

- Status and selector callbacks edit the same Telegram message in place when possible.
- Status output can be chunked; extra chunks are sent as follow-up messages.
- Stale callback actions are acknowledged with a stale-message notice instead of applying previous state.
- Busy/interrupt and artifact-retention callbacks are restart-recoverable from
  typed pending rows. If the process restarted while a prompt was open, the old
  button can resume that exact pending message, or startup reissues a fresh
  prompt/defaults it through durable Telegram ingress.
- Startup replay is limited to typed Telegram work surfaces: primary messages,
  thread-summary work, doctor work, and decision-resume work.
- Approval buttons without a typed restart-resume path are detached as stale
  after restart; they do not grant authority to work that no longer has a live
  waiter.
- Non-admin access to admin-only status views is denied via callback acknowledgement.
- Deliberation control callbacks are run-id scoped; stale controls are ignored with a stale notice.
- Durable-agent launcher callbacks are admin-only and run-id agnostic:
  - `Chat` triggers a background durable `conversation_send` kickoff for the selected agent.
  - `Refresh` reloads the durable-agent list in place.

## Operational UI Signals

- Typing indicator is emitted while active work is running in chats that support local reply delivery.
- Tool/progress updates are emitted as a single live `Working...` progress card per turn.
- Restart and detach actions return explicit user-visible summaries.
