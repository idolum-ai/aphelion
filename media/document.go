//go:build linux

package media

import (
	"context"
	"strings"
)

const (
	MIMETypePDF = "application/pdf"
)

// DocumentTextExtractor converts a structured document into normalized text.
type DocumentTextExtractor interface {
	ExtractDocumentText(ctx context.Context, req *DocumentTextExtractionRequest) (*DocumentTextExtraction, error)
}

// DocumentTextExtractionRequest describes a document text-extraction request.
//
// Callers may provide either Path or Data. Data is preferred when the caller has
// already materialized bounded bytes; Path is useful for already-local artifacts.
type DocumentTextExtractionRequest struct {
	Path     string
	Data     []byte
	MimeType string
	Filename string

	// TempDir is used when Data must be materialized for a local extractor.
	TempDir string

	// MaxBytes rejects inputs larger than the configured ceiling when positive.
	MaxBytes int64
	// MaxChars truncates normalized output when positive.
	MaxChars int
}

// DocumentTextExtraction is normalized extracted document text plus lightweight
// extraction metadata.
type DocumentTextExtraction struct {
	Text      string
	Pages     []DocumentPageText
	MimeType  string
	Filename  string
	Bytes     int64
	Truncated bool
	Extractor string
	PageCount int
}

// DocumentPageText is the text extracted from one document page.
type DocumentPageText struct {
	PageNumber int
	Text       string
	CharStart  int
	CharEnd    int
}

func normalizeDocumentTextExtraction(out DocumentTextExtraction, maxChars int) DocumentTextExtraction {
	out.MimeType = strings.TrimSpace(strings.ToLower(out.MimeType))
	out.Filename = strings.TrimSpace(out.Filename)
	out.Extractor = strings.TrimSpace(out.Extractor)
	out.Text = normalizeExtractedDocumentText(out.Text)
	if maxChars > 0 && len([]rune(out.Text)) > maxChars {
		runes := []rune(out.Text)
		out.Text = strings.TrimSpace(string(runes[:maxChars]))
		out.Truncated = true
	}
	if len(out.Pages) > 0 {
		out.PageCount = len(out.Pages)
	}
	return out
}

func normalizeExtractedDocumentText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
