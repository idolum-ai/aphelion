//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

type recordingMaintenanceTurnAssembler struct {
	called bool
	input  maintenanceTurnAssemblyInput
	result *turn.Result
	err    error
}

func (r *recordingMaintenanceTurnAssembler) Run(_ context.Context, input maintenanceTurnAssemblyInput) (*turn.Result, error) {
	r.called = true
	r.input = input
	return r.result, r.err
}

func TestHeartbeatUsesMaintenanceTurnAssemblerBoundary(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "none"
	cfg.Memory.Reflection.Enabled = false

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      2024,
		SourceUserID:      1002,
		SourceRole:        "approved_user",
		SourceScope:       telegramDMScopeRef(2024),
		TargetAdminChatID: 1001,
		TargetScope:       telegramDMScopeRef(1001),
		TurnFrom:          1,
		TurnTo:            1,
		Summary:           "review boundary test",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	recorder := &recordingMaintenanceTurnAssembler{
		result: &turn.Result{Turn: &core.TurnResult{Text: "stubbed heartbeat"}, Commit: turn.CommitResult{Persisted: false}},
	}
	rt.maintenanceAssembler = recorder

	err = rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("maintenance assembler not called for heartbeat")
	}
	if recorder.input.Species != maintenanceTurnHeartbeat {
		t.Fatalf("species = %q, want heartbeat", recorder.input.Species)
	}
	if recorder.input.RunKind != session.TurnRunKindHeartbeat {
		t.Fatalf("run kind = %q, want %q", recorder.input.RunKind, session.TurnRunKindHeartbeat)
	}
	if recorder.input.Key.ChatID != heartbeatSessionChatID {
		t.Fatalf("key chat id = %d, want %d", recorder.input.Key.ChatID, heartbeatSessionChatID)
	}
	if strings.TrimSpace(recorder.input.Prepared.LedgerText) == "" {
		t.Fatal("prepared ledger text empty, want heartbeat request text")
	}
	if !strings.Contains(recorder.input.Prepared.LedgerText, "Heartbeat maintenance turn.") {
		t.Fatalf("prepared ledger text = %q, want heartbeat heading", recorder.input.Prepared.LedgerText)
	}
	policy := recorder.input.PolicyFunc(turn.Request{})
	if policy.Reason != "heartbeat_outreach_policy" {
		t.Fatalf("policy reason = %q, want heartbeat_outreach_policy", policy.Reason)
	}
	if policy.Proposal {
		t.Fatal("policy proposal = true, want false when no outreach eligibility")
	}
	if policy.Render {
		t.Fatal("policy render = true, want false when no outreach eligibility")
	}
}

