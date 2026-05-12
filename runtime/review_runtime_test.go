//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundDeliversPendingReviewEventsForAdmin(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      44,
		SourceRole:        "approved_user",
		TargetAdminChatID: 42,
		TurnFrom:          1,
		TurnTo:            3,
		Summary:           "user requested package install in isolated workspace",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     42,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "status",
		MessageID:  99,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 2 {
		t.Fatalf("sent len = %d, want 2", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "ok" {
		t.Fatalf("first message = %q, want model reply", finalText)
	}
	if !strings.Contains(sender.sent[1].Text, "**Review: approved user**") {
		t.Fatalf("second message missing digest label: %q", sender.sent[1].Text)
	}
	if !strings.Contains(sender.sent[1].Text, "chat=7001") {
		t.Fatalf("second message missing source chat: %q", sender.sent[1].Text)
	}

	pending, err := store.PendingReviewEvents(42, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len = %d, want 0 after delivery", len(pending))
	}

	adminSession, err := store.Load(session.SessionKey{ChatID: 42, UserID: 0})
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if len(adminSession.Messages) != 3 {
		t.Fatalf("admin session messages len = %d, want 3", len(adminSession.Messages))
	}
	if !strings.Contains(adminSession.Messages[2].Content, "**Review: approved user**") {
		t.Fatalf("admin digest content = %q, want persisted review digest", adminSession.Messages[2].Content)
	}
}

func TestHandleInboundDoesNotDeliverReviewEventsForApprovedUser(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      8001,
		SourceUserID:      77,
		SourceRole:        "approved_user",
		TargetAdminChatID: 42,
		TurnFrom:          3,
		TurnTo:            4,
		Summary:           "requires admin review",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     99,
		SenderID:   1002,
		SenderName: "member",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1 (only model reply)", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "ok" {
		t.Fatalf("message = %q, want ok", finalText)
	}

	pending, err := store.PendingReviewEvents(42, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1 (not delivered in non-admin turn)", len(pending))
	}
}

func TestHandleInboundGeneratesReviewEventForApprovedUser(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     222,
		SenderID:   1002,
		SenderName: "member",
		Text:       "please summarize what happened",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	event := pending[0]
	if event.SourceChatID != 222 {
		t.Fatalf("source chat = %d, want 222", event.SourceChatID)
	}
	if event.SourceUserID != 1002 {
		t.Fatalf("source user = %d, want 1002", event.SourceUserID)
	}
	if event.SourceRole != "approved_user" {
		t.Fatalf("source role = %q, want approved_user", event.SourceRole)
	}
	if event.SourceScope.Kind != session.ScopeKindTelegramDM || event.SourceScope.ID != "222" {
		t.Fatalf("source scope = %#v, want telegram_dm 222", event.SourceScope)
	}
	if event.TargetScope.Kind != session.ScopeKindTelegramDM || event.TargetScope.ID != "1001" {
		t.Fatalf("target scope = %#v, want telegram_dm 1001", event.TargetScope)
	}
	if event.TurnFrom != 1 || event.TurnTo != 1 {
		t.Fatalf("turn range = %d-%d, want 1-1", event.TurnFrom, event.TurnTo)
	}
	if !strings.Contains(event.Summary, "provenance chat=222 user=1002 role=approved_user turn=1") {
		t.Fatalf("summary missing provenance: %q", event.Summary)
	}
	if !strings.Contains(event.Summary, "scope=telegram_dm:222") {
		t.Fatalf("summary missing source scope: %q", event.Summary)
	}
	if !strings.Contains(event.Summary, "reply: idolum rendered") {
		t.Fatalf("summary missing rendered reply text: %q", event.Summary)
	}
	if strings.Contains(event.Summary, "reply: governor canonical") {
		t.Fatalf("summary used governor floor text instead of rendered scene: %q", event.Summary)
	}
	if len([]rune(event.Summary)) > session.DefaultReviewSummaryMaxChars {
		t.Fatalf("summary len = %d, want <= %d", len([]rune(event.Summary)), session.DefaultReviewSummaryMaxChars)
	}
}

