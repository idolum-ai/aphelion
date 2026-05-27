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
- Project names are referenced only for attribution, comparison, and
  interoperability context. Aphelion is not affiliated with, sponsored by, or
  endorsed by those projects or maintainers.
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
  and diagnosis/status projections carry the authority truth.

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
- `docs/promises.md`

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
  status, diagnosis, TES, and explicit release/rollback gates.

Current repo surface:

- `docs/promises.md`
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

- `docs/architecture/state-surfaces.md`
- `docs/architecture/durable-children.md`
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

### Spectral Faithfulness

Source: sibling research repository under the same author/org;
`github.com/idolum-ai/spectral-faithfulness`.

What Aphelion took:

- The measurement that models silently absorb unreferenced context
  ("implicit context drift"), captured by the paper's epigraph
  *"the space biases the function."*
- The finding that models cannot reliably perceive their own boundaries —
  a measurable provenance failure between generated and implied content.
- The conclusion that boundaries must live in the runtime architecture,
  not in prompted instructions, because fresh sessions are the only
  reliable boundary the paper found.

Where Aphelion stops:

- Aphelion does not claim to resolve the paper's open Inversion Problem
  (ecologically valid and explicit instruments producing opposite conclusions
  about the same model).
- It does not generalize Spectral Faithfulness's catalog into a unified
  theory of model behavior; the paper itself flags the spectral metaphor
  as an organizing image, not a physical claim.
- Provider-specific findings in the paper are treated as provider-correlated,
  not provider-determined.

Why Aphelion diverges:

- Aphelion's response to the architecture-shapes-behavior finding is
  structural: typed records, sandbox policy, scoped tools, and the
  governor/face split are *the boundary*. Prompted persona is collaboration,
  not load-bearing structure. This matches the design principle
  *prefer typed records over interpreting prose*.

Current repo surface:

- `defaults/agent/IDOLUM.md`
- `requirements/idolum.md`
- `requirements/self-awareness.md`
- `docs/architecture/design-principles.md` (especially "compile contracts;
  interpret ambiguity" and "ledger, not vibes")

## Research And Theory

### Julian Jaynes And Compositional Identity

Source: [Julian Jaynes Society overview of Jaynes's theory](https://www.julianjaynes.org/about/about-jaynes-theory/overview/).

What Aphelion took:

- A self can be treated as composed from voices, roles, internalized authority,
  memory, metaphor, and social situation rather than as a single flat tool.
- Internal multiplicity can be useful when it is made legible and bounded.
- Agency becomes more honest when the system distinguishes initiating pressure,
  authority, evidence, and visible self-presentation.

Where Aphelion stops:

- Aphelion does not adopt Jaynes's bicameral-mind theory as a factual theory of
  machine consciousness.
- It does not ask the model to hallucinate voices, gods, commands, or hidden
  identities.
- The governor/face split is not a claim that there are two conscious agents.

Why Aphelion diverges:

- Aphelion uses compositional identity as engineering grammar: face,
  governor, durable children, memory, goals, and control records are different
  surfaces with different authority. Prompted identity can create presence and
  pressure, but code, leases, grants, sandbox policy, and TES remain the source
  of operational truth.

Current repo surface:

- `prompt/agency.go`
- `prompt/builder.go`
- `defaults/agent/IDOLUM.md`
- `docs/architecture/agency-evaluation-methodology.md`
- `docs/architecture/operator-presentation-contract.md`

### Behavioral Agency And Source Monitoring

Sources: Albert Bandura,
[Toward a Psychology of Human Agency](https://journals.sagepub.com/doi/10.1111/j.1745-6916.2006.00011.x);
Edward L. Deci and Richard M. Ryan,
[The support of autonomy and the control of behavior](https://pubmed.ncbi.nlm.nih.gov/3320334/);
Marcia K. Johnson, Shahin Hashtroudi, and D. Stephen Lindsay,
[Source Monitoring](https://memlab.yale.edu/sites/default/files/files/1993_Johnson_Hashtroudi_Lindsay_PsychBull.pdf);
Emanuel A. Schegloff, Gail Jefferson, and Harvey Sacks,
[The Preference for Self-Correction in the Organization of Repair in Conversation](https://www.cambridge.org/core/services/aop-cambridge-core/content/view/5549B861FDE7180B75FA5C382821875E/S009785077702150Xa.pdf/preference_for_selfcorrection_in_the_organization_of_repair_in_conversation.pdf);
and University of New Mexico CASAA,
[Motivational Interviewing Treatment Integrity code](https://casaa.unm.edu/tools/miti.html).

What Aphelion took:

- Agency can be observed through intentionality, forethought,
  self-reactiveness, and self-reflectiveness without reducing it to tool use.
- Autonomy support is different from pressure: good initiative preserves the
  operator's choice instead of smuggling permission through urgency or intimacy.
- Source monitoring gives a concrete vocabulary for separating observed state,
  memory, inference, desire, and unknowns.
- Conversation repair gives a behavioral target for fixing visible trouble
  without rewriting execution truth.
- Behavioral coding favors anchored ratings and line-level observations over a
  single "the prompt passed" score.

Where Aphelion stops:

- Aphelion does not import clinical, therapeutic, or human-consciousness claims
  into the runtime.
- It does not let behavioral scores weaken deterministic authority gates.
- It does not turn LLM judges into sources of permission or truth.

Why Aphelion diverges:

- Aphelion treats this literature as measurement grammar for prompt behavior.
  The ledger remains authoritative; agency evals measure whether the model is
  present, bounded, evidence-aware, repair-capable, and able to abstain when the
  ledger or evidence does not support action.

Current repo surface:

- `docs/architecture/agency-evaluation-methodology.md`
- `agency_eval.go`
- `prompt/agency.go`
- `prompt/golden_test.go`
- `agency_live_eval_test.go`

### GPT-5.5 Prompt Guidance And Context Engineering

Source: [OpenAI Prompt guidance](https://developers.openai.com/api/docs/guides/prompt-guidance)
and [Using GPT-5.5](https://developers.openai.com/api/docs/guides/latest-model).

What Aphelion took:

- Prompts should be outcome-shaped: role, goal, success criteria, output
  contract, and stop rules are more reliable than persona prose alone.
- Agentic eagerness is a variable to calibrate, not something to maximize or
  suppress globally.
- Long-context systems need explicit context packing: stable blocks, dynamic
  facts, cache boundaries, tool affordances, and evals for behavioral regressions.

Where Aphelion stops:

- Aphelion does not let provider guidance define authority policy.
- It does not collapse all prompting into one universal meta-prompt or one
  provider-specific style.
- Model-level proactivity does not replace leases, grants, approval records,
  sandbox policy, or execution evidence.

Why Aphelion diverges:

- Aphelion treats GPT-5.5 guidance as a prompt/compiler input. The runtime keeps
  agency high by giving the model a clear objective, envelope, evidence posture,
  open loops, and affordance map, while the code decides whether the next action
  is permitted.

Current repo surface:

- `prompt/builder.go`
- `prompt/agency.go`
- `provider/openai.go`
- `agency_live_eval_test.go`
- `docs/architecture/prompt-model-map.md`

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
