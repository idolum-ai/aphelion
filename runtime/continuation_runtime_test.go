//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/tool/sandbox"
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

func TestGroundContinuationPromptUsesLatestEventsInLongSession(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 81920, UserID: 0, Scope: telegramDMScopeRef(81920)}
	now := time.Now().UTC()
	for i := 0; i < 350; i++ {
		if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":1}`,
			CreatedAt:   now.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("AppendExecutionEvent(%d) err = %v", i, err)
		}
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventContinuationOffered,
		Stage:       "continuation",
		Status:      "pending",
		PayloadJSON: `{"decision_id":"continuation-latest","remaining_turns":1}`,
		CreatedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(continuation) err = %v", err)
	}

	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "continuation-latest",
		RemainingTurns: 1,
		Objective:      "Keep the refactor bounded.",
		StageSummary:   "Write focused tests first.",
	}
	candidate := "I can continue from here.\n\nShould I continue for 1 more turn(s)?"
	grounded, note := rt.groundContinuationPromptWithExecutionEvidence(key, state, candidate)
	if grounded != candidate {
		t.Fatalf("grounded prompt = %q note=%q, want candidate grounded by latest continuation event", grounded, note)
	}
	if note != "" {
		t.Fatalf("grounding note = %q, want empty", note)
	}
}

func TestGroundContinuationBlockedNoticeWithExecutionEvidenceFallsBackWithoutBlockedEvent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8193, UserID: 0, Scope: telegramDMScopeRef(8193)}
	state := session.ContinuationState{
		Status:                 session.ContinuationStatusIdle,
		HandshakeBlockedReason: "governor_not_ratified",
	}
	candidate := "I can't continue right now."
	grounded, note := rt.groundContinuationBlockedNoticeWithExecutionEvidence(key, state, candidate)
	if grounded != renderContinuationBlockedFallback(state, rt.governorName()) {
		t.Fatalf("grounded blocked notice = %q, want deterministic fallback without TES evidence", grounded)
	}
	if !strings.Contains(note, "continuation evidence is unavailable") {
		t.Fatalf("grounding note = %q, want missing-evidence explanation", note)
	}
}

func TestGroundContinuationBlockedNoticeWithExecutionEvidenceFallsBackWhenLatestIsNotBlocked(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8194, UserID: 0, Scope: telegramDMScopeRef(8194)}
	state := session.ContinuationState{
		Status:                 session.ContinuationStatusIdle,
		HandshakeBlockedReason: "governor_not_ratified",
	}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventContinuationOffered,
		Stage:       "continuation",
		Status:      "pending",
		PayloadJSON: `{"decision_id":"continuation-foo"}`,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("AppendExecutionEvent() err = %v", err)
	}

	candidate := "I can't continue right now."
	grounded, note := rt.groundContinuationBlockedNoticeWithExecutionEvidence(key, state, candidate)
	if grounded != renderContinuationBlockedFallback(state, rt.governorName()) {
		t.Fatalf("grounded blocked notice = %q, want deterministic fallback when latest event is not blocked", grounded)
	}
	if !strings.Contains(note, "latest=continuation.offered") {
		t.Fatalf("grounding note = %q, want latest continuation event explanation", note)
	}
}

func TestRenderContinuationBlockedNoticeUsesAnonymousGovernorName(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Identity.AnonymousProfile = true
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	key := session.SessionKey{ChatID: 8195, UserID: 0, Scope: telegramDMScopeRef(8195)}
	state := session.ContinuationState{
		Status:                 session.ContinuationStatusIdle,
		HandshakeBlockedReason: "governor_not_ratified",
	}
	got := rt.renderContinuationBlockedNotice(context.Background(), key, core.InboundMessage{
		ChatID: key.ChatID,
		Text:   "continue",
	}, state)
	if !strings.Contains(got, "System did not ratify") {
		t.Fatalf("blocked notice = %q, want anonymous governor name", got)
	}
	if strings.Contains(got, "Idolum") {
		t.Fatalf("blocked notice = %q, want no branded governor name in anonymous profile", got)
	}
}

