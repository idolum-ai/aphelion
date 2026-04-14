//go:build linux

package turn

import (
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestDefaultPolicyInteractiveBrokerageTurn(t *testing.T) {
	policy := DefaultPolicy(Request{
		RunKind: session.TurnRunKindInteractive,
		Inbound: core.InboundMessage{Text: "help me think through the architecture of this codebase"},
	})
	if !policy.Brokerage || !policy.Proposal || !policy.Render {
		t.Fatalf("policy = %#v, want brokerage+proposal+render", policy)
	}
}

func TestDefaultPolicyInteractiveSimpleTurnStillRenders(t *testing.T) {
	policy := DefaultPolicy(Request{
		RunKind: session.TurnRunKindInteractive,
		Inbound: core.InboundMessage{Text: "what time is it"},
	})
	if policy.Brokerage || policy.Proposal || !policy.Render {
		t.Fatalf("policy = %#v, want render only", policy)
	}
}

func TestDefaultPolicySlashCommandSkipsFacePath(t *testing.T) {
	policy := DefaultPolicy(Request{
		RunKind: session.TurnRunKindInteractive,
		Inbound: core.InboundMessage{Text: "/status"},
	})
	if policy.Brokerage || policy.Proposal || policy.Render {
		t.Fatalf("policy = %#v, want no face stages", policy)
	}
}
