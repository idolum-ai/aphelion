//go:build linux

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/idolum-ai/aphelion/internal/maintenancecli"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestVerifyDeploymentSuccessRunsGoldenPathAndCleansProbeSession(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{
				AdminUserIDs: []int64{42},
			},
		},
		Sessions: config.SessionsConfig{
			DBPath: filepath.Join(root, "state", "sessions.db"),
			TESRetention: config.SessionsTESRetentionConfig{
				Enabled:         true,
				MaxAge:          "168h",
				MinRetainedRows: 300,
				MaxDeletePerGC:  50,
				ExportDir:       filepath.Join(root, "state", "tes-exports"),
			},
		},
		Agent: config.AgentConfig{
			PromptRoot:        filepath.Join(root, "agent"),
			ExecRoot:          filepath.Join(root, "workspace"),
			SharedMemoryRoot:  filepath.Join(root, "agent"),
			UserWorkspaceRoot: filepath.Join(root, "state", "isolated", "workspaces"),
			UserMemoryRoot:    filepath.Join(root, "state", "isolated", "memory"),
			ToolTimeout:       30,
		},
	}

	origBuilder := deployVerificationRuntimeBuilder
	defer func() { deployVerificationRuntimeBuilder = origBuilder }()

	deployVerificationRuntimeBuilder = func(cfg *config.Config, store *session.SQLiteStore) (builtDeployVerificationRuntime, error) {
		sender := &deployVerificationSender{}
		reply := "DEPLOYMENT VERIFIED: the service is ready."
		runner := deployTurnRunnerFunc(func(ctx context.Context, msg core.InboundMessage) (*core.TurnResult, error) {
			key := session.SessionKey{ChatID: msg.ChatID, UserID: 0}
			sess, err := store.Load(key)
			if err != nil {
				return nil, err
			}
			sess.ChatType = "dm"
			sess.UserName = msg.SenderName
			sess.TurnCount++
			sess.LastFloorText = "Verification floor."
			newMessages := []session.Message{
				{
					Role:         "user",
					Content:      msg.Text,
					ContentChars: len(msg.Text),
					TurnIndex:    sess.TurnCount,
				},
				{
					Role:         "assistant",
					Content:      reply,
					ContentChars: len(reply),
					TurnIndex:    sess.TurnCount,
				},
			}
			if err := store.Save(sess, newMessages, core.TokenUsage{}); err != nil {
				return nil, err
			}
			if _, err := sender.SendMessage(ctx, core.OutboundMessage{ChatID: msg.ChatID, Text: reply}); err != nil {
				return nil, err
			}
			return &core.TurnResult{Text: "Verification floor."}, nil
		})
		return builtDeployVerificationRuntime{
			Runner: runner,
			Sender: sender,
			Probe: func(ctx context.Context, key session.SessionKey, p principal.Principal) (string, error) {
				state := session.PlanState{
					Explanation: "tool probe",
					Steps: []session.PlanStep{
						{Step: "tool path", Status: session.PlanStatusInProgress},
					},
				}
				if err := store.UpdatePlanStateWithEvent(key, state, session.PlanEventKindToolUpdated); err != nil {
					return "", err
				}
				return "tool probe persisted plan state", nil
			},
		}, nil
	}

	report, err := verifyDeployment(context.Background(), cfg, deployVerificationOptions{
		ConfigPath: "/tmp/aphelion.toml",
	})
	if err != nil {
		t.Fatalf("verifyDeployment() err = %v", err)
	}
	if report.Status != "passed" {
		t.Fatalf("report.Status = %q, want passed", report.Status)
	}
	if !report.Blessed {
		t.Fatal("report.Blessed = false, want true")
	}
	if len(report.Probes) != 6 {
		t.Fatalf("probe len = %d, want 6", len(report.Probes))
	}
	if report.Probes[2].Name != "service_binary" || report.Probes[2].Status != deployProbeStatusPass {
		t.Fatalf("service binary probe = %#v, want pass", report.Probes[2])
	}
	bootProbe := report.Probes[0]
	if bootProbe.Name != "boot" {
		t.Fatalf("first probe = %q, want boot", bootProbe.Name)
	}
	if !strings.Contains(bootProbe.Detail, "export_dir=") {
		t.Fatalf("boot probe detail = %q, want retention summary with export_dir", bootProbe.Detail)
	}

	db, err := sql.Open("sqlite3", cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("sql.Open() err = %v", err)
	}
	defer db.Close()

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, report.ProbeSessionID).Scan(&remaining); err != nil {
		t.Fatalf("query probe session cleanup: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("probe session rows = %d, want 0 after successful cleanup", remaining)
	}
}

