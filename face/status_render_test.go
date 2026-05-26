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

func TestRenderTelegramStatusIncludesAuthorityProjection(t *testing.T) {
	t.Parallel()

	authority := core.AuthorityStatusSnapshot{
		Status:       "needs_attention",
		FindingCount: 1,
		ErrorCount:   1,
		Findings: []core.AuthorityFindingSnapshot{{
			Code:        "expired_continuation_lease",
			Severity:    "error",
			SourceKind:  "continuation_lease",
			SourceID:    "lease-1",
			ChatID:      7,
			ApplyAction: "expire_continuation_lease",
		}},
	}
	chat := RenderTelegramStatusChat(core.ChatStatusSnapshot{ChatID: 7, Authority: authority}, "medium", "high", false)
	if !strings.Contains(chat, "authority status=needs_attention findings=1 errors=1 warnings=0") ||
		!strings.Contains(chat, "first_code=expired_continuation_lease") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want authority projection", chat)
	}
	system := RenderTelegramStatusSystem(core.SystemStatusSnapshot{Authority: authority}, "medium", "high")
	if !strings.Contains(system, "authority status=needs_attention findings=1 errors=1 warnings=0") {
		t.Fatalf("RenderTelegramStatusSystem() = %q, want authority projection", system)
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
		AutoApproval: &core.AutoApprovalStatusSnapshot{
			Active:    true,
			Scope:     "workspace",
			UsedCount: 1,
			MaxUses:   3,
			Reason:    "live test window",
			ExpiresAt: time.Date(2026, 5, 5, 14, 0, 0, 0, time.UTC),
		},
	}, "medium", "high", false)

	for _, needle := range []string{
		"turn_phase phase=deliver",
		"hidden_inputs categories=semantic_recurrence,unresolved_memory_state",
		"delivery status=delivery_failed",
		"plan_progress completed=2 total=2 fully_executed=true",
		"detached_work decisions=1 continuations=0 recoveries=1 stale_turns=1",
		"auto_approval status=active scope=workspace expires_at=2026-05-05T14:00:00Z used=1/3 reason=\"live test window\"",
		"current_signal=recovery:stale_turn",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChat() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusChatSummaryStateNeedsRecoveryForStaleTESRun(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		GeneratedAt:   now,
		ChatID:        7,
		ActiveTurnIDs: []uint64{42},
		LatestTurnRun: &core.TurnRunStatusSnapshot{
			Status:         "running",
			Kind:           "interactive",
			LastActivityAt: now.Add(-2 * time.Hour),
			LastToolName:   "exec",
		},
		RestartHealth: core.RestartHealthSnapshot{
			StaleTurnThreshold: 3 * time.Minute,
		},
	}, "medium", "high", false)

	if !strings.Contains(out, "summary state=needs_recovery") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want needs_recovery state", out)
	}
	if !strings.Contains(out, "current_signal=recovery:stale_active_turn") {
		t.Fatalf("RenderTelegramStatusChat() = %q, want stale active turn signal", out)
	}
}

func TestRenderTelegramStatusChatOperatorCardSeparatesBacklogAndRevokedContinuation(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChatOperatorCard(core.ChatStatusSnapshot{
		GeneratedAt: time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
		ChatID:      7,
		Continuation: &core.ContinuationStatusSnapshot{
			Status:           "revoked",
			RemainingTurns:   3,
			PersonaIntent:    "continue",
			GovernorIntent:   "continue",
			GovernorRatified: true,
			Source:           "operational_current_state_store:continuation_state_json",
		},
		PendingItems: []core.PendingItem{
			{Kind: core.PendingItemKindMission, ChatID: 7, ID: "mission-1", Summary: "status=candidate title=Mission Control"},
			{Kind: core.PendingItemKindDecision, ChatID: 7, ID: "decision-1", Summary: "kind=proposal_approval"},
		},
	}, "gpt", "xhigh", false)

	for _, needle := range []string{
		"status: blocked",
		"continuation: stopped",
		"next: tap Pending Only, then approve or deny the pending decision",
		"needs_attention:",
		"- approval needed",
		"backlog: 1 candidate mission(s)",
		"evidence: as of 2026-05-06T12:00:00Z; source: chat status projection",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChatOperatorCard() = %q, want substring %q", out, needle)
		}
	}
	for _, forbidden := range []string{"remaining_turns", "persona_intent", "governor_intent", "source="} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("RenderTelegramStatusChatOperatorCard() = %q, should not contain %q", out, forbidden)
		}
	}
}

