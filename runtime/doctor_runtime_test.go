//go:build linux

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestDoctorTelegramSummarySystemNoteUsesOutcomeStructure(t *testing.T) {
	t.Parallel()

	note := doctorTelegramSummarySystemNote()
	for _, want := range []string{
		"Role: You are compressing a /doctor report for Telegram.",
		"## Goal",
		"shortest useful operator-facing health summary",
		"## Success Criteria",
		"## Constraints",
		"## Output",
		"Return one operator-facing message only.",
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
		`next_repair="expire, refresh, or revoke the capability grant before the next child/tool wake"`,
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
	provider.replyText = "State of Things\nRuntime is diagnosable.\n\nRecommendations\nKeep /doctor read-only."
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
	if err := os.WriteFile(filepath.Join(root, "memory", "decisions.md"), []byte("# decisions\n\n- /doctor must not run tools."), 0o600); err != nil {
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
		Text:       "/doctor",
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
	if sent[0].ChatID != 1001 || !strings.Contains(sent[0].Text, "Thinking") || !strings.Contains(sent[0].Text, "Loading prompt and memory context") {
		t.Fatalf("progress message = %#v, want live doctor progress", sent[0])
	}
	if sent[0].ReplyTo == nil || *sent[0].ReplyTo != 17 {
		t.Fatalf("progress reply_to = %#v, want 17", sent[0].ReplyTo)
	}
	if sent[1].ChatID != 1001 || !strings.Contains(sent[1].Text, "State of Things") {
		t.Fatalf("report message = %#v, want doctor report to admin", sent[1])
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
	if lastEdit.ChatID != 1001 || lastEdit.MessageID != 1 || !strings.HasPrefix(lastEdit.Text, "Done.") || !strings.Contains(lastEdit.Text, "Sending the doctor report to Telegram") {
		t.Fatalf("final progress edit = %#v, want completed doctor progress", lastEdit)
	}

	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("messages len = %d, want synthetic /doctor turn", len(sess.Messages))
	}
	userMsg := sess.Messages[len(sess.Messages)-2]
	assistantMsg := sess.Messages[len(sess.Messages)-1]
	if userMsg.Role != "user" || userMsg.Content != "/doctor" {
		t.Fatalf("user doctor message = %#v, want persisted /doctor request", userMsg)
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
		Text:       "/doctor",
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

func TestRunDoctorOnceCondensesOversizedTelegramReport(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	fullReport := "State of Things\n" + strings.Repeat("Active failure: prioritize fixing provider retry visibility before lower-risk cleanup. Evidence points to alert fatigue and oversized doctor output. ", 90)
	summary := strings.Join([]string{
		"State of Things",
		"Top fix: keep provider retry and timeout failures visible in Telegram without flooding the chat.",
		"",
		"Most Important Fix",
		"1. active: tighten the alert/progress path so failures are visible once, deduplicated, and actionable.",
		"",
		"Residual Risk",
		"- residual_risk: full details stay in session history; Telegram gets this prioritized summary.",
	}, "\n")
	provider.replyText = fullReport
	provider.doctorSummaryReplyText = summary

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
		Text:       "/doctor",
		MessageID:  31,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("runDoctorOnce() err = %v", err)
	}

	sender.mu.Lock()
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	edits := append([]messageEdit(nil), sender.edits...)
	sender.mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("sent len = %d, want progress and condensed report", len(sent))
	}
	if got := doctorCharCount(sent[1].Text); got > doctorTelegramMaxChars {
		t.Fatalf("telegram report chars = %d, want <= %d", got, doctorTelegramMaxChars)
	}
	if sent[1].Text != summary {
		t.Fatalf("telegram report = %q, want condensed summary", sent[1].Text)
	}
	if !doctorEditsContain(edits, "Condensing the doctor report for one Telegram message") {
		t.Fatalf("progress edits = %#v, want condensation progress", edits)
	}

	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("messages len = %d, want synthetic /doctor turn", len(sess.Messages))
	}
	assistantMsg := sess.Messages[len(sess.Messages)-1]
	if assistantMsg.Content != strings.TrimSpace(fullReport) {
		t.Fatalf("assistant content chars = %d, want full report preserved", len(assistantMsg.Content))
	}
	if assistantMsg.FloorContent != summary {
		t.Fatalf("assistant floor = %q, want telegram summary", assistantMsg.FloorContent)
	}
	if !strings.Contains(assistantMsg.FloorMetadata, "doctor_full_report_chars=") || !strings.Contains(assistantMsg.FloorMetadata, "doctor_telegram_limit_chars=") {
		t.Fatalf("floor metadata = %q, want doctor report sizing metadata", assistantMsg.FloorMetadata)
	}

	provider.mu.Lock()
	if len(provider.lastDoctorSummaryTools) != 0 {
		t.Fatalf("doctor summary tools = %#v, want none", provider.lastDoctorSummaryTools)
	}
	var summaryPrompt string
	for _, msg := range provider.lastDoctorSummaryMsgs {
		if msg.Role == "user" {
			summaryPrompt += "\n" + msg.Content
		}
	}
	provider.mu.Unlock()
	if !strings.Contains(summaryPrompt, doctorSummaryMarker) || !strings.Contains(summaryPrompt, "service_single_message_limit_chars=") || !strings.Contains(summaryPrompt, "Full report to condense:") {
		t.Fatalf("summary prompt = %q, want telegram condensation instructions", summaryPrompt)
	}
}

func TestDoctorCodexWorkMigrationReviewReportsPersistedInterfaceEvidence(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Work.Codex.AppServerAddress = "ws://127.0.0.1:4666"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:     "op-codex-work",
		Status: session.OperationStatusActive,
		Work: session.WorkOperationMetadata{
			Executor:         "codex",
			CodexLaneMode:    "workspace_write",
			CodexThreadID:    "thread-1",
			CodexLastTurnID:  "turn-1",
			PatchPreview:     "@@ patch",
			CommitLaneStatus: "commit_requires_separate_lease",
			CodexEvents: []session.WorkCodexEvent{
				{Kind: "file_change", Path: "runtime/work_executor.go"},
				{Kind: "command", Command: "go test ./runtime"},
				{Kind: "subagent", Subject: "reviewer"},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status: session.ContinuationStatusApproved,
		ActionProposal: session.ActionProposal{
			ID:             "aprop",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write", "run_tests"},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      time.Now().UTC().Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease",
			Status:         session.ContinuationLeaseStatusActive,
			AllowedActions: []string{"workspace_write", "run_tests"},
			ExpiresAt:      time.Now().UTC().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorCodexWorkMigrationReview(context.Background(), &b, doctorDiagnosticInput{Key: key, Now: time.Now().UTC()})
	report := b.String()
	for _, want := range []string{
		`codex_work_executor="codex"`,
		`codex_work_thread_id="thread-1"`,
		`codex_work_event_count="3"`,
		`codex_work_file_change_events="1"`,
		`codex_work_command_events="1"`,
		`codex_work_subagent_events="1"`,
		`codex_work_commit_lane_status="commit_requires_separate_lease"`,
		`codex_work_migration_status="evidence_present"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("migration review missing %s:\n%s", want, report)
		}
	}
}

func TestDoctorRuntimeConfigReportsAutonomyPolicy(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.DefaultMode = "review_only"
	cfg.Autonomy.Ceiling = "leased"
	cfg.Autonomy.AllowLiveOverrides = true
	cfg.Autonomy.MaxOverrideDuration = "2h"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorRuntimeConfig(&b, pipeline.TurnExecutionContract{}, sandbox.Scope{})
	report := b.String()
	for _, want := range []string{
		`autonomy_default_mode="review_only"`,
		`autonomy_ceiling="leased"`,
		`autonomy_live_overrides="true"`,
		`autonomy_max_override_duration="2h0m0s"`,
		`autonomy_authority_behavior="existing proposal and approval flows"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("runtime config report missing %s:\n%s", want, report)
		}
	}
}

func TestDoctorAutonomyStatusReportsActiveOverridePrecedenceAndExpiry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "leased"
	cfg.Autonomy.AllowLiveOverrides = true
	cfg.Autonomy.MaxOverrideDuration = "2h"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 99140, 1001, "leased 30m workspace uses=2 doctor evidence"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorAutonomyStatus(&b, session.SessionKey{ChatID: 99140, UserID: 0, Scope: telegramDMScopeRef(99140)}, 1001, time.Now().UTC())
	report := b.String()
	for _, want := range []string{
		`autonomy_effective_default_mode="ask_first"`,
		`autonomy_effective_ceiling="leased"`,
		`autonomy_raw_active_lease_count="1"`,
		`autonomy_effective_active_override="true"`,
		`autonomy_active_override_mode="leased"`,
		`autonomy_active_override_scope="workspace"`,
		`autonomy_active_override_max="2"`,
		`autonomy_precedence_status="active_within_ceiling"`,
		`autonomy_expiry_status="active_until_expiry"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("autonomy doctor report missing %s:\n%s", want, report)
		}
	}
}

func TestDoctorAutonomyStatusReportsLegacyLeaseBlockedByConfig(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "ask_first"
	cfg.Autonomy.AllowLiveOverrides = true
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateOperatorAutoApprovalLease(session.OperatorAutoApprovalLease{
		ID:          "doctor-blocked-legacy",
		AdminUserID: 1001,
		ChatID:      99141,
		Scope:       session.OperatorAutoApprovalScopeAll,
		CreatedAt:   now.Add(-time.Minute),
		ExpiresAt:   now.Add(30 * time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorAutonomyStatus(&b, session.SessionKey{ChatID: 99141, UserID: 0, Scope: telegramDMScopeRef(99141)}, 1001, now)
	report := b.String()
	for _, want := range []string{
		`autonomy_effective_ceiling="ask_first"`,
		`autonomy_raw_active_lease_count="1"`,
		`autonomy_effective_active_override="false"`,
		`autonomy_precedence_status="blocked_by_config"`,
		`autonomy_precedence_reason="autonomy mode leased exceeds configured ceiling ask_first"`,
		`autonomy_expiry_status="none"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("autonomy doctor report missing %s:\n%s", want, report)
		}
	}
}

func TestDoctorSandboxReadinessReportsOperatorVisibleWarnings(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Sandbox.Profiles.Admin.Mode = "trusted"
	cfg.Sandbox.Profiles.Admin.Network = "deny"
	cfg.Sandbox.Profiles.ApprovedUser.Mode = "trusted"
	cfg.Sandbox.Profiles.ApprovedUser.Network = "allowlist"
	cfg.Sandbox.Profiles.DurableAgent.Mode = "trusted"
	cfg.Sandbox.Profiles.DurableAgent.Network = "allowlist"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorSandboxReadiness(&b, time.Now().UTC())
	report := b.String()
	for _, want := range []string{
		`sandbox_readiness_issue_count="2"`,
		`code="trusted_network_policy_unenforced"`,
		`role="admin"`,
		`code="non_admin_trusted_sandbox"`,
		`role="approved_user"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("sandbox doctor report missing %s:\n%s", want, report)
		}
	}
}

func TestDoctorIssueStatusChecksGenericTelegramChildBotRunnerReadiness(t *testing.T) {
	t.Parallel()

	cfg, _, _, _ := buildRuntimeFixtures(t)
	if err := os.WriteFile(filepath.Join(cfg.Agent.ExecRoot, "main_telegram_child_bot.go"), []byte(`package main
func runTelegramChildBotCommandWithDeps(){}
func validateTelegramChildBotTokenMetadata(){}
type telegramChildBotHealthStatus struct{}
func runTelegramChildBotGetMeSmoke(){}
func runTelegramChildBotDryStart(){}
type telegramChildBotNoSendOutbound struct{}
`), 0o600); err != nil {
		t.Fatalf("write runner source: %v", err)
	}
	docDir := filepath.Join(cfg.Agent.ExecRoot, "docs", "architecture")
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatalf("mkdir doc dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docDir, "telegram-child-bot-runbook.md"), []byte("Implement a generic but narrow telegram-child-bot command.\n"), 0o600); err != nil {
		t.Fatalf("write runner runbook: %v", err)
	}

	rt := &Runtime{cfg: cfg}
	var b strings.Builder
	rt.writeDoctorIssueStatusChecks(&b, doctorDiagnosticInput{Scope: sandbox.Scope{WorkingRoot: cfg.Agent.ExecRoot}})
	report := b.String()
	if !strings.Contains(report, `issue=telegram_child_bot_runner status=likely_fixed`) {
		t.Fatalf("doctor issue checks = %s, want generic child bot runner likely_fixed", report)
	}
}

func TestDoctorExternalChannelAdapterReadinessProjectsGogCLIContract(t *testing.T) {
	t.Setenv(gogCLIRequiredSecretEnvName, "test-redacted-value")
	cfg, store, _, _ := buildRuntimeFixtures(t)
	workspaceRoot := filepath.Join(t.TempDir(), "child", "workspace")
	memoryRoot := filepath.Join(t.TempDir(), "child", "memory")
	runtimeBin := filepath.Join(filepath.Dir(workspaceRoot), "runtime-bin")
	configRoot := filepath.Join(workspaceRoot, ".config", "gogcli")
	if err := os.MkdirAll(filepath.Join(configRoot, "keyring"), 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.MkdirAll(runtimeBin, 0o755); err != nil {
		t.Fatalf("mkdir runtime bin: %v", err)
	}
	for path, body := range map[string]string{
		filepath.Join(runtimeBin, "gog"):                      "#!/usr/bin/env bash\necho gog\n",
		filepath.Join(runtimeBin, "gog_cli"):                  "#!/usr/bin/env bash\nexport XDG_CONFIG_HOME=\"${HOME}/.config\"\nexec \"$(dirname \"$0\")/gog\" \"$@\"\n",
		filepath.Join(configRoot, "config.json"):              `{}`,
		filepath.Join(configRoot, "credentials.json"):         `{}`,
		filepath.Join(configRoot, "keyring", "token:example"): `{}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	agent := core.DurableAgent{
		AgentID:           "child-mail",
		ChannelKind:       "external_channel",
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address: "mailbox@example.test", Account: "mailbox@example.test", Adapter: gogCLIAdapterName, Query: "label:inbox", PollInterval: "24h",
		}},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: gogCLIAdapterName, ImplementationRef: "external:gog_cli", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.UpsertToolInstallRecord(session.ToolInstallRecord{ToolName: gogCLIAdapterName, Status: session.ToolInstallStatusVerified, InstalledAt: now, AttestedAt: now}); err != nil {
		t.Fatalf("UpsertToolInstallRecord() err = %v", err)
	}
	if _, err := store.UpsertToolAuditRecord(session.ToolAuditRecord{ToolName: gogCLIAdapterName, Status: session.ToolAuditStatusPassed, AuditedAt: now}); err != nil {
		t.Fatalf("UpsertToolAuditRecord() err = %v", err)
	}
	if _, err := store.UpsertToolProbeRecord(session.ToolProbeRecord{ToolName: gogCLIAdapterName, Status: session.ToolProbeStatusPassed, ProbedAt: now}); err != nil {
		t.Fatalf("UpsertToolProbeRecord() err = %v", err)
	}
	principalID := core.DurableAgentPrincipal(agent.AgentID)
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-child-mail-gog-tool",
		Kind:           session.CapabilityKindTool,
		TargetResource: gogCLIAdapterName,
		GrantedTo:      principalID,
		AllowedActions: []string{"invoke", "connection_test"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_binds":[{"source":"` + runtimeBin + `","target":"/usr/local/bin"}],"env_from_parent":["GOG_KEYRING_PASSWORD"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(tool) err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-child-mail-gog-account",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "gog_cli:mailbox@example.test",
		GrantedTo:      principalID,
		AllowedActions: []string{"read", "search", "metadata", "connection_test"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_paths":["` + configRoot + `"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(account) err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{ExternalChannel: &core.DurableAgentExternalChannelRuntimeState{
		Adapter: gogCLIAdapterName, LastStatus: "wake_blocked", LastError: "gog_cli keyring backend requires interactive/passphrase material; no TTY is available.", FailureCount: 4, BackoffUntil: now.Add(time.Hour),
	}}
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	rt := &Runtime{cfg: cfg, store: store}
	var b strings.Builder
	rt.writeDoctorExternalChannelAdapterReadiness(&b, doctorDiagnosticInput{Now: now})
	report := b.String()
	for _, want := range []string{
		"classification_contract: external-channel adapter readiness is metadata-only",
		"agent=child-mail adapter=gog_cli",
		"executable=/usr/local/bin/gog_cli",
		"layer=tool_lifecycle status=ready",
		"layer=grant_tool_runtime status=ready",
		"env_from_parent=GOG_KEYRING_PASSWORD",
		"layer=child_config_metadata status=ready",
		"layer=last_wake status=wake_blocked failure_count=4",
		"interactive/passphrase material; no TTY is available",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("doctor adapter readiness = %s, want %s", report, want)
		}
	}
	if strings.Contains(report, "test-redacted-value") {
		t.Fatalf("doctor adapter readiness leaked env value: %s", report)
	}
}

