//go:build linux

package runtime

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestExternalChannelReadinessSelectsInvokeGrantWhenOtherGrantIsNewer(t *testing.T) {
	principalID := core.DurableAgentPrincipal("child-mail")
	grants := []session.CapabilityGrant{
		{
			GrantID:        "grant-child-mailbox-maintenance",
			Kind:           session.CapabilityKindTool,
			TargetResource: "mailbox_adapter",
			GrantedTo:      principalID,
			AllowedActions: []string{"connection_test", "audit_log"},
			Status:         session.CapabilityGrantStatusActive,
		},
		{
			GrantID:        "grant-child-mailbox-invoke",
			Kind:           session.CapabilityKindTool,
			TargetResource: "mailbox_adapter",
			GrantedTo:      principalID,
			AllowedActions: []string{"invoke", "read", "search", "metadata"},
			Status:         session.CapabilityGrantStatusActive,
			Contract:       `{"child_runtime":{"readonly_paths":["/tmp/adapter-config"]}}`,
		},
	}

	toolGrant, _, toolOK, toolEvidence := selectExternalChannelToolGrant(grants, principalID, "mailbox_adapter")
	if !toolOK || toolGrant.GrantID != "grant-child-mailbox-invoke" {
		t.Fatalf("tool grant = %s ok=%v evidence=%q, want invoke grant", toolGrant.GrantID, toolOK, toolEvidence)
	}
	if strings.Contains(toolEvidence, "maintenance") {
		t.Fatalf("tool evidence = %q, want selected invoke grant", toolEvidence)
	}
}
