//go:build linux

package openai

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/media"
)

func TestTranscriptionClientTranscribeBuildsMultipartRequestAndMapsResponse(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "audio.wav")
	if err := os.WriteFile(path, []byte("fake-audio"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/audio/transcriptions" {
			t.Fatalf("path = %s, want /audio/transcriptions", r.URL.Path)
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse media type: %v", err)
		}
		if mediaType != "multipart/form-data" {
			t.Fatalf("media type = %s, want multipart/form-data", mediaType)
		}

		mr := multipart.NewReader(r.Body, params["boundary"])
		fields := map[string]string{}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read part: %v", err)
			}
			if part.FormName() == "file" {
				fields["file"] = string(data)
				fields["filename"] = part.FileName()
				continue
			}
			fields[part.FormName()] = string(data)
		}
		if fields["model"] != "whisper-1" {
			t.Fatalf("model = %q, want whisper-1", fields["model"])
		}
		if fields["language"] != "en" {
			t.Fatalf("language = %q, want en", fields["language"])
		}
		if fields["prompt"] != "be concise" {
			t.Fatalf("prompt = %q, want be concise", fields["prompt"])
		}
		if fields["response_format"] != "verbose_json" {
			t.Fatalf("response_format = %q, want verbose_json", fields["response_format"])
		}
		if fields["filename"] != "audio.wav" {
			t.Fatalf("filename = %q, want audio.wav", fields["filename"])
		}
		if fields["file"] != "fake-audio" {
			t.Fatalf("file body = %q, want fake-audio", fields["file"])
		}

		return jsonResponse(t, http.StatusOK, map[string]any{
			"text":     "hello world",
			"language": "en",
			"segments": []map[string]any{
				{"start": 0.0, "end": 1.5, "text": "hello", "speaker": "spk_1"},
				{"start": 1.5, "end": 2.0, "text": "world", "speaker": "spk_1"},
			},
		}), nil
	})
	transcriber, err := NewTranscriptionClient(client, TranscriptionOptions{
		Model: "whisper-1",
	})
	if err != nil {
		t.Fatalf("new transcription client: %v", err)
	}

	got, err := transcriber.Transcribe(context.Background(), &media.TranscriptionRequest{
		Path:     path,
		Language: "en",
		Prompt:   "be concise",
	})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if got.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", got.Text)
	}
	if len(got.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(got.Segments))
	}
	if got.Segments[0].Speaker != "spk_1" {
		t.Fatalf("speaker = %q, want spk_1", got.Segments[0].Speaker)
	}
}

func TestTranscriptionClientRejectsMissingPath(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call for missing path")
		return nil, nil
	})
	transcriber, err := NewTranscriptionClient(client, TranscriptionOptions{
		Model: "whisper-1",
	})
	if err != nil {
		t.Fatalf("new transcription client: %v", err)
	}

	_, err = transcriber.Transcribe(context.Background(), &media.TranscriptionRequest{})
	if err == nil {
		t.Fatal("expected missing path error")
	}
}
