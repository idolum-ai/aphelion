//go:build linux

package memory

import (
	"strings"
	"testing"
)

func TestExtractIdentityBlock(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"# MEMORY.md",
		"",
		IdentityBeginMarker,
		"## Identity",
		"- keep me",
		IdentityEndMarker,
		"",
		"## Runtime",
		"- clear me",
	}, "\n")

	got := ExtractIdentityBlock(raw)
	if !strings.Contains(got, "keep me") {
		t.Fatalf("ExtractIdentityBlock() = %q, want identity content", got)
	}
	if strings.Contains(got, "clear me") {
		t.Fatalf("ExtractIdentityBlock() = %q, want runtime content excluded", got)
	}
}

func TestPreserveMemoryIdentity(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"# MEMORY.md",
		"",
		IdentityBeginMarker,
		"## Identity",
		"- keep me",
		IdentityEndMarker,
		"",
		"## Runtime",
		"- clear me",
	}, "\n")

	got, ok := PreserveMemoryIdentity(raw)
	if !ok {
		t.Fatal("PreserveMemoryIdentity() = false, want true")
	}
	if !strings.Contains(got, "keep me") {
		t.Fatalf("PreserveMemoryIdentity() = %q, want preserved identity content", got)
	}
	if strings.Contains(got, "clear me") {
		t.Fatalf("PreserveMemoryIdentity() = %q, want runtime content removed", got)
	}
}
