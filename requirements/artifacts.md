# Artifacts — Channel-Neutral Files, Media, and Derived Content

## Overview

Aphelion should stop treating file support as a growing list of Telegram special cases.

The system needs a first-class **artifact** layer:

- transport normalizes inbound files/media into artifacts
- deterministic preprocessing establishes what the runtime can actually do with them
- the governor decides material handling and retention within that envelope
- the face stages the resulting scene

This is the architectural path to broad Telegram file support without turning the runtime into a pile of message-type branches.

## Design Laws

- **Artifacts are channel-neutral.** Telegram is one transport into the artifact layer, not the artifact model itself.
- **Capability comes before interpretation.** The runtime must know what an artifact can materially support before the models deliberate over it.
- **Interpretation is broader than retention.** The models may interpret a file flexibly, but retention must remain policy-bounded.
- **Raw artifact and derived content are different things.** A PDF file, its extracted text, and a durable memory note derived from that text are not the same artifact.
- **Visible ledger stays conversational.** The session transcript stores concise scene-facing references such as `[image attached]`; the floor sidecar stores artifact handling and provenance.
- **Supporting every Telegram file type does not mean deep understanding of every file type.** Some artifacts will only support metadata inspection, storage, forwarding, or honest acknowledgement.

## Scope

### Required

- a first-class artifact model
- Telegram normalization of all major inbound attachment classes into artifacts
- deterministic capability envelopes before model involvement
- temporary local persistence for turn processing
- retention classes and policy ceilings
- floor-sidecar storage of artifact references and handling decisions
- honest bounded handling for unsupported or metadata-only artifact types

### Next Phase

- outbound native artifact delivery policy (`sendPhoto`, `sendDocument`, `sendAudio`, `sendVideo`, etc.)
- per-principal isolated artifact roots
- artifact hashing/deduplication
- artifact references reusable across turns without replaying raw bytes into prompts
- derived-artifact lineage tracking

### Deferred

- full artifact indexing/search across all artifact kinds
- video frame extraction and richer multimodal summarization
- remote object stores as first-class artifact backends
- learned retention heuristics

## Artifact Model

Current `core.Media` is too narrow for the long-term channel/file story. The artifact layer should supersede that ad hoc shape.

Illustrative shape:

```go
type Artifact struct {
    ID               string
    Channel          string // "telegram"
    SourceType       string // photo, document, voice, audio, video, video_note, animation, sticker, poll, location
    Kind             string // image, audio, video, document, sticker, structured, archive
    Subtype          string // pdf, static_sticker, animated_sticker, voice_note, etc.
    MimeType         string
    Filename         string
    SizeBytes        int64
    Caption          string
    Scope            string // shared | principal
    PrincipalID      string
    LocalPath        string
    Metadata         map[string]string
    Capabilities     []string
    DefaultRetention string
    RetentionCeiling string
}

type DerivedArtifact struct {
    Kind       string // transcript, extracted_text, thumbnail, metadata_note
    Content    string
    SourceID   string
    Confidence string
}

type ArtifactReference struct {
    ArtifactID string
    Kind       string
    Summary    string
    Handling   string
    Retention  string
}
```

The conceptual requirement is not this exact struct layout. The requirement is that the runtime has a first-class artifact type rich enough to:

- normalize channel input
- declare capabilities
- carry scope and provenance
- record retention decisions
- distinguish raw files from derived outputs

## Artifact Kinds

The artifact layer should support at least these broad kinds:

- `image`
- `audio`
- `video`
- `document`
- `sticker`
- `structured`
- `archive`

`structured` covers Telegram-native objects that are not ordinary files but still deserve first-class treatment, such as:

- contacts
- locations
- venues
- polls

Not all of these need deep interpretation in current implementation. They do need honest normalization.

## Telegram Mapping

Telegram-specific attachment classes should map into artifacts like this:

- `photo` -> `image`
- `document` with `image/*` -> `image`
- `document` with `application/pdf` -> `document` / `pdf`
- `document` with text/code mime or known text extension -> `document`
- `voice` -> `audio` / `voice_note`
- `audio` -> `audio`
- `video` -> `video`
- `video_note` -> `video` / `video_note`
- `animation` -> `video` / `animation`
- static `sticker` -> `sticker` / `static_sticker`
- animated or video `sticker` -> `sticker` / `animated_sticker` or `video_sticker`
- `contact`, `location`, `venue`, `poll` -> `structured`

This mapping is about transport normalization, not about guaranteeing equal interpretive depth.

## Capability Envelope

Before the models see the artifact, the runtime should establish a deterministic capability envelope.

Examples:

- `image/png` may allow:
  - `vision`
  - `ocr`
  - `inspect_metadata`
  - `store_reference`
- `application/pdf` may allow:
  - `extract_text`
  - `inspect_metadata`
  - `store_reference`
  - `quarantine_for_review`
- `audio/ogg` voice note may allow:
  - `transcribe`
  - `inspect_metadata`
  - `store_reference`
- unknown binary file may allow only:
  - `inspect_metadata`
  - `store_reference`

The runtime must not let the models choose a handling path outside the advertised capability envelope.