func TestDoctorDesignPrincipleHealthSurfacesRetiredDebtGates(t *testing.T) {
	t.Parallel()

	cfg, _, _, _ := buildRuntimeFixtures(t)
	writeDoctorDesignPrincipleFixture(t, cfg.Agent.ExecRoot, true)

	rt := &Runtime{}
	var b strings.Builder
	rt.writeDoctorDesignPrincipleHealth(&b, doctorDiagnosticInput{Scope: sandbox.Scope{WorkingRoot: cfg.Agent.ExecRoot}})
	report := b.String()
	for _, want := range []string{
		`issue=design_principles_doc status=likely_fixed`,
		`issue=principle_debt_ledger status=likely_fixed`,
		`issue=string_authority_retired status=likely_fixed`,
		`issue=short_debug_path_contract status=likely_fixed`,
		`design_principle_next="keep typed interpretation`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("design principle health = %s, want %s", report, want)
		}
	}
}

func TestDoctorDesignPrincipleHealthFlagsMissingRetirementEvidence(t *testing.T) {
	t.Parallel()

	cfg, _, _, _ := buildRuntimeFixtures(t)
	writeDoctorDesignPrincipleFixture(t, cfg.Agent.ExecRoot, false)

	rt := &Runtime{}
	var b strings.Builder
	rt.writeDoctorDesignPrincipleHealth(&b, doctorDiagnosticInput{Scope: sandbox.Scope{WorkingRoot: cfg.Agent.ExecRoot}})
	report := b.String()
	for _, want := range []string{
		`issue=principle_debt_ledger status=active`,
		`issue=string_authority_retired status=active`,
		`issue=short_debug_path_contract status=active`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("design principle health = %s, want %s", report, want)
		}
	}
}

