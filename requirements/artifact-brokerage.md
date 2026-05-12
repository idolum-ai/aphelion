# Artifact Brokerage — Interpretation, Handling, and Retention

## Overview

Files and media are often ambiguous.

The deterministic runtime can know:

- what a file is
- what operations are available
- what retention ceiling policy allows

But it cannot always know:

- why the user sent it
- whether it is emotionally or structurally important
- whether it should matter only for this turn or for later continuity

Artifact brokerage exists to let `Idolum` and `Idolum (System)` deliberate over that ambiguity without letting either invent unsupported file handling.

The design split is:

- `Idolum` pushes the **meaning** of the artifact
- `Idolum (System)` ratifies the **material handling** of the artifact

## Scope

### Required

- artifact-bearing interactive turns may surface artifact meaning through ordinary floor/scene authoring
- Idolum may name what the artifact seems to mean in this turn when it materially shapes the scene
- current implementation runtime handling is deterministic from the capability envelope
- current implementation retention is deterministic from the policy envelope and recorded in floor metadata
- the floor sidecar must preserve artifact handling decisions
- unsupported artifact types must still receive honest bounded treatment

### Next Phase

- multi-artifact brokerage across batches/albums
- bounded artifact-specific brokerage between Idolum and Idolum (System)
- Idolum (System) ratifies handling and retention from a bounded machine vocabulary rather than only inheriting the deterministic current implementation path
- heartbeat or maintenance review of stored session artifacts
- outbound artifact scene planning
- stronger principal-aware artifact storage policy

### Deferred

- learned retention policy
- automatic archive promotion from repeated artifact use
- rich artifact-centric retrieval and reuse

## Preconditions

Artifact brokerage should only run after deterministic normalization has already established:

- artifact kind/subtype
- mime and filename
- size and scope
- capabilities
- default retention
- retention ceiling

Without that envelope, the models are too likely to hallucinate what the system can do.

## Roles

### Deterministic Runtime

The runtime owns:

- normalization
- capability detection
- size and policy ceilings
- actual download / extraction primitives
- final application of storage and retention policy

### `Idolum`

`Idolum` may push:

- what the artifact seems to signify
- what role it should play in the scene
- whether it feels ephemeral, intimate, archival, unresolved, evidentiary, or durable

`Idolum` does **not** get to decide:

- unsupported handling paths
- cross-principal sharing
- quarantine promotion
- raw-binary memory writes

### `Idolum (System)`

In current implementation, `Idolum (System)` consumes the results of deterministic handling and may speak from them in the floor, but it does not yet emit a dedicated artifact-ratification contract.

In future phase, `Idolum (System)` should ratify:

- what the deterministic handling path materially means for the turn
- what derived outputs may be trusted
- what retention class is allowed this turn
- whether the artifact stays ephemeral, session-local, memory-candidate, or quarantined

## Artifact Handling Vocabulary

The runtime must maintain a bounded handling vocabulary. In current implementation, the deterministic capability layer chooses the handling path before the governor turn. In future phase, Idolum (System) may ratify among these choices directly.

### `attach_for_vision`

Pass the artifact directly as multimodal input to a vision-capable provider.

### `extract_text`

Run a deterministic extraction path and reason over the extracted text.

### `transcribe`

Run a deterministic transcription path and reason over the transcript.

### `inspect_metadata`

Use only metadata such as mime, filename, size, and Telegram-side shape.

### `store_reference_only`

Do not deeply process the artifact now; keep only a bounded reference for later continuity.

### `ignore`

Do not materially use the artifact beyond acknowledging it.

### `quarantine_for_review`

Treat the artifact as a corpus/archive candidate that must go through explicit review before ordinary retrieval.

The runtime may extend this vocabulary later, but handling must remain enumerable and machine-auditable.

## Retention Vocabulary

The runtime uses the retention classes from `artifacts.md`:

- `ephemeral`
- `session_reference`
- `memory_candidate`
- `quarantine`

The runtime must reject any retention choice above the artifact's retention ceiling.

## Brokerage Shape

Artifact brokerage should be bounded like planning brokerage, but artifact-specific.

The face-side note should remain short and natural. It should not look like a bureaucratic form by default.

Examples of good `Idolum` pushes:

- "This screenshot looks less like a random image and more like a bug report we may want to keep in session."
- "This voice note sounds like it contains a durable instruction, not just passing chat."
- "This PDF feels archival, but I think we only need extracted text for this turn."

## Ratification Contract

This contract is deferred to future phase.

Aphelion's side must be parseable enough for runtime execution.

Suggested contract:

```text
ARTIFACT: <artifact-id>
INTERPRETATION: <brief material reading of what the artifact is doing in this turn>
RETENTION: ephemeral | session_reference | memory_candidate | quarantine
RATIONALE: <brief explanation tied to capabilities, user intent, and policy>
```

Optional fields:

```text
HANDLING: attach_for_vision | extract_text | transcribe | inspect_metadata | store_reference_only | ignore | quarantine_for_review
DERIVED_OUTPUT: transcript | extracted_text | metadata_note | none
SIGNAL_JUDGMENT: confirmed | adapted | not_material
```

`HANDLING` is optional in current implementation. In practice, current implementation handling remains deterministic and this full contract belongs to future phase when handling ratification becomes explicitly model-mediated.

`SIGNAL_JUDGMENT` is useful when the artifact is materially shaping Idolum's push in a way that overlaps with hidden-input brokerage.

## Defaults

In current implementation, the handling path is deterministic even when no explicit artifact brokerage runs.

Default assumptions should be conservative:

- handling defaults to the narrowest useful supported path
- retention defaults to `ephemeral`
- raw binary does not enter curated memory by default
- corpus/archive behavior defaults to `quarantine`, not immediate retrieval

## Floor and Scene

Artifact decisions belong first to the floor.

The floor should preserve:

- artifact id/reference
- handling choice
- retention choice
- derived output summary
- refusals or limits when processing was unavailable

The scene should then speak naturally from that floor:

- explain what was read, transcribed, or ignored
- avoid claiming unsupported understanding
- avoid exposing protocol fields unless the user needs them

## Storage Discipline

Interpretation may be flexible.

Storage must remain strict.

Rules:

- raw binaries should normally remain `ephemeral` or `session_reference`
- `memory_candidate` should usually apply to derived text or derived summaries, not the original binary
- `quarantine` is for corpus-like/archive-like promotion paths and remains operator-controlled downstream
- no artifact brokerage path may override principal isolation or semantic-store review rules

## Relationship to Other Specs

- `artifacts.md` defines the artifact model, capabilities, and retention classes
- `planning-brokerage.md` remains the general turn-mode/posture negotiation layer
- `hidden-inputs.md` explains how latent state may shape Idolum's push
- `semantic-store.md` governs review and approval after quarantine

## Illustrative Cases

### Screenshot as Bug Report

Idolum pushes that the screenshot seems evidentiary and worth keeping in session.
Idolum (System) ratifies:

- `HANDLING: attach_for_vision`
- `RETENTION: session_reference`

The floor preserves the bug-report reading and the session artifact reference. The scene responds naturally to the screenshot.

### Voice Note as Durable Instruction

Idolum pushes that the file sounds like a real instruction, not casual chatter.
Idolum (System) ratifies:

- `HANDLING: transcribe`
- `RETENTION: memory_candidate`

The raw voice file may still stay ephemeral while the transcript or its distilled commitment becomes a memory candidate.

### Large Foreign PDF Corpus

Idolum pushes that the document feels archival and important.
Idolum (System) ratifies:

- `HANDLING: quarantine_for_review`
- `RETENTION: quarantine`

The scene can acknowledge the artifact and explain the review boundary. The runtime does not silently inject it into retrieval.

## Tests

- **TestArtifactBrokerageUsesCapabilityEnvelope**
- **TestIdolumMayPushMeaningButNotOverrideHandlingLimits**
- **TestAphelionRatifiesHandlingFromBoundedVocabulary**
- **TestArtifactRetentionDefaultsToEphemeral**
- **TestArtifactRetentionCannotExceedCeiling**
- **TestMemoryCandidateAppliesToDerivedTextNotRawBinaryByDefault**
- **TestQuarantineForReviewFlowsIntoSemanticImportAudit**
- **TestSceneSpeaksNaturallyFromArtifactFloor**
