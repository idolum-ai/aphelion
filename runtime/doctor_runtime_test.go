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

func TestDoctorIssueStatusChecksProjectSynthTelegramRunnerReadiness(t *testing.T) {
	t.Parallel()

	cfg, _, _, _ := buildRuntimeFixtures(t)
	if err := os.WriteFile(filepath.Join(cfg.Agent.ExecRoot, "main_telegram_child_bot.go"), []byte(`package main
func runTelegramChildBotCommandWithDeps(){}
func validateTelegramChildBotTokenMetadata(){}
type telegramChildBotHealthStatus struct{}
func runTelegramChildBotGetMeSmoke(){}
`), 0o600); err != nil {
		t.Fatalf("write runner source: %v", err)
	}
	docDir := filepath.Join(cfg.Agent.ExecRoot, "docs", "architecture")
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatalf("mkdir doc dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docDir, "synth-telegram-bot-subsystem-plan.md"), []byte("## Validation gates\n## Launch runbook\naphelion telegram-child-bot status\n"), 0o600); err != nil {
		t.Fatalf("write runner runbook: %v", err)
	}

	rt := &Runtime{cfg: cfg}
	var b strings.Builder
	rt.writeDoctorIssueStatusChecks(&b, doctorDiagnosticInput{Scope: sandbox.Scope{WorkingRoot: cfg.Agent.ExecRoot}})
	report := b.String()
	if !strings.Contains(report, `issue=synth_telegram_child_bot_runner status=likely_fixed`) {
		t.Fatalf("doctor issue checks = %s, want Synth runner likely_fixed", report)
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
