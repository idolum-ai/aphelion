# Hidden Inputs — Latent Signal and Proactive Reaction Model

## Overview

Aphelion should never act "out of nowhere" when it moves without being asked.

Ordinary turns responding directly to a user message are valid with no hidden input at all. A direct question deserves a direct answer; the system should not invent provenance where there is none.

Hidden inputs matter when the system moves beyond the literal user message — in proactive outreach, in brokerage posture, in floor constraints that arise from prior state. When the system moves in that way, the cause should be machine-legible and traceable.

This spec defines four related concepts:

- **Hidden Inputs** — the latent signals that can legitimately move the system
- **Proactive Eligibility** — when those signals justify unsolicited action
- **Latent Signal Provenance** — how the system preserves the cause of its own behavior
- **Scene/Floor Reaction Model** — how hidden inputs shape brokerage, floor authoring, and scene authoring differently

## Telos

The ancient human heard a god speak. What actually moved them was hidden accumulated input — unresolved tension, environmental pressure, pattern recurrence — that had no named cause and so became voice.

Aphelion should not pretend its actions are spontaneous. It should trace what moved the turn. That is not less alive. It is more honest about what alive means.

This also changes the brokerage layer: Idolum's push is not just about tone. It is grounded in retrieved latent state. Aphelion's floor is not just policy. It is a reaction to real substrate pressure.

## Hidden Inputs

A hidden input is any signal that can legitimately influence a turn, proactive scene, or brokerage push — beyond the literal content of the latest user message.

### Categories

**Semantic recurrence**
A theme, tension, or question that has appeared in multiple prior turns without resolution. When the same cluster surfaces again, it is not noise. It is signal.

**Unresolved memory state**
An open question in `memory/questions.md`, a deferred decision, a commitment made but not yet discharged, a stale thread in `memory/dreams.md`.

**Temporal pressure**
A work window is active. A deadline noted in memory is approaching or has passed. A long silence has followed a prior moment of high engagement.

**Environmental context**
Time of day, session kind (interactive vs heartbeat), channel context, detected delivery mode.

**Pattern across turns**
A recurring motif — the user keeps circling a topic without naming it directly. The system has noticed this pattern even if the user has not.

**Architectural tension**
Prior brokerage or floor output that was not fully resolved. A ratification that was adapted rather than accepted. A scene that departed noticeably from the floor.

### Rules

- Hidden inputs do not override the user's explicit message. They inform how the system reads it.
- Hidden inputs must be recoverable. The system should be able to say what moved it.
- Hidden inputs may not be fabricated. If the signal is not present in memory, session history, or environmental state, it does not exist.

## Proactive Eligibility

Heartbeat maintenance runs on its cadence. Cron wakes due jobs on their schedule. Neither needs hidden-input convergence to run.

What requires convergence is **reflective outreach** — a user-visible message that was not requested and is not job-linked. Heartbeat-originated outreach falls here: it should not fire without converging signals.

Cron-originated outreach is different. A due reminder or scheduled job is already scheduler-originated and job-linked. It does not need recurrence-plus-memory convergence to be allowed to send. Hidden-input convergence applies to heartbeat's reflective proactivity, not to cron's scheduled delivery.

A proactive outreach is eligible when multiple signals converge:

1. a semantic theme has recurred across turns without resolution
2. an unresolved question or commitment exists in memory
3. a work window is active or a temporal pressure is present
4. the prior turn did not already address the cluster

A single signal is usually not enough. Convergence is the threshold.

### Examples

**Eligible:** A recurring design fork appears in three separate sessions. An unresolved question about it sits in `memory/questions.md`. The current time falls within an active work window. Idolum says: "You've circled the semantic layer three times without choosing. I think the indecision is the real blocker."

**Not eligible:** An hour has passed. Nothing has recurred. Nothing is unresolved. The heartbeat stays quiet.

**Eligible:** A commitment was made three days ago. It is recorded in memory. The work window is active. No follow-up has appeared in session history. Idolum surfaces it without being asked.

### Idolum's Role in Proactive Eligibility

Idolum may propose a proactive check-in during heartbeat when it can name the hidden input that justifies it. The proposal must include the signal that triggered it — not just the tone of the message.

Idolum (System) ratifies or declines based on whether the signal is real and the timing is appropriate.

## Latent Signal Provenance

When the system moves in a way that was not directly requested, it should be able to account for itself.

Provenance means: the system can trace what input moved the turn.

### What Provenance Looks Like

