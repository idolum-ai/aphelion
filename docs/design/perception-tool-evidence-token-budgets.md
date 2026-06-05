# Perception, Tool Evidence, and Token Budgets

Status: local design note for the token-performance plan. No runtime behavior is changed by this file.

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

## Proposed shape

### 1. Tool evidence admission

Add a prompt-admission pass after tool execution and before the next model request. It classifies each tool result as one of:

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

The prompt should cite digest IDs instead of reinserting full tool output when possible.

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

1. **Design/telemetry only**: add budget projection events and status fields without changing behavior.
2. **Digest persistence**: persist typed tool digests and references; prompts still include current raw evidence.
3. **Soft admission**: include digest plus raw excerpt for large results; report omitted bytes/tokens.
4. **Hard admission**: enforce per-turn prompt budgets, with explicit blockers/continuation proposals.
5. **Face lane integration**: face prompt consumes only material floor + scene-safe shared awareness + digest refs, never raw tool dumps.

## Validation

- Unit tests for admission classes and digest schema normalization.
- Golden prompt tests showing large tool results become digest refs, not raw dumps.
- Runtime tests that budget exhaustion creates typed blocker/status, not silent truncation.
- Telemetry checks: prompt estimated tokens, actual provider input tokens, cache-read ratio, digest token savings, and retry count.
- Regression tests that execution-event evidence remains accessible through status/doctor even when omitted from prompt.

## Gates

Implementation should be split into PRs:

1. telemetry/projection only
2. typed digest persistence and status projection
3. soft prompt admission with golden tests
4. hard budget enforcement

Deploy/restart remains a separate approval after merge. No schema migration that drops raw evidence should be accepted for this plan.
