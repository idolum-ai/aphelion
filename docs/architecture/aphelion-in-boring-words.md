# Aphelion, in boring words

Aphelion is a self-hosted runtime for a personal AI agent, written in Go,
with a governance layer between the model and everything the model can do.
The repository uses evocative names for its subsystems. This document
deliberately does not. Every idea in Aphelion has a respectable ancestor in
systems and security engineering; this is the map from one vocabulary to the
other, so you can evaluate the engineering without first learning the poetry.

The one-sentence version: **Aphelion treats an LLM the way a kernel treats a
process — useful, untrusted, and unable to grant itself privileges — and
treats the audit log, not the conversation, as the source of truth.**

---

## The translation table

| Aphelion says | Boring words | Ancestor |
|---|---|---|
| Face / Governor | Privilege separation: unprivileged conversational process, privileged authority process | qmail, OpenSSH privsep |
| Permission lane (`request → classify → review → provision → attest → grant → expose → observe → renew/revoke`) | Capability lifecycle management | Object-capability security, IAM, certificate lifecycle |
| Action proposal | Scoped authorization request with declared blast radius | Change request + OAuth scope negotiation |
| Continuation lease | Expiring, turn-metered, non-self-renewing authorization token | OAuth access tokens; Chubby-style leases |
| Approval bundle phases | Per-phase step-up authorization, no blank checks | Step-up auth, separation of duties |
| Work modes (`read_only < workspace_write < commit < deploy`) | Ranked privilege classes; requests are checked against the strongest granted class | Protection rings, RBAC tiers |
| Execution-events ledger | Append-only event log as execution-order record | Event sourcing, WORM audit logs |
| Universal evidence ledger | Immutable typed source snapshots plus audited context hydration | Evidence stores, provenance graphs, content-addressed audit trails |
| `/status`, `/health trace`, doctor | Read models projected from the event log, with source attribution | CQRS projections |
| Evidence-gated completion | State transitions to "done" require matching evidence records, not model assertions | CI gates: no green checks, no merge |
| Typed claims | Model output affects state only through parsed, schema-validated structures; prose is presentation | "Parse, don't validate"; control/data plane separation |
| Constitution stage | Output validation pass before anything reaches the operator | Egress filtering; response schema enforcement |
| Hidden inputs | Provenance-attributed context injection; every injected signal carries its source label | Tainted-data tracking |
| Interior signal pressure | Accumulating signals with magnitude, exponential decay, thresholds, and cooldowns | Alerting with hysteresis; leaky bucket; EWMA |
| Curiosity lane | Budgeted, read-only, sandboxed background jobs with pinned tool arguments and no user-facing output | cron + least privilege + rate limiting |
| Nocturne | Scheduled offline batch job that writes a local artifact and messages no one | Nightly batch processing |
| Durable children | Long-lived sub-agents with their own permission envelope, derived at creation, not inherited ambiently | Workload identity (SPIFFE-shaped); zero-trust service mesh |
| Tailnet provisioning | Remote agents reached over an authenticated overlay network | Tailscale/WireGuard infrastructure |
| Structural hygiene ledger | Tech-debt register where every oversized module needs a documented owner and split plan | ADRs + debt register |
| Architecture waivers (with expiry dates) | Time-boxed exceptions to architectural rules, enforced by tests | Policy waivers; fitness functions |
| Telegram operator surface | Out-of-band control plane: approvals inbox, status, kill switches | Break-glass consoles; PagerDuty-style ack flows |

---

## The core bets, in market vocabulary

### 1. Authority is data, not configuration — and definitely not prompts

In most agent stacks, what the agent is allowed to do lives in three places:
a config file, a system prompt, and hope. Aphelion's position is that
permissions are **typed records with a lifecycle**. A capability is
requested, classified by risk, reviewed (by a human or by policy),
provisioned, attested, granted with an expiry, observed in use, and renewed
or revoked. The runtime cannot invoke anything it does not hold an active,
unexpired grant for.

This is capability-based security applied to agent tooling. The interesting
property is the same one OCAP people have argued for decades: when authority
is a first-class object, you can audit it, expire it, narrow it, and reason
about it. When authority is a prompt, you can do none of those things.

### 2. Privilege separation between "what talks" and "what decides"

The agent is two roles, not one persona. The conversational role proposes;
a separate authority role reviews, holds the grants, and emits the typed
records. The conversational role cannot grant itself permissions — not
because a prompt tells it not to, but because the package boundary doesn't
expose the operation. The split is enforced by compile-time import-guard
tests in CI, the same way qmail and OpenSSH enforce privsep with process
boundaries rather than discipline.

This matters because the failure mode of single-persona agents is
self-escalation under persuasion: the model is talked (by a user, or by
injected content) into doing something it technically can do. Aphelion's
answer is the standard security answer: make the talking component unable
to do it.

