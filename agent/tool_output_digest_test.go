//go:build linux

package agent

import (
	"strings"
	"testing"
)

func TestFormatToolOutputForHistoryLargeOutputPreservesHeadTailAndHash(t *testing.T) {
	t.Parallel()

	output := "HEAD: important first line\n" + strings.Repeat("middle line that should be omitted\n", 20) + "TAIL: important final line\n"
	got := FormatToolOutputForHistory(output, 160)

	for _, want := range []string{
		"[TOOL_OUTPUT_DIGEST]",
		"sha256: sha256:",
		"omitted_bytes:",
		"HEAD: important first line",
		"TAIL: important final line",
		"[/TOOL_OUTPUT_DIGEST]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted digest missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, strings.Repeat("middle line that should be omitted\n", 10)) {
		t.Fatalf("formatted digest retained too much middle output:\n%s", got)
	}
}

func TestFormatToolOutputForHistorySmallOutputStaysVerbatim(t *testing.T) {
	t.Parallel()

	output := "small result\n"
	if got := FormatToolOutputForHistory(output, 160); got != output {
		t.Fatalf("FormatToolOutputForHistory() = %q, want verbatim %q", got, output)
	}
}