func TestRenderContinuationPromptUsesAnonymousFaceNameInRepairNotes(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Identity.AnonymousProfile = true
	provider.repairReplyText = "Ready to continue."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8196, UserID: 0, Scope: telegramDMScopeRef(8196)}
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-anonymous-face",
		RemainingTurns: 1,
	}
	_ = rt.renderContinuationPrompt(context.Background(), key, core.InboundMessage{
		ChatID: key.ChatID,
		Text:   "continue",
	}, state)
	seen := strings.Join(provider.seenFaceSystem, "\n")
	if !strings.Contains(seen, "Keep this in first person as Assistant.") {
		t.Fatalf("face repair prompt = %q, want anonymous face repair note", seen)
	}
	if strings.Contains(seen, "first person as Idolum") {
		t.Fatalf("face repair prompt = %q, want no default face name in repair note", seen)
	}
}

func TestHandleInboundSkipsContinuationWhenPersonaRationaleMissing(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor still ratifies continuation for the next bounded step.",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8111, UserID: 0, Scope: telegramDMScopeRef(8111)}
	if err := store.UpdatePlanState(key, session.PlanState{
		Explanation: "Keep moving.",
		Steps: []session.PlanStep{
			{Step: "Ship the remaining tests", Status: session.PlanStatusInProgress},
		},
	}); err != nil {
		t.Fatalf("UpdatePlanState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		Objective: "Finalize continuation behavior.",
		Proposal: session.OperationProposal{
			Summary: "Only ask for continuation when rationale is clear.",
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-persona",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8111, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline count = %d, want 0 without persona rationale", len(sender.inline))
	}

	state, err := store.ContinuationState(session.SessionKey{ChatID: 8111, UserID: 0})
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusIdle {
		t.Fatalf("status = %q, want idle when clearing stale pending continuation", state.Status)
	}
	if state.DecisionID != "" {
		t.Fatalf("decision id = %q, want cleared", state.DecisionID)
	}
	if state.PersonaIntent.Decision != session.ContinuationIntentDecisionContinue {
		t.Fatalf("persona decision = %q, want continue when explicit intent is present", state.PersonaIntent.Decision)
	}
	if state.HandshakeBlockedReason != "persona_rationale_missing" {
		t.Fatalf("handshake blocked reason = %q, want persona_rationale_missing", state.HandshakeBlockedReason)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var blocked session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationBlocked {
			blocked = event
		}
	}
	if blocked.ID == 0 {
		t.Fatalf("events = %#v, want continuation.blocked event", events)
	}
	payload := executionEventPayload(blocked.PayloadJSON)
	if payloadString(payload, "reason") != state.HandshakeBlockedReason {
		t.Fatalf("blocked payload reason = %q, want %q", payloadString(payload, "reason"), state.HandshakeBlockedReason)
	}
	remainingTurns, ok := payloadInt64(payload, "remaining_turns")
	if !ok || remainingTurns != 0 {
		t.Fatalf("blocked payload remaining_turns = %d (ok=%v), want 0", remainingTurns, ok)
	}
	if payloadString(payload, "state_source") != "continuation_state" {
		t.Fatalf("blocked payload state_source = %q, want continuation_state", payloadString(payload, "state_source"))
	}
}

func TestHandleInboundSkipsContinuationWhenGovernorRationaleMissing(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I should continue because there is a concrete next step.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8112, UserID: 0, Scope: telegramDMScopeRef(8112)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-governor",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8112, SenderID: 1001, SenderName: "admin", Text: "hello", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline count = %d, want 0 without governor rationale", len(sender.inline))
	}

	state, err := store.ContinuationState(session.SessionKey{ChatID: 8112, UserID: 0})
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusIdle {
		t.Fatalf("status = %q, want idle when clearing stale pending continuation", state.Status)
	}
	if state.DecisionID != "" {
		t.Fatalf("decision id = %q, want cleared", state.DecisionID)
	}
	if state.GovernorIntent.Decision != session.ContinuationIntentDecisionContinue {
		t.Fatalf("governor decision = %q, want continue when explicit intent is present", state.GovernorIntent.Decision)
	}
	if state.HandshakeBlockedReason != "governor_rationale_missing" {
		t.Fatalf("handshake blocked reason = %q, want governor_rationale_missing", state.HandshakeBlockedReason)
	}
}