func TestVerifyDeploymentFailsRequiredDurableChildWake(t *testing.T) {
	cfg := newVerifyDeployTestConfig(t)
	installSuccessfulDeployVerificationBuilder(t,
		"DEPLOYMENT VERIFIED: the service is ready.",
		func(store *session.SQLiteStore) error {
			return store.UpsertDurableAgent(core.DurableAgent{
				AgentID:     "paper-scout",
				ChannelKind: "external_channel",
				Status:      "active",
				LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
					Charter:      "Review reports.",
					OutboundMode: "read_only",
				}),
			})
		},
		func(context.Context, string, time.Time) error {
			return fmt.Errorf("child wake unavailable")
		},
	)

	report, err := verifyDeployment(context.Background(), cfg, deployVerificationOptions{
		ConfigPath:      "/tmp/aphelion.toml",
		DurableChildren: "required",
	})
	if err == nil {
		t.Fatal("verifyDeployment() err = nil, want durable child failure")
	}
	if !strings.Contains(err.Error(), "durable child wake failed") {
		t.Fatalf("verifyDeployment() err = %v, want durable child wake failure", err)
	}
	last := report.Probes[len(report.Probes)-1]
	if last.Name != "durable_children" || last.Status != deployProbeStatusFail {
		t.Fatalf("last probe = %#v, want failed durable_children", last)
	}
}

func TestVerifyDeploymentWarnsDurableChildWake(t *testing.T) {
	cfg := newVerifyDeployTestConfig(t)
	installSuccessfulDeployVerificationBuilder(t,
		"DEPLOYMENT VERIFIED: the service is ready.",
		func(store *session.SQLiteStore) error {
			return store.UpsertDurableAgent(core.DurableAgent{
				AgentID:     "paper-scout",
				ChannelKind: "external_channel",
				Status:      "active",
				LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
					Charter:      "Review reports.",
					OutboundMode: "read_only",
				}),
			})
		},
		func(context.Context, string, time.Time) error {
			return fmt.Errorf("child wake unavailable")
		},
	)

	report, err := verifyDeployment(context.Background(), cfg, deployVerificationOptions{
		ConfigPath:      "/tmp/aphelion.toml",
		DurableChildren: "warn",
	})
	if err != nil {
		t.Fatalf("verifyDeployment() err = %v, want warning-only pass", err)
	}
	if report.Status != "passed" {
		t.Fatalf("report.Status = %q, want passed", report.Status)
	}
	last := report.Probes[len(report.Probes)-1]
	if last.Name != "durable_children" || last.Status != deployProbeStatusPass || !strings.Contains(last.Detail, "warning:") {
		t.Fatalf("last probe = %#v, want warning durable_children pass", last)
	}
}

func TestVerifyDeploymentDefaultDurableChildrenStatusDoesNotWakeChildren(t *testing.T) {
	cfg := newVerifyDeployTestConfig(t)
	wakeCalls := 0
	installSuccessfulDeployVerificationBuilder(t,
		"DEPLOYMENT VERIFIED: the service is ready.",
		func(store *session.SQLiteStore) error {
			return store.UpsertDurableAgent(core.DurableAgent{
				AgentID:     "paper-scout",
				ChannelKind: "external_channel",
				Status:      "active",
				LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
					Charter:      "Review reports.",
					OutboundMode: "read_only",
				}),
			})
		},
		func(context.Context, string, time.Time) error {
			wakeCalls++
			return fmt.Errorf("child wake should not run in default status mode")
		},
	)

	report, err := verifyDeployment(context.Background(), cfg, deployVerificationOptions{
		ConfigPath: "/tmp/aphelion.toml",
	})
	if err != nil {
		t.Fatalf("verifyDeployment() err = %v, want non-invasive status pass", err)
	}
	if wakeCalls != 0 {
		t.Fatalf("wakeCalls = %d, want 0 for default durable-child status mode", wakeCalls)
	}
	last := report.Probes[len(report.Probes)-1]
	if last.Name != "durable_children" || last.Status != deployProbeStatusPass || !strings.Contains(last.Detail, "wake probe skipped by default") {
		t.Fatalf("last probe = %#v, want non-invasive durable_children status detail", last)
	}
}