func writeDoctorDesignPrincipleFixture(t *testing.T, root string, trackDebt bool) {
	t.Helper()
	writeDoctorFixtureFile(t, root, "docs/architecture/design-principles.md", `# Aphelion Design Principles

### Text is presentation, not authority
### Compile contracts; interpret ambiguity
### Short paths to truth
`)
	if trackDebt {
		writeDoctorFixtureFile(t, root, "runtime/interpretation_claims.go", `package runtime
const interpretationClaimsMarker = "INTERPRETATION_CLAIMS"
func interpretCurrentTurnClaims() {}
type InterpretationClaim struct{}
`)
		writeDoctorFixtureFile(t, root, "core/interpretation.go", `package core
type DebugBreadcrumb struct{ TraceID string; InspectCommand string; NextRepairAction string }
`)
		writeDoctorFixtureFile(t, root, "runtime/status_lifecycle.go", `package runtime
func attachPendingItemDebugBreadcrumbs() {}
func pendingItemDebugBreadcrumb() {}
`)
		writeDoctorFixtureFile(t, root, "face/status_render.go", `package face
const _ = "next_repair_action inspect_command"
`)
	}
	writeDoctorFixtureFile(t, root, "docs/architecture/principle-debt.md", `# Aphelion Principle Debt Ledger

## Active Debt

`+map[bool]string{true: "None.", false: "### DP-test"}[trackDebt]+`

## Retired Debt

- Exit gate: replace text inference with typed contracts.
- Debug breadcrumbs: trace_id canonical_record projection inspect_command code_owner next_repair_action.

## Machine-Checked Paths
`)
}

