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

System health includes provider pressure as a typed projection. A
`provider_health` line summarizes recent provider failures, retries, failovers,
and successes across the last few hours, with the latest provider/model/reason
when a failure is still the newest evidence. Treat `degraded` as an inference
surface issue before assuming the turn logic or Telegram transport is broken;
`residual_risk` means a later success exists but recent provider pressure is
still worth knowing about.

Aphelion advances Telegram offsets only after an update is durably accepted,
durably handled, durably queued for a turn, terminally skipped/completed, or
recorded as a failure. Accepted but not-yet-started updates live in
`telegram_ingress_updates` and are replayed on startup before polling resumes.
If Telegram redelivers an update that is already terminal in the ledger,
Aphelion advances the offset without dispatching the work again.
If Telegram delivers a malformed or handler-failing update, Aphelion records the
failure in the ingress ledger, advances the Telegram offset, and keeps polling.
System status and `/health trace` show recent `telegram_ingress_updates` and
`telegram_ingress_failures` so the operator can see pending, running,
interrupted, completed, and skipped updates as evidence instead of inferring
from chat history.

Terminal-only callback or skipped rows may show `accepted_at` equal to the
ledger write time. Treat `status`, `completed_at`, and the reason text as the
evidence for those updates; `accepted_at` is literal only for accepted work rows.
Long-running callback work, including `/health diagnose`, is recorded on a
callback-work ingress surface before it starts so restart replay can recover it.
Startup replay uses the compiled Telegram work-surface registry: primary
messages, thread-summary callback work, doctor callback work, busy-decision
resume work, and artifact-retention resume work are replayable; arbitrary
callback text is not.
Busy/interrupt and artifact-retention prompts keep a typed pending row alongside
the Telegram approval prompt. After restart, Aphelion either resumes from the
typed row when the old button is clicked, reissues a fresh prompt, or applies the
prompt's timeout default through the same synthetic Telegram ingress ledger.
Restart-loaded approval prompts that cannot be resumed are detached as stale; the
newest prompt is authoritative.

The stale-turn watchdog is a scoped recovery mechanism. When it finds stale
running turns, it records the observation, cancels any matching in-process turn,
interrupts those exact turn-run rows and matching Telegram ingress rows, then
records `watchdog.recovered`. A process restart remains an explicit operator
action; watchdog recovery should leave unrelated chats and threads alone.

## Keep Parallel Requests Apart

Use side threads when you want to keep separate requests from sharing one live
chat plan.

```text
/thread
/thread create a read-only child for inbox triage
(thread 1) use the safe cadence presets
reply to a side-thread message: continue that setup
/threads
/absorb 1
```

Messages without a prefix go to the main chat session, also called thread `0`.
`/thread` creates the next numeric side thread and shows a compact guide.
`/thread <message>` creates the next thread and routes the message as its first
turn. Later messages that start with `(thread N)` route to that existing open
thread. Replies and progress cards from a side thread begin with `(thread N)` so
the visible radio traffic stays attributable. If you reply to a message that
Aphelion can match to a side thread through its Telegram ingress or outbound
ledger, progress-card row, thread-created row, or thread-guide row, the reply
routes to that thread.

Thread targeting is lane selection, not a shortcut around governance. A
targeted message still passes through the busy/interrupt gate, artifact
retention prompts, durable Telegram ingress, and replay recovery for that
thread before the turn is accepted.

Bare slash commands still operate on the main chat-level view, except for the
global operator surfaces which are always global. Work-lane commands can be
explicitly pointed at a side thread. Examples: `(thread N) /status`, `(thread N) /memory`, `(thread N) /stop`, `(thread N) /new`, or `(thread N) /detach`.
You can also send those commands as replies to side-thread messages. `/health`,
`/tailnet`, `/model`, `/agents`, `/thread`, `/threads`, `/absorb`, `/restart`,
`/reinstall`, mission controls, durable-agent setup, and approval-window
callbacks remain global/operator surfaces.

