//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/turn"
)

func TestDeliveryMaterializesDeniedCapabilityAccessApprovalCard(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	tools := toolpkg.NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 99261, UserID: 0, Scope: telegramDMScopeRef(99261)}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	audit := newTurnAuditRecorder(key, "telegram", string(principal.RoleAdmin), "finish account report")
	audit.ToolFinished(
		"capability_authority",
		`{"action":"access_check","kind":"external_account","target_resource":"account-primary","principal":"durable_agent:mail-child","capability_action":"read"}`,
		"[CAPABILITY_ACCESS]\nkind: external_account\ntarget_resource: account-primary\nprincipal: durable_agent:mail-child\naction: read\nallowed: false\n",
		"",
	)
	reply := "Next approval needed:\n\nApprove granting durable_agent:mail-child read access to account-primary for one bounded read-only account/report run."
	port := &turnDeliveryPort{
		runtime:        rt,
		key:            key,
		sess:           sess,
		msg:            core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, SenderName: "admin", Text: "finish account report", MessageID: 1},
		deliver:        true,
		recordOutbound: true,
		audit:          audit,
	}

	if _, err := port.Deliver(context.Background(), turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: key.ChatID, Text: reply},
		Result:  &turn.Result{VisibleReply: reply},
	}); err != nil {
		t.Fatalf("Deliver() err = %v", err)
	}

	requests, err := store.CapabilityRequests(10, session.CapabilityReviewStatusProposed, session.CapabilityKindExternalAccount, "durable_agent:mail-child")
	if err != nil {
		t.Fatalf("CapabilityRequests() err = %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("CapabilityRequests() len = %d, want 1", len(requests))
	}
	request := requests[0]
	if request.TargetResource != "account-primary" || request.RequestedFor != "durable_agent:mail-child" {
		t.Fatalf("request = %#v, want durable-agent external-account read request", request)
	}

	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open next actions = %#v, want one missing grant action", open)
	}
	if got := open[0]; got.ResourceBlocker != "missing_capability_grant" ||
		got.OperationTool != "capability_authority" ||
		!strings.Contains(got.OperationInputJSON, `"action":"grant_set"`) ||
		!strings.Contains(got.OperationInputJSON, `"allowed_actions":["read"]`) {
		t.Fatalf("next action = %#v, want capability_authority grant_set for read", got)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want one capability approval card; sent=%#v", len(sender.inline), sender.sent)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "account-primary") || !strings.Contains(got, "durable_agent:mail-child") {
		t.Fatalf("inline text = %q, want exact external account and principal", got)
	}
	if len(sender.inline[0].rows) == 0 || len(sender.inline[0].rows[len(sender.inline[0].rows)-1]) != 2 {
		t.Fatalf("inline rows = %#v, want reject/approve buttons", sender.inline[0].rows)
	}
}

func TestDeliveryDoesNotMaterializeCapabilityCardWithoutDeniedAccessEvidence(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	tools := toolpkg.NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 99262, UserID: 0, Scope: telegramDMScopeRef(99262)}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	reply := "Next approval needed:\n\nApprove granting a broad mailbox permission."
	port := &turnDeliveryPort{
		runtime:        rt,
		key:            key,
		sess:           sess,
		msg:            core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, SenderName: "admin", Text: "finish account report", MessageID: 1},
		deliver:        true,
		recordOutbound: true,
		audit:          newTurnAuditRecorder(key, "telegram", string(principal.RoleAdmin), "finish email report"),
	}

	if _, err := port.Deliver(context.Background(), turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: key.ChatID, Text: reply},
		Result:  &turn.Result{VisibleReply: reply},
	}); err != nil {
		t.Fatalf("Deliver() err = %v", err)
	}

	requests, err := store.CapabilityRequests(10, session.CapabilityReviewStatusProposed, "", "")
	if err != nil {
		t.Fatalf("CapabilityRequests() err = %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("CapabilityRequests() = %#v, want no request from prose without denied access evidence", requests)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline count = %d, want no approval card from prose only", len(sender.inline))
	}
}