func writeDoctorFixtureFile(t *testing.T, root string, rel string, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDoctorRuntimeAdjudicationsSummarizesStructuredEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9401, UserID: 0, Scope: telegramDMScopeRef(9401)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType: core.ExecutionEventReplyClaimAdjudicated,
		Stage:     "reply",
		Status:    "adjudicated",
		PayloadJSON: `{
			"adjudication_kind":"execution_claim",
			"surface":"final_reply",
			"operator_label":"Reply claim repaired",
			"visible_action":"persona_repaired",
			"findings":[{"kind":"test_execution","claim_type":"test_execution","detail":"test-execution claim has no test-related tool evidence"}]
		}`,
		CreatedAt: now,
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorRuntimeAdjudications(context.Background(), &b, key, now)
	report := b.String()
	for _, want := range []string{"kind=execution_claim", "action=persona_repaired", "Reply claim repaired", "test_execution", "test-execution claim"} {
		if !strings.Contains(report, want) {
			t.Fatalf("doctor adjudications = %q, want %q", report, want)
		}
	}
}

func TestDoctorRuntimeAdjudicationsIncludesContinuationApprovals(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9402, UserID: 0, Scope: telegramDMScopeRef(9402)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType: core.ExecutionEventContinuationAdjudicated,
		Stage:     "continuation",
		Status:    "adjudicated",
		PayloadJSON: `{
			"adjudication_kind":"continuation_approval",
			"surface":"phase_materialization",
			"operator_label":"Continuation approval blocked",
			"visible_action":"blocked_status",
			"findings":[{"kind":"approval_blocked","claim_type":"approval_blocked","detail":"waiting for explicit opt-in"}]
		}`,
		CreatedAt: now,
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorRuntimeAdjudications(context.Background(), &b, key, now)
	report := b.String()
	for _, want := range []string{"kind=continuation_approval", "action=blocked_status", "Continuation approval blocked", "approval_blocked", "waiting for explicit opt-in"} {
		if !strings.Contains(report, want) {
			t.Fatalf("doctor adjudications = %q, want %q", report, want)
		}
	}
}