func TestVerifyDeploymentRejectsInternalLayerLeakInBlessingReply(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{
				AdminUserIDs: []int64{42},
			},
		},
		Sessions: config.SessionsConfig{
			DBPath: filepath.Join(root, "state", "sessions.db"),
		},
		Agent: config.AgentConfig{
			PromptRoot:        filepath.Join(root, "agent"),
			ExecRoot:          filepath.Join(root, "workspace"),
			SharedMemoryRoot:  filepath.Join(root, "agent"),
			UserWorkspaceRoot: filepath.Join(root, "state", "isolated", "workspaces"),
			UserMemoryRoot:    filepath.Join(root, "state", "isolated", "memory"),
			ToolTimeout:       30,
		},
	}

	origBuilder := deployVerificationRuntimeBuilder
	defer func() { deployVerificationRuntimeBuilder = origBuilder }()

	deployVerificationRuntimeBuilder = func(cfg *config.Config, store *session.SQLiteStore) (builtDeployVerificationRuntime, error) {
		sender := &deployVerificationSender{}
		reply := "DEPLOYMENT VERIFIED: governor and Idolum are ready."
		runner := deployTurnRunnerFunc(func(ctx context.Context, msg core.InboundMessage) (*core.TurnResult, error) {
			key := session.SessionKey{ChatID: msg.ChatID, UserID: 0}
			sess, err := store.Load(key)
			if err != nil {
				return nil, err
			}
			sess.ChatType = "dm"
			sess.UserName = msg.SenderName
			sess.TurnCount++
			sess.LastFloorText = "Verification floor."
			newMessages := []session.Message{
				{
					Role:         "user",
					Content:      msg.Text,
					ContentChars: len(msg.Text),
					TurnIndex:    sess.TurnCount,
				},
				{
					Role:         "assistant",
					Content:      reply,
					ContentChars: len(reply),
					TurnIndex:    sess.TurnCount,
				},
			}
			if err := store.Save(sess, newMessages, core.TokenUsage{}); err != nil {
				return nil, err
			}
			if _, err := sender.SendMessage(ctx, core.OutboundMessage{ChatID: msg.ChatID, Text: reply}); err != nil {
				return nil, err
			}
			return &core.TurnResult{Text: "Verification floor."}, nil
		})
		return builtDeployVerificationRuntime{
			Runner: runner,
			Sender: sender,
			Probe: func(ctx context.Context, key session.SessionKey, p principal.Principal) (string, error) {
				return "tool probe persisted plan state", nil
			},
		}, nil
	}

	report, err := verifyDeployment(context.Background(), cfg, deployVerificationOptions{
		ConfigPath: "/tmp/aphelion.toml",
	})
	if err == nil {
		t.Fatal("verifyDeployment() err = nil, want leaked internal layer failure")
	}
	if !strings.Contains(err.Error(), "leaked internal layer markers") {
		t.Fatalf("verifyDeployment() err = %v, want leaked internal layer markers", err)
	}
	if report.Status != "failed" {
		t.Fatalf("report.Status = %q, want failed", report.Status)
	}
}

func TestRunVerifyDeployCommandPrintsFailureReport(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)

	origRunner := deployVerificationRunner
	defer func() { deployVerificationRunner = origRunner }()

	deployVerificationRunner = func(_ context.Context, _ *config.Config, _ deployVerificationOptions) (deployVerificationReport, error) {
		return deployVerificationReport{
			Status:         "failed",
			Blessed:        false,
			ProbeChatID:    -9100000001,
			ProbeSessionID: "telegram_dm:-9100000001",
			Diagnosis:      "deployment verification failed on the live governed reply path: no outbound reply",
			Probes: []deployProbeResult{
				{Name: "boot", Status: deployProbeStatusPass, DurationMS: 12, Detail: "runtime initialized"},
				{Name: "golden_path", Status: deployProbeStatusFail, DurationMS: 18, Detail: "no outbound reply"},
			},
		}, fmt.Errorf("no outbound reply")
	}

	out, err := captureStdout(t, func() error {
		return runVerifyDeployCommand([]string{"--config", cfgPath, "--format=kv"})
	})
	if err == nil {
		t.Fatal("runVerifyDeployCommand() err = nil, want failure")
	}
	if !strings.Contains(out, "action: verify-deploy") {
		t.Fatalf("verify-deploy output = %q, want action header", out)
	}
	if !strings.Contains(out, "status: failed") {
		t.Fatalf("verify-deploy output = %q, want failed status", out)
	}
	if !strings.Contains(out, "golden_path: fail") {
		t.Fatalf("verify-deploy output = %q, want golden_path failure", out)
	}
}

