# Voice — STT, TTS, and Voice-Reply Behavior

## Overview

Voice is the speech surface of Aphelion.

For the first serious voice path, the intended stack is:

- **speech-to-text**: Whisper / OpenAI transcription
- **text-to-speech**: ElevenLabs

Voice is a channel behavior layered on top of the governor/face split:

- the governor decides what to say
- `Idolum` renders the wording
- the voice subsystem turns that wording into audio when voice output is enabled

## Scope

### Required

- ingest voice messages from supported channels
- transcribe voice into ordinary user content
- configurable voice reply mode
- TTS generation for spoken replies
- fallback to text when voice generation fails

### Deferred

- always-on live voice channels
- streaming sentence-by-sentence TTS
- multiple TTS providers
- speaker diarization and richer voice identity

## Core Pipeline

Voice handling should follow this flow:

1. receive voice/media message
2. normalize media metadata
3. download audio into the allowed workspace root
4. transcribe via the configured STT backend
5. inject transcript as ordinary user content
6. run governor turn
7. attempt Idolum scene rendering for the reply
8. if scene rendering is unavailable or skipped, derive a deterministic spoken fallback from the floor
9. synthesize spoken reply when voice mode says to do so
10. send audio reply, with text fallback if needed

## Speech-to-Text

The preferred first cloud path is OpenAI transcription.

That should support:

- `whisper-1`
- newer OpenAI transcribe-capable models later

The media/transcription interface already belongs to `media.md`; this spec defines how the voice channel uses it.

Rules:

- the transcript is the canonical user input for the turn
- raw audio remains a media artifact, not the main transcript format
- transcription must respect the same principal isolation rules as any other media file

## Text-to-Speech

The preferred first premium TTS path is ElevenLabs.

Why:

- high-quality natural voice
- good fit for a warm `Idolum` layer
- simpler than inventing a local TTS story first

Voice generation should be treated as a rendering step after Idolum text exists. TTS must not replace the visible text artifact; it is an additional delivery format.

## Reply Modes

Voice behavior should be explicit.

Supported modes should include:

- `off`
- `auto`
- `all`

### `off`

Text only.

### `auto`

If the inbound message was a voice or audio-originated message, reply with voice by default.

If the inbound message was text-only, reply with text by default.

If the user asks for transcription or another text-extraction task, that turn replies in text even when the inbound artifact is audio.

If a text turn asks Aphelion to transcribe the next audio, the system records a one-shot pending media intent and consumes it on the next audio turn.

This should be the default messaging-gateway voice mode.

### `all`

Reply with voice for all messages, not just voice-originated ones.

## Default Voice Behavior

The intended default is:

- when voice mode is enabled
- and the user sends voice or audio
- Aphelion replies in voice unless explicitly configured otherwise
- transcription/extraction intent overrides the default and answers in text

This is the right "natural" rule for messaging platforms.

It keeps voice conversational without forcing spoken output on every text exchange.

## Transcript and Persistence

The visible session ledger should still be text-first.

Rules:

- store the transcribed user text as the visible user message
- store the rendered Idolum text as the visible assistant message
- keep voice/audio metadata as sidecar media artifacts
- do not make raw audio blobs the primary session transcript format

This keeps replay, memory, and review flows coherent.

## Principal and Isolation

Voice/media handling must respect the same role boundaries as text.

### Admin

- audio may be stored in admin/global allowed roots
- transcripts may affect shared/global state through ordinary governor behavior

### `approved_user`

- audio downloads only into the user's isolated writable roots
- transcripts remain isolated unless summarized upward through review flow
- voice/TTS generation must not leak isolated artifacts into shared/global state

## Idolum and Voice

`Idolum` should be the voice-facing persona by default.

That means:

- spoken output uses Idolum-rendered wording
- TTS voice selection should aim to match Idolum's relational style
- voice reply is a rendering mode, not a second decision layer

The voice subsystem does not get to rewrite governor intent; it only renders the already-spoken Idolum output into speech.

When ordinary scene authorship is unavailable, the fallback serializer may provide deterministic spoken wording. That is a degraded path, not the normal voice path.

## Failure Behavior

Voice failure should degrade gracefully.

If STT fails:

- report the failure clearly
- optionally ask for text resend or retry
- do not pretend a transcript exists

If TTS fails:

- send the text reply
- record the TTS failure as an audit/event artifact

## Config Surface

See `config.md`, but the intended shape should preserve:

```toml
[voice]
mode = "auto"                 # off | auto | all
stt_provider = "openai"
tts_provider = "elevenlabs"

[openai.transcription]
enabled = true
model = "whisper-1"

[voice.elevenlabs]
voice_id = ""
model_id = "eleven_multilingual_v2"
```

Additional per-channel or per-principal voice overrides may come later.

## Decisions

- **Whisper/OpenAI first for STT.** It is the cleanest first cloud transcription path.
- **ElevenLabs first for TTS.** It is the strongest first premium spoken-output path.
- **Voice mode is explicit.** `auto` is the best default for messaging.
- **Voice replies follow voice/audio input by default.** If the user sends voice or audio and voice mode is enabled, reply with voice unless configured otherwise.
- **The session ledger stays text-first.** Audio remains sidecar media state.
- **Voice is rendering, not authority.** Idolum (System) authorizes, Idolum speaks, voice renders the speech form.

## Test Plan

- **TestVoiceMessageTriggersTranscription**: inbound voice is transcribed before the turn runs
- **TestAutoModeRepliesToVoiceInput**: `auto` sends spoken replies only for voice/audio-originated turns
- **TestAutoModeFallsBackToTextForTextInput**: `auto` does not emit spoken replies for text-originated turns
- **TestAllModeSpeaksForTextInput**: `all` mode also produces spoken replies for text turns
- **TestOffModeUsesTextOnly**: `off` mode never emits TTS output
- **TestVoiceReplyUsesDeliveredSceneText**: spoken output is synthesized from the delivered Idolum scene text
- **TestVoiceFaceFailureUsesSpokenFallbackOverlay**: if face rendering fails, spoken output falls back to the deterministic voice overlay rather than raw floor text
- **TestTTSFallbackToText**: TTS failure still sends the text reply
- **TestApprovedUserVoiceArtifactsStayIsolated**: non-admin audio/transcripts remain in isolated roots
- **TestVisibleLedgerStoresTranscriptAndScene**: session replay remains text-first even when voice media is used
