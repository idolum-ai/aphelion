//go:build linux

package runtime

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDoctorTelegramSummarySystemNoteUsesOutcomeStructure(t *testing.T) {
	t.Parallel()

	note := doctorTelegramSummarySystemNote()
	for _, want := range []string{
		"Role: You are compressing a /health diagnose report for Telegram.",
		"## Goal",
		"shortest useful operator-facing health summary",
		"## Success Criteria",
		"## Constraints",
		"## Output",
		"Return one operator-facing message only.",
		"Health diagnosis — read-only",
		"Public command name is /health diagnose",
		"do not mention /doctor in operator-visible output",
		"## Stop Rules",
		"Do not include exhaustive logs",
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("doctor summary prompt missing %q: %q", want, note)
		}
	}
}

func TestDoctorAuthorityProjectionReportsConsistencyFindings(t *testing.T) {
	cfg, store, _, _ := buildRuntimeFixtures(t)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	key := session.SessionKey{ChatID: 99150, UserID: 0, Scope: telegramDMScopeRef(99150)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:     session.ContinuationStatusPending,
		DecisionID: "missing-decision",
		ActionProposal: session.ActionProposal{
			ID:        "proposal-missing-decision",
			Status:    session.ProposalStatusPending,
			ExpiresAt: now.Add(10 * time.Minute),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-expired",
			ProposalID:     "different-proposal",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       2,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if _, err := store.CreateOperatorAutoApprovalLease(session.OperatorAutoApprovalLease{
		ID:          "auto-authority",
		AdminUserID: 1001,
		ChatID:      99150,
		Scope:       session.OperatorAutoApprovalScopeWorkspace,
		CreatedAt:   now.Add(-time.Minute),
		ExpiresAt:   now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-expired-runtime",
		Kind:           session.CapabilityKindTool,
		TargetResource: "sample_tool",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       "{}",
		Constraints:    "{}",
		ExpiresAt:      now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	rt := &Runtime{cfg: cfg, store: store}
	var b strings.Builder
	rt.writeDoctorAuthorityProjection(&b, now)
	report := b.String()
	for _, want := range []string{
		`authority_projection_status="needs_attention"`,
		`authority_autoapproval_active_leases="1"`,
		`code="active_capability_grant_expired"`,
		`code="child_runtime_contract_missing"`,
		`code="continuation_lease_proposal_mismatch"`,
		`code="expired_continuation_lease"`,
		`code="pending_proposal_missing_decision"`,
		`suggested_repair="expire, refresh, or revoke the capability grant before the next child/tool wake"`,
		`apply_action="expire_capability_grant"`,
		`apply_scope="capability_grant"`,
		`applicable=true`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("authority projection report missing %q:\n%s", want, report)
		}
	}
}

func TestDoctorAuthorityProjectionHealthyWhenRecordsConsistent(t *testing.T) {
	cfg, store, _, _ := buildRuntimeFixtures(t)
	now := time.Date(2026, 5, 10, 13, 0, 0, 0, time.UTC)
	key := session.SessionKey{ChatID: 99151, UserID: 0, Scope: telegramDMScopeRef(99151)}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:          "decision-present",
		OwnerKey:    session.SessionIDForKey(key),
		Kind:        "continuation",
		ChatID:      99151,
		SenderID:    1001,
		Prompt:      "Approve continuation?",
		ChoicesJSON: `[{"id":"continue","label":"Continue"}]`,
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("UpsertPendingDecision() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:     session.ContinuationStatusPending,
		DecisionID: "decision-present",
		ActionProposal: session.ActionProposal{
			ID:        "proposal-present",
			Status:    session.ProposalStatusPending,
			ExpiresAt: now.Add(10 * time.Minute),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-present",
			ProposalID:     "proposal-present",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       2,
			RemainingTurns: 2,
			ExpiresAt:      now.Add(10 * time.Minute),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-valid-runtime",
		Kind:           session.CapabilityKindTool,
		TargetResource: "sample_tool",
		GrantedTo:      core.DurableAgentPrincipal("child-beta"),
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_paths":["` + t.TempDir() + `"]}}`,
		Constraints:    "{}",
		ExpiresAt:      now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	rt := &Runtime{cfg: cfg, store: store}
	var b strings.Builder
	rt.writeDoctorAuthorityProjection(&b, now)
	report := b.String()
	for _, want := range []string{
		`authority_projection_status="healthy"`,
		`authority_finding_count="0"`,
		"authority_findings:\n- none",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("authority projection report missing %q:\n%s", want, report)
		}
	}
}

func TestDoctorMissionLedgerShowsHandoffAndResultEvidence(t *testing.T) {
	cfg, store, _, _ := buildRuntimeFixtures(t)
	now := time.Date(2026, 5, 10, 13, 30, 0, 0, time.UTC)
	mission, err := store.UpsertMission(session.MissionState{
		ID:        "mission-release-proof",
		Title:     "Release proof",
		Objective: "Track release restart evidence.",
		Scope:     "system",
		Owner:     "aphelion",
		Status:    session.MissionStatusActive,
	}, "test", "create")
	if err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}
	if _, err := store.CreateMissionHandoff(session.MissionHandoff{
		ID:               "handoff-release-restart",
		MissionID:        mission.ID,
		OperationID:      "op-release",
		PlannedAction:    "restart aphelion.service",
		RecoveryQuestion: "Did restart verification pass?",
	}); err != nil {
		t.Fatalf("CreateMissionHandoff() err = %v", err)
	}
	if _, err := store.CreateMissionHandoff(session.MissionHandoff{
		ID:            "handoff-build",
		MissionID:     mission.ID,
		OperationID:   "op-release",
		PlannedAction: "build release artifact",
	}); err != nil {
		t.Fatalf("CreateMissionHandoff(build) err = %v", err)
	}
	if _, err := store.RecordMissionResult(session.MissionResult{
		HandoffID:     "handoff-build",
		MissionID:     mission.ID,
		OperationID:   "op-release",
		Status:        "completed",
		Summary:       "build artifact verified",
		RemainingRisk: "restart still pending",
	}); err != nil {
		t.Fatalf("RecordMissionResult() err = %v", err)
	}

	rt := &Runtime{cfg: cfg, store: store}
	var b strings.Builder
	rt.writeDoctorMissionLedger(&b, session.SessionKey{ChatID: 1001, Scope: telegramDMScopeRef(1001)}, now)
	report := b.String()
	for _, want := range []string{
		`mission_pending_handoffs="1"`,
		"pending_mission_handoffs:",
		`id=handoff-release-restart`,
		`action="restart aphelion.service"`,
		"recent_mission_results:",
		`handoff_id=handoff-build`,
		`summary="build artifact verified"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("mission ledger report missing %q:\n%s", want, report)
		}
	}
}

func TestAuthorityProjectionReportsRemainingRoadmapChecks(t *testing.T) {
	cfg, store, _, _ := buildRuntimeFixtures(t)
	now := time.Date(2026, 5, 10, 14, 0, 0, 0, time.UTC)
	rt := &Runtime{cfg: cfg, store: store}

	leaseKey := session.SessionKey{ChatID: 99152, UserID: 0, Scope: telegramDMScopeRef(99152)}
	if err := store.UpdateContinuationState(leaseKey, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		ParkedAt:       now.Add(-5 * time.Minute),
		ParkedReason:   "deploy_restart",
		RemainingTurns: 1,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-without-proposal",
			Status:         session.ContinuationLeaseStatusActive,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	blockedKey := session.SessionKey{ChatID: 99153, UserID: 0, Scope: telegramDMScopeRef(99153)}
	if err := store.UpdateOperationState(blockedKey, session.OperationState{
		ID:     "op-blocked-no-escalation",
		Status: session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{{
			ID:                "phase-blocked",
			Status:            session.PlanStatusInProgress,
			BlockedReasonCode: "needs_external_authority",
		}}},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	if _, err := store.AppendExecutionEvents(leaseKey, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventAutoApprovalUsed,
		Stage:       "auto_approval",
		Status:      "used",
		PayloadJSON: `{"lease_id":"auto-scope-mismatch","scope":"workspace","work_mode":"deploy"}`,
		CreatedAt:   now.Add(-time.Minute),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:         "capg-invoked-without-request",
		Kind:            session.CapabilityKindTool,
		TargetResource:  "sample_tool",
		GrantedTo:       core.DurableAgentPrincipal("child-gamma"),
		AllowedActions:  []string{"invoke"},
		Status:          session.CapabilityGrantStatusActive,
		Contract:        `{"child_runtime":{"readonly_paths":["` + t.TempDir() + `"]}}`,
		Constraints:     "{}",
		ExpiresAt:       now.Add(time.Hour),
		InvocationCount: 1,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	if _, err := store.UpsertTailnetGrantBinding(session.TailnetGrantBinding{
		BindingID:         "tailnet-bind-missing-active-grant",
		GrantID:           "capg-missing",
		SurfaceID:         "tailnet:missing:surface",
		GrantedTo:         core.DurableAgentPrincipal("child-gamma"),
		CapabilityKind:    string(session.CapabilityKindNetworkAccess),
		TargetResource:    "grafana.tailnet",
		DesiredPolicyJSON: `{"grant_id":"capg-missing"}`,
		Status:            session.TailnetGrantBindingStatusApplied,
		AppliedPolicyHash: "sha256:applied",
	}); err != nil {
		t.Fatalf("UpsertTailnetGrantBinding() err = %v", err)
	}

	snapshot, err := rt.AuthorityStatusSnapshot(now)
	if err != nil {
		t.Fatalf("AuthorityStatusSnapshot() err = %v", err)
	}
	for _, want := range []string{
		"active_continuation_lease_missing_proposal",
		"parked_lease_needs_recovery_review",
		"blocked_phase_missing_escalation",
		"auto_approval_used_outside_scope",
		"capability_grant_invocation_missing_turn_lease_evidence",
		"tailnet_binding_surface_missing",
		"tailnet_binding_active_grant_missing",
	} {
		if !authoritySnapshotHasFinding(snapshot, want) {
			t.Fatalf("authority findings = %#v, want %s", snapshot.Findings, want)
		}
	}
}

func authoritySnapshotHasFinding(snapshot core.AuthorityStatusSnapshot, code string) bool {
	for _, finding := range snapshot.Findings {
		if strings.TrimSpace(finding.Code) == code {
			return true
		}
	}
	return false
}

func TestRunDoctorOncePersistsDeliversAndRedactsDiagnostics(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "State of Things\nRuntime is diagnosable.\n\nRecommendations\nKeep /health diagnose read-only."
	cfg.Agent.BootstrapFiles = []string{"SOUL.md", "IDENTITY.md", "AGENTS.md"}
	cfg.Agent.DynamicFiles = []string{"MEMORY.md", "SKILLS.md", "memory/knowledge.md", "memory/decisions.md"}

	root := cfg.Agent.SharedMemoryRoot
	if err := os.WriteFile(filepath.Join(root, "SOUL.md"), []byte("Idolum (System) is the governor of this system.\nAphelion is the repo/service/harness that hosts it.\n"), 0o600); err != nil {
		t.Fatalf("write SOUL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "IDENTITY.md"), []byte("Name: Idolum (System)\nAphelion: repo/service/harness\nIdolum (System) decides.\nIdolum speaks.\n"), 0o600); err != nil {
		t.Fatalf("write IDENTITY.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Agent topology:\n- Idolum (System) is the governor/system.\n- Idolum is the public-facing persona.\n- Aphelion is the repo/service/harness.\n"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILLS.md"), []byte("# Skills\n\n- [Commit Archaeology](practices/commit-archeology.md)"), 0o600); err != nil {
		t.Fatalf("write SKILLS.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "practices"), 0o755); err != nil {
		t.Fatalf("mkdir practices: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "practices", "commit-archeology.md"), []byte("# Commit Archaeology\n\nDiagnose commits with evidence."), 0o600); err != nil {
		t.Fatalf("write practice: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory", "knowledge.md"), []byte("# knowledge\n\n- Provider timeouts must surface to Telegram."), 0o600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory", "decisions.md"), []byte("# decisions\n\n- /health diagnose must not run tools."), 0o600); err != nil {
		t.Fatalf("write decisions: %v", err)
	}
	logPath := filepath.Join(filepath.Dir(cfg.Sessions.DBPath), "aphelion.log")
	if err := os.WriteFile(logPath, []byte("WARN provider timeout api_key = \"sk-secret-do-not-leak\"\nAuthorization: Bearer bearer-secret\nOPENAI_API_KEY=sk-env-secret\n{\"Authorization\":\"Bearer json-secret\",\"password\":\"pw-secret\"}\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventProviderAttemptFailed,
		Stage:       "provider",
		Status:      "failed",
		PayloadJSON: `{"error":"codex timeout"}`,
		CreatedAt:   time.Now().Add(-time.Minute),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	err = rt.runDoctorOnce(context.Background(), core.InboundMessage{
		ChatID:     1001,
		SenderID:   1001,
		SenderName: "admin",
		ChatType:   "private",
		Text:       "/health diagnose",
		MessageID:  17,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("runDoctorOnce() err = %v", err)
	}

	sender.mu.Lock()
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	edits := append([]messageEdit(nil), sender.edits...)
	inlineCount := len(sender.inline)
	editInlineCount := len(sender.editInline)
	sender.mu.Unlock()

	if len(sent) != 2 {
		t.Fatalf("sent len = %d, want progress message and final report", len(sent))
	}
	if sent[0].ChatID != 1001 || !strings.HasPrefix(sent[0].Text, "Working...") || strings.Contains(sent[0].Text, "Thinking") || !strings.Contains(sent[0].Text, "Loading prompt and memory context") {
		t.Fatalf("progress message = %#v, want live doctor progress", sent[0])
	}
	if sent[0].ReplyTo == nil || *sent[0].ReplyTo != 17 {
		t.Fatalf("progress reply_to = %#v, want 17", sent[0].ReplyTo)
	}
	if sent[1].ChatID != 1001 || !strings.Contains(sent[1].Text, "State of Things") {
		t.Fatalf("report message = %#v, want health diagnosis report to admin", sent[1])
	}
	if sent[1].ReplyTo == nil || *sent[1].ReplyTo != 17 {
		t.Fatalf("report reply_to = %#v, want 17", sent[1].ReplyTo)
	}
	if inlineCount != 0 || editInlineCount != 0 {
		t.Fatalf("inline progress = sent:%d edited:%d, want plain progress without controls", inlineCount, editInlineCount)
	}
	if len(edits) == 0 {
		t.Fatal("progress edits = 0, want live progress updates")
	}
	lastEdit := edits[len(edits)-1]
	if lastEdit.ChatID != 1001 || lastEdit.MessageID != 1 || !strings.HasPrefix(lastEdit.Text, "Done.") || !strings.Contains(lastEdit.Text, "Sending the health diagnosis report to Telegram") {
		t.Fatalf("final progress edit = %#v, want completed doctor progress", lastEdit)
	}

	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("messages len = %d, want synthetic /health diagnose turn", len(sess.Messages))
	}
	userMsg := sess.Messages[len(sess.Messages)-2]
	assistantMsg := sess.Messages[len(sess.Messages)-1]
	if userMsg.Role != "user" || userMsg.Content != "/health diagnose" {
		t.Fatalf("user doctor message = %#v, want persisted /health diagnose request", userMsg)
	}
	if assistantMsg.Role != "assistant" || !strings.Contains(assistantMsg.Content, "Runtime is diagnosable") {
		t.Fatalf("assistant doctor message = %#v, want persisted report", assistantMsg)
	}
	latest, ok, err := rt.LatestDoctorReport(context.Background(), 1001, 1001)
	if err != nil {
		t.Fatalf("LatestDoctorReport() err = %v", err)
	}
	if !ok {
		t.Fatal("LatestDoctorReport() ok = false, want persisted report")
	}
	if latest.FullReport != assistantMsg.Content || latest.TelegramReport != assistantMsg.FloorContent || latest.TurnIndex != assistantMsg.TurnIndex {
		t.Fatalf("LatestDoctorReport() = %#v, want persisted assistant doctor message", latest)
	}

	var userPrompt string
	provider.mu.Lock()
	if len(provider.lastGovernorTools) != 0 {
		t.Fatalf("doctor provider tools = %#v, want none for read-only diagnostics", provider.lastGovernorTools)
	}
	for _, msg := range provider.lastGovernorMsgs {
		if msg.Role == "user" {
			userPrompt += "\n" + msg.Content
		}
	}
	provider.mu.Unlock()
	for _, want := range []string{
		doctorRequestMarker,
		"memory/knowledge.md",
		"provider.attempt.failed",
		"semantic_enabled",
		"Recent Service Log Tail",
		"Known Issue Status Checks",
		"Maintainer Delegate",
		"maintainer_delegate_status=\"absent\"",
		"issue=prompt_identity_canonical status=likely_fixed",
		"issue=dynamic_skills_prompt_loading status=likely_fixed",
		"tailnet_surfaces: none",
		"allowed_statuses: active, likely_fixed, historical_resolved, residual_risk, unknown",
	} {
		if !strings.Contains(userPrompt, want) {
			t.Fatalf("doctor prompt missing %q:\n%s", want, userPrompt)
		}
	}
	for _, secret := range []string{"sk-secret-do-not-leak", "bearer-secret", "sk-env-secret", "json-secret", "pw-secret"} {
		if strings.Contains(userPrompt, secret) {
			t.Fatalf("doctor prompt leaked secret %q:\n%s", secret, userPrompt)
		}
	}
}

func TestRunDoctorOnceTerminalizesBoundTelegramIngress(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "State of Things\nHealth diagnosis ingress is bound.\n\nRecommendations\nKeep diagnosis recoverable."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Date(2026, time.May, 16, 12, 20, 0, 0, time.UTC)
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     "telegram:callback-work:doctor",
		UpdateID:    9901,
		UpdateKind:  "callback_doctor",
		ChatID:      1001,
		SenderID:    1001,
		MessageID:   77,
		SessionID:   session.SessionIDForKey(key),
		Status:      session.TelegramIngressUpdateQueued,
		InboundJSON: `{"ChatID":1001,"SenderID":1001,"Text":"/health diagnose","MessageID":77}`,
		AcceptedAt:  now,
		QueuedAt:    now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	err = rt.runDoctorOnce(context.Background(), core.InboundMessage{
		ChatID:          1001,
		SenderID:        1001,
		SenderName:      "admin",
		ChatType:        "private",
		Text:            "/health diagnose",
		MessageID:       77,
		IngressSurface:  "telegram:callback-work:doctor",
		IngressUpdateID: 9901,
	}, now)
	if err != nil {
		t.Fatalf("runDoctorOnce() err = %v", err)
	}

	record, ok, err := store.TelegramIngressUpdate("telegram:callback-work:doctor", 9901)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%t err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateCompleted || record.TurnRunID == 0 || record.CompletedAt.IsZero() {
		t.Fatalf("doctor ingress = %#v, want completed row tied to turn run", record)
	}
}

func TestRunDoctorOnceDelegatesToActiveMaintainerChild(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "State of Things\nMaintainer delegated diagnosis is healthy.\n\nRecommendations\nKeep implementation work in /tmp PR clones."
	childWorkspace := filepath.Join(t.TempDir(), "maintainer", "workspace")
	childMemory := filepath.Join(t.TempDir(), "maintainer", "memory")
	agent := core.DurableAgent{
		AgentID:            "aphelion-maintainer-live",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LocalStorageRoots:  []string{childWorkspace, childMemory},
		Status:             "active",
		BootstrapLLM:       durableGroupTestBootstrapLLM(),
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:                   "Review Aphelion and propose fixes.",
			CapabilityEnvelope:        []string{"session_log_read", "repo_read", "bounded_review_artifact", "patch_proposal"},
			OutboundMode:              "read_only",
			DriftPolicy:               "admin_review",
			PublicSurfaceMode:         "explicit_parent_relay_only",
			SharedInferenceReuse:      "disabled",
			SharedInferenceReuseScope: "public_prefix_only",
		}),
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	writeMaintainerProvenance(t, childMemory)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	err = rt.runDoctorOnce(context.Background(), core.InboundMessage{
		ChatID:     1001,
		SenderID:   1001,
		SenderName: "admin",
		ChatType:   "private",
		Text:       "/health diagnose",
		MessageID:  41,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("runDoctorOnce() err = %v", err)
	}

	provider.mu.Lock()
	var userPrompt string
	var systemPrompt string
	for _, msg := range provider.lastGovernorMsgs {
		if msg.Role == "user" {
			userPrompt += "\n" + msg.Content
		}
		if msg.Role == "system" {
			systemPrompt += "\n" + msg.Content
		}
	}
	provider.mu.Unlock()
	for _, want := range []string{
		"maintainer_delegate_status=\"active\"",
		"maintainer_delegate_agent_id=\"aphelion-maintainer-live\"",
		"Maintainer runtime boundary",
		"/tmp clone",
		"GitHub PR",
	} {
		if !strings.Contains(userPrompt, want) && !strings.Contains(systemPrompt, want) {
			t.Fatalf("doctor delegate prompt missing %q\nsystem:\n%s\nuser:\n%s", want, systemPrompt, userPrompt)
		}
	}

	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	assistantMsg := sess.Messages[len(sess.Messages)-1]
	if !strings.Contains(assistantMsg.FloorMetadata, "doctor_delegate_agent_id=aphelion-maintainer-live") ||
		!strings.Contains(assistantMsg.FloorMetadata, "doctor_delegate_artifact=artifacts/reports/") {
		t.Fatalf("assistant floor metadata = %q, want maintainer delegate artifact", assistantMsg.FloorMetadata)
	}
	reportFiles, err := filepath.Glob(filepath.Join(childMemory, "artifacts", "reports", "*-doctor.md"))
	if err != nil {
		t.Fatalf("Glob(report) err = %v", err)
	}
	if len(reportFiles) != 1 {
		t.Fatalf("report files = %#v, want one maintainer artifact", reportFiles)
	}
	reportRaw, err := os.ReadFile(reportFiles[0])
	if err != nil {
		t.Fatalf("ReadFile(report artifact) err = %v", err)
	}
	if !strings.Contains(string(reportRaw), "Maintainer delegated diagnosis is healthy") ||
		!strings.Contains(string(reportRaw), "aphelion-maintainer-live") {
		t.Fatalf("report artifact = %q, want delegated doctor report", reportRaw)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(childMemory, "artifacts", "ARTIFACTS.json"))
	if err != nil {
		t.Fatalf("ReadFile(ARTIFACTS.json) err = %v", err)
	}
	if !strings.Contains(string(manifestRaw), `"kind": "doctor_report"`) ||
		!strings.Contains(string(manifestRaw), `"source": "doctor_delegate"`) {
		t.Fatalf("ARTIFACTS.json = %s, want doctor artifact manifest entry", manifestRaw)
	}
}
