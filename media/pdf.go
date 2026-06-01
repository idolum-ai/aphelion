//go:build linux

package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	ErrDocumentExtractionUnsupported = errors.New("document text extraction unsupported")
	ErrDocumentBytesUnavailable      = errors.New("document bytes unavailable")
	ErrDocumentTooLarge              = errors.New("document exceeds configured size limit")
)

// CommandRunner runs a local command and returns bounded stdout/stderr buffers.
type CommandRunner func(ctx context.Context, name string, args ...string) (stdout []byte, stderr []byte, err error)

// PDFTextExtractor extracts text from local PDFs using the Poppler pdftotext
// command. It is provider-neutral: callers own retention, prompt injection, and
// runtime policy decisions.
type PDFTextExtractor struct {
	Command string
	Runner  CommandRunner
}

func NewPDFTextExtractor() PDFTextExtractor {
	return PDFTextExtractor{Command: "pdftotext"}
}

func (e PDFTextExtractor) ExtractDocumentText(ctx context.Context, req *DocumentTextExtractionRequest) (*DocumentTextExtraction, error) {
	if req == nil {
		return nil, fmt.Errorf("pdf extraction request is required")
	}
	if !looksLikePDF(*req) {
		return nil, fmt.Errorf("%w: %s", ErrDocumentExtractionUnsupported, firstNonEmpty(req.MimeType, req.Filename, req.Path, "document"))
	}
	path, cleanup, bytesCount, err := e.materializePDF(req)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	command := strings.TrimSpace(e.Command)
	if command == "" {
		command = "pdftotext"
	}
	runner := e.Runner
	if runner == nil {
		runner = runCommand
	}
	// Omit -nopgbrk so form-feed page breaks remain available for page metadata.
	stdout, stderr, err := runner(ctx, command, "-layout", path, "-")
	if err != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, fmt.Errorf("pdf text extraction failed: %s", msg)
		}
		return nil, fmt.Errorf("pdf text extraction failed: %w", err)
	}
	pages := splitPDFPages(string(stdout))
	text := joinDocumentPages(pages)
	out := normalizeDocumentTextExtraction(DocumentTextExtraction{
		Text:      text,
		Pages:     pages,
		MimeType:  MIMETypePDF,
		Filename:  req.Filename,
		Bytes:     bytesCount,
		Extractor: command,
	}, req.MaxChars)
	return &out, nil
}

func (e PDFTextExtractor) materializePDF(req *DocumentTextExtractionRequest) (path string, cleanup func(), bytesCount int64, err error) {
	cleanup = func() {}
	if len(req.Data) > 0 {
		bytesCount = int64(len(req.Data))
		if req.MaxBytes > 0 && bytesCount > req.MaxBytes {
			return "", cleanup, bytesCount, ErrDocumentTooLarge
		}
		tmpDir := strings.TrimSpace(req.TempDir)
		if tmpDir == "" {
			tmpDir = os.TempDir()
		}
		if err := os.MkdirAll(tmpDir, 0o700); err != nil {
			return "", cleanup, bytesCount, fmt.Errorf("create pdf temp root: %w", err)
		}
		tmp, err := os.CreateTemp(tmpDir, "aphelion-doc-*.pdf")
		if err != nil {
			return "", cleanup, bytesCount, fmt.Errorf("create temp pdf: %w", err)
		}
		path = filepath.Clean(tmp.Name())
		cleanup = func() { _ = os.Remove(path) }
		if _, err := tmp.Write(req.Data); err != nil {
			_ = tmp.Close()
			return "", cleanup, bytesCount, fmt.Errorf("write temp pdf: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return "", cleanup, bytesCount, fmt.Errorf("close temp pdf: %w", err)
		}
		return path, cleanup, bytesCount, nil
	}
	path = strings.TrimSpace(req.Path)
	if path == "" {
		return "", cleanup, 0, ErrDocumentBytesUnavailable
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", cleanup, 0, fmt.Errorf("stat pdf: %w", err)
	}
	if info.IsDir() {
		return "", cleanup, 0, fmt.Errorf("pdf path is a directory")
	}
	bytesCount = info.Size()
	if req.MaxBytes > 0 && bytesCount > req.MaxBytes {
		return "", cleanup, bytesCount, ErrDocumentTooLarge
	}
	return filepath.Clean(path), cleanup, bytesCount, nil
}

func looksLikePDF(req DocumentTextExtractionRequest) bool {
	mimeType := strings.TrimSpace(strings.ToLower(req.MimeType))
	if mimeType == MIMETypePDF || mimeType == "application/x-pdf" {
		return true
	}
	for _, value := range []string{req.Filename, req.Path} {
		if strings.EqualFold(filepath.Ext(strings.TrimSpace(value)), ".pdf") {
			return true
		}
	}
	return false
}

func splitPDFPages(raw string) []DocumentPageText {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	parts := strings.Split(raw, "\f")
	pages := make([]DocumentPageText, 0, len(parts))
	cursor := 0
	for _, part := range parts {
		text := normalizeExtractedDocumentText(part)
		if text == "" {
			continue
		}
		start := cursor
		end := start + len(text)
		pages = append(pages, DocumentPageText{PageNumber: len(pages) + 1, Text: text, CharStart: start, CharEnd: end})
		cursor = end + 2
	}
	return pages
}

func joinDocumentPages(pages []DocumentPageText) string {
	texts := make([]string, 0, len(pages))
	for _, page := range pages {
		if text := strings.TrimSpace(page.Text); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n\n")
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