func TestHandleInboundSkipsContinuationWithoutExplicitPersonaIntent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.proposalReplyText = strings.Join([]string{
		"INSPECT: no",
		"QUESTION: no",
		"ANSWER: yes",
		"I can keep moving.",
	}, "\n")
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor ratifies another bounded turn.",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8113, UserID: 0, Scope: telegramDMScopeRef(8113)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-persona-intent",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8113, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline count = %d, want 0 without explicit persona intent contract", len(sender.inline))
	}

	state, err := store.ContinuationState(session.SessionKey{ChatID: 8113, UserID: 0})
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.HandshakeBlockedReason != "persona_intent_missing" {
		t.Fatalf("handshake blocked reason = %q, want persona_intent_missing", state.HandshakeBlockedReason)
	}
}

func TestHandleInboundSendsPersonaVoicedContinuationBlockedNotice(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.repairReplyText = "I can't continue yet because Aphelion did not ratify this continuation request."
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I can continue after one more approval.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor rationale exists but ratification is withheld.",
		false,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8114, UserID: 0, Scope: telegramDMScopeRef(8114)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-pending",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8114, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) < 2 {
		t.Fatalf("sent count = %d, want main reply plus continuation blocked notice", len(sender.sent))
	}
	notice := sender.sent[len(sender.sent)-1].Text
	if notice != provider.repairReplyText {
		t.Fatalf("blocked notice = %q, want persona-rendered repair text", notice)
	}
	if !strings.HasPrefix(strings.TrimSpace(notice), "I ") {
		t.Fatalf("blocked notice = %q, want first-person phrasing", notice)
	}
}

func TestHandleInboundDoesNotSendContinuationBlockedNoticeWithoutPriorActiveContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.repairReplyText = "I can't continue yet because Aphelion did not ratify this continuation request."
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I can continue after one more approval.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor rationale exists but ratification is withheld.",
		false,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8115, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent count = %d, want only main reply when no prior continuation was active", len(sender.sent))
	}
	events, err := store.ExecutionEventsBySession(session.SessionKey{ChatID: 8115, UserID: 0, Scope: telegramDMScopeRef(8115)}, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var blocked session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationBlocked {
			blocked = event
		}
	}
	if blocked.ID == 0 {
		t.Fatalf("events = %#v, want blocked event for internal telemetry", events)
	}
	payload := executionEventPayload(blocked.PayloadJSON)
	got, ok := payload["user_visible"].(bool)
	if !ok || got {
		t.Fatalf("blocked payload user_visible = %v (ok=%v), want false", got, ok)
	}
}

