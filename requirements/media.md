# Media — Uploads, Downloads, Transcription & Translation

## Overview

This spec covers media handling across channels plus platform services used to process media.

OpenAI belongs here for:

- audio transcription
- audio translation
- media-file preprocessing inputs

This is separate from inference-provider concerns.
Media services are also separate from the face layer. The governor may invoke transcription or extraction services; the face may only present the resulting content.

## Scope

### v0 required

- Telegram media normalization
- lazy media download when needed

### v0.5

- media routed into isolated/non-isolated workspaces according to principal role
- admin and non-admin media processing obey the same isolation rules as text/tool sessions

### Deferred after v0.5

- OpenAI transcription/translation
- diarization-aware transcription
- richer media indexing and attachment flows

## Media Pipeline

1. normalize inbound Telegram media metadata
2. defer actual download until the agent needs bytes
3. store transient local copies in the session's allowed writable root
4. hand media off to the appropriate processor

## Transcription

Transcription is a distinct service surface.

### Interface

```go
type TranscriptionProvider interface {
    Transcribe(ctx context.Context, req *TranscriptionRequest) (*Transcription, error)
    Translate(ctx context.Context, req *TranscriptionRequest) (*Transcription, error)
}

type TranscriptionRequest struct {
    Path            string
    Language        string
    Prompt          string
    Diarize         bool
    SpeakerClips    []string
}

type Transcription struct {
    Text        string
    Language    string
    Segments    []TranscriptSegment
    Speakers    []TranscriptSpeaker
}

type TranscriptSegment struct {
    StartSec float64
    EndSec   float64
    Text     string
    Speaker  string
}

type TranscriptSpeaker struct {
    ID   string
    Name string
}
```

## OpenAI Audio Services

OpenAI should be supported here for speech-to-text and translation.

Planned uses:

- `audio/transcriptions`
- `audio/translations`
- support for classic Whisper-compatible flows
- support for higher-quality transcribe models later

Design rules:

- transcription is a media service, not an inference-provider responsibility
- uploaded audio for transcription should respect the same principal isolation rules as any other file
- the resulting transcript may enter the session as ordinary user content or as an attachment-derived content block

## Isolation Rules for Media

For admin sessions:

- media can be downloaded into the global/admin workspace
- transcription outputs may feed shared/global state

For non-admin sessions:

- media is downloaded only into that principal's isolated workspace
- transcription outputs are local to that session unless summarized upward through review flow
- raw uploaded media should not be exposed to other sessions by default

## Config

```toml
[media]
download_dir = "/tmp/aphelion-media"
lazy_download = true

[openai.transcription]
enabled = false
provider = "openai"
model = "whisper-1"
translation_model = "whisper-1"
```

## Tests

### v0

- **TestNormalizePhotoMessage**
- **TestNormalizeCaptionFallback**
- **TestLazyDownloadOnlyWhenNeeded**

### Deferred transcription

- **TestOpenAITranscribe**
- **TestOpenAITranslate**
- **TestTranscriptInjectedIntoSession**
- **TestNonAdminTranscriptStaysIsolated**
- **TestAdminTranscriptMayAffectSharedState**
