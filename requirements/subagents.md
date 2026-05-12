# Subagents — First-Class Sessions, Capability Depth, and Delegation

## Overview

Subagents in Aphelion are first-class subordinate sessions, not just invisible helper calls.

They are used for:

- delegated focused tasks
- parallel workstreams
- bounded research or implementation slices
- later, longer-lived subordinate runs when needed

Aphelion should borrow the useful shape from both Hermes and OpenClaw:

- Hermes: task-focused delegated children with fresh context and restricted toolsets
- OpenClaw: first-class subagent sessions with lifecycle, capability depth, and control boundaries

See [`docs/architecture/influences-and-departures.md`](../docs/architecture/influences-and-departures.md)
for the attribution and departure record behind these references.

The result should be:

- real child sessions
- default light context
- inherited authority ceiling
- explicit parent/child linkage
- clear completion and cleanup semantics

## Scope

### Required

- first-class child session identity
- governor-only spawning
- bounded task delegation
- parent/child linkage
- light-context spawn by default
- completion result returned to parent

### Deferred

- persistent named child agents
- child-specific identities beyond generic subordinate sessions
- subagent-to-subagent orchestration beyond configured depth
- cross-channel child delivery

## Core Principles

1. **Subagents are real sessions.**
   They have their own session identity and lifecycle.
2. **Subagents are subordinate.**
   They do not outrank or bypass the parent governor.
3. **Context should be narrow by default.**
   The default is task-focused context, not a full parent mind fork.
4. **Authority cannot increase across delegation.**
   A child must never exceed the parent principal’s ceiling.
5. **Completion flows back upward.**
   The parent governor remains the integrator of child results.

## Session Model

Every spawned subagent should have its own session identity.

Minimum fields:

- `session_id`
- `parent_session_id`
- `spawn_depth`
- `subagent_role`
- `control_scope`
- `principal_role`
- `status`
- `created_at`
- `updated_at`

Suggested roles:

- `main`
- `orchestrator`
- `leaf`

Suggested control scopes:

- `children`
- `none`

This follows the useful OpenClaw idea that depth and control scope are explicit runtime concepts, not just prompt lore.

## Capability by Depth

The system should resolve subagent capabilities from depth and role.

### Main

- top-level session
- may spawn children
- may control children

### Orchestrator

- spawned child that may still spawn further children
- may control its own children
- subject to max-depth limits

### Leaf

- spawned child that may not spawn further children
- no child-control capabilities

The exact names may evolve, but the concept should remain.

## Spawning

Only the governor may spawn subagents.

The face may describe or suggest delegation, but it does not directly spawn.

Spawn request should include:

- task
- optional label
- optional model override
- optional thinking mode
- optional timeout
- optional sandbox mode
- optional attachments
- whether light context is requested

Example target shape:

```go
type SpawnRequest struct {
    Task              string
    Label             string
    LightContext      bool
    ModelOverride     string
    Timeout           time.Duration
    AllowSpawnChildren bool
}
```

## Context Model

Default behavior should be **light context**.

That means the child gets:

- delegated task
- relevant context summary
- relevant file or workspace hints
- bounded tool surface
- principal role and authority ceiling

By default, the child should not get:

- full parent transcript
- full workspace memory
- unrelated prompt files
- full hidden audit state

This aligns with Hermes’ delegated-worker style and reduces prompt noise.

### Fuller inheritance

Richer inheritance may exist later, but it should be explicit and exceptional.

## Tool Surface

Subagents should receive a restricted tool surface.

At minimum, children should not get:

- user-clarification tools
- direct outbound messaging
- shared-memory mutation by default
- unrestricted recursive delegation

Recursion should be controlled by explicit depth and capability rules, not by trust alone.

## Principal Ceiling

Subagents inherit the principal ceiling of the parent session.

That means:

- admin parent -> child may still be admin-scoped, but only within delegated boundaries
- approved-user parent -> child remains approved-user scoped and isolated

Delegation must never become a privilege-escalation path.

This rule matters more than the subagent’s prompt identity.

## Workspace and Sandbox

Subagents should inherit or resolve a workspace/sandbox context consistent with the parent.

### Admin parent

- child may inherit admin workspace/sandbox profile
- task-level restrictions still apply

### Approved-user parent

- child must stay inside the parent’s isolated roots
- child must not gain access to global writable roots
- child must inherit the non-admin sandbox ceiling

## Lifecycle

Subagent lifecycle should be explicit:

1. parent governor issues spawn request
2. child session record is created
3. child begins running with its own turn budget
4. child reports progress optionally
5. child completes, fails, or times out
6. completion artifact is attached to parent context
7. child session is retained or cleaned up according to policy

## Completion Model

The parent should receive a completion artifact, not the child’s entire internal transcript by default.

Completion should include:

- status
- concise result summary
- files changed or created
- errors encountered
- optional child session reference

The parent may later inspect or replay the child session if needed, but that should not be the default prompt path.

## Announcements and UX

For interactive channels, subagent progress may be surfaced as bounded status updates.

Useful patterns:

- spawn acknowledgment
- progress summaries
- completion notice

But the parent should avoid polling loops when the runtime can push completion events.

## Persistence and Audit

Subagent activity should be durable.

Persist at least:

- parent/child linkage
- spawn parameters
- resolved model/backend
- spawn depth and role
- completion status
- completion summary
- timeout/failure reason

The child’s own session transcript remains independently inspectable.

## Failure Model

Subagent failure is normal.

Examples:

- spawn denied by depth
- spawn denied by authority
- child timed out
- child sandbox denied
- child model/provider failed

Failures should return explicit completion artifacts to the parent, not vanish.

## Relationship to Sessions and Reviews

Subagents are sessions, but they are not ordinary peer conversations.

They should:

- have their own session identity
- remain linked to the parent session
- avoid polluting the parent’s visible transcript with raw child internals

Completion summaries belong in parent-visible flow. Full child transcripts remain inspectable sidecar state.

## Config Surface

See `config.md`, but the intended ownership includes:

```toml
[subagents]
enabled = true
max_spawn_depth = 2
max_concurrent = 3
default_timeout = "10m"
default_light_context = true

[subagents.capabilities.main]
can_spawn = true
can_control_children = true

[subagents.capabilities.orchestrator]
can_spawn = true
can_control_children = true

[subagents.capabilities.leaf]
can_spawn = false
can_control_children = false
```

## Decisions

- **Subagents are first-class sessions.**
- **Light context is the default.**
- **Authority cannot increase through delegation.**
- **Depth and control scope should be explicit runtime concepts.**
- **Parent sees completion artifacts first, not full child internals.**
- **OpenClaw-style lifecycle plus Hermes-style focused delegation is the right blend.**

## Test Plan

- **TestSpawnCreatesChildSession**: spawning creates a durable child session linked to the parent
- **TestChildInheritsPrincipalCeiling**: child cannot exceed parent authority
- **TestApprovedUserChildStaysIsolated**: non-admin child stays inside isolated roots/sandbox
- **TestLightContextDefault**: child starts with delegated task context, not full parent transcript
- **TestLeafCannotSpawnChildren**: leaf capability prevents recursive spawn
- **TestCompletionArtifactReturnsToParent**: parent receives bounded completion summary
- **TestChildFailureReturnsExplicitResult**: timeout or failure returns explicit completion artifact
- **TestChildTranscriptRemainsInspectable**: full child session can be inspected separately from the parent transcript