## Lifecycle

1. channel transport receives attachment metadata
2. principal admission is resolved before expensive download/work
3. transport normalizes the attachment into an artifact
4. size and policy checks determine whether bytes may be fetched locally
5. deterministic preprocessing establishes capabilities and default retention
6. optional artifact brokerage interprets the artifact and suggests handling
7. the governor ratifies actual handling and retention
8. derived artifacts may be produced
9. visible ledger stores concise conversational references
10. floor sidecar stores artifact references, provenance, and handling decisions

## Retention Classes

Retention must be explicit and bounded.

### `ephemeral`

The raw artifact exists only for the current turn or temporary processing window.

Use for:

- one-off screenshots
- transient voice notes
- files processed once and not needed for continuity

### `session_reference`

The visible transcript may refer to the artifact, and the floor sidecar stores a durable artifact reference, but the artifact is not promoted into curated memory or semantic corpora.

Use for:

- bug screenshots tied to a specific conversation
- a PDF the user wants discussed this week
- a video attachment whose metadata matters for the current session

### `memory_candidate`

The artifact or more often a **derived textual summary/extract** may be eligible for curated memory promotion.

This is a candidate state, not automatic promotion.

Use for:

- a PDF whose extracted policy text becomes a durable decision reference
- a screenshot whose derived summary establishes a lasting fact
- a voice note whose transcript contains a durable preference or commitment

Raw binaries should not enter curated memory directly by default. Derived text is the normal memory candidate.

### `quarantine`

The artifact is being treated as a corpus/input candidate for broader retrieval or archive use and must not influence ordinary retrieval until reviewed.

Use for:

- imported archives
- large reference corpora
- foreign semantic substrate imports

Promotion out of quarantine remains operator-controlled and should hand off into the semantic-store review model rather than invent a second approval system.

## Policy Ceiling

Every artifact should have both:

- a `default_retention`
- a `retention_ceiling`

Example:

- a private screenshot might default to `ephemeral` and ceiling at `session_reference`
- an imported archive might default to `quarantine` and ceiling at `quarantine`
- a user-authored PDF might default to `session_reference` and ceiling at `memory_candidate`

The models may choose among allowed options. They must not exceed the ceiling.

## Derived Artifacts

Aphelion should distinguish raw artifacts from derived artifacts such as:

- transcript
- extracted text
- OCR result
- metadata summary
- thumbnail/frame extract

Derived artifacts may have different retention rules from the raw source.

This matters because the safest durable thing is often:

- not the original binary
- but the bounded text derived from it

## Session and Floor Behavior

The visible ledger should remain concise and conversational:

- `[image attached]`
- `[pdf attached]`
- `[voice note attached]`
- `[video attached]`

The floor sidecar should preserve:

- artifact references
- capability envelope summary
- handling choice
- retention choice
- derived-artifact summary when material

The raw bytes themselves should not be replayed into the visible transcript.

## Outbound Reply Artifacts

Outbound reply media should remain governed rather than ad hoc.

Current implemented rules:

- normal assistant replies may stage local files for native Telegram delivery
- runtime may parse material outbound directives from governor text
- those directives are removed from the visible reply before face rendering
- the visible reply text becomes the media caption when appropriate

Illustrative directive surface:

- `MEDIA: reports/chart.png`
- `MEDIA: shared/output/report.pdf`
- `[[audio_as_voice]]`

The directive surface is the current delivery contract; the artifact constitution remains the authoritative target.

### Path safety

Outbound local paths must remain inside the active execution scope.

The first implementation only allows local files inside:

- working root
- shared-memory root
- user-memory root

This keeps ordinary replies from exfiltrating arbitrary host files through Telegram delivery.

Remote artifact URLs are deliberately out of scope for this tranche.

### Native Telegram mapping

Current outbound media kinds map to Telegram as:

- `image` -> `sendPhoto`
- `document` -> `sendDocument`
- `video` -> `sendVideo`
- `audio` -> `sendAudio`
- `voice` -> `sendVoice`
- `animation` -> `sendAnimation`

If outbound media is present, it outranks synthesized voice reply mode for that turn.

## Relationship to Other Specs

- `telegram.md` defines Telegram transport behavior
- `media.md` defines processing services such as transcription and extraction
- `artifact-brokerage.md` defines how Idolum and Aphelion deliberate over interpretation and retention
- `semantic-store.md` governs quarantine/review once an artifact is promoted into corpus-like retrieval state

## Tests

- **TestTelegramPhotoNormalizesToImageArtifact**
- **TestTelegramVoiceNormalizesToAudioArtifact**
- **TestTelegramStickerNormalizesWithoutFalseUnderstanding**
- **TestArtifactCapabilityEnvelopeRejectsUnsupportedHandling**
- **TestArtifactRetentionCannotExceedCeiling**
- **TestVisibleLedgerStoresArtifactReferenceNotRawBytes**
- **TestFloorSidecarStoresArtifactHandlingDecision**
- **TestDerivedTextMayBeMemoryCandidateWhileRawBinaryStaysEphemeral**
- **TestQuarantinedArtifactCannotEnterOrdinaryRetrieval**
