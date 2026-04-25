//go:build linux

package face

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestRenderTelegramStatusChatSummaryStateQueued(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID:     7,
		QueueDepth: 3,
		PendingItems: []core.PendingItem{
			{Kind: core.PendingItemKindQueue, ChatID: 7, Summary: "queue_depth=3"},
		},
	}, "medium", "high", false)

	if !strings.Contains(out, "summary state=queued") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want queued summary state", out)
	}
	if !strings.Contains(out, "current_signal=queue:3") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want queue current signal", out)
	}
}

func TestRenderTelegramStatusChatSummaryStateInterrupted(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID: 7,
		LatestTurnRun: &core.TurnRunStatusSnapshot{
			Status: "interrupted",
			Kind:   "interactive",
		},
	}, "medium", "high", false)

	if !strings.Contains(out, "summary state=interrupted") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want interrupted summary state", out)
	}
	if !strings.Contains(out, "current_signal=turn:interactive:interrupted") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want interrupted turn signal", out)
	}
}

func TestRenderTelegramStatusChatSummaryStateBlockedIncludesOperationAndPlan(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID:           7,
		OperationStatus:  "blocked",
		OperationStage:   "approval_wait",
		OperationSummary: "Waiting for admin review",
		PlanStepStatus:   "in_progress",
		PlanStep:         "Await admin approval",
		LatestTurnRun: &core.TurnRunStatusSnapshot{
			Status: "interrupted",
			Kind:   "interactive",
		},
	}, "medium", "high", false)

	if !strings.Contains(out, "summary state=blocked") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want blocked summary state", out)
	}
	if !strings.Contains(out, "operation status=blocked stage=approval_wait") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want operation status line", out)
	}
	if !strings.Contains(out, "summary=\"Waiting for admin review\"") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want operation summary", out)
	}
	if !strings.Contains(out, "plan_step status=in_progress step=\"Await admin approval\"") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want plan step line", out)
	}
	if !strings.Contains(out, "current_signal=operation:blocked:approval_wait") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want blocked operation current signal", out)
	}
}

