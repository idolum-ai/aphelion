//go:build linux

package runtime

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/turn"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContinuationOperatorCardUsesMayDeleteRiskNote(t *testing.T) {
	t.Parallel()

	lines := continuationOperatorCardLines(session.ContinuationState{
		ActionProposal: session.ActionProposal{
			ID:             "aprop-cleanup",
			Summary:        "Clean generated files.",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"delete_generated_files"},
			BoundedEffect:  "Remove generated files under tmp only.",
		},
		ContinuationLease: session.ContinuationLease{LeaseClass: session.ContinuationLeaseClassLocalWorkspace},
	})
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "Risk note: may delete") {
		t.Fatalf("operator card = %q, want may delete risk note", text)
	}
	if strings.Contains(strings.ToLower(text), "destructive") {
		t.Fatalf("operator card = %q, want no destructive label", text)
	}
}

func TestSendContinuationApprovalPromptRecordsThreadCallbackMessage(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "continue scoped work", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	rt := &Runtime{store: store, outbound: &fakeSender{}}
	key := session.SessionKey{ChatID: 1001, Scope: session.TelegramThreadScopeRef(1001, thread.ThreadID)}
	msg := core.InboundMessage{ChatID: 1001, SenderID: 2002, MessageID: 401, TelegramThreadID: thread.ThreadID}
	state := session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-thread", RemainingTurns: 1}
	if err := rt.sendContinuationApprovalPrompt(context.Background(), key, msg, state, "Continue scoped work?"); err != nil {
		t.Fatalf("sendContinuationApprovalPrompt() err = %v", err)
	}
	sender := rt.outbound.(*fakeSender)
	sender.mu.Lock()
	if len(sender.inline) != 1 || !strings.Contains(sender.inline[0].text, "(thread 1)") || !strings.Contains(sender.inline[0].text, "Continue scoped work?") {
		t.Fatalf("inline = %#v, want thread-prefixed approval card", sender.inline)
	}
	sender.mu.Unlock()
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 1); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(continuation prompt) = %d ok=%v err=%v, want thread %d", got, ok, err, thread.ThreadID)
	}
}

func TestContinuationOperatorCardDoesNotMayDeleteNegatedReview(t *testing.T) {
	t.Parallel()

	lines := continuationOperatorCardLines(session.ContinuationState{
		ActionProposal: session.ActionProposal{
			ID:            "aprop-review",
			Summary:       "Review deletion handling.",
			RiskClass:     "read_only_review",
			BoundedEffect: "Review the migration plan without deleting data or changing files.",
		},
		ContinuationLease: session.ContinuationLease{LeaseClass: session.ContinuationLeaseClassLocalWorkspace},
	})
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "Risk note: may delete") {
		t.Fatalf("operator card = %q, want no may delete note for negated read-only review", text)
	}
}

func TestRenderContinuationPromptFallbackDedupesAndKeepsCompact(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-live",
		RemainingTurns: 1,
		Objective:      "Diagnose and recover the blocked email child credentials.",
		StageSummary:   "Inspect child adapter metadata.",
		PersonaIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "expired approval callback",
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:    session.ContinuationIntentDecisionContinue,
			Rationale:   "expired approval callback",
			Constraints: "Read local non-secret metadata only. No deploy or restart.",
			Ratified:    true,
		},
		ActionProposal: session.ActionProposal{
			ID:               "aprop-live",
			Summary:          "Inspect child adapter metadata.",
			BoundedEffect:    "Read local non-secret metadata only. No deploy or restart.",
			AllowedActions:   []string{"inspect_durable_agent_state", "deploy"},
			ForbiddenActions: []string{"deploy", "restart"},
			Status:           session.ProposalStatusPending,
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-live",
			ProposalID:       "aprop-live",
			Status:           session.ContinuationLeaseStatusPending,
			MaxTurns:         1,
			RemainingTurns:   1,
			LeaseClass:       session.ContinuationLeaseClassDeployRestart,
			AllowedActions:   []string{"inspect_durable_agent_state", "deploy"},
			ForbiddenActions: []string{"deploy", "restart"},
		},
	}

	text := renderContinuationPromptFallback(state)
	if strings.Count(text, "expired approval callback") != 1 {
		t.Fatalf("fallback = %q, want deduped rationale", text)
	}
	for _, notWant := range []string{"Allowed actions:", "Forbidden actions:", "Operator card:", "Lease class:"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("fallback = %q, want no raw %q block", text, notWant)
		}
	}
	if !strings.Contains(text, "Should I continue for 1 more turn") {
		t.Fatalf("fallback = %q, want continuation question", text)
	}
}

