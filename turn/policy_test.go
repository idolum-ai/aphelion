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
		Inbound: core.InboundMessage{Text: "INSPECT: yes\nQUESTION: no\nANSWER: yes"},
	})
	if !policy.Brokerage || !policy.Proposal || !policy.Render {
		t.Fatalf("policy = %#v, want brokerage+proposal+render", policy)
	}
}

func TestDefaultPolicyInteractiveSimpleTurnStillUsesProposalAndRender(t *testing.T) {
	policy := DefaultPolicy(Request{
		RunKind: session.TurnRunKindInteractive,
		Inbound: core.InboundMessage{Text: "what time is it"},
	})
	if policy.Brokerage || !policy.Proposal || !policy.Render {
		t.Fatalf("policy = %#v, want proposal+render without brokerage", policy)
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

func TestDefaultPolicyMaintenanceKindsSkipFacePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		runKind session.TurnRunKind
		reason  string
	}{
		{name: "heartbeat", runKind: session.TurnRunKindHeartbeat, reason: "heartbeat_default"},
		{name: "cron", runKind: session.TurnRunKindCron, reason: "cron_default"},
		{name: "recovery", runKind: session.TurnRunKindRecovery, reason: "recovery_default"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			policy := DefaultPolicy(Request{
				RunKind: tc.runKind,
				Inbound: core.InboundMessage{Text: "maintenance turn input"},
			})
			if policy.Brokerage || policy.Proposal || policy.Render {
				t.Fatalf("policy = %#v, want no face stages for %s", policy, tc.runKind)
			}
			if policy.Reason != tc.reason {
				t.Fatalf("policy.Reason = %q, want %q", policy.Reason, tc.reason)
			}
		})
	}
}
