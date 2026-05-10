//go:build linux

package runtime

import (
	"strings"
	"testing"
)

func TestBrokerageContextUsesConfiguredFaceName(t *testing.T) {
	t.Parallel()

	got := brokerageContextForGovernor("Assistant", turnBrokerage{
		Active:     true,
		Phase:      "proposal",
		IdolumNote: "Push for a concrete next step.",
	})
	if !strings.Contains(got, "guidance from Assistant") {
		t.Fatalf("brokerage context = %q, want configured face name", got)
	}
	if strings.Contains(got, "guidance from Idolum") {
		t.Fatalf("brokerage context leaked default face name: %q", got)
	}
}