func TestRawContinuationAuthorityRepairDetectsPersistedDeployContradiction(t *testing.T) {
	t.Parallel()

	raw := `{
		"status":"revoked",
		"action_proposal":{
			"allowed_actions":["inspect_durable_agent_state","deploy","prepare_release_handoff"],
			"forbidden_actions":["deploy","restart"]
		},
		"continuation_lease":{
			"lease_class":"deploy_restart",
			"allowed_actions":["deploy","prepare_release_handoff"],
			"forbidden_actions":["deploy","restart"]
		}
	}`
	if !rawContinuationStateAuthorityNeedsSanitization(raw, session.ContinuationState{}) {
		t.Fatal("rawContinuationStateAuthorityNeedsSanitization() = false, want persisted deploy contradiction detected")
	}

	clean := `{
		"status":"pending",
		"action_proposal":{
			"allowed_actions":["inspect_durable_agent_state"],
			"forbidden_actions":["deploy","restart"]
		},
		"continuation_lease":{
			"lease_class":"local_workspace",
			"allowed_actions":["inspect_durable_agent_state"],
			"forbidden_actions":["deploy","restart"]
		}
	}`
	if rawContinuationStateAuthorityNeedsSanitization(clean, session.ContinuationState{}) {
		t.Fatal("rawContinuationStateAuthorityNeedsSanitization() = true, want clean read-only state ignored")
	}
}