In brokerage:
- Idolum's push includes the hidden input that informed it, not just the conversational posture it recommends
- "The user keeps avoiding the deployment question. That's the buried blocker here." — hidden input: semantic recurrence

In the governor floor:
- when a refusal is grounded in an authority boundary, the floor names that boundary
- when an allowed action was constrained by prior commitment, the floor names that commitment

In scene authorship:
- Idolum does not need to expose provenance to the user by default
- but Idolum should speak from it — the scene should feel grounded in something real, not like a generic tone choice

In audit state:
- the floor sidecar should preserve the hidden inputs that shaped the floor when they were material
- the persisted message split stays explicit: scene text in `messages.content`, floor payload in `messages.floor_content`, and provenance payload in `messages.floor_metadata`
- this makes the system's behavior reviewable over time

### What Provenance Is Not

Provenance is not a disclaimer. The system does not preface every message with "I am saying this because of signal X."

Provenance is a machine-internal discipline. It keeps behavior honest. It surfaces in brokerage and audit state. It shapes the scene without always naming itself in the scene.

## Scene/Floor Reaction Model

Hidden inputs affect the two authoring layers differently.

### Floor Reaction

The governor floor reacts to hidden inputs by:

- adjusting what is permitted or refused based on authority constraints and prior commitments
- narrowing or widening allowed actions based on accumulated architectural tension
- surfacing relevant unresolved commitments as explicit floor fields

The floor does not perform. It reacts structurally. A hidden input that reveals a prior commitment becomes a `COMMITMENTS` or `REFUSALS` field. A hidden input that reveals a permission boundary narrows `ALLOWED_ACTIONS`.
Its persisted shape is sidecar data (`messages.floor_content` + `messages.floor_metadata`), not user-facing scene text.

### Scene Reaction

Idolum reacts to hidden inputs by:

- choosing the register of the scene — whether to comfort, confront, name something, hold silence, or press
- grounding creativity in retrieved structure rather than generic tone
- refusing to smooth over a real tension just because the user did not name it

The scene should feel like it came from somewhere. That somewhere is the hidden input.
Its persisted shape is transcript content (`messages.content`) and should not silently absorb floor sidecar fields.

### Brokerage as Signal Negotiation

When brokerage is active, Idolum's proposal should include the hidden input that is shaping its push. Idolum (System)'s ratification should either confirm that signal or name why it is not material for this turn.

The negotiated brokerage block should preserve both:
- Idolum's named signal and proposed reaction
- Idolum (System)'s structural reaction to the same hidden input

This makes brokerage a real negotiation between two readings of the same latent state, not just a posture handshake.

## Scope

### Current Phase

- heuristic hidden-input assembly for brokerage and heartbeat turns
- semantic recurrence detection in heartbeat eligibility
- unresolved memory state as proactive eligibility signal
- temporal pressure as proactive eligibility signal
- hidden-input convergence gates heartbeat-originated reflective outreach
- heartbeat hidden-input convergence is backed by durable interior signal pressure with magnitude, decay, dedupe, and surfaced cooldown
- curiosity can spend a disabled-by-default, read-only standing lease to inspect one candidate source from accumulated interior pressure and record a silent typed observation
- reflection and Nocturne can feed low-weight typed observations back into interior pressure, so quiet maintenance leaves reusable residue instead of only artifacts or summaries
- heartbeat can include a compact quiet-observation trail when accumulated pressure crosses the outreach threshold
- Idolum brokerage proposals name the hidden input when one is materially shaping the push
- Idolum (System) ratification may preserve `SIGNAL_JUDGMENT` when Idolum named a hidden input
- floor sidecar metadata preserves hidden inputs when material
- runtime self-awareness exposes active hidden-input categories and whether a provenance summary was assembled

### Next Phase

- automated latent signal extraction across session history
- cross-session semantic clustering for recurrence detection
- user-visible provenance surface by request
- broader tuning of signal weights, half-life, and thresholds from live session evidence

## Decisions