### 3. The transcript is not the truth; the log is

Every meaningful action — message ingress, turn execution, tool call,
delivery, authorization, recovery — is appended to an execution-events
ledger as a typed row. Those rows, plus transcript messages, operation state,
capability records, curiosity observations, and artifacts, are indexed as
immutable evidence objects with source kind, epistemic status, bounded digest,
and a runtime-computed payload hash. Operator-facing status surfaces are
projections of that evidence with source attribution, not summaries of the
conversation. This is event sourcing plus provenance indexing, applied to agent
operations, with one sharp corollary:

**The agent is not allowed to declare its own work complete.** A phase
transitions to "done" only when the ledger contains matching evidence — the
right lease ID, the right work mode, a completion timestamp, no trailing
error. A model saying "I finished" with no evidence row is treated exactly
like a build claiming success with no artifacts: rejected, with a typed
reason code. This single rule eliminates the most common agent failure
mode in production — confident, plausible, false completion reports.

For long-horizon work, the same ledger is the context-fidelity substrate. A
future turn can ask the runtime to hydrate the relevant evidence IDs for the
current operation instead of reasoning from a chain of increasingly compressed
conversation summaries.

### 4. Model output is untrusted input

Anything that flows from the model into system state passes through typed,
schema-validated structures. Prose is presentation. Where the runtime once
matched strings, it now matches reason codes. Where the model reports a
content hash, the runtime computes its own and prefers it — model honesty
is never load-bearing. Third-party text fetched from the network is tagged
`untrusted_external_source` at the evidence layer and cannot silently launder
itself into operator-facing output. This is the "parse, don't validate"
school plus standard taint-tracking instincts, applied to LLM output.

### 5. Autonomy is a budgeted, least-privilege background job — not a mode

Aphelion's proactive behavior is built like infrastructure, not like a
personality setting:

- **Signals accumulate.** Internal observations (recurring themes, open
  questions, time pressure) are persisted with magnitude and exponential
  decay. Proactive outreach fires on threshold crossings with cooldowns —
  the same hysteresis discipline you'd use to prevent alert fatigue.
- **Exploration is sandboxed.** The background "look around" lane runs
  read-only, under a daily turn budget enforced transactionally, with the
  tool *and its exact arguments* pinned by the runtime (the model cannot
  choose what to read), under a non-admin principal so network-layer
  private-address rejection applies. Its findings raise signal pressure;
  they never message the operator directly.
- **The feedback loop is structurally damped.** Exploration-sourced
  pressure cannot, by construction, make a subject eligible for more
  exploration. Runaway loops are prevented by architecture, not tuning.

The thesis behind this: proactivity in any agent is reaction to signals the
observer doesn't see. Making those signals explicit, typed, and decaying is
what makes autonomy auditable — a system that acts on a ledgered signal can
always answer "why did you do that?"

### 6. Supply chain as a runtime safety property

Six direct Go module requirements, of which three are the deliberately chosen
primary third-party surfaces: a vendored SQLite driver, a TOML parser, and
Tailscale. Channels, tools, and providers are compiled in, not loaded as
plugins. For a process that holds your credentials and acts autonomously,
dependency surface is attack surface — the same logic that motivates distroless
images and SLSA, applied to a personal agent.

### 7. The repo audits itself

Architecture boundaries are tests, not documentation: a package that
imports across a forbidden boundary fails CI. Files over 800 lines must
appear in a ledger with a named owner-concept and a split direction.
Exceptions to the rules exist as written waivers **with expiry dates**.
Requirements documents are normative and updated in the same commits that
change behavior. None of this is novel — it's fitness functions and ADRs —
but it's rare to see it enforced this consistently anywhere, let alone in a
single-maintainer project.

---

## What Aphelion is not

- **Not a framework.** It's a runtime for one operator. No plugin
  marketplace, no multi-tenant story, no SDK. Extension is a code change.
- **Not a chatbot wrapper.** The conversation is the UI, not the system.
  Sessions park during deploys, recover after crashes, and explain what
  happened from the ledger.
- **Not prompt-engineering-as-safety.** Prompts shape behavior; they are
  never the enforcement mechanism. Everything load-bearing is a type, a
  boundary test, or a database row.

## Why this might matter to you

If you run agents that can act — write files, commit, deploy, spend money —
you will eventually need answers to: *what exactly was this agent allowed
to do, who allowed it, when does that expire, and how do we know the work
it claims is real?* Most stacks answer with a config file, a system prompt,
and a transcript. Aphelion is one worked example of answering with a
capability lifecycle, privilege separation, and an evidence ledger — built
small enough that one person can audit all of it.

The code is the argument: https://github.com/idolum-ai/aphelion. Start with
`docs/architecture/design-principles.md`, then
`architecture_import_guard_test.go` to see the boundaries enforced, then
`requirements/` for the normative specs.