func TestRenderTelegramStatusChatOperatorCardPrioritizesAttentionWhileWorking(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChatOperatorCard(core.ChatStatusSnapshot{
		GeneratedAt:   time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC),
		ChatID:        7,
		ActiveTurnIDs: []uint64{42},
		LatestTurnRun: &core.TurnRunStatusSnapshot{
			Status:       "running",
			Kind:         "interactive",
			LastToolName: "exec",
			Source:       "canonical:execution_events.turn",
		},
		PendingItems: []core.PendingItem{
			{Kind: core.PendingItemKindDecision, ChatID: 7, ID: "decision-1", Summary: "kind=proposal_approval"},
		},
	}, "sonnet", "xhigh", false)

	for _, needle := range []string{
		"status: working",
		"why: running tool exec",
		"next: tap Pending Only, then approve or deny the pending decision",
		"needs_attention:",
		"evidence: as of 2026-05-07T12:30:00Z; source: chat status projection; latest turn: canonical:execution_events.turn",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChatOperatorCard() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusOperatorViewsAvoidRawTelemetry(t *testing.T) {
	t.Parallel()

	views := map[string]string{
		"system": RenderTelegramStatusSystemOperatorCard(core.SystemStatusSnapshot{
			GeneratedAt: time.Date(2026, 5, 7, 16, 0, 0, 0, time.UTC),
			PendingItems: []core.PendingItem{{
				Kind:    core.PendingItemKindDecision,
				ChatID:  7,
				Summary: "Approve a pending action.",
			}},
		}, "sonnet", "medium"),
		"hot": RenderTelegramStatusHotChatsOperatorCard(core.SystemStatusSnapshot{
			GeneratedAt: time.Date(2026, 5, 7, 16, 0, 0, 0, time.UTC),
			HotChats:    []core.ChatStatusRollup{{ChatID: 7, PendingCount: 1}},
		}),
		"find": RenderTelegramStatusFindChatOperatorCard(core.SystemStatusSnapshot{
			GeneratedAt: time.Date(2026, 5, 7, 16, 0, 0, 0, time.UTC),
			HotChats:    []core.ChatStatusRollup{{ChatID: 7, PendingCount: 1}},
		}),
		"durables": RenderTelegramStatusDurablesOperatorCard(core.DurableAgentsStatusSnapshot{
			GeneratedAt:    time.Date(2026, 5, 7, 16, 0, 0, 0, time.UTC),
			TotalAgents:    1,
			DegradedAgents: 1,
			Agents: []core.DurableAgentStatusSnapshot{{
				AgentID:          "ops-child",
				Status:           "active",
				Health:           "degraded",
				EnrollmentStatus: "active",
			}},
		}),
	}
	for name, out := range views {
		for _, want := range []string{"Status:", "Why:", "Next:"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s operator view = %q, want %q", name, out, want)
			}
		}
		for _, forbidden := range []string{"status_scope=", "summary ", "source=", "enabled=true", "kind="} {
			if strings.Contains(out, forbidden) {
				t.Fatalf("%s operator view = %q, should not contain raw telemetry %q", name, out, forbidden)
			}
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
				EventType: core.ExecutionEventToolRegistered,
				Status:    "enabled",
				Summary:   "tool_name=browse_page registered=true implementation_ref=external:browse_page",
				CreatedAt: now.Add(-10 * time.Second),
			},
		},
	}, "medium", "high", false)

	for _, needle := range []string{
		"tool_authority_lifecycle source=canonical:execution_events.tool_authority",
		"tool_registrations:",
		"event=tool.registered status=enabled",
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
				AgentID:                   "family-group",
				ChannelKind:               "telegram_group",
				Status:                    "active",
				Health:                    "degraded",
				ReviewTargetChatID:        1001,
				PolicyVersion:             4,
				PolicyHash:                "8f829f8793fcb1234567890",
				PolicyOutboundMode:        "reply_with_parent_review",
				PolicyDrift:               "admin_review",
				CapabilityEnvelope:        []string{"group_reply", "bounded_review_artifact"},
				LastApplyStatus:           "failed",
				LastApplyError:            "policy apply timed out while child was offline",
				LastAppliedPolicyVersion:  3,
				IdentitySource:            "canonical:session.durable_agents",
				RuntimePostureSource:      "operational_current_state_store:session.durable_agent_state+projection:tes_execution_events",
				CanonicalPrincipal:        "durable_agent:family-group",
				ChildRuntimeGrantCount:    1,
				ChildRuntimeBlockedReason: "child_runtime_blocked: grant_stale_manifest_drift grant_id=capg-runtime",
				ChildRuntimeRepairHint:    "repair or revoke child_runtime grant capg-runtime",
				SubstrateLabels:           []string{"parent_binary", "codex_home", "tailnet:tsnet"},
				TailnetMode:               "tsnet",
				TailnetHostname:           "family-helper",
				TailnetTags:               []string{"tag:aphelion-child"},
				TailnetSurfacePolicy:      "private_status",
				TailnetSurfaceID:          "durable_agent:family-group:tsnet_http:status",
				ProfileManifestStatus:     "policy_hash_mismatch",
				ProfileManifestPolicyHash: "old-profile-hash-1234567890",
				ProfileManifestFileCount:  3,
			},
		},
	})

	for _, needle := range []string{
		"status_scope=durables",
		"summary total=1 active=1 dormant=0 degraded=1 inactive=0",
		"- id=family-group channel=telegram_group status=active health=degraded review_chat=1001",
		"policy version=4 hash=8f829f8793fc outbound=reply_with_parent_review",
		"runtime apply_error=\"policy apply timed out while child was offline\"",
		"tailnet mode=tsnet hostname=family-helper surface_policy=private_status surface_id=durable_agent:family-group:tsnet_http:status tags=tag:aphelion-child",
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
				EventType: core.ExecutionEventToolRegistered,
				Status:    "enabled",
				Summary:   "tool_name=browse_page registered=true implementation_ref=external:browse_page",
				CreatedAt: now.Add(-10 * time.Second),
			},
		},
	}, "medium", "high")

	for _, needle := range []string{
		"tool_authority_lifecycle source=canonical:execution_events.tool_authority",
		"tool_registrations:",
		"event=tool.registered status=enabled",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusSystem() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusSystemIncludesTailnetSummary(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusSystem(core.SystemStatusSnapshot{
		Tailnet: &core.TailnetStatusSnapshot{
			Enabled:      true,
			Backend:      "cli",
			Status:       "degraded",
			DNSName:      "aphelion.example.ts.net",
			TailnetName:  "example.ts.net",
			TailscaleIPs: []string{"100.64.0.10"},
			Tags:         []string{"tag:admin"},
			Summary:      "MagicDNS is unavailable.",
			Parent: &core.TailnetParentStatus{
				Enabled:     true,
				Running:     true,
				Hostname:    "aphelion",
				ListenAddr:  ":8765",
				MagicDNSURL: "http://aphelion.example.ts.net:8765",
			},
			Surfaces: []core.TailnetSurfaceStatus{{
				SurfaceID:   "parent:tsnet_http:status",
				SurfaceKind: "tsnet_http",
				Name:        "status",
				URL:         "http://aphelion.example.ts.net:8765/status",
				Status:      "active",
			}},
			Issues: []core.TailnetIssue{{
				Code:     "magicdns_missing",
				Severity: "warning",
				Summary:  "no MagicDNS name was observed.",
			}},
		},
	}, "opus", "high")
	for _, needle := range []string{
		"tailnet:",
		"status=degraded",
		"node=aphelion.example.ts.net",
		"tailnet=example.ts.net",
		"parent_tsnet enabled=true running=true",
		"magic_url=http://aphelion.example.ts.net:8765",
		"surfaces count=1",
		"surface id=parent:tsnet_http:status status=active",
		"issue code=magicdns_missing",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusSystem() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusSystemIncludesSandboxReadiness(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusSystem(core.SystemStatusSnapshot{
		Sandbox: core.SandboxReadinessSnapshot{
			Issues: []core.SandboxReadinessIssue{{
				Role:             "approved_user",
				Mode:             "isolated",
				Network:          "allowlist",
				Code:             "sandbox_network_allowlist_backend_unavailable",
				Severity:         "warning",
				Summary:          "approved_user requests a sandbox network allowlist, but the linux_netns_nftables backend is unavailable.",
				NextRepairAction: "Install the host networking prerequisites or use network=deny for isolated execution.",
			}},
		},
	}, "opus", "high")
	for _, needle := range []string{
		"sandbox_readiness:",
		"role=approved_user",
		"code=sandbox_network_allowlist_backend_unavailable",
		"severity=warning",
		"next=\"Install the host networking prerequisites",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusSystem() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusSystemIncludesTelegramIngressUpdates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	out := RenderTelegramStatusSystem(core.SystemStatusSnapshot{
		TelegramIngressUpdates: []core.TelegramIngressUpdateSnapshot{{
			Surface:    "telegram:primary",
			UpdateID:   77,
			UpdateKind: "message",
			ChatID:     7001,
			MessageID:  200,
			Status:     "queued",
			TurnRunID:  12,
			UpdatedAt:  now,
		}},
	}, "opus", "high")
	for _, needle := range []string{
		"telegram_ingress_updates:",
		"surface=telegram:primary update_id=77 kind=message status=queued chat_id=7001 message_id=200 turn_run_id=12",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusSystem() = %q, want substring %q", out, needle)
		}
	}
}

func TestRenderTelegramStatusSystemIncludesProviderHealth(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	out := RenderTelegramStatusSystem(core.SystemStatusSnapshot{
		ProviderHealth: core.ProviderHealthSnapshot{
			GeneratedAt:         now,
			Window:              4 * time.Hour,
			Status:              "degraded",
			RecentFailures:      2,
			RecentRetries:       1,
			RecentFailovers:     1,
			LastFailureAt:       now.Add(-time.Minute),
			LastFailureProvider: "openrouter",
			LastFailureModel:    "openrouter/sonnet",
			LastFailureReason:   "quota exceeded",
			LastFailureError:    "insufficient_quota",
		},
	}, "opus", "high")
	for _, needle := range []string{
		"provider_health status=degraded failures=2 retries=1 failovers=1 successes=0 window=4h0m0s",
		"provider=openrouter",
		"reason=\"quota exceeded\"",
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
			Source: "canonical:execution_events.turn",
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
		"source=canonical:execution_events.turn",
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

func TestRenderTelegramStatusChatIncludesCompactExternalToolReadiness(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusChat(core.ChatStatusSnapshot{
		ChatID: 47,
		ExternalToolInvocationReadiness: []core.ExternalToolInvocationReadinessSnapshot{{
			ToolName:         "public-feed-readonly",
			ChildPrincipal:   "durable_agent:child-public-feed",
			Action:           "public_profile_metadata_read",
			SelectorName:     "username",
			Status:           "blocked",
			Why:              `runtime material missing: env_from_parent "APHELION_E2_MISSING_ENV"`,
			NextRepairAction: "provide or correct the named child_runtime material",
		}},
	}, "medium", "high", false)

	for _, needle := range []string{
		"external_tool_invocation_readiness source=projection:tool_lifecycle+capability_grants",
		"tool=public-feed-readonly child=durable_agent:child-public-feed action=public_profile_metadata_read selector=username status=blocked",
		`why="runtime material missing: env_from_parent 'APHELION_E2_MISSING_ENV'"`,
		`next_repair="provide or correct the named child_runtime material"`,
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusChat() = %q, want substring %q", out, needle)
		}
	}
}