func TestHandleInboundClosesCompletedOperationContinuationQuietly(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "done"
	provider.faceReplyText = "The bounded work is complete."
	provider.repairReplyText = "I can't continue yet because Aphelion did not ratify this continuation request."
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I can continue after one more approval.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor rationale exists but ratification is withheld.",
		false,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8117, UserID: 0, Scope: telegramDMScopeRef(8117)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "prior-pending-completed",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "completed-operation",
		Objective: "Finish the bounded phase.",
		Status:    session.OperationStatusCompleted,
		Stage:     "completed",
		Summary:   "All approved work completed.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8117, SenderID: 1001, SenderName: "admin", Text: "nice", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	sentCount := len(sender.sent)
	lastText := ""
	if sentCount > 0 {
		lastText = sender.sent[sentCount-1].Text
	}
	sender.mu.Unlock()
	if sentCount != 1 || lastText != "The bounded work is complete." {
		t.Fatalf("sent count/text = %d/%q, want only main reply", sentCount, lastText)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.HandshakeBlockedReason != "" {
		t.Fatalf("continuation = %#v, want quiet idle close without blocked reason", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationBlocked {
			t.Fatalf("events = %#v, want no blocked event after completed operation close", events)
		}
	}
}

func TestApproveContinuationPersistsApproverIdentity(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8102, UserID: 0, Scope: telegramDMScopeRef(8102)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusPending, RemainingTurns: 1}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	state, err := rt.ApproveContinuation(8102, 1002)
	if err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	if state.ApprovedBy != 1002 {
		t.Fatalf("ApprovedBy = %d, want 1002", state.ApprovedBy)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ApprovedBy != 1002 {
		t.Fatalf("persisted ApprovedBy = %d, want 1002", got.ApprovedBy)
	}
}

func TestApproveContinuationRejectsNonPendingState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8106, UserID: 0, Scope: telegramDMScopeRef(8106)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "decision",
		RemainingTurns: 1,
		ApprovedBy:     1002,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	_, err = rt.ApproveContinuation(8106, 1001)
	if !errors.Is(err, core.ErrContinuationNotPending) {
		t.Fatalf("ApproveContinuation() err = %v, want ErrContinuationNotPending", err)
	}
}

func TestApproveContinuationActivatesContinuationLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8107, UserID: 0, Scope: telegramDMScopeRef(8107)}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusPending,
		DecisionID:        "decision-lease-approve",
		RemainingTurns:    1,
		ActionProposal:    session.ActionProposal{ID: "aprop-lease-approve", Summary: "Approve lease", ExpiresAt: expiresAt, PlanHash: "sha256:lease"},
		ContinuationLease: session.ContinuationLease{ID: "lease-approve", ProposalID: "aprop-lease-approve", Status: session.ContinuationLeaseStatusPending, MaxTurns: 1, RemainingTurns: 1, ExpiresAt: expiresAt, PlanHash: "sha256:lease"},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	state, err := rt.ApproveContinuation(8107, 1002)
	if err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	if state.ActionProposal.Status != session.ProposalStatusApproved {
		t.Fatalf("proposal status = %q, want approved", state.ActionProposal.Status)
	}
	if state.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("lease status = %q, want active", state.ContinuationLease.Status)
	}
	if state.ContinuationLease.ApprovedBy != 1002 || state.ContinuationLease.ApprovedAt.IsZero() {
		t.Fatalf("lease approval = by %d at %v, want recorded approver", state.ContinuationLease.ApprovedBy, state.ContinuationLease.ApprovedAt)
	}
}

func TestHandleInboundTypedApprovalConsumesPendingContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8116, UserID: 0, Scope: telegramDMScopeRef(8116)}
	action := session.ActionProposal{
		ID:            "aprop-typed-approval",
		Summary:       "Run the approved typed continuation.",
		BoundedEffect: "Run one bounded follow-up and report evidence.",
		Status:        session.ProposalStatusPending,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusPending,
		DecisionID:        "typed-approval",
		Objective:         "Continue a pending plan.",
		StageSummary:      "Run the approved typed continuation.",
		RemainingTurns:    1,
		ActionProposal:    action,
		ContinuationLease: buildContinuationLease(action, 1, now),
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8116, SenderID: 1001, SenderName: "admin", Text: "approved", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called for approved continuation")
	}
	if recorder.input.Msg.Origin != core.InboundOriginTurnAuthorization {
		t.Fatalf("origin = %q, want turn authorization", recorder.input.Msg.Origin)
	}
	if recorder.input.Msg.Text == "approved" || !strings.Contains(recorder.input.Msg.Text, "Next: Run the approved typed continuation") {
		t.Fatalf("continuation text = %q, want machine-authored approved step", recorder.input.Msg.Text)
	}
	for _, notWant := range []string{"approved_step:", "proposal_id:", "lease_id:", "risk_class:"} {
		if strings.Contains(recorder.input.Msg.Text, notWant) {
			t.Fatalf("continuation text = %q, did not want internal fragment %q", recorder.input.Msg.Text, notWant)
		}
	}
	if recorder.input.Actor.Role != principal.RoleAdmin {
		t.Fatalf("actor role = %q, want admin", recorder.input.Actor.Role)
	}

	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 || got.ApprovedBy != 0 || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want idle state with consumed lease after typed approval", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationApproved) || !hasExecutionEvent(events, core.ExecutionEventContinuationConsumed) {
		t.Fatalf("events = %#v, want approved and consumed events", events)
	}
}