func TestStartDoctorRejectsNonAdmin(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	err = rt.StartDoctor(context.Background(), core.InboundMessage{
		ChatID:   1002,
		SenderID: 1002,
		ChatType: "private",
		Text:     "/doctor",
	})
	if !errors.Is(err, ErrPrincipalDenied) {
		t.Fatalf("StartDoctor() err = %v, want ErrPrincipalDenied", err)
	}
}

func doctorEditsContain(edits []messageEdit, want string) bool {
	for _, edit := range edits {
		if strings.Contains(edit.Text, want) {
			return true
		}
	}
	return false
}

func writeMaintainerProvenance(t *testing.T, memoryRoot string) {
	t.Helper()
	profileRoot := filepath.Join(memoryRoot, "profile")
	if err := os.MkdirAll(filepath.Join(profileRoot, "archetype", "profile"), 0o755); err != nil {
		t.Fatalf("MkdirAll(profile archetype) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "ARCHETYPE.json"), []byte(`{"name":"aphelion-maintainer","files":["profile/archetype/AGENT.md"]}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(ARCHETYPE.json) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "archetype", "AGENT.md"), []byte("# Aphelion Maintainer\n\nReview and propose fixes.\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(AGENT.md) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "archetype", "profile", "runtime.md"), []byte("Never mutate the local Aphelion clone. Approved implementation uses a /tmp clone and GitHub PR.\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(runtime.md) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "archetype", "profile", "charter.md"), []byte("Review Aphelion and propose fixes.\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(charter.md) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "archetype", "profile", "capabilities.md"), []byte("- session_log_read\n- repo_read\n- patch_proposal\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(capabilities.md) err = %v", err)
	}
}
