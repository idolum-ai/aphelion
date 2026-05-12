# Terminology — Brokerage, Floor, Scene, Fallback

## Overview

Aphelion should describe the live conversation architecture with four primary nouns:

- **brokerage**: the bounded pre-turn negotiation between `Idolum` and `Aphelion`
- **floor**: the governor-owned material truth/permission/refusal/commitment artifact for a turn
- **scene**: the user-visible reply authored by `Idolum` from that floor
- **fallback**: the degraded direct-delivery path used when scene authorship is skipped or fails, normally via a dedicated floor-to-user fallback serializer

These are conceptual terms. They should be preferred in architectural writing, reviews, logs, and new code.

## State Truth Classes (Normative)

For storage and operator-surface discussions, use only these three state-surface
classes:

- `canonical`: authoritative source for a specific question.
- `projection`: rendered or derived view with no independent authority.
- `operational current-state store`: mutable declared "now" state used by
  runtime operations.
- Removed surfaces are deletion/rejection concerns, not truth classes.

## Rules

### Use These Terms

- `brokerage` for the pre-turn handshake
- `floor` for the governor-owned turn artifact
- `scene` for the delivered face-authored reply
- `fallback` for degraded direct delivery, normally mediated by the fallback serializer

### Demote These Terms

The following terms are implementation or migration details, not first-class architectural nouns:

- `MaterialPacket`
- `sidecar`
- `floor text`
- `serialized floor fallback`

### Phase Out These Terms In Conceptual Docs

Do not use these as primary architectural terms:

- `canonical reply`
- `rendered reply`
- `governor passthrough`

If a spec must mention them, it should do so only to describe removed fields or current storage serialization.

## Mapping

- old `canonical reply` -> `floor`
- old `rendered reply` -> `scene`
- old `governor_passthrough` -> `floor_fallback`
- old `canonical sidecar` -> `floor sidecar`

## Migration Note

The floor may still be serialized as text for storage, replay, search, or emergency last-resort direct delivery.

That serialization detail is not the conceptual model. The conceptual model remains:

1. user turn
2. brokerage
3. floor
4. scene
5. fallback if needed