func TestApproveContinuationReturnsTypedExpiredErrorAndRecordsBlocked(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8109, UserID: 0, Scope: telegramDMScopeRef(8109)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-expired-approval",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:        "aprop-expired-approval",
			Summary:   "Expired approval",
			Status:    session.ProposalStatusPending,
			ExpiresAt: expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-expired-approval",
			ProposalID:     "aprop-expired-approval",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      expiredAt,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	state, err := rt.ApproveContinuation(8109, 1002)
	if !errors.Is(err, core.ErrContinuationExpired) {
		t.Fatalf("ApproveContinuation() err = %v, want ErrContinuationExpired", err)
	}
	if state.Status != session.ContinuationStatusIdle || state.RemainingTurns != 0 {
		t.Fatalf("state status/turns = %q/%d, want idle/0", state.Status, state.RemainingTurns)
	}
	if state.ActionProposal.Status != session.ProposalStatusExpired || state.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("state proposal/lease status = %q/%q, want expired/expired", state.ActionProposal.Status, state.ContinuationLease.Status)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.ActionProposal.Status != session.ProposalStatusExpired || got.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("persisted continuation = %#v, want expired idle state", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationBlocked) {
		t.Fatalf("events = %#v, want continuation blocked event", events)
	}
}

func TestRefreshContinuationProposalCreatesFreshLeaseForSameBoundedAction(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback
	key := session.SessionKey{ChatID: 8110, UserID: 0, Scope: telegramDMScopeRef(8110)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	prior := session.ContinuationState{
		Status:         session.ContinuationStatusIdle,
		Objective:      "Finish the bounded local patch.",
		StageSummary:   "Patch and test the callback flow.",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-old-expired",
			OperationID:      "op-refresh-v1",
			Summary:          "Refresh the expired lease",
			WhyNow:           "The previous prompt expired.",
			BoundedEffect:    "Patch only continuation callback refresh behavior.",
			RiskClass:        "system_change",
			AllowedActions:   []string{"patch_code", "run_tests"},
			ForbiddenActions: []string{"deploy", "restart"},
			ValidationPlan:   []string{"go test ./..."},
			Status:           session.ProposalStatusExpired,
			ExpiresAt:        expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-old-expired",
			ProposalID:     "aprop-old-expired",
			Status:         session.ContinuationLeaseStatusExpired,
			MaxTurns:       1,
			RemainingTurns: 0,
			ExpiresAt:      expiredAt,
		},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	state, sent, err := rt.RefreshContinuationProposal(context.Background(), 8110, "expired approval callback")
	if err != nil {
		t.Fatalf("RefreshContinuationProposal() err = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want fresh inline prompt")
	}
	if state.Status != session.ContinuationStatusPending || state.RemainingTurns != 1 {
		t.Fatalf("state status/turns = %q/%d, want pending/1", state.Status, state.RemainingTurns)
	}
	if state.ActionProposal.ID == prior.ActionProposal.ID || state.ContinuationLease.ID == prior.ContinuationLease.ID {
		t.Fatalf("fresh ids reused old proposal/lease: proposal=%q lease=%q", state.ActionProposal.ID, state.ContinuationLease.ID)
	}
	if state.ActionProposal.OperationID != prior.ActionProposal.OperationID || state.ActionProposal.BoundedEffect != prior.ActionProposal.BoundedEffect {
		t.Fatalf("fresh proposal = %#v, want same operation and bounded effect", state.ActionProposal)
	}
	if state.ActionProposal.Status != session.ProposalStatusPending || !state.ActionProposal.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("fresh proposal status/expires = %q/%v, want pending future expiry", state.ActionProposal.Status, state.ActionProposal.ExpiresAt)
	}
	if state.ContinuationLease.Status != session.ContinuationLeaseStatusPending || state.ContinuationLease.ProposalID != state.ActionProposal.ID {
		t.Fatalf("fresh lease = %#v, want pending lease tied to fresh proposal", state.ContinuationLease)
	}

	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ActionProposal.ID != state.ActionProposal.ID || got.Status != session.ContinuationStatusPending {
		t.Fatalf("persisted state = %#v, want fresh pending state", got)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want one fresh prompt", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Patch only continuation callback refresh behavior") {
		t.Fatalf("inline text = %q, want refreshed bounded effect", sender.inline[0].text)
	}
	oldCallback := core.EncodeContinuationCallbackData(prior.ActionProposal.ID, "approve_lease")
	newCallback := sender.inline[0].rows[0][0].CallbackData
	if newCallback == "" || newCallback == oldCallback {
		t.Fatalf("fresh callback = %q old = %q, want distinct non-empty callback", newCallback, oldCallback)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationOffered) {
		t.Fatalf("events = %#v, want continuation offered event", events)
	}
}

