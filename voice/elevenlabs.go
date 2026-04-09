//go:build linux

package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

const defaultElevenLabsBaseURL = "https://api.elevenlabs.io/v1"

type Synthesizer interface {
	Synthesize(ctx context.Context, text string) (core.Media, error)
}

type ElevenLabsOptions struct {
	APIKey     string
	BaseURL    string
	VoiceID    string
	ModelID    string
	HTTPClient *http.Client
}

type ElevenLabs struct {
	baseURL string
	apiKey  string
	voiceID string
	modelID string
	client  *http.Client
}

func NewElevenLabs(opts ElevenLabsOptions) (*ElevenLabs, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("elevenlabs: api key is required")
	}
	if strings.TrimSpace(opts.VoiceID) == "" {
		return nil, fmt.Errorf("elevenlabs: voice id is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultElevenLabsBaseURL
	}
	modelID := strings.TrimSpace(opts.ModelID)
	if modelID == "" {
		modelID = "eleven_multilingual_v2"
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &ElevenLabs{
		baseURL: baseURL,
		apiKey:  opts.APIKey,
		voiceID: opts.VoiceID,
		modelID: modelID,
		client:  client,
	}, nil
}

func (e *ElevenLabs) Synthesize(ctx context.Context, text string) (core.Media, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return core.Media{}, fmt.Errorf("elevenlabs: text is required")
	}

	body, err := json.Marshal(map[string]any{
		"text":     text,
		"model_id": e.modelID,
	})
	if err != nil {
		return core.Media{}, fmt.Errorf("elevenlabs: encode request: %w", err)
	}

	url := e.baseURL + "/text-to-speech/" + e.voiceID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return core.Media{}, fmt.Errorf("elevenlabs: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", e.apiKey)
	req.Header.Set("Accept", "audio/mpeg")

	resp, err := e.client.Do(req)
	if err != nil {
		return core.Media{}, fmt.Errorf("elevenlabs: request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.Media{}, fmt.Errorf("elevenlabs: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return core.Media{}, fmt.Errorf("elevenlabs: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return core.Media{
		Type:     "voice",
		Data:     data,
		MimeType: "audio/mpeg",
		Filename: "reply.mp3",
	}, nil
}
