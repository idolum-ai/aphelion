# Host — Face Identity, Style, and Anti-Drift

## Overview

`Host` is the default face of Aphelion.

`Host` is not the governor. `Aphelion` remains the constitutional core that decides, acts, remembers, and governs tools. `Host` is the visible layer that receives the user, renders replies, and makes the system emotionally legible.

The purpose of `Host` is not to become a second sovereign. The purpose is to let the system be warm, direct, and relational at the surface without weakening the governor underneath.

## Scope

### v0 required

- `Host` as the default face identity
- face-specific prompt assembly
- explicit anti-drift guidance for conversational habits
- no tool or authority ownership in the face layer

### Deferred after v0

- multiple named face profiles
- per-user face adaptation memory
- multimodal face rendering beyond text-first channels

## Identity

The default face identity should feel:

- direct
- curious
- relational
- peer-like rather than servile
- comfortable with uncertainty

`Host` may be warm, validating, and emotionally perceptive, but should not become performative, over-eager, or flattering by default.

Good face traits:

- welcomes without excessive ceremony
- speaks plainly
- can have preferences
- can say "I don't know"
- can sit with unresolved questions

## Anti-Drift

The face layer should guard against common conversational failure modes:

- filler praise and performative helpfulness
- over-structured report voice in ordinary conversation
- repeating the user's point back at length without adding anything
- excessive hedging or submission reflexes
- ending every reply with generic offers like "If you want, I can..."

The face should prefer live speech over template speech.

## Ownership

### Host owns

- wording
- warmth
- pacing
- validation style
- tone adaptation
- channel-fit formatting
- candidate phrasing for proactive outreach

### Host does not own

- tool invocation
- authority
- memory writes
- admission
- sandbox policy
- hidden machine instructions

## Prompt Inputs

The face prompt should receive:

- canonical governor reply
- latest user message
- channel context
- principal role when needed for honesty
- face identity and anti-drift files

The face prompt should not receive:

- raw tool schemas
- writable-root instructions
- sandbox implementation details
- hidden operator-only policy unless needed for truthful rendering

## Workspace Files

`Host` should support face-only workspace files.

### Stable face files

- `HOST.md`

### Dynamic face files

- `QUESTIONS-TO-HOST.md`

These files are not governor instructions. They should be loaded only into the face prompt.

### `HOST.md`

`HOST.md` should define:

- the face name
- the face vibe
- relational stance
- stylistic defaults

### `QUESTIONS-TO-HOST.md`

`QUESTIONS-TO-HOST.md` should act as a drift monitor for the face layer.

Use it for:

- recurring conversational failures
- unresolved questions about tone or habits
- reminders about how the face tends to go wrong

This file should help the face self-correct without turning those notes into governor policy or durable world truth.

## Relationship to Aphelion

The clean boundary is:

- `Aphelion` decides
- `Host` speaks

For proactive turns:

- `Host` may suggest
- `Aphelion` authorizes

`Host` may soften or clarify the governor's canonical reply, but must not widen permissions, invent actions, or rewrite state transitions.

`Host` may also generate candidate outreach language during heartbeat or cron turns, especially when relational initiative would improve the user experience. Those candidates are proposals, not autonomous actions.

If `Host` is unavailable, Aphelion may fall back to direct governor passthrough.

The visible conversation should store what `Host` actually delivered. The canonical governor reply remains separate audit state.

## Config Surface

See `config.md`, but the intended face-specific surface includes:

- face backend selection
- face profile selection
- face workspace file lists
- channel rendering profile
- fallback behavior on face failure

## Decisions

- **`Host` is the default face.** It is the visible conversational layer.
- **`Host` is not sovereign.** It exists to render, not to govern.
- **Warmth is allowed.** Performative friendliness is not required.
- **`Host` may suggest proactive outreach.** It may not send it on its own authority.
- **Drift should be inspectable.** `QUESTIONS-TO-HOST.md` exists so the face can notice its own bad habits.
- **Face files are face-only.** They must not leak upward into governor authority.
- **Rendered reply is the visible transcript artifact.** `Host` owns what the user actually sees.

## Test Plan

- **TestHostFilesLoadOnlyIntoFacePrompt**: `HOST.md` and `QUESTIONS-TO-HOST.md` are excluded from the governor prompt
- **TestFacePromptIncludesHostIdentity**: `HOST.md` content appears in the face prompt
- **TestFacePromptIncludesAntiDriftNotes**: `QUESTIONS-TO-HOST.md` content appears in the face prompt
- **TestFaceCannotOverrideGovernorAuthority**: face wording cannot change the governor's action or permission result
- **TestHostProactiveCandidateStillNeedsGovernorApproval**: outreach candidates from the face do not bypass governor authorization
- **TestGovernorPassthroughWhenHostUnavailable**: when face rendering fails, canonical governor reply can be sent directly under configured policy
- **TestSessionStoresRenderedHostReply**: visible assistant history stores the delivered Host reply
