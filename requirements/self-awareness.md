# Self-Awareness — How Aphelion Knows Itself

## Overview

Aphelion should not infer its own operating reality from scattered hints.

It should be told, explicitly and machine-authored, what it is, where it is, what it can do, and what kind of turn it is in.

This spec defines the system's **self-awareness surface**: the runtime facts that become model-visible so `Aphelion` and `Idolum` can reason correctly about the system they are operating inside.

## Telos

The goal is not theatrical self-description.

The goal is:

- correct self-knowledge
- reduced tool misuse
- reduced false self-description
- clean separation between machine truth and operator-authored soul/style
- auditability of what the model was told about the runtime

This matches Aphelion's broader telos:

- machine-authored floor over inferred ambient behavior
- governor authority over face appearance
- local durable truth over hidden, drifting state

## Core Rule

Aphelion should know itself through **machine-authored self-description**, not through inference.

That self-description must be:

- machine-generated
- turn-specific where needed
- auditable
- cache-aware
- non-overridable by workspace files

Workspace files may shape identity, temperament, and policy preference. They must not redefine runtime truth.

## Self-Awareness Layers

Aphelion's self-awareness has three layers.

### 1. Constitutional awareness

Stable facts about what the system is.

Examples:

- the governor is `Aphelion`
- the default face is `Idolum`
- the governor decides and acts
- the face proposes and renders
- tools are machine-owned reality

These facts belong in machine-owned prompt headers and constitutional specs, not in mutable operator memory.

### 2. Runtime awareness

Facts about the current operating state of this turn.

Examples:

- governor backend
- provider and model
- session kind
- run kind
- whether planning brokerage is active
- principal role
- channel
- capabilities
- sandbox mode
- writable roots
- available tools
- reasoning mode
- active runtime effort recipes when temporary operator overrides are in effect
- delivery mode

These facts should be injected every turn in a machine-authored runtime block.

### 3. Recalled operational context

Facts from prior runs or recovery analysis that matter now.

Examples:

- interrupted prior turn
- degraded provider path used recently
- pending review delivery
- prior fallback or recovery note

This is not part of constitutional identity. It is machine-authored contextual awareness and should be fenced or otherwise clearly scoped.

## Required Awareness Domains

At minimum, the governor should be made aware of:

### Identity

- current layer: governor or face
- current name: `Aphelion` or `Idolum`
- whether this is an interactive, heartbeat, cron, recovery, or subagent turn

### Runtime

- active governor backend
- active inference provider
- active model
- reasoning effort and summary mode
- current runtime persona/governor effort recipes when toggled
- stream vs non-stream behavior

### Authority

- principal role
- session ownership
- writable vs read-only roots
- approval/escalation policy
- whether tools are enabled at all

### Tools

- actual tool manifest for this turn
- run-kind restrictions
- current sandbox/execution profile

### Memory

- which memory layers are loaded
- whether recalled context was injected
- whether memory is shared, operator-scoped, or principal-scoped
- whether semantic retrieval results were injected, and from which source class

### Delivery

- channel
- output capabilities
- whether the reply will be rendered by `Idolum`, passed through, voiced, or kept silent
- whether supported media is attached to the current turn
- whether that media is being handled as vision input or extracted document text
- Idolum's suggested turn mode and Aphelion's ratified turn mode when brokerage is active

## Governor vs Idolum

Self-awareness is not identical across layers.

### Governor

`Aphelion` should receive the full machine-owned operating picture needed for correct action.

That includes:

- authority
- tools
- sandbox
- runtime/provider facts
- delivery mode
- recovery/degradation state

### Idolum

`Idolum` should receive only the subset needed to speak coherently and honestly.

That usually includes:

- channel
- visible delivery mode
- whether the turn is degraded
- canonical governor reply
- user-visible context

It should not receive raw tool schemas, root maps, or other deep execution details unless they matter for honesty in the reply.

## Prompt Placement

Self-awareness should be split between:

### Stable machine header

Cache-friendly facts that do not change often:

- constitutional roles
- broad authority rules
- stable tool-governance rules

### Dynamic runtime block

Turn-specific facts:

- backend
- model
- principal
- run kind
- channel
- capabilities
- active roots
- stream/degraded/fallback state
- retrieved semantic context, when present, labeled as retrieval rather than constitutional truth

This dynamic runtime block belongs after the stable cache boundary where needed, but before volatile user/session material that would make it hard to reason about the current turn.

## What Must Not Define Self-Awareness

These may influence behavior, but they are not the source of runtime truth:

- `SOUL.md`
- `IDENTITY.md`
- `AGENTS.md`
- `TOOLS.md`
- `IDOLUM.md`
- `MEMORY.md`
- daily notes

They are operator-authored surfaces, not machine truth.

## Reliability and Recovery

Self-awareness must include degraded-state truth when relevant.

Examples:

- Codex failed and native fallback is active
- Anthropic failed and OpenRouter is active
- face rendering failed and governor passthrough is active
- previous turn was interrupted

The system should know when it is degraded. It should not merely behave differently and leave the model guessing why.

## Subagents

Subagents should receive a narrower self-awareness surface than the main session.

They must know:

- their role
- their parent relationship
- their authority ceiling
- their run kind
- their available tools and roots

They must not inherit unnecessary global runtime detail by default.

## Config and Audit

The implementation should make it possible to inspect:

- effective config path
- prompt root
- exec root
- active governor backend
- active provider/model
- active fallback/degraded state
- loaded prompt files
- active tool manifest

This should be available through operator diagnostics and reflected in logs or status surfaces.

## Decisions

- **Self-awareness is a first-class system concern.**
- **Runtime truth is machine-authored.**
- **Operator files shape character, not machine reality.**
- **`Aphelion` should know its own authority and limits explicitly.**
- **`Idolum` should know enough to speak honestly, not enough to pretend it governs execution.**
- **Degraded mode must be legible to the system.**

## Test Plan

- **TestGovernorPromptIncludesRuntimeAwarenessBlock**: governor receives machine-authored runtime facts for the turn
- **TestIdolumPromptGetsDeliveryAwarenessOnly**: face prompt gets delivery/degraded context without raw execution internals
- **TestWorkspaceFilesCannotOverrideRuntimeTruth**: operator files do not replace machine runtime facts
- **TestFallbackStateIsVisibleToGovernor**: degraded provider state is injected when fallback is active
- **TestSubagentAwarenessIsNarrowed**: subordinate sessions receive reduced self-awareness scoped to their role