func TestRenderTelegramStatusChatIncludesTurnPhaseHiddenInputsDeliveryAndDetachedWork(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID:             88,
		TurnPhase:          "deliver",
		TurnPhaseSummary:   "sending telegram reply",
		HiddenInputSummary: "pending review events keep converging around approvals",
		HiddenInputCategories: []string{
			"semantic_recurrence",
			"unresolved_memory_state",
		},
		DeliveryStatus:     "delivery_failed",
		DeliverySummary:    "persisted turn failed during delivery; no retry queue is active",
		PlanCompletedSteps: 2,
		PlanTotalSteps:     2,
		PlanFullyExecuted:  true,
		PendingItems: []core.PendingItem{
			{Kind: core.PendingItemKindDecision, ChatID: 88, ID: "decision-1"},
			{Kind: core.PendingItemKindRecovery, ChatID: 88, ID: "recovery-1"},
		},
		StaleRunningTurns: []core.TurnRunStatusSnapshot{
			{ID: 41, ChatID: 88, Status: "running"},
		},
	}, "medium", "high", false)

	for _, needle := range []string{
		"turn_phase phase=deliver",
		"hidden_inputs categories=semantic_recurrence,unresolved_memory_state",
		"delivery status=delivery_failed",
		"plan_progress completed=2 total=2 fully_executed=true",
		"detached_work decisions=1 continuations=0 recoveries=1 stale_turns=1",
		"current_signal=phase:deliver",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChat() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusChatIncludesCanonicalToolLifecycleSnapshot(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID: 45,
		ToolLifecycle: []core.ToolLifecycleStatusSnapshot{{
			ToolName:             "browse_page",
			InstallStatus:        "verified",
			ProbeStatus:          "passed",
			AuditStatus:          "passed",
			InstallRef:           "workspace:tooling-v3",
			DriftSource:          "workspace_drift",
			StaleReason:          "workspace_drift: baseline=sha256:a current=sha256:b",
			AttestationStatus:    "stale",
			ManifestHash:         "sha256:abcdefghijklmnopqrstuvwxyz",
			WorkspaceFingerprint: "sha256:zyxwvutsrqponmlkjihgfedcba",
			ProbeFailures:        1,
			TraceStage:           "probe",
			TraceSummary:         "probe_run passed against the declared probe command",
			TraceArtifactCount:   1,
		}},
	}, "medium", "high", false)

	for _, needle := range []string{
		"tool_lifecycle source=canonical:session.tool_install_records+tool_audit_records",
		"- tool_name=browse_page install=verified probe=passed audit=passed install_ref=workspace:tooling-v3 attestation=stale drift_source=workspace_drift stale_reason=workspace_drift: baseline=sha256:a current=sha256:b failures=install:0,probe:1,audit:0 manifest_hash=sha256:abcdefghijkl workspace_fingerprint=sha256:zyxwvutsrqpo trace=probe:probe_run passed against the declared probe command refs=1",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChat() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusChatIncludesToolAuthorityLifecycleProjection(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID: 44,
		RecentExecution: []core.ExecutionEventSummary{
			{
				EventType: core.ExecutionEventToolExposureChanged,
				Status:    "enabled",
				Summary:   "tool_name=search_web principal=idolum-email active=true",
				CreatedAt: now.Add(-5 * time.Second),
			},
			{
				EventType: core.ExecutionEventToolRegistered,
				Status:    "enabled",
				Summary:   "tool_name=search_web registered=true implementation_ref=tool/search_web.go",
				CreatedAt: now.Add(-10 * time.Second),
			},
			{
				EventType: core.ExecutionEventToolProposalReviewed,
				Status:    "approved",
				Summary:   "proposal_id=tp_123 tool_name=search_web review_status=approved ratified_via=decision_broker",
				CreatedAt: now.Add(-15 * time.Second),
			},
		},
	}, "medium", "high", false)

	for _, needle := range []string{
		"tool_authority_lifecycle source=canonical:execution_events.tool_authority",
		"tool_proposals:",
		"event=tool.proposal.reviewed status=approved",
		"tool_registrations:",
		"event=tool.registered status=enabled",
		"tool_exposures:",
		"event=tool.exposure.changed status=enabled",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChat() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusDurablesIncludesHealthCards(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusDurables(core.DurableAgentsStatusSnapshot{
		TotalAgents:    1,
		ActiveAgents:   1,
		DegradedAgents: 1,
		Agents: []core.DurableAgentStatusSnapshot{
			{
				AgentID:                      "family-group",
				ChannelKind:                  "telegram_group",
				Status:                       "active",
				Health:                       "degraded",
				ReviewTargetChatID:           1001,
				PolicyVersion:                4,
				PolicyHash:                   "8f829f8793fcb1234567890",
				PolicyOutboundMode:           "reply_with_parent_review",
				PolicyDrift:                  "admin_review",
				CapabilityEnvelope:           []string{"group_reply", "bounded_review_artifact"},
				CapacityState:                "provisional",
				CapacityCanCount:             2,
				CapacityCannotCount:          1,
				CapacityUncertainCount:       1,
				CapacitySuccessCriteriaCount: 2,
				CapacityEvidenceSignalCount:  1,
				LastApplyStatus:              "failed",
				LastApplyError:               "policy apply timed out while child was offline",
				LastAppliedPolicyVersion:     3,
				IdentitySource:               "canonical:session.durable_agents",
				RuntimePostureSource:         "operational_current_state_store:session.durable_agent_state+projection:tes_execution_events",
			},
		},
	})

	for _, needle := range []string{
		"status_scope=durables",
		"summary total=1 active=1 dormant=0 degraded=1 inactive=0",
		"- id=family-group channel=telegram_group status=active health=degraded review_chat=1001",
		"policy version=4 hash=8f829f8793fc outbound=reply_with_parent_review",
		"capacity state=provisional can=2 cannot=1 uncertain=1 success=2 evidence=1",
		"runtime apply_error=\"policy apply timed out while child was offline\"",
		"enrollment status=none",
		"sources identity=canonical:session.durable_agents runtime_posture=operational_current_state_store:session.durable_agent_state+projection:tes_execution_events",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusDurables() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusSystemIncludesToolAuthorityLifecycleProjection(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	out := RenderTelegramStatusSystem(core.SystemStatusSnapshot{
		RecentExecution: []core.ExecutionEventSummary{
			{
				EventType: core.ExecutionEventToolProposalCreated,
				Status:    "proposed",
				Summary:   "proposal_id=tp_999 tool_name=search_web review_status=proposed",
				CreatedAt: now.Add(-20 * time.Second),
			},
			{
				EventType: core.ExecutionEventToolRegistered,
				Status:    "enabled",
				Summary:   "tool_name=search_web registered=true implementation_ref=tool/search_web.go",
				CreatedAt: now.Add(-10 * time.Second),
			},
		},
	}, "medium", "high")

	for _, needle := range []string{
		"tool_authority_lifecycle source=canonical:execution_events.tool_authority",
		"tool_proposals:",
		"event=tool.proposal.created status=proposed",
		"tool_registrations:",
		"event=tool.registered status=enabled",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusSystem() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusDurablesShowsEmptyState(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusDurables(core.DurableAgentsStatusSnapshot{})
	if !strings.Contains(out, "status_scope=durables") {
		t.Fatalf("RenderTelegramStatusDurables() = %q, want durables scope", out)
	}
	if !strings.Contains(out, "agents:\n- none") {
		t.Fatalf("RenderTelegramStatusDurables() = %q, want empty durable list marker", out)
	}
}

func TestRenderTelegramStatusChatIncludesSourceMarkers(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID: 17,
		LatestTurnRun: &core.TurnRunStatusSnapshot{
			Status: "completed",
			Kind:   "interactive",
			Source: "compatibility_fallback:turn_runs",
		},
		Continuation: &core.ContinuationStatusSnapshot{
			Status: "pending",
			Source: "operational_current_state_store:continuation_state_json",
		},
		PendingItems: []core.PendingItem{
			{
				Kind:          core.PendingItemKindDecision,
				ChatID:        17,
				ID:            "d-1",
				SourceClass:   "operational_current_state_store",
				SourceSurface: "pending_decisions",
			},
		},
	}, "medium", "high", false)

	for _, needle := range []string{
		"latest_turn status=completed kind=interactive",
		"source=compatibility_fallback:turn_runs",
		"continuation status=pending",
		"source=operational_current_state_store:continuation_state_json",
		"source_class=operational_current_state_store source_surface=pending_decisions",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChat() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusChatIncludesCapabilityDelegationState(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID: 46,
		CapabilityRequests: []core.CapabilityRequestStatusSnapshot{{
			RequestID:       "cap-status",
			Kind:            "purchase",
			TargetResource:  "amazon",
			ReviewStatus:    "approved",
			RequestedFor:    "family-child",
			ParentPrincipal: "telegram:200",
			RiskClass:       "spend",
			Purpose:         "order approved supplies",
			GrantID:         "capg-status",
		}},
		CapabilityGrants: []core.CapabilityGrantStatusSnapshot{{
			GrantID:           "capg-status",
			RequestID:         "cap-status",
			Kind:              "purchase",
			TargetResource:    "amazon",
			Status:            "active",
			GrantedTo:         "family-child",
			AllowedActions:    []string{"order"},
			AnchorFingerprint: "sha256:abcdefghijklmnopqrstuvwxyz",
			InvocationCount:   2,
			FailureCount:      1,
		}},
		RecentExecution: []core.ExecutionEventSummary{{
			EventType: core.ExecutionEventCapabilityGrantChanged,
			Status:    "active",
			Summary:   "grant_id=capg-status kind=purchase target_resource=amazon",
			CreatedAt: now,
		}},
	}, "medium", "high", false)

	for _, needle := range []string{
		"capability_requests source=canonical:session.capability_requests",
		"request_id=cap-status kind=purchase target_resource=amazon status=approved requested_for=family-child parent_principal=telegram:200 risk_class=spend grant_id=capg-status",
		"capability_grants source=canonical:session.capability_grants",
		"grant_id=capg-status kind=purchase target_resource=amazon status=active granted_to=family-child actions=order request_id=cap-status anchor=sha256:abcdefghijkl counters=invocations:2,failures:1",
		"capability_lifecycle source=canonical:execution_events.capability_delegation",
		"event=capability.grant.changed status=active",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChat() = %q, want substring %q", out, needle)
		}
	}
}