func TestRunParkRestartCommandParksLiveContinuation(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	key := session.SessionKey{
		ChatID: 9901,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "9901"},
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "deploy-decision",
		Objective:      "Resume after service reinstall.",
		StageSummary:   "Approved before a deploy restart.",
		RemainingTurns: 1,
		ApprovedBy:     1,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-deploy-decision",
			Summary:       "Resume after deploy",
			BoundedEffect: "One bounded follow-up after restart.",
			Status:        session.ProposalStatusApproved,
			ExpiresAt:     time.Now().UTC().Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-deploy-decision",
			ProposalID:     "aprop-deploy-decision",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1,
			ExpiresAt:      time.Now().UTC().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	store.Close()

	out, err := captureStdout(t, func() error {
		return runParkRestartCommand([]string{"--config", cfgPath, "--source", "test_reinstall"})
	})
	if err != nil {
		t.Fatalf("runParkRestartCommand() err = %v", err)
	}
	if !strings.Contains(out, "action: park-restart") || !strings.Contains(out, "approved_continuations_parked: 1") {
		t.Fatalf("park-restart output = %q, want parked approved continuation", out)
	}
	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer store.Close()
	parked, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if parked.ParkedSource != "test_reinstall" || parked.Status != session.ContinuationStatusApproved {
		t.Fatalf("parked continuation = %#v, want approved test_reinstall marker", parked)
	}
}

func TestRepairLiveStateClosesContinuationsPlanLeasesAndPendingDecisions(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	key := session.SessionKey{
		ChatID: 9902,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "9902"},
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-decision",
		Objective:      "Old turn-by-turn recovery.",
		StageSummary:   "Generated before live repair.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-stale-decision",
			Summary:       "Run one more stale turn",
			BoundedEffect: "Continue old turn-by-turn work.",
			Status:        session.ProposalStatusPending,
			ExpiresAt:     time.Now().UTC().Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-stale-decision",
			ProposalID:     "aprop-stale-decision",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      time.Now().UTC().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:      "op-stale",
		Status:  session.OperationStatusActive,
		Stage:   "old_live_loop",
		Summary: "Old state",
		PlanLease: session.OperationPlanLease{
			ID:             "plan-lease-stale",
			Summary:        "Old one-step lease bundle",
			Status:         session.PlanLeaseStatusActive,
			TurnBudget:     1,
			RemainingTurns: 1,
			Lanes: []session.OperationPlanLeaseLane{{
				ID:             "lane-1",
				Summary:        "Old lane",
				AuthorityClass: "read_only_review",
				ExpectedTurns:  1,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:          "decision-stale",
		OwnerKey:    "telegram:9902",
		Kind:        "exec",
		ChatID:      9902,
		Prompt:      "Approve stale tool call?",
		ChoicesJSON: `[{"id":"approve","label":"Approve"}]`,
	}); err != nil {
		t.Fatalf("UpsertPendingDecision() err = %v", err)
	}

	result, err := maintenancecli.RepairLiveState(context.Background(), store, "test_live_repair", true, time.Now().UTC(), maintenanceLiveStateRepairDeps())
	if err != nil {
		t.Fatalf("maintenancecli.RepairLiveState() err = %v", err)
	}
	if result.ContinuationsClosed != 1 || result.PlanLeasesRevoked != 1 || result.PendingDecisionsCleared != 1 {
		t.Fatalf("repair result = %#v, want continuation, plan lease, and decision cleaned", result)
	}
	continuation, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if continuation.Status != session.ContinuationStatusRevoked || continuation.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked {
		t.Fatalf("continuation = %#v, want revoked lease", continuation)
	}
	_, op, exists, err := store.PlanAndOperationStateIfExists(key)
	if err != nil {
		t.Fatalf("PlanAndOperationStateIfExists() err = %v", err)
	}
	if !exists || op.PlanLease.Status != session.PlanLeaseStatusRevoked || !strings.Contains(op.Summary, "Live state repair revoked") {
		t.Fatalf("operation = %#v exists=%t, want revoked plan lease repair note", op, exists)
	}
	decisions, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("pending decisions = %#v, want cleared", decisions)
	}
}

