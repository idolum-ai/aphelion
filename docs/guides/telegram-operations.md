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

## Common Workflows

| Goal | Use |
|---|---|
| Check whether the service is ready | `/health`, then `/health trace` or `/health diagnose` when needed |
| See active work and pending decisions | `/status` |
| Keep a separate task from mixing with the main chat | `/thread <message>`, replies to that thread, then `/threads` |
| Notice stale side lanes without polling manually | Default-on stale-thread reminders; `/threads remind` for admin diagnostics |
| Close a side lane after it is no longer active | `/absorb N` |
| Inspect what is shaping replies | `/context` and `/memory` |
| Review objective candidates | `/mission` |
| Change model routing or OpenAI speed | `/model` |
| Recover from stuck work | `/stop`, `/new`, `/detach`, then `/health` |

## Inspect Health

Use `/health` first when you need to know whether Aphelion is ready.

Admin health controls include status, trace, diagnosis, service restart, and
reinstall panels. `/health trace` starts with a compact quick read and can expand
into deeper evidence. `/health diagnose` runs read-only diagnosis from a private
admin chat.

Use `/status` when the question is about active work, pending approvals,
durable-agent state, or a specific chat.

When you approve a continuation with `Start`, Aphelion may keep going for more
than one turn if the approved lease has remaining turns and the next phase stays
inside the same approved authority. You should see compact "continuing approved
lease" messages before automatic follow-up turns. If work needs a wider action,
new capability grant, expired/stale lease, deploy/restart boundary, or a mission
has completed or blocked, Aphelion stops and asks again instead of renewing the
lease silently.

System health includes provider pressure as a typed projection. A
`provider_health` line summarizes recent provider failures, retries, failovers,
and successes across the last few hours, with the latest provider/model/failure
kind when a failure is still the newest evidence. Transport timeouts, context
window pressure, request-buffer limits, and continuation rejections are grouped
as stable failure kinds instead of raw provider strings. Treat `degraded` as an
inference surface issue before assuming the turn logic or Telegram transport is
broken; `residual_risk` means a later success exists but recent provider
pressure is still worth knowing about.

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
Callback work that launches model turns, including `/health diagnose` and
`/context`/`/memory`/`/mission` `Ask Me`, is recorded on a callback-work ingress
surface before it starts so restart replay can recover it.
Startup replay uses the compiled Telegram work-surface registry: primary
messages, thread-summary callback work, doctor callback work,
context/memory/mission-clarification callback work, busy-decision resume work,
and artifact-retention resume work are replayable; arbitrary callback text is
not.
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

For continuation diagnosis, `/status` and `/health trace` can surface recent
bundle narrowing, continuation compile repair, class-scoped lease consumption,
and loop-boundary events. Doctor evidence also includes turn accounting such as
assistant/tool character ratio and provider input/output/cache token counts so
context-pressure problems are visible without reading raw logs first.

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
explicitly pointed at a side thread. Examples: `(thread N) /status`, `(thread N) /context`, `(thread N) /memory`, `(thread N) /stop`, `(thread N) /new`, or `(thread N) /detach`.
You can also send those commands as replies to side-thread messages. `/health`,
`/tailnet`, `/model`, `/agents`, `/thread`, `/threads`, `/absorb`, `/restart`,
`/reinstall`, mission controls, durable-agent setup, and approval-window
callbacks remain global/operator surfaces.

Each side thread has its own durable session scope, router queue, plan state,
progress state, continuation approvals, busy/interrupt decisions, artifact
retention prompts, read-only context/memory panels, and recovery records. `/absorb N` closes the
thread and appends a compact outcome note to the main chat for bookkeeping.
Before closing, Aphelion stops that side-thread lane and takes the thread session
lock so queued or active side-thread work cannot write after absorb. It
does not copy the whole side transcript into thread `0`, and it does not
automatically write curated memory; use `/memory` in the relevant lane to review
memory candidates when something from a thread should become durable knowledge.

`/threads` also exposes `Analyze` when there are open side threads. That
button queues ordinary thread-0 work with bounded evidence from the open
threads, so Aphelion can produce a compact thread-board triage note without
absorbing, promoting, closing, or modifying anything. The analysis prompt asks
for a quick read, threads needing action, likely stale/absorbable threads,
blocked/waiting threads, and one suggested next move.

### Stale Thread Reminders

Open side threads are eligible for default-on stale-thread reminders. The
reminder path is intentionally conservative: Aphelion chooses candidates with a
deterministic heuristic, sends at most a small bounded number per sweep, applies
per-thread cooldown and one-reminder-per-activity-epoch dedupe, and suppresses
threads that already have pending, ignored, resumed, absorbed, or expired
reminder state for the same activity epoch.

A reminder is a Telegram message that tells the operator there is an old living
side thread and offers three safe ways forward:

- reply to the reminder to continue the original side thread;
- press `ignore` to suppress the reminder without deleting thread content;
- press `absorb` to use the existing thread absorb flow.

`/threads remind` remains an admin diagnostic for exercising the same selection
and emission path without waiting for the background sweep. It is not the
primary product surface and should not be treated as required ordinary workflow.
Keep it until live observation shows the background path is stable enough to
remove or hide further.

`/doctor` / health evidence may show pending reminder counts through the
Telegram thread section and sweep telemetry through execution events. Treat that
as repair-loop evidence, not as a separate authority surface.

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

## Request Button-Backed Approval

The native `request_approval` tool is for cases where the model already has a
bounded phase contract and needs to show the real Telegram approval card. It
persists a pending operation phase, validates the authority contract before
offering buttons, and then relies on the continuation materialization path to
show `Start`, `Details`, `Change`, `Pause`, and `Stop`.