Each side thread has its own durable session scope, router queue, plan state,
progress state, continuation approvals, busy/interrupt decisions, artifact
retention prompts, memory focus, and recovery records. `/absorb N` closes the
thread and appends a compact outcome note to the main chat for bookkeeping.
Before closing, Aphelion stops that side-thread lane and takes the thread session
lock so queued or active side-thread work cannot write after absorb. It
does not copy the whole side transcript into thread `0`, and it does not
automatically write curated memory; use `/memory` in the relevant lane to review
memory candidates when something from a thread should become durable knowledge.

`/threads` also exposes `Summarize` when there are open side threads. That
button queues ordinary thread-0 work with bounded evidence from the open
threads, so Aphelion can send one short main-chat status without absorbing or
closing anything.

## Stop Or Reset A Chat

```text
/stop
/new
/detach
```

`/stop` cancels current work and clears queued follow-up messages for the chat.
`/new` starts a fresh chat session without clearing memory. `/detach` clears
active, queued, continuation, and approval state for you in the chat. When these
commands are explicitly targeted at a side thread, they affect only that
side-thread lane.

Queued work includes both the root Telegram ingress queue and the per-session
turn router queue. A stopped chat drains accepted but not-yet-started Telegram
items before canceling the active turn, so a pre-stop instruction does not run
after the stop.

If the service itself needs attention, use `/health` first so the next action is
grounded in current state.

## Grant Bounded Automation

After an approval succeeds, the approved message shows `Approve 15m` and
`Close`. `Approve 15m` opens a bounded approval window for matching
requests in the current chat or side thread. It creates the temporary automation
gate and the spendable approval grant together, so the operator does not have to
manage them as separate controls.

An active window shows `Double time` and `Cancel approvals`. Each `Double time`
press doubles the current window duration within the configured live-override
ceiling. `Cancel approvals` revokes both the approval grant and the matching
automation gate for that scope. Side-thread approval windows are consumed only by
that side thread; they do not approve default-chat work or another thread's work.

## Manage Work Surfaces

`/threads` shows open side threads by default. Use the **Show non-open** button, or `/threads nonopen`, to inspect closed/absorbed threads without mixing them into the default work view. Open side threads use reusable display slots, so when slot 2 is closed the next new side thread can become thread 2 again. Closed threads keep their durable internal thread id and receive an archived display name like `2-2026-05-17`, with `-1`, `-2`, etc. added if that archived name is already taken on the server-local date. Admins can audit or repair old rows with `telegram-threads sanitize` (`--apply` to mutate; dry-run by default).

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
request as normal routed work. It is marked queued in the Telegram ingress
ledger before the poll offset can move past the command.

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

## Scoped Telegram Thread and Approval-Window Smoke Checklist

Before deploying changes that affect scoped Telegram threads, approval windows, recovery, or schema invariants:

- Run `go test ./...`.
- Run `aphelion --check-config --config ~/.aphelion/aphelion.toml` and remove unsupported watchdog restart keys if present (`recovery.watchdog.restart_cooldown`, `recovery.watchdog.max_restart_attempts`).
- Run a build check for the service binary.
- Take a sessions DB backup.
- Run schema verification against a copied DB, for example `aphelion schema verify --db copied-sessions.db`.
- Check `/health diagnose` after startup.
- Run `/threads` and verify only visible open thread numbers are shown in normal UI.
- Create a side thread, reply to it, then verify replies still route to that visible thread number after restart.
- Run `/absorb <visible-thread-number>` and verify that the visible label becomes available for the next open thread.
- Approve a request and verify the approved message offers `Approve 15m`.
- Open an approval window from a side-thread approval prompt and verify the edit starts with `(thread N)`.
- Verify a thread-scoped approval window cannot approve default-chat work or another thread's work.
- Observe durable-wake/provider warnings: repeated transient failures should stay compact and actionable, while permanent child blockers may interrupt chat.