func TestRepairLiveStateRepairsMetadataAuthorityDriftWithoutClosingLive(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	key := session.SessionKey{
		ChatID: 9903,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "9903"},
	}
	leaseID := "lease-metadata-drift"
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-metadata-drift",
		Objective: "Resume metadata-only preflight.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval",
		Proposal: session.OperationProposal{
			ID:      "phase-op-metadata-drift-phase-metadata",
			Kind:    session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
			Summary: "Live-adjacent metadata preflight. BLOCKED: approval button render failed; auto-approved lease was revoked after action_not_allowed/workspace_write mismatch.",
			Status:  session.ProposalStatusApproved,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-metadata-drift",
			CurrentPhaseID: "phase-metadata",
			Phases: []session.OperationPhase{{
				ID:             "phase-metadata",
				Summary:        "Live-adjacent metadata preflight. BLOCKED: approval button render failed; auto-approved lease was revoked after action_not_allowed/workspace_write mismatch.",
				Status:         session.PlanStatusPending,
				AuthorityClass: session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
				BoundedEffect:  "No action under this phase until approval is real and visible.",
				AllowedActions: []string{"report_button_diagnosis"},
				LeaseID:        leaseID,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:       session.ContinuationStatusRevoked,
		StageSummary: "Live-adjacent metadata preflight. BLOCKED: approval button render failed; auto-approved lease was revoked after action_not_allowed/workspace_write mismatch.",
		ActionProposal: session.ActionProposal{
			ID:        "aprop-phase-op-metadata-drift-phase-metadata",
			RiskClass: session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
			Summary:   "Live-adjacent metadata preflight. BLOCKED: approval button render failed; auto-approved lease was revoked after action_not_allowed/workspace_write mismatch.",
			Status:    session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             leaseID,
			ProposalID:     "aprop-phase-op-metadata-drift-phase-metadata",
			Status:         session.ContinuationLeaseStatusRevoked,
			MaxTurns:       1,
			RemainingTurns: 0,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	result, err := maintenancecli.RepairLiveState(context.Background(), store, "test_authority_repair", false, time.Now().UTC(), maintenanceLiveStateRepairDeps())
	if err != nil {
		t.Fatalf("maintenancecli.RepairLiveState() err = %v", err)
	}
	if result.AuthorityContractsRepaired != 1 || result.ContinuationsClosed != 0 {
		t.Fatalf("repair result = %#v, want one authority repair without broad close", result)
	}
	op, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	phase := op.PhasePlan.Phases[0]
	if strings.Contains(phase.Summary, "workspace_write mismatch") || phase.LeaseID != "" || phase.Status != session.PlanStatusPending {
		t.Fatalf("phase = %#v, want cleaned pending phase without stale lease", phase)
	}
	if !stringSliceContains(phase.AllowedActions, session.AuthorityWorkActionReadOnly) || stringSliceContains(phase.AllowedActions, "workspace_write") {
		t.Fatalf("allowed actions = %#v, want read_only but not workspace_write", phase.AllowedActions)
	}
	if !stringSliceContains(phase.ForbiddenActions, "telegram_api_call") || !stringSliceContains(phase.ForbiddenActions, "read_token_contents") {
		t.Fatalf("forbidden actions = %#v, want metadata/live-effect denials", phase.ForbiddenActions)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ActionProposal.Status != session.ProposalStatusSuperseded || strings.Contains(cont.StageSummary, "workspace_write mismatch") {
		t.Fatalf("continuation = %#v, want superseded cleaned prior proposal", cont)
	}
}

func TestPruneExecutionEventsForRetentionExportsThenPrunes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}

	key := session.SessionKey{
		ChatID: 4242,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "telegram_dm:4242"},
	}
	now := time.Date(2026, time.April, 22, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 130; i++ {
		if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: fmt.Sprintf(`{"index":%d}`, i),
			CreatedAt:   now.Add(-72*time.Hour + time.Duration(i)*time.Minute),
		}); err != nil {
			t.Fatalf("AppendExecutionEvent(%d) err = %v", i, err)
		}
	}

	cfg := config.Default()
	cfg.Sessions.DBPath = dbPath
	cfg.Sessions.TESRetention.Enabled = true
	cfg.Sessions.TESRetention.MaxAge = "24h"
	cfg.Sessions.TESRetention.MinRetainedRows = 100
	cfg.Sessions.TESRetention.MaxDeletePerGC = 3
	cfg.Sessions.TESRetention.ExportDir = filepath.Join(root, "tes-exports")

	removed, status, err := maintenancecli.PruneExecutionEventsForRetention(&cfg, now)
	if err != nil {
		t.Fatalf("maintenancecli.PruneExecutionEventsForRetention() err = %v", err)
	}
	if removed != 3 {
		t.Fatalf("removed = %d, want 3", removed)
	}
	if !strings.Contains(status, "export=") {
		t.Fatalf("status = %q, want export path", status)
	}

	remaining, err := store.ExecutionEventsBySession(key, 0, 1000)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if len(remaining) != 127 {
		t.Fatalf("remaining events = %d, want 127", len(remaining))
	}
	remainingIDs := make(map[int64]struct{}, len(remaining))
	for _, event := range remaining {
		remainingIDs[event.ID] = struct{}{}
	}

	exports, err := filepath.Glob(filepath.Join(cfg.Sessions.TESRetention.ExportDir, "*", "*.json"))
	if err != nil {
		t.Fatalf("Glob(export files) err = %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("export files = %#v, want one export file", exports)
	}
	raw, err := os.ReadFile(exports[0])
	if err != nil {
		t.Fatalf("ReadFile(%s) err = %v", exports[0], err)
	}
	var bundle maintenancecli.TESRetentionExportBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("json.Unmarshal(export) err = %v", err)
	}
	if bundle.SchemaVersion != "tes_retention_export.v1" {
		t.Fatalf("SchemaVersion = %q, want tes_retention_export.v1", bundle.SchemaVersion)
	}
	if bundle.Count != 3 || len(bundle.Events) != 3 {
		t.Fatalf("bundle count/events = %d/%d, want 3/3", bundle.Count, len(bundle.Events))
	}
	if bundle.FirstID == 0 || bundle.LastID == 0 {
		t.Fatalf("bundle id bounds = %d/%d, want non-zero", bundle.FirstID, bundle.LastID)
	}
	if bundle.Events[0].ID != bundle.FirstID || bundle.Events[len(bundle.Events)-1].ID != bundle.LastID {
		t.Fatalf("bundle event bounds mismatch: first=%d last=%d events=%#v", bundle.FirstID, bundle.LastID, bundle.Events)
	}
	for _, event := range bundle.Events {
		if _, ok := remainingIDs[event.ID]; ok {
			t.Fatalf("exported event id %d still present after prune", event.ID)
		}
	}
}