Use `request_approval` instead of `update_operation` when the immediate goal is
operator approval. Use `update_operation` for ordinary operation bookkeeping,
findings, artifacts, or durable phase-plan state that should not by itself force
a visible approval prompt.

`request_approval` does not execute the requested work and does not create
authority. Execution is still gated on the operator pressing the approval button.

Some operation phases also declare `required_capability_grants`. Treat those as
phase-local dependencies, not ambient permission. When the operator explicitly
approves the phase through the button-backed approval path, Aphelion may
approve/create the named bounded capability grant after validating the
continuation authority contract and all required grant specs. Approval windows do
not auto-approve capability grants. If validation fails, no partial grant should
be left behind. Existing grants count only if they cover all requested actions;
new grants default to the continuation lease expiry unless the grant spec names
an explicit expiry.

## Grant Bounded Automation

After an approval succeeds, the approved message shows `Approve 15m` and
`Close`. `Approve 15m` opens a bounded approval window for new approval
requests in the current chat or side thread. It creates the temporary automation
gate and the spendable approval grant together, so the operator does not have to
manage them as separate controls.

An active window shows `Double time` and `Cancel approvals`. Each `Double time`
press doubles the current window duration within the configured live-override
ceiling. `Cancel approvals` revokes both the approval grant and the matching
automation gate for that scope. Side-thread approval windows are consumed only by
that side thread; they do not approve default-chat work or another thread's work.

## Manage Work Surfaces

`/threads` shows open side threads by default. Use the **Show absorbed** button, or `/threads nonopen`, to inspect absorbed/closed threads without mixing them into the default work view. Open side threads use reusable display slots, so when slot 2 is closed the next new side thread can become thread 2 again. Closed threads keep their durable internal thread id and receive an archived display name like `2-2026-05-17`, with `-1`, `-2`, etc. added if that archived name is already taken on the server-local date. Admins can audit or repair old rows with `telegram-threads sanitize` (`--apply` to mutate; dry-run by default).

Use `/agents` to inspect durable children as governed work surfaces. Open an
agent card before acting: `Brief` asks for a bounded child status check, `Park`
pauses scheduled or poll wakes without deleting history, `Resume` reactivates a
parked child after checks, and `Retire` removes a child from active use only
after a confirmation card. `Analyze` queues a read-only main-chat board analysis
and does not wake children or change authority. Replies to `/agents` cards route
back to the ledgered child and show an `(agent <id>)` prefix for attribution.

Use `/context` to inspect the current chat/thread context that is shaping replies.
It is read-only; `Ask Me` queues clarification questions without writing memory.

Use `/memory` to inspect read-only durable/semantic memory state and recall
previews. `Ask Me` queues confirmation/correction questions without writing
curated memory or changing state.

Use `/mission` to review objectives and mission candidates. Mission state
preserves intent and review material; it does not grant new authority on its
own.

Mission Question is Aphelion's low-burden mission clarification surface. After
an ordinary turn, Aphelion can offer a small card when the current work appears
semantically close to a mission or to a possible new durable objective. The card
does not write mission state. `Ask Me` queues one natural clarification question
as durable callback work; `Ignore` records that this association should stay out
of the way. If you answer the clarification, the next turn receives the prompt
id as hidden context and the model can resolve the prompt through the Mission
Ledger tool. Low-confidence prompts are throttled to about once per day per
owner, high-confidence prompts to about once per four hours, and ignored
associations stay quiet longer.

Use `/tailnet` to inspect declared Tailnet surfaces and grant bindings. Surface
and grant lists are paged and button-driven; open a surface detail card before
revoking local registry trust. Telegram revoke records a local Aphelion registry
event only. It does not mutate live Tailscale policy.

## Watch Re-entry And Heartbeat Nudges

Re-entry `Possible next steps` cards appear only after the current chat has a
completed latest turn, remains quiet for the runtime quiet window, and has no
active continuation/proposal gate blocking a recommendation. They are useful
for resuming work after silence, but they do not authorize the work they name;
the next turn should still request the smallest required approval.

The candidates are typed paths, not prose guesses. Runtime weighs the current
operation, missions, same-chat threads, interior-pressure signals, memory, and
hydrated evidence refs, then presents at most three short choices only when at
least one concrete opportunity exists. Labels are rendered from typed state, so
they should name the path being resumed or repaired instead of asking you to
reconsider work in general. A selected candidate carries its source, intent,
timing, why-now reason, and evidence refs into the next turn as context, but the
normal authority and evidence gates still decide what can happen.

Ignoring a card dampens the same semantic opportunity for a short cooldown, and
stale cards are penalized before they can resurface. Pure fallback cards such
as "ask what would be useful next" are suppressed; Aphelion should stay quiet
when it cannot name a useful path from durable state.

Heartbeat nudges may also mention recurring interior pressure. Those nudges are
continuity signals derived from hidden-input recurrence and support pressure.
They should be read as attention requests, not facts, approval, or permission.

There are no operator config keys for these policies today. Timing, candidate
count, cooldowns, and thresholds are hard-coded runtime defaults. If live use
shows nagging, stale pressure, or confusing timing, prefer a small follow-up PR
that documents the observed issue before turning the values into configurable
knobs.

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

The current slots are `Persona`, `Main`, `Health`, and `Children`. Slot changes
stay active until changed again or cleared; `Clear` returns the slot to the
configured default. OpenAI slots can also use `Fast`, which requests OpenAI's
priority service tier. Other providers keep provider-default speed behavior.

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