func TestCronUsesMaintenanceTurnAssemblerBoundary(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	recorder := &recordingMaintenanceTurnAssembler{
		result: &turn.Result{Turn: &core.TurnResult{Text: "stubbed cron"}, Commit: turn.CommitResult{Persisted: false}},
	}
	rt.maintenanceAssembler = recorder

	job := config.CronJobConfig{
		ID:       "nightly",
		Enabled:  true,
		Every:    "1h",
		Delivery: "none",
		Prompt:   "summarize pending maintenance",
	}
	if err := rt.runCronJobOnce(context.Background(), job); err != nil {
		t.Fatalf("runCronJobOnce() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("maintenance assembler not called for cron")
	}
	if recorder.input.Species != maintenanceTurnCron {
		t.Fatalf("species = %q, want cron", recorder.input.Species)
	}
	if recorder.input.RunKind != session.TurnRunKindCron {
		t.Fatalf("run kind = %q, want %q", recorder.input.RunKind, session.TurnRunKindCron)
	}
	if recorder.input.Key.ChatID != cronSessionChatID(job.ID) {
		t.Fatalf("key chat id = %d, want %d", recorder.input.Key.ChatID, cronSessionChatID(job.ID))
	}
	if recorder.input.CronJobID != job.ID {
		t.Fatalf("cron job id = %q, want %q", recorder.input.CronJobID, job.ID)
	}
	if recorder.input.ScheduledJobID != job.ID || recorder.input.ScheduledJobKind != "cron" {
		t.Fatalf("scheduled job identity = %q/%q, want %q/cron", recorder.input.ScheduledJobID, recorder.input.ScheduledJobKind, job.ID)
	}
	if !strings.Contains(recorder.input.Prepared.LedgerText, "Cron job run: nightly") {
		t.Fatalf("prepared ledger text = %q, want cron heading", recorder.input.Prepared.LedgerText)
	}
	policy := recorder.input.PolicyFunc(turn.Request{})
	if policy.Reason != "cron_delivery_policy" {
		t.Fatalf("policy reason = %q, want cron_delivery_policy", policy.Reason)
	}
	if policy.Render {
		t.Fatal("policy render = true, want false for delivery=none")
	}
}

func TestStartupRecoveryUsesMaintenanceTurnAssemblerBoundary(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 1500, UserID: 0, Scope: telegramDMScopeRef(1500)}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if _, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "recover boundary run"); err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}

	recorder := &recordingMaintenanceTurnAssembler{
		result: &turn.Result{Turn: &core.TurnResult{Text: "stubbed recovery"}, Commit: turn.CommitResult{Persisted: false}},
	}
	rt.maintenanceAssembler = recorder

	err = rt.runStartupRecoveryOnce(context.Background(), time.Date(2026, time.April, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("runStartupRecoveryOnce() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("maintenance assembler not called for startup recovery")
	}
	if recorder.input.Species != maintenanceTurnRecovery {
		t.Fatalf("species = %q, want recovery", recorder.input.Species)
	}
	if recorder.input.RunKind != session.TurnRunKindRecovery {
		t.Fatalf("run kind = %q, want %q", recorder.input.RunKind, session.TurnRunKindRecovery)
	}
	if recorder.input.Key.ChatID != heartbeatSessionChatID {
		t.Fatalf("key chat id = %d, want %d", recorder.input.Key.ChatID, heartbeatSessionChatID)
	}
	if len(recorder.input.RecoveryRuns) != 1 {
		t.Fatalf("recovery runs len = %d, want 1", len(recorder.input.RecoveryRuns))
	}
	if !strings.Contains(recorder.input.Prepared.LedgerText, "Startup recovery analysis.") {
		t.Fatalf("prepared ledger text = %q, want recovery heading", recorder.input.Prepared.LedgerText)
	}
	policy := recorder.input.PolicyFunc(turn.Request{})
	if policy.Reason != "startup_recovery_maintenance" {
		t.Fatalf("policy reason = %q, want startup_recovery_maintenance", policy.Reason)
	}
	if policy.Render || policy.Proposal || policy.Brokerage {
		t.Fatalf("policy = %#v, want no proposal/render/brokerage", policy)
	}
}

func TestMaintenanceRenderSkipsFaceForHeartbeat(t *testing.T) {
	t.Parallel()

	renderer := &countingFaceRenderer{text: "unexpected face render"}
	coordinator := &maintenanceTurnCoordinator{
		runtime:          &Runtime{},
		species:          maintenanceTurnHeartbeat,
		currentFaceModel: renderer,
		lastGovernor: &turn.GovernorResult{
			Turn:      &core.TurnResult{Text: "Heartbeat checked."},
			FloorText: "Heartbeat checked.",
		},
	}

	got, err := coordinator.Render(context.Background(), turn.FaceRenderRequest{})
	if err != nil {
		t.Fatalf("Render() err = %v", err)
	}
	if renderer.calls != 0 {
		t.Fatalf("face render calls = %d, want maintenance heartbeat floor fallback", renderer.calls)
	}
	if strings.TrimSpace(got.Text) != "Heartbeat checked." {
		t.Fatalf("render text = %q, want floor fallback", got.Text)
	}
}
