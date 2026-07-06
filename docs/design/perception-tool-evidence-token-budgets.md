# Perception, Tool Evidence, and Token Budgets

Status: local design note for the token-performance plan. The current branch
implements the provider-context digest/admission and accounting parts described
below; first-class persisted digest records and hard prompt-admission enforcement
remain future work.

## Problem

The current runtime already has context preflight, tool-result compaction, execution events, and status projections, but the admission rules are spread across prompt assembly, agent provider preflight, and ad-hoc status summarization. Expensive turns can still spend tokens on repeated raw tool evidence, over-large perception payloads, or unbounded retry/iteration context before the governor has made a typed decision about what evidence is necessary.

The goal is to reduce prompt/input token load without weakening the typed-records-over-prose stance. The ledger and execution-event timeline remain canonical; compact prompts are projections.

## Invariants

1. Evidence is never silently erased from canonical records to save prompt tokens.
2. Prompt admission is role-aware: governor gets authority/evidence needed for judgment; face gets only material needed to render the approved floor.
3. Tool outputs are stored as typed evidence records first, then summarized into prompt digests.
4. Budgets are explicit execution constraints, not model suggestions.
5. When evidence is omitted from a prompt for budget reasons, the prompt must include a typed digest and evidence reference so the model can ask for more or report uncertainty.
6. Budget exhaustion should produce a typed blocker or continuation proposal, not a hidden truncation.

## Implemented shape

### Provider-context tool evidence projection

Before provider calls, runtime estimates context pressure and projects large
tool-result messages into compact provider-context digests when needed. The
projection preserves raw evidence in the session history / canonical runtime
records and only changes what is admitted into the provider request.

The current digest shape includes:

- `projection_kind: tool_result_digest`
- source marker `provider_context_projection`
- original character count
- optional tool call id and tool name
- bounded key-fact excerpts

The projection is triggered by tool-result size and recent tool-output pressure.
Known fat tools such as `fetch_url`, `read_file`, `exec`, and
`request_approval` also lower the compaction threshold when their recent output
is large enough to predict context pressure.

### Runtime accounting

Turn budgets now track provider input and output token usage alongside model
iteration/tool-call pressure. `turn_runs`, `/status`, `/health trace`, and doctor
evidence can project:

- turn index
- tool input characters
- assistant output characters
- assistant/tool character ratio
- provider input/output tokens
- provider cache-read/cache-write tokens

These fields are accounting and diagnosis projections, not billing authority and
not canonical execution history.

### Stable prompt prefix and evidence admission

Stable governance/persona prompt material should appear before volatile runtime
awareness so provider prompt caches can hit. Anthropic receives explicit cache
breakpoints; the official OpenAI API uses automatic exact-prefix prompt caching
for long prompts, so it benefits from the same stable-prefix/dynamic-tail layout
without an OpenAI-specific transport cache-control field. Volatile objective,
evidence, signals, delivery mode, latest user text, and channel facts belong
late.

The evidence ledger is cheap-to-write and hydrate-on-demand. Ordinary turns carry
a compact evidence-ledger pointer plus the `evidence_hydrate` affordance; selected
evidence is pushed automatically only when typed recovery, continuation,
active-operation, or explicit recall pressure requires source-fact
fidelity. This keeps the ledger canonical without turning every prompt into an
audit replay.

## Proposed remaining shape

### 1. Tool evidence admission

Extend the current provider-context projection into a first-class
prompt-admission pass after tool execution and before the next model request. It
classifies each tool result as one of:

- `inline_required`: small/load-bearing evidence needed for immediate reasoning.
- `digest_required`: large evidence summarized with stable refs and bounded excerpts.
- `ref_only`: canonical record exists, but only reference metadata enters the prompt.
- `excluded_from_prompt`: irrelevant/noisy for the current turn; visible through status/doctor only.

Admission inputs: tool name, result size, MIME/kind, user objective, current phase, authority class, provider context headroom, and whether the result supports an explicit claim.

### 2. Typed persisted tool digests

Persist a `tool_evidence_digest` execution event or adjacent table record with:

- `digest_id`
- `run_id`, `turn_index`, `tool_call_id`, `tool_name`
- `source_event_id` / artifact refs
- `raw_size_bytes`, `raw_estimated_tokens`
- `digest_text`, `digest_tokens`
- `admission_class`
- `evidence_refs`
- `omitted_reason`
- `lossiness` (`none`, `bounded_excerpt`, `semantic_summary`, `ref_only`)
- `created_at`, `policy_version`

The current provider-context digest does not create stable digest IDs. The
future persisted form should cite digest IDs instead of reinserting full tool
output when possible.

### 3. Perception budget enforcement

Create a per-turn `PerceptionBudget` with separate ceilings:

- tool raw input bytes/tokens admitted to prompt
- tool digest tokens
- media/OCR/transcript tokens
- memory/operation/context tokens
- max model iterations/tool loops
- reserve headroom for final governor judgment and face rendering

Enforcement points:

- before tool output admission
- before each model request preflight
- before retry/failover loops
- before face render

If admission would exceed budget, store the full evidence canonically, admit a digest/ref, and emit a budget event. If the governor truly needs more, request a bounded continuation/approval.

### 4. Token-aware iteration budgets

Each turn run should carry an iteration budget record:

- `max_model_requests`
- `max_tool_calls`
- `max_input_tokens_estimated`
- `max_output_tokens`
- `reserved_final_tokens`
- `reserved_face_tokens`
- `budget_policy_version`

Runtime decrements budget from actual provider usage when available and from estimates otherwise. Tool loops stop before the prompt becomes too large, with a typed blocker such as `budget_exhausted_need_continuation`.

## Rollout phases

1. **Implemented soft projection/accounting**: provider-context digests,
   known-fat-tool anticipatory compaction, token-aware turn budget warnings, and
   status/doctor accounting projection.
2. **Digest persistence**: persist typed tool digests and references; prompts
   still include current raw evidence when policy allows.
3. **Hard admission**: enforce per-turn prompt budgets with explicit
   blockers/continuation proposals.
4. **Face lane integration**: face prompt consumes only material floor +
   scene-safe shared awareness + digest refs, never raw tool dumps.

## Validation

- Unit tests for provider-context digest rendering, admission counters, and
  compaction prediction.
- Unit tests for future admission classes and digest schema normalization.
- Golden prompt tests showing large tool results become digest refs, not raw dumps.
- Runtime tests that budget exhaustion creates typed blocker/status, not silent truncation.
- Eval cost-fidelity reports that track estimated prompt tokens, model-call
  count, cache-eligible stable prefixes, and stable-prefix hash stability before
  paid provider canaries.
- Telemetry checks: prompt estimated tokens, actual provider input tokens, cache-read ratio, digest token savings, and retry count.
- Regression tests that execution-event evidence remains accessible through status/doctor even when omitted from prompt.

## Gates

Remaining implementation should be split into small, reviewable changes:

1. typed digest persistence and status projection
2. soft prompt admission with golden tests
3. hard budget enforcement

Deploy/restart remains a separate approval after merge. No schema migration that drops raw evidence should be accepted for this plan.
