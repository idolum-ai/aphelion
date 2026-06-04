//go:build linux

package runtime

import (
	"context"
	"errors"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/runtime/doctor"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
		Text:       "/health diagnose",
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
	if got := doctor.CharCount(sent[1].Text); got > doctor.TelegramMaxChars {
		t.Fatalf("telegram report chars = %d, want <= %d", got, doctor.TelegramMaxChars)
	}
	if sent[1].Text != summary {
		t.Fatalf("telegram report = %q, want condensed summary", sent[1].Text)
	}
	if !doctorEditsContain(edits, "Condensing the health diagnosis report for one Telegram message") {
		t.Fatalf("progress edits = %#v, want condensation progress", edits)
	}

	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("messages len = %d, want synthetic /health diagnose turn", len(sess.Messages))
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
	if !strings.Contains(summaryPrompt, doctor.SummaryMarker) || !strings.Contains(summaryPrompt, "service_single_message_limit_chars=") || !strings.Contains(summaryPrompt, "Full report to condense:") {
		t.Fatalf("summary prompt = %q, want telegram condensation instructions", summaryPrompt)
	}
}

func TestDoctorCodexWorkEvidenceReviewReportsPersistedInterfaceEvidence(t *testing.T) {
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
	rt.writeDoctorCodexWorkEvidenceReview(context.Background(), &b, DiagnosticInput{Key: key, Now: time.Now().UTC()})
	report := b.String()
	for _, want := range []string{
		`codex_work_executor="codex"`,
		`codex_work_thread_id="thread-1"`,
		`codex_work_event_count="3"`,
		`codex_work_file_change_events="1"`,
		`codex_work_command_events="1"`,
		`codex_work_subagent_events="1"`,
		`codex_work_commit_lane_status="commit_requires_separate_lease"`,
		`codex_work_evidence_status="evidence_present"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("evidence review missing %s:\n%s", want, report)
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
		`autonomy_authority_behavior="approvals require an open auto-mode window"`,
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
	if _, err := rt.ConfigureAutonomy(context.Background(), 99140, 1001, "leased 30m workspace doctor evidence"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorAutonomyStatus(&b, session.SessionKey{ChatID: 99140, UserID: 0, Scope: telegramDMScopeRef(99140)}, 1001, time.Now().UTC())
	report := b.String()
	for _, want := range []string{
		`autonomy_effective_default_mode="ask_first"`,
		`autonomy_effective_ceiling="leased"`,
		`autonomy_raw_active_mode_count="1"`,
		`autonomy_effective_active_override="true"`,
		`autonomy_active_override_mode="leased"`,
		`autonomy_active_override_scope="workspace"`,
		`autonomy_precedence_status="active_within_ceiling"`,
		`autonomy_expiry_status="active_until_expiry"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("autonomy doctor report missing %s:\n%s", want, report)
		}
	}
}