- **Proactivity requires convergence.** Single signals do not justify unsolicited action.
- **Idolum must name its signal.** A push without a grounded cause is noise.
- **Provenance is internal discipline, not a user-facing disclaimer.** It shapes behavior; it does not narrate it by default.
- **Hidden inputs are real inputs.** They must be traceable to actual memory, session history, or environmental state. They cannot be invented.
- **Interior pressure is advisory.** Accumulated signal magnitude may shape attention and heartbeat outreach, but it does not grant authority, assert completion, or bypass consent.
- **Curiosity is a governed read, not autonomous work.** Curiosity may re-read allowlisted sources and write typed observations, but it must not deliver user messages, mutate durable memory directly, perform writes, or infer authority from pressure.
- **Silent maintenance leaves typed residue.** Nocturne, reflection, and curiosity may strengthen future attention only through bounded interior signal observations with source, evidence, confidence, weight, dedupe, and decay.
- **The scene should feel grounded.** Generic tone is not enough. Idolum authors from retrieved structure.
- **Brokerage becomes signal negotiation.** Both layers read the same latent state and their readings should be preserved together.

## Illustrative User Stories

### Direct Question, No Hidden Input

A user asks: "How does compaction work in this repo?"

Idolum (System) answers directly from the visible request and available code context. No hidden input is required. No provenance should be invented. Idolum stages the scene clearly, but the system does not pretend there was some deeper latent cause.

### Strategic Turn With Brokerage

A user asks: "Come up with some features for my codebase."

Idolum notices a recurring architectural tension and pushes for inspection before answering rather than generic brainstorming. If that push is materially shaped by a hidden input, the brokerage note names it. Idolum (System) ratifies the resulting execution contract, records whether the signal is confirmed or not material, and executes the main turn under that negotiated artifact. The user receives one coherent answer, but the system preserves both the signal-shaped push and the structural ratification.

### Reflective Heartbeat Outreach

Heartbeat wakes on cadence, but it stays quiet unless reflective outreach is eligible.

A recurring design fork has appeared across multiple turns. `memory/questions.md` still carries the unresolved question. The current time falls inside the active work window. Idolum proposes outreach and names the hidden input. Idolum (System) confirms that the signal is real and that the timing is appropriate. The delivered scene feels proactive, but the cause is machine-legible rather than mystical.

### Heartbeat Silence After Review

Heartbeat wakes on cadence and performs maintenance, but the signals do not converge into outreach.

Perhaps one thematic recurrence exists, but there is no unresolved memory state, or the work window is inactive, or the prior turn already addressed the cluster. Idolum does not get to speak just because the subsystem woke up. Idolum (System) ratifies silence. Maintenance still happens; no user-visible scene is delivered.

### Scheduled Cron Reminder

A user created a scheduled reminder to revisit deployment rollback on Friday afternoon.

When the job becomes due, cron sends the reminder because it is scheduler-originated and job-linked. It does not need hidden-input convergence. The user may still experience it as proactive, but the internal cause remains the stored obligation, not reflective heartbeat noticing.

### Refusal With Structural Provenance

A user asks for something outside the system's authority boundary.

Idolum (System)'s floor narrows allowed actions or emits a refusal, possibly shaped by prior commitment, principal boundary, or sandbox rule. Idolum stages that refusal so it feels intentional rather than robotic. The scene remains alive, but the floor keeps the refusal materially grounded.

### Contradiction Nudge

Curated memory and recent turns point in different directions about the system's desired identity or posture.

On a later architectural turn, Idolum names the contradiction as the real pressure in the conversation. Idolum (System) ratifies that signal as material and constrains the floor accordingly. The result is not generic ideation but a redirection toward resolving the contradiction before adding more behavior.

### Recovery Continuity

A long turn is interrupted after tool execution but before the user receives the final scene.

Later, recovery or heartbeat can surface that interruption from structured machine state. The system can explain that work completed partway and delivery did not. The user experiences continuity, while the actual cause remains grounded in recovery facts rather than fabricated spontaneity.

## Test Plan

- **TestProactiveEligibilityRequiresConvergence**: single-signal heartbeat turns do not trigger proactive output
- **TestProactiveEligibilityFiresOnConvergence**: recurrence + unresolved memory + active window triggers eligible proactive turn
- **TestIdolumBrokerageProposalNamesHiddenInputWhenPresent**: when a hidden input is materially shaping the push, the brokerage note names it; when no hidden input is present, the note remains valid without one
- **TestFloorPreservesHiddenInputsInSidecar**: when hidden inputs shaped the floor, they appear in floor sidecar audit state
- **TestProvenanceNotExposedToUserByDefault**: hidden input provenance does not appear in the delivered scene unless explicitly surfaced
- **TestBrokeragePreservesBothSignalReadings**: negotiated brokerage block includes both Idolum's named signal and Aphelion's structural reaction
- **TestRunCuriosityOnceRecordsSilentObservation**: a curiosity run consumes a bounded read-only lease turn, uses only the selected source, records a typed observation, and sends no Telegram message
