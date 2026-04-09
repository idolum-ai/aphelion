# System Prompt — Governor Base, Host Face, and Dynamic Updates

## Overview

Aphelion does not use one undifferentiated prompt blob.

Prompt assembly has two distinct targets:

- **governor prompt**
- **face prompt**

The governor prompt carries authority, execution reality, and tool policy. The face prompt carries interaction style and rendering guidance.

The governor prompt defines **Aphelion**. The default face prompt defines **Host**. Host may vary in tone or style over time, but it must not replace or contradict the governor's identity.

## Governor Prompt

The governor prompt should be assembled in layers.

### Stable prefix

1. machine-generated authority block
2. stable workspace files
3. machine-generated tool manifest
4. optional workspace `TOOLS.md`

### Dynamic tail

5. dynamic workspace files
6. turn-local machine updates
7. session history

The stable prefix should be as byte-stable as possible to support provider-side prompt caching and deterministic behavior.

## Authority Block

The top of the governor prompt must be machine-owned and non-negotiable.

It should state:

- resolved principal role
- active governor backend
- writable vs read-only roots
- whether tools are available
- the rule that prompt text cannot override code-enforced permissions

This block must appear before any workspace-authored file content.

## Stable Workspace Files

Stable files are operator-authored and rarely changed:

- `SOUL.md`
- `IDENTITY.md`
- `AGENTS.md`
- optional operator `USER.md`

`USER.md` in v0 should be treated as operator/admin profile, not as shared per-user memory.

`SOUL.md` should primarily define `Aphelion` as the governor identity. Face-specific tone belongs in the face prompt, not in the governor's constitutional self-model.

## Tool Guidance

Governor tool guidance is assembled from two parts:

1. machine-generated manifest from the actual registry
2. optional workspace `TOOLS.md`

The manifest is authoritative. `TOOLS.md` is advisory.

## Dynamic Files

Dynamic files belong after the cache boundary:

- `MEMORY.md`
- daily notes
- `HEARTBEAT.md`

These files may be durable on disk while still being dynamic in prompt placement.

## Turn-Local Updates

Aphelion should support machine-generated dynamic updates rather than rebuilding every instruction as one giant blob.

Examples:

- authority or root changes
- tool availability changes
- collaboration-mode-like changes
- realtime/heartbeat notices

This mirrors the useful part of Codex's approach: keep a stable base, add explicit updates for changing machine state.

## Face Prompt

The face prompt is separate from the governor prompt.

It should receive:

- canonical governor reply
- channel information
- interaction style
- face-specific identity and anti-drift rules

The face prompt should not receive tool definitions or permission rules as if they were its responsibility.

### Face layers

The default face prompt should be assembled in layers.

1. machine-generated face header
2. stable face files
3. dynamic face files
4. canonical governor reply
5. latest user message
6. channel/rendering context

The machine-generated face header should state that:

- the face is `Host`
- `Host` is presentational, not sovereign
- `Aphelion` remains the decision core

### Stable face files

- `HOST.md`

### Dynamic face files

- `QUESTIONS-TO-HOST.md`

These files are face-only and must not be loaded into the governor prompt.

## Transcript Boundary

The session ledger primarily stores the user-visible transcript.

Review digests, bot notices, and face-rendered replies should enter the session history as conversation items. They should not be silently merged into the governor prompt as hidden memory.

Canonical governor artifacts may later be stored alongside the session for audit, but they are not a replacement for the visible ledger.

The replay rule is:

- visible transcript replays rendered replies
- canonical governor artifacts remain sidecar audit state

## Config Surface

See `config.md`, but prompt-related ownership should include:

- bootstrap file list
- dynamic file list
- tool-manifest inclusion
- face rendering profile
- face file lists
- cache-boundary rules

## Decisions

- **Machine-owned instructions come first.** Authority and permissions must outrank workspace files.
- **Stable and dynamic content are separate by design.** This is for both clarity and cache behavior.
- **Governor and face prompts are different artifacts.** They should not be collapsed into one text blob once the architecture is split.
- **`Aphelion` belongs to the governor layer.** Face personas may vary without replacing the governor's identity.
- **`Host` is the default face.** It owns presentation, not authority.
- **`USER.md` is operator-facing in v0.** Per-user memory belongs elsewhere.
- **Face files are separate from governor files.** `HOST.md` and `QUESTIONS-TO-HOST.md` must not leak into governor authority.

## Test Plan

- **TestAuthorityBlockPrecedesWorkspaceFiles**: machine authority block is first in governor prompt
- **TestToolManifestPrecedesToolsMD**: machine-generated tool manifest appears before advisory `TOOLS.md`
- **TestDynamicFilesAfterCacheBoundary**: `MEMORY.md` and `HEARTBEAT.md` appear after stable sections
- **TestUserMDTreatedAsOperatorProfile**: global `USER.md` is not treated as shared mutable user memory
- **TestFacePromptOmitsToolDefinitions**: face prompt does not include tool schemas or authority rules
- **TestFacePromptLoadsHostFilesOnly**: `HOST.md` and `QUESTIONS-TO-HOST.md` are loaded into the face prompt and excluded from the governor prompt
- **TestReviewDigestStoredAsHistoryNotHiddenPrompt**: delivered review digest enters conversation history instead of hidden prompt state
- **TestVisibleReplayUsesRenderedReply**: visible session replay uses the delivered face-rendered reply rather than the canonical sidecar artifact
