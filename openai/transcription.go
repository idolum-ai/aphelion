//go:build linux

package openai

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/media"
)

var _ media.TranscriptionProvider = (*TranscriptionClient)(nil)

// TranscriptionOptions configures OpenAI transcription endpoints.
type TranscriptionOptions struct {
	Model            string
	TranslationModel string
}

// TranscriptionClient implements media.TranscriptionProvider.
type TranscriptionClient struct {
	client           *Client
	model            string
	translationModel string
}

// NewTranscriptionClient creates a new transcription client.
func NewTranscriptionClient(client *Client, opts TranscriptionOptions) (*TranscriptionClient, error) {
	if client == nil {
		return nil, fmt.Errorf("openai transcription: client is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("openai transcription: model is required")
	}
	translationModel := opts.TranslationModel
	if strings.TrimSpace(translationModel) == "" {
		translationModel = opts.Model
	}
	return &TranscriptionClient{
		client:           client,
		model:            opts.Model,
		translationModel: translationModel,
	}, nil
}

// Transcribe submits audio to OpenAI's transcription endpoint.
func (c *TranscriptionClient) Transcribe(ctx context.Context, req *media.TranscriptionRequest) (*media.Transcription, error) {
	return c.submit(ctx, "/audio/transcriptions", c.model, req, true)
}

// Translate submits audio to OpenAI's translation endpoint.
func (c *TranscriptionClient) Translate(ctx context.Context, req *media.TranscriptionRequest) (*media.Transcription, error) {
	return c.submit(ctx, "/audio/translations", c.translationModel, req, false)
}

func (c *TranscriptionClient) submit(ctx context.Context, path string, model string, req *media.TranscriptionRequest, includeLanguage bool) (*media.Transcription, error) {
	if req == nil {
		return nil, fmt.Errorf("openai transcription: request is required")
	}
	if req.Diarize || len(req.SpeakerClips) > 0 {
		return nil, fmt.Errorf("openai transcription: diarization scaffolding is not wired yet")
	}
	if strings.TrimSpace(req.Path) == "" {
		return nil, fmt.Errorf("openai transcription: path is required")
	}
	file, err := os.Open(req.Path)
	if err != nil {
		return nil, fmt.Errorf("openai transcription: open audio: %w", err)
	}
	defer file.Close()

	bodyReader, contentType, err := buildTranscriptionRequest(filepath.Base(req.Path), model, req, includeLanguage, file)
	if err != nil {
		return nil, err
	}

	httpReq, err := c.client.newRequest(ctx, http.MethodPost, path, bodyReader)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", contentType)

	var response transcriptionResponse
	if err := c.client.doJSON(httpReq, &response); err != nil {
		return nil, err
	}
	return mapTranscription(response), nil
}

func buildTranscriptionRequest(filename string, model string, req *media.TranscriptionRequest, includeLanguage bool, src io.Reader) (io.Reader, string, error) {
	reader, writer := io.Pipe()
	mpw := multipart.NewWriter(writer)

	go func() {
		defer writer.Close()
		defer mpw.Close()

		writeField := func(name string, value string) bool {
			if strings.TrimSpace(value) == "" {
				return true
			}
			if err := mpw.WriteField(name, value); err != nil {
				_ = writer.CloseWithError(fmt.Errorf("openai transcription: write %s field: %w", name, err))
				return false
			}
			return true
		}

		if !writeField("model", model) {
			return
		}
		if !writeField("response_format", "verbose_json") {
			return
		}
		if includeLanguage && !writeField("language", req.Language) {
			return
		}
		if !writeField("prompt", req.Prompt) {
			return
		}

		part, err := mpw.CreateFormFile("file", filename)
		if err != nil {
			_ = writer.CloseWithError(fmt.Errorf("openai transcription: create form file: %w", err))
			return
		}
		if _, err := io.Copy(part, src); err != nil {
			_ = writer.CloseWithError(fmt.Errorf("openai transcription: copy audio: %w", err))
			return
		}
	}()

	return reader, mpw.FormDataContentType(), nil
}

type transcriptionResponse struct {
	Text     string                 `json:"text"`
	Language string                 `json:"language"`
	Segments []transcriptionSegment `json:"segments"`
}

type transcriptionSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker string  `json:"speaker"`
}

func mapTranscription(in transcriptionResponse) *media.Transcription {
	out := &media.Transcription{
		Text:     in.Text,
		Language: in.Language,
	}
	if len(in.Segments) == 0 {
		return out
	}
	out.Segments = make([]media.TranscriptSegment, 0, len(in.Segments))
	for _, segment := range in.Segments {
		out.Segments = append(out.Segments, media.TranscriptSegment{
			StartSec: segment.Start,
			EndSec:   segment.End,
			Text:     segment.Text,
			Speaker:  segment.Speaker,
		})
	}
	return out
}