func TestShouldGenerateReviewEvent(t *testing.T) {
	t.Parallel()

	if !shouldGenerateReviewEvent(principal.Principal{Role: principal.RoleApprovedUser}, session.SessionKey{ChatID: 1, UserID: 0}) {
		t.Fatal("approved_user should generate review event")
	}
	if shouldGenerateReviewEvent(principal.Principal{Role: principal.RoleAdmin}, session.SessionKey{ChatID: 1, UserID: 0}) {
		t.Fatal("admin top-level session should not generate review event")
	}
	if !shouldGenerateReviewEvent(principal.Principal{Role: principal.RoleAdmin}, session.SessionKey{ChatID: 1, UserID: 7}) {
		t.Fatal("admin subordinate session should generate review event")
	}
}

func TestHandleInboundDeliversActionableCapabilityReviewEventWithButtons(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-button",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "test inline approval delivery",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 42,
		Summary:           "Capability request cap-button",
		MetadataJSON:      `{"request_id":"cap-button","review_status":"proposed"}`,
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{ChatID: 42, SenderID: 1001, SenderName: "admin", Text: "status", MessageID: 99})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want actionable review delivered as inline keyboard", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "**Review: capability request**") || !strings.Contains(sender.inline[0].text, "Capability request cap-button") {
		t.Fatalf("inline text = %q, want review digest", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 2 {
		t.Fatalf("inline rows = %#v, want reject/approve row", sender.inline[0].rows)
	}
	if sender.inline[0].rows[0][0].Text != "Reject" || sender.inline[0].rows[0][1].Text != "Approve" {
		t.Fatalf("inline row = %#v, want Reject/Approve", sender.inline[0].rows[0])
	}
}

func TestReviewEventCompactStatusUsesTypedMetadataNotQuotedProse(t *testing.T) {
	t.Parallel()

	event := session.ReviewEvent{
		SourceRole:   "durable_agent",
		SourceScope:  session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "image2", DurableAgentID: "image2"},
		Summary:      "summary: Processed parent request: \"If generation is blocked, return the exact blocker.\" Local response: One-shot complete; generated one PNG artifact successfully.",
		MetadataJSON: `{"agent_id":"image2","summary":"Processed parent request: \"If generation is blocked, return the exact blocker.\" Local response: One-shot complete; generated one PNG artifact successfully.","interval_label":"2026-05-04T19:50:16Z","local_actions":["Processed pending parent guidance during this durable child turn."],"risk_flags":["parent_conversation_sync"],"artifact_refs":["conversation://durable-agent/image2"],"metadata":{"channel_kind":"external_channel","status":"completed","status_source":"review_status","artifact_count":"1"}}`,
	}

	text := FormatReviewEventCompactMessage(event)
	if !strings.Contains(text, "COMPLETED") {
		t.Fatalf("compact text = %q, want COMPLETED from typed metadata", text)
	}
	if strings.Contains(text, "\nBLOCKED\n") {
		t.Fatalf("compact text = %q, must not classify quoted blocked prose as BLOCKED", text)
	}
}

func TestReviewEventCompactStatusDefaultsUpdateWithoutTypedStatus(t *testing.T) {
	t.Parallel()

	event := session.ReviewEvent{
		SourceRole:   "durable_agent",
		SourceScope:  session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "image2", DurableAgentID: "image2"},
		Summary:      "summary: Processed parent request: \"If generation is blocked, return the exact blocker.\" Local response: One-shot complete; generated one PNG artifact successfully.",
		MetadataJSON: `{"agent_id":"image2","summary":"Processed parent request: \"If generation is blocked, return the exact blocker.\" Local response: One-shot complete; generated one PNG artifact successfully.","interval_label":"2026-05-04T19:50:16Z","metadata":{"channel_kind":"external_channel"}}`,
	}

	text := FormatReviewEventCompactMessage(event)
	if !strings.Contains(text, "UPDATE") {
		t.Fatalf("compact text = %q, want UPDATE when typed status is absent", text)
	}
	if strings.Contains(text, "\nBLOCKED\n") || strings.Contains(text, "\nCOMPLETED\n") {
		t.Fatalf("compact text = %q, must not infer terminal status from prose", text)
	}
}

func TestReviewEventCompactStatusExplicitBlockedMetadata(t *testing.T) {
	t.Parallel()

	event := session.ReviewEvent{
		SourceRole:   "durable_agent",
		SourceScope:  session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "image2", DurableAgentID: "image2"},
		Summary:      "summary: Child reported a concrete blocker.",
		MetadataJSON: `{"agent_id":"image2","summary":"Child reported a concrete blocker.","metadata":{"channel_kind":"external_channel","status":"blocked","status_source":"review_status"}}`,
	}

	text := FormatReviewEventCompactMessage(event)
	if !strings.Contains(text, "BLOCKED") {
		t.Fatalf("compact text = %q, want BLOCKED from explicit metadata", text)
	}
}