func TestRefreshContinuationProposalSanitizesLiveNegatedDeployAuthority(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback
	key := session.SessionKey{ChatID: 81100, UserID: 0, Scope: telegramDMScopeRef(81100)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	prior := session.ContinuationState{
		Status:         session.ContinuationStatusIdle,
		Objective:      "Diagnose and recover the blocked mail-child credentials without mailbox access.",
		StageSummary:   "Repair child-scoped mailbox adapter credential materialization, then run only a non-mailbox auth-status smoke.",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:        "aprop-live-corrupt",
			Summary:   "Repair child-scoped mailbox adapter credential materialization",
			RiskClass: "credential_recovery",
			BoundedEffect: "May create or adjust child-scoped mailbox adapter credential materialization, wrapper/env, or grant contract. " +
				"No mailbox content/label/inbox/message query, no OAuth, no account mutation, no public/external contact, no email actions, no deploy/restart unless separately approved.",
			AllowedActions: []string{
				"create_child_scoped_mailbox_adapter_materialization_if_approved",
				"copy_or_bind_existing_host_mailbox_credentials_without_printing_values",
				"adjust_child_mailbox_adapter_wrapper_or_grant_contract_if_needed",
				"run_child_sandbox_external_account_auth_status_only",
				"report_repair_evidence",
				"deploy",
				"prepare_release_handoff",
			},
			ForbiddenActions: []string{
				"read_or_print_secret_values",
				"run_mailbox_adapter_query",
				"read_mailbox_contents",
				"deploy",
				"restart",
			},
			Status:    session.ProposalStatusExpired,
			ExpiresAt: expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-live-corrupt",
			ProposalID:       "aprop-live-corrupt",
			Status:           session.ContinuationLeaseStatusExpired,
			MaxTurns:         1,
			RemainingTurns:   0,
			LeaseClass:       session.ContinuationLeaseClassDeployRestart,
			AllowedActions:   []string{"deploy", "prepare_release_handoff"},
			ForbiddenActions: []string{"deploy", "restart"},
			ExpiresAt:        expiredAt,
		},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	state, sent, err := rt.RefreshContinuationProposal(context.Background(), 81100, "expired approval callback")
	if err != nil {
		t.Fatalf("RefreshContinuationProposal() err = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want fresh prompt")
	}
	if actionListContains(state.ActionProposal.AllowedActions, "deploy") ||
		actionListContains(state.ContinuationLease.AllowedActions, "deploy") ||
		state.ContinuationLease.LeaseClass == session.ContinuationLeaseClassDeployRestart {
		t.Fatalf("refreshed state = %#v, want deploy authority stripped from negated credential recovery", state)
	}
	if !actionListContains(state.ActionProposal.ForbiddenActions, "deploy") || !actionListContains(state.ActionProposal.ForbiddenActions, "restart") {
		t.Fatalf("forbidden actions = %#v, want deploy/restart preserved", state.ActionProposal.ForbiddenActions)
	}
}

func TestTriggerContinuationExpiresStaleLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8108, UserID: 0, Scope: telegramDMScopeRef(8108)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusApproved,
		DecisionID:        "decision-expired-lease",
		RemainingTurns:    1,
		ApprovedBy:        1002,
		ActionProposal:    session.ActionProposal{ID: "aprop-expired-lease", Summary: "Expired lease", Status: session.ProposalStatusApproved, ExpiresAt: expiredAt},
		ContinuationLease: session.ContinuationLease{ID: "lease-expired", ProposalID: "aprop-expired-lease", Status: session.ContinuationLeaseStatusActive, MaxTurns: 1, RemainingTurns: 1, ApprovedBy: 1002, ExpiresAt: expiredAt},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8108); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("lease status = %q, want expired", got.ContinuationLease.Status)
	}
	if got.ActionProposal.Status != session.ProposalStatusExpired {
		t.Fatalf("proposal status = %q, want expired", got.ActionProposal.Status)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 {
		t.Fatalf("continuation state = %q/%d, want idle/0", got.Status, got.RemainingTurns)
	}
}

