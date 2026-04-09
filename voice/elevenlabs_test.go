//go:build linux

package voice

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestElevenLabsSynthesize(t *testing.T) {
	t.Parallel()

	client, err := NewElevenLabs(ElevenLabsOptions{
		APIKey:  "xi-test",
		BaseURL: "https://api.elevenlabs.io/v1",
		VoiceID: "voice-123",
		ModelID: "eleven_multilingual_v2",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://api.elevenlabs.io/v1/text-to-speech/voice-123" {
				t.Fatalf("url = %s, want text-to-speech endpoint", req.URL.String())
			}
			if req.Header.Get("xi-api-key") != "xi-test" {
				t.Fatalf("xi-api-key = %q, want xi-test", req.Header.Get("xi-api-key"))
			}
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), `"text":"hello"`) {
				t.Fatalf("body = %s, want text payload", string(body))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("audio-bytes")),
				Header:     http.Header{"Content-Type": {"audio/mpeg"}},
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewElevenLabs() err = %v", err)
	}

	media, err := client.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() err = %v", err)
	}
	if media.Type != "voice" || media.MimeType != "audio/mpeg" || string(media.Data) != "audio-bytes" {
		t.Fatalf("media = %#v, want synthesized voice media", media)
	}
}