func TestReviewEventCompactGrantExpiredPauseIsOperatorReadable(t *testing.T) {
	t.Parallel()

	event := session.ReviewEvent{
		SourceRole:   "durable_agent",
		SourceScope:  session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "console", DurableAgentID: "console"},
		Summary:      "durable_agent=console channel=external_channel interval=2026-05-06T03:14:45Z\nsummary: Console wake paused: Codex app-server heartbeat grant expired.\nlocal: Backoff is recorded; no retry loop is running.\nquestions: Renew the grant only if there is a concrete parent/user work item.\nrisks: external_channel; adapter_dispatch",
		MetadataJSON: `{"agent_id":"console","summary":"Console wake paused: Codex app-server heartbeat grant expired.","interval_label":"2026-05-06T03:14:45Z","local_actions":["Backoff is recorded; no retry loop is running."],"questions":["Renew the grant only if there is a concrete parent/user work item."],"risk_flags":["external_channel","adapter_dispatch"],"metadata":{"channel_kind":"external_channel","channel_adapter":"codex_app_server","external_channel_status":"wake_blocked","operator_status":"paused","operator_title":"Console wake paused","operator_summary":"The Codex app-server heartbeat grant expired, so Console did not wake.","operator_point":"Backoff is recorded; no retry loop is running.","operator_action":"no_action_unless_work_item","operator_next_action":"Renew the grant only if Console has a concrete parent/user work item.","child_runtime_block_reason":"grant_expired","grant_id":"capg-console-codex-app-server-readonly-heartbeat-20260505T0040Z","grant_label":"Codex app-server heartbeat grant","external_channel_error":"child_runtime_blocked: grant_expired grant_id=capg-console-codex-app-server-readonly-heartbeat-20260505T0040Z"}}`,
	}

	compact := FormatReviewEventCompactMessage(event)
	for _, want := range []string{"**Console wake paused**", "PAUSED", "The Codex app-server heartbeat grant expired, so Console did not wake.", "Backoff is recorded; no retry loop is running.", "**No action needed**", "Renew the grant only if Console has a concrete parent/user work item."} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact text = %q, want %q", compact, want)
		}
	}
	for _, notWant := range []string{"capg-console", "child_runtime_blocked", "risk: external_channel", "risk: adapter_dispatch", "**Needs attention**", "wake wake_blocked"} {
		if strings.Contains(compact, notWant) {
			t.Fatalf("compact text = %q, did not want %q", compact, notWant)
		}
	}

	details := FormatReviewEventDetailsMessage(event)
	for _, want := range []string{"grant_id: capg-console-codex-app-server-readonly-heartbeat-20260505T0040Z", "external_channel_error: child_runtime_blocked: grant_expired"} {
		if !strings.Contains(details, want) {
			t.Fatalf("details text = %q, want diagnostic detail %q", details, want)
		}
	}
}

func TestReviewEventCompactUsesSafeSummaryForRedactedChildSummary(t *testing.T) {
	t.Parallel()

	event := session.ReviewEvent{
		ID:           77,
		SourceRole:   "durable_agent",
		SourceScope:  session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "mail-child", DurableAgentID: "mail-child"},
		Summary:      "durable_agent=mail-child channel=external_channel interval=2026-05-08T02:50:01Z\nsummary: [REDACTED: summary]\nrisks: external_channel",
		MetadataJSON: `{"agent_id":"mail-child","summary":"[REDACTED: summary]","interval_label":"2026-05-08T02:50:01Z","risk_flags":["external_channel"],"artifact_refs":["forensic://durable-agent/mail-child/example.json"],"metadata":{"channel_kind":"external_channel","external_channel_status":"wake_blocked","operator_summary":"External-channel wake blocked: mailbox adapter credential backend requires an interactive passphrase prompt; no TTY is available.","redacted_fields":"summary","redaction_action":"quarantined_fields","redaction_source":"deterministic","redaction_reason":"concrete_secret_value"}}`,
	}

	compact := FormatReviewEventCompactMessage(event)
	for _, want := range []string{"**Review: mail-child**", "BLOCKED", "External-channel wake blocked: mailbox adapter credential backend requires an interactive passphrase prompt", "Details shows the safe review record"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact text = %q, want %q", compact, want)
		}
	}
	if strings.Contains(compact, "Details has the full child update.") {
		t.Fatalf("compact text = %q, must not claim details has full child update for redacted raw text", compact)
	}

	details := FormatReviewEventDetailsMessage(event)
	if !strings.Contains(details, "Raw redacted text is stored only in the local forensic sidecar.") {
		t.Fatalf("details text = %q, want local forensic sidecar footer", details)
	}
	for _, want := range []string{
		"**Debug**",
		"trace_id: review_event:77",
		"canonical_record: review_events id=77",
		"projection: runtime.FormatReviewEventDetailsMessage",
		"inspect_command: aphelion durable-agent forensic --agent mail-child --ref forensic://durable-agent/mail-child/example.json show",
		"code_owner: runtime/turn.go",
	} {
		if !strings.Contains(details, want) {
			t.Fatalf("details text = %q, want debug breadcrumb %q", details, want)
		}
	}
}

