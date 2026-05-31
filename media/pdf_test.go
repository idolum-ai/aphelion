//go:build linux

package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPDFTextExtractorRunsPdftotextAndReturnsPageMetadata(t *testing.T) {
	t.Parallel()

	var gotName string
	var gotArgs []string
	extractor := PDFTextExtractor{Runner: func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotName = name
		gotArgs = append([]string(nil), args...)
		if len(args) != 3 || args[0] != "-layout" || args[2] != "-" {
			t.Fatalf("args = %#v, want pdftotext -layout <path> -", args)
		}
		if _, err := os.Stat(args[1]); err != nil {
			t.Fatalf("materialized PDF path stat err = %v", err)
		}
		return []byte(" Page one text \n\fPage two text\n"), nil, nil
	}}

	got, err := extractor.ExtractDocumentText(context.Background(), &DocumentTextExtractionRequest{
		Data:     []byte("%PDF fixture"),
		MimeType: MIMETypePDF,
		Filename: "fixture.pdf",
		TempDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ExtractDocumentText() err = %v", err)
	}
	if gotName != "pdftotext" {
		t.Fatalf("command = %q, want pdftotext", gotName)
	}
	if filepath.Ext(gotArgs[1]) != ".pdf" {
		t.Fatalf("materialized path = %q, want .pdf", gotArgs[1])
	}
	if got.Text != "Page one text\n\nPage two text" {
		t.Fatalf("text = %q", got.Text)
	}
	wantPages := []DocumentPageText{
		{PageNumber: 1, Text: "Page one text", CharStart: 0, CharEnd: 13},
		{PageNumber: 2, Text: "Page two text", CharStart: 15, CharEnd: 28},
	}
	if !reflect.DeepEqual(got.Pages, wantPages) {
		t.Fatalf("pages = %#v, want %#v", got.Pages, wantPages)
	}
	if got.PageCount != 2 || got.MimeType != MIMETypePDF || got.Filename != "fixture.pdf" || got.Bytes != int64(len("%PDF fixture")) || got.Extractor != "pdftotext" {
		t.Fatalf("metadata = %#v", got)
	}
}

func TestPDFTextExtractorEnforcesSizeLimit(t *testing.T) {
	t.Parallel()

	_, err := NewPDFTextExtractor().ExtractDocumentText(context.Background(), &DocumentTextExtractionRequest{
		Data:     []byte("012345"),
		MimeType: MIMETypePDF,
		MaxBytes: 5,
	})
	if !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("err = %v, want ErrDocumentTooLarge", err)
	}
}

func TestPDFTextExtractorRejectsUnsupportedDocument(t *testing.T) {
	t.Parallel()

	_, err := NewPDFTextExtractor().ExtractDocumentText(context.Background(), &DocumentTextExtractionRequest{
		Data:     []byte("hello"),
		MimeType: "text/plain",
		Filename: "note.txt",
	})
	if !errors.Is(err, ErrDocumentExtractionUnsupported) {
		t.Fatalf("err = %v, want ErrDocumentExtractionUnsupported", err)
	}
}

func TestPDFTextExtractorTruncatesNormalizedText(t *testing.T) {
	t.Parallel()

	extractor := PDFTextExtractor{Runner: func(context.Context, string, ...string) ([]byte, []byte, error) {
		return []byte("abcdef"), nil, nil
	}}
	got, err := extractor.ExtractDocumentText(context.Background(), &DocumentTextExtractionRequest{
		Data:     []byte("pdf"),
		MimeType: MIMETypePDF,
		TempDir:  t.TempDir(),
		MaxChars: 3,
	})
	if err != nil {
		t.Fatalf("ExtractDocumentText() err = %v", err)
	}
	if got.Text != "abc" || !got.Truncated {
		t.Fatalf("text/truncated = %q/%t, want abc/true", got.Text, got.Truncated)
	}
}

func TestPDFTextExtractorReportsStderr(t *testing.T) {
	t.Parallel()

	extractor := PDFTextExtractor{Runner: func(context.Context, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("syntax error"), errors.New("exit 1")
	}}
	_, err := extractor.ExtractDocumentText(context.Background(), &DocumentTextExtractionRequest{
		Data:     []byte("pdf"),
		MimeType: MIMETypePDF,
		TempDir:  t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("err = %v, want stderr", err)
	}
}