func TestTriggerContinuationRunsAsApprovedUser(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &principalRecordingTools{defs: []agent.ToolDef{testExecToolDef()}, supportsPrincipal: true}
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	key := session.SessionKey{ChatID: 8103, UserID: 0, Scope: telegramDMScopeRef(8103)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusApproved, RemainingTurns: 1, ApprovedBy: 1002}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8103); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if tools.lastPrincipal.Role != principal.RoleApprovedUser {
		t.Fatalf("last principal role = %q, want approved_user", tools.lastPrincipal.Role)
	}
	if tools.lastPrincipal.TelegramUserID != 1002 {
		t.Fatalf("last principal user id = %d, want 1002", tools.lastPrincipal.TelegramUserID)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle {
		t.Fatalf("status = %q, want idle when continuation consensus is missing", got.Status)
	}
	if got.ApprovedBy != 0 {
		t.Fatalf("ApprovedBy = %d, want cleared after approved continuation turn", got.ApprovedBy)
	}
	if got.HandshakeBlockedReason == "" {
		t.Fatal("HandshakeBlockedReason empty, want explicit reason when continuation is not offered again")
	}
}

func TestTriggerSandboxedOrganicProposalContinuationDowngradesAdminToApprovedUserSandbox(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{}}
	rt.interactiveDMAssembler = recorder

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8104, UserID: 0, Scope: telegramDMScopeRef(8104)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "organic-proposal-sandbox",
		Objective:      "Run one Organic proposal system-change step.",
		StageSummary:   "Patch one local file and report evidence.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-organic-proposal-sandbox",
			Summary:        "Patch one local file",
			RiskClass:      "system_change",
			AllowedActions: []string{organicProposalSandboxAction, organicProposalSandboxWriteBoundary},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:organic-proposal-sandbox",
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-organic-proposal-sandbox",
			ProposalID:       "aprop-organic-proposal-sandbox",
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         1,
			RemainingTurns:   1,
			AllowedActions:   []string{organicProposalSandboxAction, organicProposalSandboxWriteBoundary},
			ForbiddenActions: []string{"deploy"},
			ExpiresAt:        expiresAt,
			PlanHash:         "sha256:organic-proposal-sandbox",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), 8104); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called")
	}
	if recorder.input.Actor.Role != principal.RoleApprovedUser {
		t.Fatalf("actor role = %q, want approved_user sandbox execution", recorder.input.Actor.Role)
	}
	if recorder.input.Actor.TelegramUserID != 1001 {
		t.Fatalf("actor user id = %d, want admin approver preserved", recorder.input.Actor.TelegramUserID)
	}
	if recorder.input.Scope.Profile.Mode != sandbox.ModeIsolated || recorder.input.Scope.Profile.Network != sandbox.NetworkDeny {
		t.Fatalf("scope profile = %#v, want isolated network-deny approved_user sandbox", recorder.input.Scope.Profile)
	}
	if !strings.Contains(recorder.input.Scope.WorkingRoot, "isolated/workspaces/1001") {
		t.Fatalf("working root = %q, want admin approver isolated user workspace", recorder.input.Scope.WorkingRoot)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var consumed session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationConsumed {
			consumed = event
		}
	}
	if consumed.ID == 0 {
		t.Fatalf("events = %#v, want continuation consumed event", events)
	}
	payload := executionEventPayload(consumed.PayloadJSON)
	if payloadString(payload, "execution_principal_role") != string(principal.RoleApprovedUser) {
		t.Fatalf("execution role payload = %q, want approved_user", payloadString(payload, "execution_principal_role"))
	}
	if payloadString(payload, "sandbox_profile") != organicProposalSandboxProfile || payloadString(payload, "sandboxed_from_role") != string(principal.RoleAdmin) {
		t.Fatalf("sandbox payload = profile %q from %q, want approved_user_isolated from admin", payloadString(payload, "sandbox_profile"), payloadString(payload, "sandboxed_from_role"))
	}
}

