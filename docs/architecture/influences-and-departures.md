# Influences And Departures

_Status: canonical design-lineage record._

This file attributes the systems, research areas, and working vocabulary that
shaped Aphelion. It is not a dependency notice, endorsement claim, affiliation
claim, or proof that code was copied. Legal notices for vendored code live in
[`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md).

Use this document to answer three questions:

- What Aphelion took.
- Where Aphelion stops.
- Why Aphelion diverges.

## Reading Rules

- Public URLs are included only when a stable public anchor was checked.
- Adjacent projects without a stable public source are named as lineage context,
  not as formal public citations.
- Influence is not implementation authority. Aphelion's authority comes from its
  own typed records, config, code, and execution evidence.
- If a new requirement says "closer to X" or "unlike Y", update this file in the
  same change.

## Nearby Systems

### OpenAI Codex And Codex CLI

Source: [OpenAI Codex CLI getting started](https://help.openai.com/en/articles/11096431-openai-codex-cli-getting-started)
and [OpenAI Codex](https://openai.com/codex).

What Aphelion took:

- Local coding-agent ergonomics: inspect, edit, run checks, and report evidence.
- The useful prompt shape of stable base context plus dynamic runtime updates.
- Release/install simplicity: a binary and a direct operator path should be
  enough to run the system.

Where Aphelion stops:

- Aphelion is not primarily an IDE, coding task queue, or cloud coding agent.
- Aphelion does not make multi-agent code execution its operator surface.
- Codex-style approval modes do not become Aphelion's authority model.

Why Aphelion diverges:

- Aphelion is an outpost for live personal-agent authority, not a programming
  assistant. Telegram and CLI remain the operator surfaces; leases, grants, TES,
  and doctor/status projections carry the authority truth.

Current repo surface:

- `requirements/system-prompt.md`
- `requirements/deployment.md`
- `requirements/thinking.md`
- `runtime`, `turn`, `pipeline`

### Hermes

Source: adjacent project context in this repo's local design lineage; no stable
public URL is asserted here.

What Aphelion took:

- Registry clarity for tools and routines.
- Focused delegated children with lighter context.
- Practical staged controls for reasoning and interaction modes.
- Prompt and memory fencing patterns that keep recalled context visible.

Where Aphelion stops:

- A registry or instruction file does not make a capability real.
- Routine/skill/self-improvement concepts do not override runtime authority.
- Global command approval is not enough when scope, principal, sandbox, and run
  kind change per turn.

Why Aphelion diverges:

- Aphelion resolves authority per run from typed principal state, leases, grants,
  sandbox policy, and execution records. The face layer never owns tools or
  permissions.

Current repo surface:

- `requirements/tools.md`
- `requirements/subagents.md`
- `requirements/memory.md`
- `requirements/thinking.md`

### OpenClaw

Source: adjacent project context in this repo's local design lineage; no stable
public URL is asserted here.

What Aphelion took:

- Layered, per-run enforcement rather than one static tool list.
- Local-first memory and retrieval influence, including import/provenance
  concerns.
- Subagent lifecycle ideas: explicit child sessions, depth, and control
  boundaries.

Where Aphelion stops:

- Aphelion does not become a broad local-first multi-channel assistant.
- It does not let channel breadth, memory breadth, or tool breadth become the
  center of the product.
- OpenClaw-style enforcement is adapted under Aphelion's governor/face split.

Why Aphelion diverges:

- Aphelion's edge is legible authority over live personal-agent action. It keeps
  the operator surface narrow, rejects plugin-marketplace growth, and treats
  memory/retrieval as subordinate to local canonical records.

Current repo surface:

- `requirements/tools.md`
- `requirements/subagents.md`
- `requirements/semantic-store.md`
- `docs/architecture/done-done-roadmap.md`

### Ralph Loops And Ralph-Style Loop Vocabulary

Source: [Ralph Loops](https://ralphloops.io/) and
[Ralph Workflow](https://ralphworkflow.com/). The public Ralph Loops site
attributes the format to Geoffrey Huntley's Ralph loop methodology and Agent
Skills. Aphelion records this as Ralph-style loop vocabulary, not as direct
implementation inheritance.

What Aphelion took:

- Iteration should be backed by durable files, commands, checks, and feedback.
- A loop should have an inspectable package of instructions and validation
  commands, not only an in-chat prompt.
- Fresh context plus persistent state can be useful for long-running work.

Where Aphelion stops:

- Aphelion does not run unbounded "keep trying until done" loops.
- Completion markers, repeated prompts, and feedback commands are not enough to
  authorize external effects.
- A loop package is not a substitute for leases, grants, operator consent, or
  rollback evidence.

Why Aphelion diverges:

- Aphelion treats feedback loops as one operational pattern inside a governed
  outpost. Work must remain stoppable, reviewable, scoped, and visible through
  status, doctor, TES, and explicit release/rollback gates.

Current repo surface:

- `docs/architecture/done-done-roadmap.md`
- `docs/architecture/transparent-execution-sequence.md`
- `requirements/operations.md`
- `verify_deploy.go`

### Tailscale And `tsnet`

Source: [Tailscale tsnet docs](https://tailscale.com/kb/1244/tsnet).

What Aphelion took:

- A private network identity can be embedded in a Go process.
- Private reachability can be a substrate for parent/child control traffic.
- Network policy and observed reachability should become evidence.

Where Aphelion stops:

- Tailscale identity is request evidence, not standalone authority.
- Aphelion does not turn Tailnet state into a web dashboard or public exposure
  layer.
- Live Tailscale policy mutation remains gated behind Aphelion grants and
  operator approval.

Why Aphelion diverges:

- Tailscale grants reachability. Aphelion grants authority. The two must be
  bound by explicit records instead of collapsed into one trust claim.

Current repo surface:

- `docs/architecture/tailscale-agent-substrate-project.md`
- `tailnet`
- `runtime/tailnet.go`
- `maintenance_tailnet.go`

### Telegram Bot API

Source: [Telegram Bot API](https://core.telegram.org/bots/api).

What Aphelion took:

- A simple, durable, remotely reachable operator channel.
- Inline controls, callbacks, and message delivery as a compact radio link.

Where Aphelion stops:

- Telegram is not one adapter in an omnichannel product surface.
- Telegram messages and buttons are projections, not authority records.
- Future channels must be compiled-in transport boundaries, not plugin sprawl.

Why Aphelion diverges:

- The project is a governed outpost. Telegram is the radio link; CLI is the
  maintenance surface; the ledger remains the source of truth.

Current repo surface:

- `telegram`
- `commands.go`
- `requirements/telegram.md`
- `docs/telegram-ui-features.md`

## Research And Theory

### Reason/Act Language Agents

Source: [ReAct: Synergizing Reasoning and Acting in Language Models](https://arxiv.org/abs/2210.03629).

What Aphelion took:

- Reasoning and action should inform each other.
- External observations can reduce hallucination and improve task progress.
- Tool-use traces should be interpretable.

Where Aphelion stops:

- ReAct-style `Thought`/`Act` text is not an authority layer.
- Action traces do not become permission by being plausible.

Why Aphelion diverges:

- Aphelion compiles closed contracts and records actual execution in TES. The
  model can reason, but the runtime must validate authority, scope, and evidence.

Current repo surface:

- `tool`
- `turn`
- `runtime/constitution_runtime.go`
- `docs/architecture/transparent-execution-sequence.md`

### Reflective And Feedback-Driven Agents

Source: [Reflexion: Language Agents with Verbal Reinforcement Learning](https://arxiv.org/abs/2303.11366).

What Aphelion took:

- Feedback can be converted into durable context for later attempts.
- Reflection is useful when it is tied to real outcomes.

Where Aphelion stops:

- Self-reflection does not automatically promote memory, widen authority, or
  justify repeated autonomous attempts.
- Feedback summaries are not proof unless they point to evidence.

Why Aphelion diverges:

- Aphelion routes durable learning through curated memory, review events, TES,
  and explicit operator controls. Reflection remains subordinate to evidence.

Current repo surface:

- `memory`
- `runtime/review_*`
- `docs/architecture/state-surfaces.md`

### Capability Security And Least Authority

Source: Saltzer and Schroeder,
[The Protection of Information in Computer Systems](https://web.cs.wpi.edu/~cs557/f14/papers/saltzer1975_alt.html).

What Aphelion took:

- Economy of mechanism, fail-safe defaults, complete mediation, and least
  privilege.
- Permissions should be checked at the point of use, not assumed from context.

Where Aphelion stops:

- OS/process permission is necessary but not enough for personal-agent work.
- A principal alone does not answer consent, purpose, duration, or evidence.

Why Aphelion diverges:

- Aphelion combines least authority with consent subjects, leases, capability
  grants, sandbox policy, and operator-readable repair surfaces.

Current repo surface:

- `principal`
- `session/capability_store.go`
- `tool/sandbox`
- `docs/architecture/capability-delegation-lane.md`

### Promise And Commitment Vocabulary

Source: Mark Burgess and Jan Bergstra, public anchor:
[Promise Theory](https://markburgess.org/BookOfPromises.pdf).

What Aphelion took:

- Promises and commitments are more inspectable than ambient obligation claims.
- System behavior is easier to reason about when commitments are explicit.

Where Aphelion stops:

- Aphelion does not implement Promise Theory as a formal calculus.
- The promise ledger tracks public claims and implementation truth, not agent
  autonomy by promise alone.

Why Aphelion diverges:

- Aphelion's promises are operational: they must map to code, tests, config,
  docs, or planned gaps. Authority still comes from typed runtime records.

Current repo surface:

- `docs/promises.md`
- `docs/architecture/principle-debt.md`
- `scripts/check-public-readiness.sh`

### Situated Action And Activity Theory

Sources: Lucy Suchman,
[Plans and Situated Actions](https://openlibrary.org/works/OL4962782W/Plans_and_Situated_Actions),
and Yrjo Engestrom,
[Learning by Expanding](https://lchc.ucsd.edu/MCA/Paper/Engestrom/Learning-by-Expanding.html).

What Aphelion took:

- Plans are resources for action, not the action itself.
- Real work is situated in tools, people, timing, artifacts, and constraints.
- Learning and expansion should be visible in the activity system, not hidden in
  private model state.

Where Aphelion stops:

- Aphelion does not make conversational interpretation the source of permission.
- It does not treat every situated ambiguity as a reason to widen capability.

Why Aphelion diverges:

- The runtime interprets ambiguity, then compiles or rejects concrete contracts.
  Operator text remains presentation until it is transformed into typed state.

Current repo surface:

- `requirements/planning-brokerage.md`
- `requirements/operations.md`
- `docs/architecture/operator-presentation-contract.md`

### Event Sourcing And Audit-Ledger Practice

Source: engineering lineage rather than one formal citation.

What Aphelion took:

- Runtime truth should be reconstructable from durable event records.
- Operator projections should say where their claims came from.

Where Aphelion stops:

- An event log alone is not enough. Aphelion also keeps operational current-state
  tables for leases, continuations, grants, child state, and Tailnet surfaces.

Why Aphelion diverges:

- The system needs short paths to truth during live operation. Canonical TES,
  operational stores, and projections each have explicit truth classes.

Current repo surface:

- `docs/architecture/transparent-execution-sequence.md`
- `docs/architecture/state-surfaces.md`
- `session/store_execution.go`
- `commands_status.go`

## Non-Goals From The Lineage

- No plugin marketplace.
- No omnichannel operator console.
- No web dashboard as a new control surface.
- No prompt text as authority.
- No unbounded agent loop as a completion strategy.
- No memory or retrieval hit that outranks constitutional/runtime truth.
- No external network identity that replaces Aphelion grants.

## Maintenance Contract

When adding a new inspiration, reference, or contrast:

1. Add it here with `What Aphelion took`, `Where Aphelion stops`, and
   `Why Aphelion diverges`.
2. Use a public URL only when verified.
3. If no public source is known, mark it as adjacent project context.
4. Link the entry to the current repo surface where the idea is implemented or
   constrained.
