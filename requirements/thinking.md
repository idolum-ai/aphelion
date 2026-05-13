# Thinking — Reasoning Effort, Summaries, and Budgeting

## Overview

Thinking controls how much deliberation the governor uses for a turn.

Thinking is a governor concern, not a face concern.

The useful pattern here is closer to Codex than to a vague "smart mode":

- **reasoning effort** controls depth/latency/cost
- **reasoning summary** controls whether the runtime keeps a compact external artifact of that deeper reasoning

Hermes is a decent reference for staged, practical reasoning controls. Codex adds the more important shape: reasoning effort should be an explicit knob with stable levels, and summary policy should be separate from effort.
See [`docs/architecture/influences-and-departures.md`](../docs/architecture/influences-and-departures.md)
for the attribution and departure record behind these comparisons.

## Scope

### Required

- explicit reasoning effort setting for governor-backed inference
- stable effort levels
- per-run defaults
- no raw chain-of-thought exposure

### Deferred

- provider-specific reasoning adapters
- richer reasoning summary storage
- dynamic reasoning escalation within a turn
- richer UI controls for live reasoning overrides

## Core Model

Thinking is not personality.

It is a runtime budget/control decision that trades off:

- latency
- cost
- depth
- tool-call discipline

The governor may think harder for complex work, but the face should not treat that as a style setting.

## Reasoning Effort

Supported effort levels should be:

- `none`
- `low`
- `medium`
- `high`
- `xhigh`

These levels should map onto provider-native reasoning controls where available. If a backend does not support them directly, the runtime may approximate them through model selection or fallback policy, but the visible contract should stay stable.

`none` means no extra reasoning budget beyond ordinary completion behavior. It is a real setting, not merely "use the default".

## Reasoning Summary

Reasoning summary is separate from reasoning effort.

Supported modes should include:

- `none`
- `auto`
- `compact`

### `none`

Do not persist any external reasoning summary artifact for the turn.

### `auto`

Allow the runtime/provider to retain a minimal reasoning summary artifact when it helps auditability or later debugging.

### `compact`

Persist a bounded reasoning summary intended for operator inspection, not for replay as hidden thought.

Reasoning summary must never mean exposing raw internal chain-of-thought to the user or re-injecting it as ordinary conversation history.

## Run-Kind Defaults

Thinking should vary by run kind.

### Default turns

- default `medium`
- escalate to `high` when explicitly configured or when a stronger provider/model is selected
- a narrow runtime-owned operator toggle may temporarily raise interactive/recovery effort from `medium` to `high` without rewriting the base config

### Heartbeat turns

- default `low` or `medium`
- enough to reflect, summarize, and choose silence well
- not so high that maintenance turns become wasteful

### Cron turns

- default `low`
- jobs should stay narrow and procedural unless explicitly configured otherwise

### Subagent turns

- default `medium`
- leaf workers may use `low` for straightforward tasks
- orchestrator workers may justify `high`

## Provider Mapping

Reasoning effort should be expressed once in Aphelion and mapped per backend.

### Codex / OpenAI-style backends

Codex-style reasoning effort is the clearest model:

- explicit effort level
- optional reasoning summary policy
- per-mode defaults

Aphelion should follow that shape for its governor contract even when the active governor backend is not Codex.

### Anthropic and other native providers

Where the provider exposes a native "thinking" mode or budget, the runtime should translate the configured effort level into the closest supported request shape.

### Unsupported providers

If a provider does not support reasoning controls, the runtime should:

- preserve the public effort setting
- log or audit the downgrade
- avoid pretending that unsupported reasoning controls were applied

## Security and Privacy

Raw chain-of-thought is not a user-facing artifact.

Rules:

- do not reveal hidden reasoning by default
- do not store raw reasoning as visible transcript
- do not inject raw reasoning into memory
- if summaries are stored, they must be bounded and explicitly classified as reasoning artifacts

Thinking is a control surface, not a license to leak internals.

## Config Surface

See `config.md`, but the intended shape should preserve:

```toml
[thinking]
effort = "medium"           # none | low | medium | high | xhigh
summary = "auto"            # none | auto | compact

[thinking.defaults]
default = "medium"
heartbeat = "low"
cron = "low"
subagent = "medium"
```

Plan-mode or operator-specific overrides may be added later, but these are the core knobs.

## Runtime Override Layer

Aphelion may also expose a narrow runtime-owned override layer for operator controls.

For the current system shape, that layer is intentionally small:

- interactive/recovery governor effort is operator-selectable through the admin-only `/model` slot controls across `low|medium|high|xhigh`
- heartbeat and cron should continue to use their own lower defaults

This remains a hardcoded application recipe surface, not yet a configurable recipe framework.

## Decisions

- **Thinking is governor-owned.** Idolum does not control reasoning depth.
- **Effort and summary are separate.** One controls depth; the other controls external artifact policy.
- **Stable effort levels matter.** Aphelion should present one cross-backend reasoning vocabulary.
- **Per-run defaults matter.** Heartbeat and cron should not silently inherit expensive reasoning settings meant for interactive turns.
- **No raw chain-of-thought exposure.** Auditability must not collapse into prompt leakage.

## Test Plan

- **TestReasoningEffortDefaultByRunKind**: different run kinds receive the configured default effort
- **TestReasoningSummarySeparateFromEffort**: summary policy can vary without changing effort level
- **TestCodexReasoningEffortMapping**: Codex backend receives the expected effort setting
- **TestUnsupportedProviderReasoningDowngrade**: unsupported providers degrade honestly rather than pretending the setting applied
- **TestReasoningSummaryNotStoredAsVisibleTranscript**: summaries remain sidecar artifacts, not ordinary chat history
- **TestIdolumPromptOmitsReasoningControls**: face prompt does not own or redefine governor reasoning policy
