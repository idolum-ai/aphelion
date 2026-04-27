//go:build linux

package runtime

import (
	"strings"
	"testing"
)

func TestExtractDeliberationSurfaceMarkdownHeadingBlock(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"### Surface",
		"Starting the commit scan now.",
		"",
		"INSPECT: yes",
		"QUESTION: no",
		"ANSWER: yes",
	}, "\n")
	surface, cleaned := extractDeliberationSurfaceMarkdown(raw)
	if surface != "Starting the commit scan now." {
		t.Fatalf("surface = %q, want heading surface block", surface)
	}
	if strings.Contains(cleaned, "### Surface") {
		t.Fatalf("cleaned = %q, want heading removed", cleaned)
	}
	if !strings.Contains(cleaned, "INSPECT: yes") {
		t.Fatalf("cleaned = %q, want contract preserved", cleaned)
	}
}

func TestExtractDeliberationSurfaceMarkdownSurfaceLabel(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"Surface: Starting the commit scan now.",
		"INSPECT: yes",
		"QUESTION: no",
		"ANSWER: yes",
	}, "\n")
	surface, cleaned := extractDeliberationSurfaceMarkdown(raw)
	if surface != "Starting the commit scan now." {
		t.Fatalf("surface = %q, want one-line Surface label", surface)
	}
	if strings.Contains(cleaned, "Surface:") {
		t.Fatalf("cleaned = %q, want Surface label removed", cleaned)
	}
	if !strings.Contains(cleaned, "INSPECT: yes") {
		t.Fatalf("cleaned = %q, want contract preserved", cleaned)
	}
}

func TestExtractDeliberationSurfaceMarkdownDoesNotImplicitlySurfaceInternalProse(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"Center the next turn on curiosity without overbuilding.",
		"Answer the user directly.",
		"",
		"INSPECT: yes",
		"QUESTION: no",
		"ANSWER: yes",
	}, "\n")
	surface, cleaned := extractDeliberationSurfaceMarkdown(raw)
	if surface != "" {
		t.Fatalf("surface = %q, want no implicit live surface", surface)
	}
	if !strings.Contains(cleaned, "Center the next turn on curiosity") {
		t.Fatalf("cleaned = %q, want original prose preserved", cleaned)
	}
}

func TestExtractDeliberationSurfaceMarkdownNoImplicitWhenDirectiveFirst(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"INSPECT: yes",
		"QUESTION: no",
		"ANSWER: yes",
	}, "\n")
	surface, _ := extractDeliberationSurfaceMarkdown(raw)
	if surface != "" {
		t.Fatalf("surface = %q, want empty when note starts with directives", surface)
	}
}