func TestPruneExecutionEventsForRetentionNoOpDoesNotExport(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}

	key := session.SessionKey{
		ChatID: 5151,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "telegram_dm:5151"},
	}
	now := time.Date(2026, time.April, 22, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: fmt.Sprintf(`{"index":%d}`, i),
			CreatedAt:   now.Add(-2 * time.Hour).Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("AppendExecutionEvent(%d) err = %v", i, err)
		}
	}

	cfg := config.Default()
	cfg.Sessions.DBPath = dbPath
	cfg.Sessions.TESRetention.Enabled = true
	cfg.Sessions.TESRetention.MaxAge = "24h"
	cfg.Sessions.TESRetention.MinRetainedRows = 100
	cfg.Sessions.TESRetention.MaxDeletePerGC = 2
	cfg.Sessions.TESRetention.ExportDir = filepath.Join(root, "tes-exports")

	removed, status, err := maintenancecli.PruneExecutionEventsForRetention(&cfg, now)
	if err != nil {
		t.Fatalf("maintenancecli.PruneExecutionEventsForRetention() err = %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if !strings.Contains(status, "result=no-op") {
		t.Fatalf("status = %q, want result=no-op", status)
	}
	exports, err := filepath.Glob(filepath.Join(cfg.Sessions.TESRetention.ExportDir, "*", "*.json"))
	if err != nil {
		t.Fatalf("Glob(export files) err = %v", err)
	}
	if len(exports) != 0 {
		t.Fatalf("export files = %#v, want none for no-op prune", exports)
	}
}
