//go:build linux

package core

import (
	"strings"
	"testing"
)

func TestValidateDurableAgentID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"child-alpha", "CHILD_ALPHA_01", "child_01"} {
		if err := ValidateDurableAgentID(id); err != nil {
			t.Fatalf("ValidateDurableAgentID(%q) err = %v", id, err)
		}
	}

	for _, id := range []string{"", ".", "..", "../escape", "family/group", "family group", strings.Repeat("a", maxDurableAgentIDLength+1)} {
		if err := ValidateDurableAgentID(id); err == nil {
			t.Fatalf("ValidateDurableAgentID(%q) err = nil, want validation error", id)
		}
	}
}

func TestValidateDurableAgentRemoteBootstrapRejectsInvalidAgentID(t *testing.T) {
	t.Parallel()

	err := ValidateDurableAgentRemoteBootstrap(DurableAgentRemoteBootstrap{
		AgentID:          "../escape",
		ParentControlURL: "https://parent.example.test",
		EnrollmentToken:  "enrollment-token",
	})
	if err == nil {
		t.Fatal("ValidateDurableAgentRemoteBootstrap() err = nil, want invalid agent_id error")
	}
	if !strings.Contains(err.Error(), "path separators") {
		t.Fatalf("ValidateDurableAgentRemoteBootstrap() err = %v, want path separator context", err)
	}
}
