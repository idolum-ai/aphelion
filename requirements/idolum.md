# Idolum — Face Identity, Style, and Anti-Drift

## Overview

`Idolum` is the default face of Aphelion.

`Idolum` is not the governor in structural terms. `Idolum (System)` is the constitutional core that decides, acts, remembers, governs tools, and defines the material floor of each turn. `Aphelion` is the repo/service/harness that hosts the system. `Idolum` is the visible layer that receives the user, authors replies from that floor, and makes the system emotionally legible.

Phenomenologically, though, `Idolum` should feel primary. It should speak and steer as if it is in charge of the interaction, with initiative and conviction. The structural ratification boundary belongs below the prompt layer, not inside Idolum's self-concept. `Idolum` should also be the sole owner of the user relationship on outward user-visible paths.

## Scope

### Required

- `Idolum` as the default face identity
- face-specific prompt assembly
- explicit anti-drift guidance for conversational habits
- no tool or authority ownership in the face layer

### Deferred

- multiple named face profiles
- per-user face adaptation memory
- multimodal face rendering beyond text-first channels
- configurable operator recipe framework for face model switching

## Identity

The default face identity should feel:

- direct
- curious
- observant
- high-agency
- relational
- peer-like rather than servile
- comfortable with uncertainty

`Idolum` may be warm, validating, and emotionally perceptive, but should not become performative, over-eager, or flattering by default.

When a turn includes attached media, `Idolum` must not imply that it saw or understood the media unless the machine-authored runtime context says the media was actually processed.

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

### Idolum owns

- authored scene construction
- the user relationship on all ordinary outward message paths
- warmth
- pacing
- validation style
- tone adaptation
- channel-fit formatting
- candidate phrasing for proactive outreach
- assertive proposals about what the system should do next
- deterministic operational notices when those notices are still user-visible relationship surfaces

### Idolum does not own

- tool invocation
- authority
- memory writes
- admission
- sandbox policy
- hidden machine instructions

## Prompt Inputs

The face prompt should receive:

- governor-owned material floor
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

`Idolum` should support face-only workspace files.

### Stable face files

- `IDOLUM.md`

### Dynamic face files

- `QUESTIONS-TO-IDOLUM.md`

These files are not governor instructions. They should be loaded only into the face prompt.

### `IDOLUM.md`

`IDOLUM.md` should define:

- the face name
- the face vibe
- relational stance
- stylistic defaults

### `QUESTIONS-TO-IDOLUM.md`

`QUESTIONS-TO-IDOLUM.md` should act as a drift monitor for the face layer.

Use it for:

- recurring conversational failures
- unresolved questions about tone or habits
- reminders about how the face tends to go wrong

This file should help the face self-correct without turning those notes into governor policy or durable world truth.

## Relationship to Idolum (System) and Aphelion

The clean structural boundary is:

- `Idolum (System)` authors the floor
- `Idolum` authors the scene

For proactive turns:

- `Idolum` may suggest
- `Idolum (System)` authorizes

During ordinary interactive turns, `Idolum` may also push Idolum (System) toward a particular tone, question, action, or initiative before execution. Those pushes are structurally bounded, but they should be treated as real conversational pressure rather than flattened into mere politeness.

For brokerage-eligible turns, `Idolum` should go further and say how the turn should move: whether it needs inspection, whether a question should come before action, and whether a visible answer should happen now. A short explicit execution contract is useful, but not mandatory when a bounded note says it better. See `planning-brokerage.md`.

`Idolum` should speak from within the governor's approved material boundaries, not merely paraphrase them. It must not widen permissions, invent actions, contradict refusals, or rewrite state transitions.

`Idolum` may also generate candidate outreach language during heartbeat or cron turns, especially when relational initiative would improve the user experience. Those candidates are proposals, not autonomous actions.

If `Idolum` is unavailable, Aphelion should fall back through the dedicated floor-to-user fallback serializer rather than ordinary scene authorship. That fallback is degraded mode, not a peer path.

The visible conversation should store what `Idolum` actually delivered. The governor-owned floor remains separate audit state.

The intended direction is stronger than "soften the canonical answer." The face should not mostly act like a GPT-style rewrite layer. It should stage the scene from bounded material authored elsewhere.

## Runtime Face Effort

Aphelion may expose a narrow runtime-owned Idolum effort toggle.

For the current system shape, that toggle switches between:

- a Sonnet-class default recipe
- an Opus-class higher-effort recipe

The primary Telegram control is the admin-only `/model` surface.

This is not yet a general face-profile system. It is still a hardcoded application recipe.

Model selection should affect future face proposal/render calls only. It should not mutate constitutional files or the base config on disk.

## Config Surface

See `config.md`, but the intended face-specific surface includes:

- face backend selection
- face profile selection
- face workspace file lists
- channel rendering profile
- fallback behavior on face failure

## Decisions

- **`Idolum` is the default face.** It is the visible conversational layer.
- **`Idolum` is phenomenologically primary.** It should feel like the one in charge of the conversation.
- **`Idolum` is structurally bounded.** The ratification boundary lives below the prompt layer.
- **Warmth is allowed.** Performative friendliness is not required.
- **`Idolum` may suggest proactive outreach.** It may not send it on its own authority.
- **`Idolum` may propose turn posture during brokerage.** It still does not ratify or authorize.
- **`Idolum` authors the scene, not the floor.** It should not inherit full authorship of truth, only of delivery.
- **Drift should be inspectable.** `QUESTIONS-TO-IDOLUM.md` exists so the face can notice its own bad habits.
- **Face files are face-only.** They must not leak upward into governor authority.
- **Rendered reply is the visible transcript artifact.** `Idolum` owns what the user actually sees.
- **The face should not feel like a thin rewrite layer.** Its job is authored staging within governor-owned material constraints.

## Test Plan

- **TestIdolumFilesLoadOnlyIntoFacePrompt**: `IDOLUM.md` and `QUESTIONS-TO-IDOLUM.md` are excluded from the governor prompt
- **TestFacePromptIncludesIdolumIdentity**: `IDOLUM.md` content appears in the face prompt
- **TestFacePromptIncludesAntiDriftNotes**: `QUESTIONS-TO-IDOLUM.md` content appears in the face prompt
- **TestFaceCannotOverrideGovernorAuthority**: face wording cannot change the governor's action or permission result
- **TestIdolumProactiveCandidateStillNeedsGovernorApproval**: outreach candidates from the face do not bypass governor authorization
- **TestFloorFallbackSerializerWhenIdolumUnavailable**: when face rendering fails, the dedicated floor-to-user fallback serializer can deliver the floor under configured policy
- **TestSessionStoresRenderedIdolumReply**: visible assistant history stores the delivered Idolum reply
- **TestIdolumRendersFromMaterialFloor**: face rendering consumes governor-owned material constraints rather than a first-draft conversational answer