func TestTriggerContinuationFailsClosedWithoutRecordedApprover(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8104, UserID: 0, Scope: telegramDMScopeRef(8104)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusApproved, RemainingTurns: 1}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	err = rt.TriggerContinuation(context.Background(), 8104)
	if err == nil || !strings.Contains(err.Error(), "approver is not recorded") {
		t.Fatalf("TriggerContinuation() err = %v, want missing approver error", err)
	}
}

func TestTriggerContinuationUsesMachineAuthoredContinuationEventText(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8105, UserID: 0, Scope: telegramDMScopeRef(8105)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		StageSummary:   "Bundled Phase 4B: one bounded mail-child read-only adapter proof",
		RemainingTurns: 1,
		ApprovedBy:     1002,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-phase-4b-rebundled-email-proof",
			OperationID:   "phase-4b-rebundled-email-proof",
			Summary:       "Bundled Phase 4B: one bounded mail-child read-only adapter proof",
			BoundedEffect: "Inspect current email due/backoff state, run at most one bounded read-only proof, then report.",
			RiskClass:     "status_check",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-phase-4b-rebundled-email-proof",
			ProposalID:     "aprop-phase-4b-rebundled-email-proof",
			Status:         session.ContinuationLeaseStatusActive,
			LeaseClass:     session.ContinuationLeaseClassLocalWorkspace,
			AllowedActions: []string{string(WorkModeReadOnly)},
			MaxTurns:       1,
			RemainingTurns: 1,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8105); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.lastGovernorMsgs) == 0 {
		t.Fatal("lastGovernorMsgs empty, want continuation turn input")
	}
	last := provider.lastGovernorMsgs[len(provider.lastGovernorMsgs)-1]
	if last.Role != "user" {
		t.Fatalf("last role = %q, want user-compatible provider input", last.Role)
	}
	for _, want := range []string{
		approvedContinuationEventText,
		"Approved work:",
		"Next: Bundled Phase 4B: one bounded mail-child read-only adapter proof",
		"Scope: Inspect current email due/backoff state, run at most one bounded read-only proof, then report.",
	} {
		if !strings.Contains(last.Content, want) {
			t.Fatalf("last content = %q, want substring %q", last.Content, want)
		}
	}
	for _, notWant := range []string{"proposal_id:", "operation_id:", "lease_id:", "risk_class:", "aprop-", "lease-phase-4b"} {
		if strings.Contains(last.Content, notWant) {
			t.Fatalf("last content = %q, did not want internal fragment %q", last.Content, notWant)
		}
	}
}

func testPersonaContinuationProposal(decision session.ContinuationIntentDecision, rationale string) string {
	lines := []string{
		"INSPECT: no",
		"QUESTION: no",
		"ANSWER: yes",
		"CONTINUATION_SCHEMA_VERSION: 1",
		"CONTINUATION_INTENT: " + string(decision),
		"CONTINUATION_NEXT_STEP: Resume the next bounded step.",
		"CONTINUATION_CONFIDENCE: medium",
	}
	if strings.TrimSpace(rationale) != "" {
		lines = append(lines, "CONTINUATION_RATIONALE: "+strings.TrimSpace(rationale))
	}
	return strings.Join(lines, "\n")
}

func testGovernorContinuationRatification(decision session.ContinuationIntentDecision, rationale string, ratified bool) string {
	ratifiedToken := "no"
	if ratified {
		ratifiedToken = "yes"
	}
	lines := []string{
		"INSPECT: no",
		"QUESTION: no",
		"ANSWER: yes",
		"RATIFICATION: accept",
		"PLAN:",
		"- Continue with the next bounded step.",
		"CONTINUATION_SCHEMA_VERSION: 1",
		"CONTINUATION_INTENT: " + string(decision),
		"CONTINUATION_RATIFIED: " + ratifiedToken,
		"CONTINUATION_NEXT_STEP: Continue with the next bounded step.",
		"CONTINUATION_CONSTRAINTS: Stay within the current objective and local repo scope.",
		"CONTINUATION_CONFIDENCE: high",
	}
	if strings.TrimSpace(rationale) != "" {
		lines = append(lines, "CONTINUATION_RATIONALE: "+strings.TrimSpace(rationale))
	}
	return strings.Join(lines, "\n")
}