func TestReviewEventCompactKeepsFullUpdateFooterWithoutRedactions(t *testing.T) {
	t.Parallel()

	event := session.ReviewEvent{
		SourceRole:   "durable_agent",
		SourceScope:  session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "mail-child", DurableAgentID: "mail-child"},
		Summary:      "durable_agent=mail-child channel=external_channel interval=2026-05-08T02:50:01Z\nsummary: External-channel wake blocked because mailbox adapter credentials need a passphrase prompt and no TTY is available.\nrisks: external_channel",
		MetadataJSON: `{"agent_id":"mail-child","summary":"External-channel wake blocked because mailbox adapter credentials need a passphrase prompt and no TTY is available.","interval_label":"2026-05-08T02:50:01Z","risk_flags":["external_channel"],"metadata":{"channel_kind":"external_channel","external_channel_status":"wake_blocked","redaction_action":"none","redaction_reason":"secret_concept_without_value"}}`,
	}

	compact := FormatReviewEventCompactMessage(event)
	if !strings.Contains(compact, "Details has the full child update.") {
		t.Fatalf("compact text = %q, want normal full-update footer", compact)
	}
	if strings.Contains(compact, "safe review record") {
		t.Fatalf("compact text = %q, did not want redaction footer", compact)
	}
}

func TestHandleInboundDeliversDurableReviewEventCompactWithExpandButton(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceRole:        "durable_agent",
		SourceScope:       session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "image2", DurableAgentID: "image2"},
		TargetAdminChatID: 42,
		Summary:           "durable_agent=image2 channel=external_channel interval=2026-04-30T03:00:00Z\nsummary: External-channel wake wake_completed from child image2 via adapter codex_image_generation. Generated one image artifact successfully.\nlocal: External-channel wake completed after child reported authorized adapter-local work completed.\nrisks: external_channel; adapter_dispatch",
		MetadataJSON:      `{"agent_id":"image2","summary":"External-channel wake wake_completed from child image2 via adapter codex_image_generation. Generated one image artifact successfully.","interval_label":"2026-04-30T03:00:00Z","local_actions":["External-channel wake completed after child reported authorized adapter-local work completed."],"risk_flags":["external_channel","adapter_dispatch"],"metadata":{"channel_kind":"external_channel","external_channel_status":"wake_completed"}}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{ChatID: 42, SenderID: 1001, SenderName: "admin", Text: "status", MessageID: 99})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want compact child review delivered with inline keyboard", len(sender.inline))
	}
	text := sender.inline[0].text
	if !strings.Contains(text, "**Review: image2**") || !strings.Contains(text, "**Status**") || !strings.Contains(text, "COMPLETED") || !strings.Contains(text, "Details has the full child update.") {
		t.Fatalf("compact text = %q, want readable child update summary", text)
	}
	if strings.Contains(text, "**Metadata**") {
		t.Fatalf("compact text = %q, should not include expanded metadata", text)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 1 {
		t.Fatalf("rows = %#v, want single expand row", sender.inline[0].rows)
	}
	button := sender.inline[0].rows[0][0]
	if button.Text != "Details" || button.CallbackData != core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionExpand) {
		t.Fatalf("button = %#v, want expand callback for review event %d", button, eventID)
	}
}