func TestDoctorAutonomyStatusReportsExistingModeBlockedByConfig(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "ask_first"
	cfg.Autonomy.AllowLiveOverrides = true
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateOperatorAutonomyOverride(session.OperatorAutonomyOverride{
		ID:          "doctor-blocked-existing",
		AdminUserID: 1001,
		ChatID:      99141,
		Mode:        "leased",
		Scope:       session.OperatorAutoApprovalScopeAll,
		CreatedAt:   now.Add(-time.Minute),
		ExpiresAt:   now.Add(30 * time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateOperatorAutonomyOverride() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorAutonomyStatus(&b, session.SessionKey{ChatID: 99141, UserID: 0, Scope: telegramDMScopeRef(99141)}, 1001, now)
	report := b.String()
	for _, want := range []string{
		`autonomy_effective_ceiling="ask_first"`,
		`autonomy_raw_active_mode_count="1"`,
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
	rt.writeDoctorIssueStatusChecks(&b, DiagnosticInput{Scope: sandbox.Scope{WorkingRoot: cfg.Agent.ExecRoot}})
	report := b.String()
	if !strings.Contains(report, `issue=telegram_child_bot_runner status=likely_fixed`) {
		t.Fatalf("doctor issue checks = %s, want generic child bot runner likely_fixed", report)
	}
}

func TestDoctorExternalChannelAdapterReadinessProjectsGenericContract(t *testing.T) {
	cfg, store, _, _ := buildRuntimeFixtures(t)
	// This test exercises the generic external-channel readiness contract, not
	// host bubblewrap availability. Keep the durable child sandbox trusted so the
	// expected residual risk comes from the recorded child wake state.
	cfg.Sandbox.Profiles.DurableAgent.Mode = string(sandbox.ModeTrusted)
	workspaceRoot := filepath.Join(t.TempDir(), "child", "workspace")
	memoryRoot := filepath.Join(t.TempDir(), "child", "memory")
	adapterRoot := filepath.Join(workspaceRoot, "adapter-config")
	if err := os.MkdirAll(adapterRoot, 0o700); err != nil {
		t.Fatalf("mkdir adapter config: %v", err)
	}
	agent := core.DurableAgent{
		AgentID:           "child-mail",
		ChannelKind:       "external_channel",
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address: "mailbox@example.test", Account: "mailbox@example.test", Adapter: "mailbox_adapter", Query: "label:inbox", PollInterval: "24h",
		}},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "mailbox_adapter", ImplementationRef: "external:mailbox_adapter", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.UpsertToolInstallRecord(session.ToolInstallRecord{ToolName: "mailbox_adapter", Status: session.ToolInstallStatusVerified, InstalledAt: now, AttestedAt: now}); err != nil {
		t.Fatalf("UpsertToolInstallRecord() err = %v", err)
	}
	if _, err := store.UpsertToolAuditRecord(session.ToolAuditRecord{ToolName: "mailbox_adapter", Status: session.ToolAuditStatusPassed, AuditedAt: now}); err != nil {
		t.Fatalf("UpsertToolAuditRecord() err = %v", err)
	}
	if _, err := store.UpsertToolProbeRecord(session.ToolProbeRecord{ToolName: "mailbox_adapter", Status: session.ToolProbeStatusPassed, ProbedAt: now}); err != nil {
		t.Fatalf("UpsertToolProbeRecord() err = %v", err)
	}
	principalID := core.DurableAgentPrincipal(agent.AgentID)
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-child-mail-adapter",
		Kind:           session.CapabilityKindTool,
		TargetResource: "mailbox_adapter",
		GrantedTo:      principalID,
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_paths":["` + adapterRoot + `"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(tool) err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{ExternalChannel: &core.DurableAgentExternalChannelRuntimeState{
		Adapter: "mailbox_adapter", LastStatus: "wake_blocked", LastError: "adapter reported missing credential metadata; no token value was read.", FailureCount: 4, BackoffUntil: now.Add(time.Hour),
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
	rt.writeDoctorExternalChannelAdapterReadiness(&b, DiagnosticInput{Now: now})
	report := b.String()
	for _, want := range []string{
		"classification_contract: external-channel adapter readiness is generic parent-owned metadata",
		"agent=child-mail adapter=mailbox_adapter",
		"status=residual_risk",
		"layer=tool_lifecycle status=ready",
		"layer=grant_tool_runtime status=ready",
		"layer=runtime_material status=ready",
		"layer=last_wake status=wake_blocked failure_count=4",
		"missing credential metadata",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("doctor adapter readiness = %s, want %s", report, want)
		}
	}
}

func TestDoctorDesignPrincipleHealthSurfacesRetiredDebtGates(t *testing.T) {
	t.Parallel()

	cfg, _, _, _ := buildRuntimeFixtures(t)
	writeDoctorDesignPrincipleFixture(t, cfg.Agent.ExecRoot, true)

	rt := &Runtime{}
	var b strings.Builder
	rt.writeDoctorDesignPrincipleHealth(&b, DiagnosticInput{Scope: sandbox.Scope{WorkingRoot: cfg.Agent.ExecRoot}})
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
	rt.writeDoctorDesignPrincipleHealth(&b, DiagnosticInput{Scope: sandbox.Scope{WorkingRoot: cfg.Agent.ExecRoot}})
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
	for _, want := range []string{"kind=continuation_approval", "action=blocked_status", "Continuation approval blocked", "approval_blocked", "waiting for explicit opt-in", `next="Resolve the named blocker, then request a fresh bounded approval."`} {
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
		Text:     "/health diagnose",
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