func TestHandleInboundOffersContinuationApprovalUI(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"Continue now because the scoped plan is actively in progress.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"The operation remains active and ratified for one bounded follow-up.",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8101, UserID: 0, Scope: telegramDMScopeRef(8101)}
	if err := store.UpdatePlanState(key, session.PlanState{
		Explanation: "Fix the continuation UI before merge.",
		Steps: []session.PlanStep{
			{Step: "Swap continuation button order so stop is left and continue is right", Status: session.PlanStatusCompleted},
			{Step: "Summarize the actual next-step plan in the continuation prompt", Status: session.PlanStatusInProgress},
		},
	}); err != nil {
		t.Fatalf("UpdatePlanState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		Objective: "Land the continuation UI polish cleanly.",
		Summary:   "Use plan/proposal content instead of the request preamble.",
		Proposal: session.OperationProposal{
			Summary:       "Patch continuation UI button order and summary text.",
			BoundedEffect: "Local code/test changes limited to continuation UI generation and directly affected tests.",
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{ChatID: 8101, SenderID: 1001, SenderName: "admin", Text: "keep going on the implementation", MessageID: 1})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Should I continue for 1 more turn") {
		t.Fatalf("inline text = %q, want continuation approval prompt", sender.inline[0].text)
	}
	if strings.Contains(sender.inline[0].text, "keep going on the implementation") {
		t.Fatalf("inline text = %q, want plan/proposal summary instead of user preamble", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Land the continuation UI polish cleanly.") {
		t.Fatalf("inline text = %q, want operation objective in summary", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Summarize the actual next-step plan in the continuation prompt") {
		t.Fatalf("inline text = %q, want in-progress plan step as next action", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Continue now because the scoped plan is actively in progress.") {
		t.Fatalf("inline text = %q, want explicit persona rationale summary", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "The operation remains active and ratified for one bounded follow-up.") {
		t.Fatalf("inline text = %q, want explicit governor rationale summary", sender.inline[0].text)
	}
	if strings.Contains(strings.ToLower(sender.inline[0].text), "persona intent:") {
		t.Fatalf("inline text = %q, want single-system framing without persona/governor blocks", sender.inline[0].text)
	}
	if strings.Contains(strings.ToLower(sender.inline[0].text), "governor intent:") {
		t.Fatalf("inline text = %q, want single-system framing without persona/governor blocks", sender.inline[0].text)
	}
	if strings.Contains(strings.ToLower(sender.inline[0].text), "persona rationale:") {
		t.Fatalf("inline text = %q, want single-system framing without persona/governor blocks", sender.inline[0].text)
	}
	if strings.Contains(strings.ToLower(sender.inline[0].text), "governor rationale:") {
		t.Fatalf("inline text = %q, want single-system framing without persona/governor blocks", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) != 3 {
		t.Fatalf("rows = %#v, want three pending continuation-control rows", sender.inline[0].rows)
	}
	labels := []string{
		sender.inline[0].rows[0][0].Text, sender.inline[0].rows[0][1].Text,
		sender.inline[0].rows[1][0].Text, sender.inline[0].rows[1][1].Text,
		sender.inline[0].rows[2][0].Text,
	}
	wantLabels := []string{"Start", "Details", "Change", "Pause", "Stop"}
	for i, want := range wantLabels {
		if labels[i] != want {
			t.Fatalf("button labels = %#v, want %#v", labels, wantLabels)
		}
	}
	state, err := store.ContinuationState(session.SessionKey{ChatID: 8101, UserID: 0})
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending {
		t.Fatalf("status = %q, want pending", state.Status)
	}
	if state.Objective != "Land the continuation UI polish cleanly." {
		t.Fatalf("objective = %q, want operation objective", state.Objective)
	}
	if state.StageSummary != "Summarize the actual next-step plan in the continuation prompt" {
		t.Fatalf("stage summary = %q, want in-progress plan step", state.StageSummary)
	}
	if strings.TrimSpace(state.DecisionID) == "" {
		t.Fatal("DecisionID empty, want persisted continuation decision id")
	}
	if state.PersonaIntent.Decision != session.ContinuationIntentDecisionContinue {
		t.Fatalf("persona decision = %q, want continue", state.PersonaIntent.Decision)
	}
	if strings.TrimSpace(state.PersonaIntent.Rationale) == "" {
		t.Fatal("persona rationale empty, want persisted rationale")
	}
	if state.GovernorIntent.Decision != session.ContinuationIntentDecisionContinue {
		t.Fatalf("governor decision = %q, want continue", state.GovernorIntent.Decision)
	}
	if !state.GovernorIntent.Ratified {
		t.Fatal("governor ratified = false, want true")
	}
	if state.HandshakeBlockedReason != "" {
		t.Fatalf("handshake blocked reason = %q, want empty", state.HandshakeBlockedReason)
	}
	if state.ActionProposal.ID == "" || state.ActionProposal.Status != session.ProposalStatusPending {
		t.Fatalf("ActionProposal = %#v, want pending action proposal", state.ActionProposal)
	}
	if state.ActionProposal.BoundedEffect != "Local code/test changes limited to continuation UI generation and directly affected tests." {
		t.Fatalf("ActionProposal bounded effect = %q, want operation proposal bounded effect", state.ActionProposal.BoundedEffect)
	}
	if state.ContinuationLease.ID == "" || state.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("ContinuationLease = %#v, want pending lease", state.ContinuationLease)
	}
	if state.ContinuationLease.ProposalID != state.ActionProposal.ID || state.ContinuationLease.RemainingTurns != 1 {
		t.Fatalf("ContinuationLease = %#v, want proposal-linked one-turn lease", state.ContinuationLease)
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got == "" || len(got) > core.TelegramCallbackDataMaxBytes {
		t.Fatalf("approve callback = %q len=%d, want non-empty <= %d", got, len(got), core.TelegramCallbackDataMaxBytes)
	}
	if got := sender.inline[0].rows[0][1].CallbackData; got == "" || len(got) > core.TelegramCallbackDataMaxBytes {
		t.Fatalf("continue callback = %q len=%d, want non-empty <= %d", got, len(got), core.TelegramCallbackDataMaxBytes)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var offered session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationOffered {
			offered = event
		}
	}
	if offered.ID == 0 {
		t.Fatalf("events = %#v, want continuation.offered event", events)
	}
	payload := executionEventPayload(offered.PayloadJSON)
	if payloadString(payload, "decision_id") != state.DecisionID {
		t.Fatalf("offered payload decision_id = %q, want %q", payloadString(payload, "decision_id"), state.DecisionID)
	}
	if payloadString(payload, "objective") != state.Objective {
		t.Fatalf("offered payload objective = %q, want %q", payloadString(payload, "objective"), state.Objective)
	}
	if payloadString(payload, "stage_summary") != state.StageSummary {
		t.Fatalf("offered payload stage_summary = %q, want %q", payloadString(payload, "stage_summary"), state.StageSummary)
	}
	remainingTurns, ok := payloadInt64(payload, "remaining_turns")
	if !ok || remainingTurns != 1 {
		t.Fatalf("offered payload remaining_turns = %d (ok=%v), want 1", remainingTurns, ok)
	}
	if !strings.Contains(offered.PayloadJSON, `"debug_breadcrumb"`) ||
		!strings.Contains(offered.PayloadJSON, `"canonical_record"`) ||
		!strings.Contains(offered.PayloadJSON, `"inspect_command"`) {
		t.Fatalf("offered payload = %s, want debug breadcrumb", offered.PayloadJSON)
	}
	if payloadString(payload, "state_source") != "continuation_state" {
		t.Fatalf("offered payload state_source = %q, want continuation_state", payloadString(payload, "state_source"))
	}
}

func TestContinuationApprovalButtonRowsAdaptToLeaseState(t *testing.T) {
	t.Parallel()

	pending := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-pending",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{ID: "aprop-pending"},
		ContinuationLease: session.ContinuationLease{
			ID:         "lease-pending",
			ProposalID: "aprop-pending",
			Status:     session.ContinuationLeaseStatusPending,
		},
	}
	if got, want := continuationButtonLabels(continuationApprovalButtonRows(pending)), []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("pending labels = %#v, want %#v", got, want)
	} else {
		assertContinuationButtonLabelsShort(t, got)
	}

	phase := pending
	phase.DecisionID = "decision-phase"
	phase.ActionProposal = session.ActionProposal{
		ID:             "aprop-phase-4b-rebundled-email-proof",
		OperationID:    "phase-4b-rebundled-email-proof",
		Summary:        "Bundled Phase 4B: one bounded mail-child read-only adapter proof",
		AllowedActions: []string{"execute_phase_once", "update_operation_phase_plan"},
	}
	phase.ContinuationLease = session.ContinuationLease{ID: "lease-phase-4b-rebundled-email-proof", ProposalID: "aprop-phase-4b-rebundled-email-proof", Status: session.ContinuationLeaseStatusPending}
	if got, want := continuationButtonLabels(continuationApprovalButtonRows(phase)), []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("phase labels = %#v, want %#v", got, want)
	} else {
		assertContinuationButtonLabelsShort(t, got)
	}

	approved := pending
	approved.Status = session.ContinuationStatusApproved
	approved.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	if got, want := continuationButtonLabels(continuationApprovalButtonRows(approved)), []string{"Run", "Status", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("approved labels = %#v, want %#v", got, want)
	} else {
		assertContinuationButtonLabelsShort(t, got)
	}

	expired := pending
	expired.Status = session.ContinuationStatusIdle
	expired.RemainingTurns = 0
	expired.ActionProposal.Status = session.ProposalStatusExpired
	expired.ContinuationLease.Status = session.ContinuationLeaseStatusExpired
	if got, want := continuationButtonLabels(continuationApprovalButtonRows(expired)), []string{"Refresh", "Status", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("expired labels = %#v, want %#v", got, want)
	} else {
		assertContinuationButtonLabelsShort(t, got)
	}
}

func TestRevokeContinuationReturnsUserFacingPlanLabel(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9027, UserID: 0, Scope: telegramDMScopeRef(9027)}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "bundle-resource-owner-assistant-20260505-phase-j1-intake-profile-after-onboarding",
		Objective:      "Create a consented Telegram group child agent for private-assistant support that can later grow organically if the resource owner engages.",
		StageSummary:   "Approve stages 33-36: Consent-first resource-owner intake and profile scoring rubric.",
		RemainingTurns: 3,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-bundle-resource-owner-assistant-20260505-phase-j1-intake-profile-after-onboarding",
			OperationID:   "bundle-resource-owner-assistant-20260505-phase-j1-intake-profile-after-onboarding",
			OperatorTitle: "Resource-Owner Assistant",
			Summary:       "Approve stages 33-36: Consent-first resource-owner intake and profile scoring rubric.",
			Status:        session.ProposalStatusPending,
		},
		ContinuationLease: session.ContinuationLease{
			ID:         "lease-bundle-resource-owner-assistant-20260505-phase-j1-intake-profile-after-onboarding",
			ProposalID: "aprop-bundle-resource-owner-assistant-20260505-phase-j1-intake-profile-after-onboarding",
			Status:     session.ContinuationLeaseStatusPending,
		},
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID:             "bundle-resource-owner-assistant-20260505-phase-j1-intake-profile-after-onboarding",
			Status:         session.ContinuationLeaseStatusPending,
			CurrentPhaseID: "phase-resource-owner-assistant-20260505-phase-j1-intake-profile-after-onboarding",
			Phases: []session.ContinuationApprovalBundlePhase{{
				ID:               "phase-resource-owner-assistant-20260505-phase-j1-intake-profile-after-onboarding",
				OperationPhaseID: "phase-j1-resource-owner-intake-profile-after-onboarding",
				Index:            33,
				OperatorTitle:    "Resource-Owner Assistant",
				Summary:          "Consent-first resource-owner intake and profile scoring rubric.",
				Status:           session.ContinuationLeaseStatusPending,
			}},
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	result, err := rt.RevokeContinuation(9027)
	if err != nil {
		t.Fatalf("RevokeContinuation() err = %v", err)
	}
	if !result.Revoked {
		t.Fatal("Revoked = false, want true")
	}
	if result.ContinuationLabel != "Plan: Resource-Owner Assistant (Phase J1)" {
		t.Fatalf("ContinuationLabel = %q, want human plan label", result.ContinuationLabel)
	}
	if strings.Contains(result.ContinuationLabel, "lease-") || strings.Contains(result.ContinuationLabel, "aprop-") {
		t.Fatalf("ContinuationLabel = %q, want no internal IDs", result.ContinuationLabel)
	}
}

func continuationButtonLabels(rows [][]telegram.InlineButton) []string {
	labels := make([]string, 0)
	for _, row := range rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	return labels
}

func assertContinuationButtonLabelsShort(t *testing.T, labels []string) {
	t.Helper()
	for _, label := range labels {
		if words := strings.Fields(label); len(words) > 2 {
			t.Fatalf("button label %q has %d words, want at most 2", label, len(words))
		}
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHandleInboundContinuationApprovalPromptFallsBackWhenRenderedTextUsesSplitRoleLabels(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.repairReplyText = strings.Join([]string{
		"I can continue from here.",
		"",
		"Persona intent:",
		"continue",
		"",
		"Governor intent:",
		"continue",
		"",
		"Approve 1 more turn(s)?",
	}, "\n")
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I should continue because this turn has a clear next step.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"The bounded next step remains ratified.",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8116, UserID: 0, Scope: telegramDMScopeRef(8116)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "continuation-fallback-render",
		Objective: "Finish the bounded continuation prompt rendering check.",
		Status:    session.OperationStatusActive,
		Stage:     "Check the continuation approval fallback prompt rendering.",
		Summary:   "Check the continuation approval fallback prompt rendering.",
		Proposal: session.OperationProposal{
			ID:     "continuation-fallback-render-approved",
			Status: session.ProposalStatusApproved,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8116, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	inline := strings.ToLower(sender.inline[0].text)
	if strings.Contains(inline, "persona intent:") || strings.Contains(inline, "governor intent:") {
		t.Fatalf("inline text = %q, want fallback without split-role labels", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Should I continue for 1 more turn") {
		t.Fatalf("inline text = %q, want single-system fallback approval question", sender.inline[0].text)
	}
}

func TestOfferContinuationApprovalClosesWhenIntentHasNoTypedRemainingWork(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8121, UserID: 0, Scope: telegramDMScopeRef(8121)}
	result := &turn.Result{
		PersonaIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The model wants to keep planning.",
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The governor ratified the continuation intent.",
			Ratified:  true,
		},
	}

	if err := rt.offerContinuationApproval(context.Background(), key, core.InboundMessage{ChatID: 8121, SenderID: 1001, Text: "done", MessageID: 1}, "done", result); err != nil {
		t.Fatalf("offerContinuationApproval() err = %v", err)
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sender.mu.Unlock()
	if inlineCount != 0 || sentCount != 0 {
		t.Fatalf("inline/sent count = %d/%d, want quiet close without prompt", inlineCount, sentCount)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusIdle || state.ActionProposal.Active() {
		t.Fatalf("continuation = %#v, want idle with no action proposal", state)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var consumed session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationConsumed {
			consumed = event
		}
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationOffered {
			t.Fatalf("events = %#v, want no continuation.offered without typed work", events)
		}
	}
	if consumed.ID == 0 {
		t.Fatalf("events = %#v, want continuation.consumed close event", events)
	}
	payload := executionEventPayload(consumed.PayloadJSON)
	if payloadString(payload, "reason") != "no_typed_remaining_work" {
		t.Fatalf("consumed reason = %q, want no_typed_remaining_work", payloadString(payload, "reason"))
	}
}

func TestGroundContinuationPromptWithExecutionEvidenceFallsBackWithoutMatchingEvent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8191, UserID: 0, Scope: telegramDMScopeRef(8191)}
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "continuation-missing",
		RemainingTurns: 1,
		Objective:      "Keep the refactor bounded.",
		StageSummary:   "Write focused tests first.",
		PersonaIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The thread still has one bounded action left.",
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The bounded step is ratified.",
			Ratified:  true,
		},
	}

	candidate := "I can continue from here.\n\nShould I continue for 1 more turn(s)?"
	grounded, note := rt.groundContinuationPromptWithExecutionEvidence(key, state, candidate)
	if grounded != renderContinuationPromptFallback(state) {
		t.Fatalf("grounded prompt = %q, want fallback when TES continuation evidence is missing", grounded)
	}
	if !strings.Contains(note, "continuation evidence is unavailable") {
		t.Fatalf("grounding note = %q, want missing-evidence explanation", note)
	}
}

func TestGroundContinuationPromptWithExecutionEvidenceFallsBackAfterRevocation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8192, UserID: 0, Scope: telegramDMScopeRef(8192)}
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "continuation-revoked",
		RemainingTurns: 1,
		Objective:      "Keep the refactor bounded.",
		StageSummary:   "Write focused tests first.",
		PersonaIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The thread still has one bounded action left.",
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The bounded step is ratified.",
			Ratified:  true,
		},
	}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventContinuationOffered,
			Stage:       "continuation",
			Status:      "pending",
			PayloadJSON: `{"decision_id":"continuation-revoked","remaining_turns":1}`,
			CreatedAt:   now,
		},
		{
			EventType:   core.ExecutionEventContinuationRevoked,
			Stage:       "continuation",
			Status:      "revoked",
			PayloadJSON: `{"decision_id":"continuation-revoked"}`,
			CreatedAt:   now.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	candidate := "I can continue from here.\n\nShould I continue for 1 more turn(s)?"
	grounded, note := rt.groundContinuationPromptWithExecutionEvidence(key, state, candidate)
	if grounded != renderContinuationPromptFallback(state) {
		t.Fatalf("grounded prompt = %q, want fallback when latest continuation event is revoked", grounded)
	}
	if !strings.Contains(note, "latest=continuation.revoked") {
		t.Fatalf("grounding note = %q, want revoked latest-event explanation", note)
	}
}
