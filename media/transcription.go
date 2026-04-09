//go:build linux

package media

import "context"

// TranscriptionProvider converts audio into text or translated text.
type TranscriptionProvider interface {
	Transcribe(ctx context.Context, req *TranscriptionRequest) (*Transcription, error)
	Translate(ctx context.Context, req *TranscriptionRequest) (*Transcription, error)
}

// TranscriptionRequest describes an audio-transcription request.
type TranscriptionRequest struct {
	Path         string
	Language     string
	Prompt       string
	Diarize      bool
	SpeakerClips []string
}

// Transcription is the normalized output from a transcription provider.
type Transcription struct {
	Text     string
	Language string
	Segments []TranscriptSegment
	Speakers []TranscriptSpeaker
}

// TranscriptSegment is a timed segment in a transcript.
type TranscriptSegment struct {
	StartSec float64
	EndSec   float64
	Text     string
	Speaker  string
}

// TranscriptSpeaker names a speaker in the transcript.
type TranscriptSpeaker struct {
	ID   string
	Name string
}
