//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func TestAssembleInteractiveLikeTurnCommonSpine(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	admin, ok := rt.resolver.ResolveTelegramUser(1001)
	if !ok {
		t.Fatal("ResolveTelegramUser(1001) = false, want true")
	}
	dmScope, err := rt.scopeForPrincipal(admin)
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	agentRow := core.DurableAgent{
		AgentID:            "assembly-shared-spine",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Help locally in the family group.",
			OutboundMode: "reply_with_policy_authorization",
			DriftPolicy:  "admin_review",
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agentRow); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	durableScope, err := rt.scopeForDurableAgent(agentRow)
	if err != nil {
		t.Fatalf("scopeForDurableAgent() err = %v", err)
	}

	dmMsg := core.InboundMessage{
		ChatID:     9001,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello from dm",
		MessageID:  11,
	}
	durableMsg := core.InboundMessage{
		ChatID:         -9002,
		ChatType:       "group",
		ChatTitle:      "Family",
		SenderID:       555,
		SenderName:     "alice",
		Text:           "hello from group",
		MessageID:      12,
		DurableAgentID: "assembly-shared-spine",
	}
	durablePrepared := durableMsg
	durablePrepared.Text = durableGroupInboundText(durableMsg)

	cases := []struct {
		name             string
		input            interactiveLikeAssemblyInput
		wantChannel      string
		wantPolicyReason string
	}{
		{
			name: "dm",
			input: interactiveLikeAssemblyInput{
				Scope:                dmScope,
				Key:                  session.SessionKey{ChatID: dmMsg.ChatID, Scope: telegramDMScopeRef(dmMsg.ChatID)},
				Msg:                  dmMsg,
				Channel:              "telegram",
				RunKind:              session.TurnRunKindInteractive,
				PrincipalRole:        string(admin.Role),
				AuditChannel:         "telegram",
				EventAwareness:       turn.EventAwareness{Origin: inboundOriginLabel(dmMsg)},
				PromptContextErrHint: "load workspace prompt context",
				PolicyReason:         "mapped from pipeline interactive face policy",
			},
			wantChannel:      "telegram",
			wantPolicyReason: "mapped from pipeline interactive face policy",
		},
		{
			name: "durable_group",
			input: interactiveLikeAssemblyInput{
				Scope:                durableScope,
				Key:                  session.SessionKey{ChatID: durableMsg.ChatID, Scope: durableAgentScopeRef(agentRow)},
				Msg:                  durableMsg,
				PrepareInbound:       &durablePrepared,
				Channel:              "telegram_group",
				RunKind:              session.TurnRunKindInteractive,
				PrincipalRole:        "durable_agent",
				AuditChannel:         "telegram_group",
				PromptContextErrHint: "load durable agent prompt context",
				PolicyReason:         "mapped from interactive face policy for durable groups",
			},
			wantChannel:      "telegram_group",
			wantPolicyReason: "mapped from interactive face policy for durable groups",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assembled, err := rt.assembleInteractiveLikeTurn(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("assembleInteractiveLikeTurn() err = %v", err)
			}
			if assembled.Sess == nil {
				t.Fatal("assembled.Sess = nil, want loaded session")
			}
			if strings.TrimSpace(assembled.Prepared.LedgerText) == "" {
				t.Fatal("assembled.Prepared.LedgerText empty, want prepared text")
			}
			if assembled.Exec.Provider == nil {
				t.Fatal("assembled.Exec.Provider = nil, want provider")
			}
			if assembled.PromptContext == nil {
				t.Fatal("assembled.PromptContext = nil, want prompt context")
			}
			if assembled.Machine == nil {
				t.Fatal("assembled.Machine = nil, want machine")
			}
			if assembled.Machine.Options.Channel != tc.wantChannel {
				t.Fatalf("machine channel = %q, want %q", assembled.Machine.Options.Channel, tc.wantChannel)
			}
			if assembled.BaseGovernorAwareness.Channel != tc.wantChannel {
				t.Fatalf("governor awareness channel = %q, want %q", assembled.BaseGovernorAwareness.Channel, tc.wantChannel)
			}
			if assembled.BaseGovernorAwareness.RunKind != string(session.TurnRunKindInteractive) {
				t.Fatalf("governor awareness run kind = %q, want interactive", assembled.BaseGovernorAwareness.RunKind)
			}
			policy := assembled.Machine.PolicyFunc(turn.Request{})
			if policy.Reason != tc.wantPolicyReason {
				t.Fatalf("policy reason = %q, want %q", policy.Reason, tc.wantPolicyReason)
			}
			wantPolicy := pipeline.DecideInteractiveFacePolicy(assembled.Prepared.LedgerText)
			if policy.Proposal != wantPolicy.Proposal {
				t.Fatalf("policy proposal = %v, want %v", policy.Proposal, wantPolicy.Proposal)
			}
			if policy.Render != wantPolicy.Render {
				t.Fatalf("policy render = %v, want %v", policy.Render, wantPolicy.Render)
			}
		})
	}
}

func TestAssembleInteractiveLikeTurnSpeciesOverrides(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	admin, ok := rt.resolver.ResolveTelegramUser(1001)
	if !ok {
		t.Fatal("ResolveTelegramUser(1001) = false, want true")
	}
	dmScope, err := rt.scopeForPrincipal(admin)
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	dmMsg := core.InboundMessage{
		ChatID:       9011,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "approve and continue",
		MessageID:    21,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: "continuation_approve",
	}
	dmAssembled, err := rt.assembleInteractiveLikeTurn(context.Background(), interactiveLikeAssemblyInput{
		Scope:                dmScope,
		Key:                  session.SessionKey{ChatID: dmMsg.ChatID, Scope: telegramDMScopeRef(dmMsg.ChatID)},
		Msg:                  dmMsg,
		Channel:              "telegram",
		RunKind:              session.TurnRunKindInteractive,
		PrincipalRole:        string(admin.Role),
		AuditChannel:         "telegram",
		EventAwareness:       turn.EventAwareness{Origin: inboundOriginLabel(dmMsg), TurnAuthorizationKind: inboundOriginDetailLabel(dmMsg)},
		PromptContextErrHint: "load workspace prompt context",
		PolicyReason:         "mapped from pipeline interactive face policy",
	})
	if err != nil {
		t.Fatalf("assembleInteractiveLikeTurn(dm) err = %v", err)
	}
	if dmAssembled.BaseGovernorAwareness.EventOrigin != string(core.InboundOriginTurnAuthorization) {
		t.Fatalf("dm event origin = %q, want %q", dmAssembled.BaseGovernorAwareness.EventOrigin, core.InboundOriginTurnAuthorization)
	}
	if dmAssembled.BaseGovernorAwareness.TurnAuthorizationKind != "continuation_approve" {
		t.Fatalf("dm turn authorization kind = %q, want continuation_approve", dmAssembled.BaseGovernorAwareness.TurnAuthorizationKind)
	}

	agentRow := core.DurableAgent{
		AgentID:            "assembly-species-override",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Help locally in the family group.",
			OutboundMode: "reply_with_policy_authorization",
			DriftPolicy:  "admin_review",
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agentRow); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	durableScope, err := rt.scopeForDurableAgent(agentRow)
	if err != nil {
		t.Fatalf("scopeForDurableAgent() err = %v", err)
	}

	durableMsg := core.InboundMessage{
		ChatID:         -9012,
		ChatType:       "group",
		ChatTitle:      "Family",
		SenderID:       555,
		SenderName:     "alice",
		Text:           "Can you summarize what we agreed?",
		MessageID:      22,
		DurableAgentID: agentRow.AgentID,
	}
	durablePrepared := durableMsg
	durablePrepared.Text = durableGroupInboundText(durableMsg)
	durableAssembled, err := rt.assembleInteractiveLikeTurn(context.Background(), interactiveLikeAssemblyInput{
		Scope:                durableScope,
		Key:                  session.SessionKey{ChatID: durableMsg.ChatID, Scope: durableAgentScopeRef(agentRow)},
		Msg:                  durableMsg,
		PrepareInbound:       &durablePrepared,
		Channel:              "telegram_group",
		RunKind:              session.TurnRunKindInteractive,
		PrincipalRole:        "durable_agent",
		AuditChannel:         "telegram_group",
		PromptContextErrHint: "load durable agent prompt context",
		PolicyReason:         "mapped from interactive face policy for durable groups",
	})
	if err != nil {
		t.Fatalf("assembleInteractiveLikeTurn(durable) err = %v", err)
	}
	if durableAssembled.BaseGovernorAwareness.EventOrigin != "" {
		t.Fatalf("durable event origin = %q, want empty", durableAssembled.BaseGovernorAwareness.EventOrigin)
	}
	if strings.TrimSpace(durableAssembled.Prepared.UserText) != strings.TrimSpace(durablePrepared.Text) {
		t.Fatalf("durable prepared text = %q, want transformed text %q", durableAssembled.Prepared.UserText, durablePrepared.Text)
	}
}

func TestAssembleInteractiveLikeTurnIncludesWorkingObjectiveBesideTerminalOperation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	admin, ok := rt.resolver.ResolveTelegramUser(1001)
	if !ok {
		t.Fatal("ResolveTelegramUser(1001) = false, want true")
	}
	dmScope, err := rt.scopeForPrincipal(admin)
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9025, UserID: 0, Scope: telegramDMScopeRef(9025)}
	if err := store.UpdateWorkingObjective(key, session.WorkingObjective{
		Objective: "current durable children resource-separation question",
		Source:    "inferred",
	}); err != nil {
		t.Fatalf("UpdateWorkingObjective() err = %v", err)
	}
	stale := budgetRecoveryTestOperationState()
	stale.ID = "stale-side-thread-operation"
	stale.Objective = "Document old Imexx SSH recall context."
	stale.Status = session.OperationStatusCompleted
	stale.Stage = "completed"
	stale.PhasePlan.Phases[0].Status = session.PlanStatusCompleted
	if err := store.UpdateOperationState(key, stale); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	assembled, err := rt.assembleInteractiveLikeTurn(context.Background(), interactiveLikeAssemblyInput{
		Scope:                dmScope,
		Key:                  key,
		Msg:                  core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, SenderName: "admin", Text: "what do durable children need here?", MessageID: 31},
		Channel:              "telegram",
		RunKind:              session.TurnRunKindInteractive,
		PrincipalRole:        string(admin.Role),
		AuditChannel:         "telegram",
		EventAwareness:       turn.EventAwareness{Origin: string(core.InboundOriginUser)},
		PromptContextErrHint: "load workspace prompt context",
		PolicyReason:         "mapped from pipeline interactive face policy",
	})
	if err != nil {
		t.Fatalf("assembleInteractiveLikeTurn() err = %v", err)
	}
	aw := assembled.BaseGovernorAwareness
	if got, want := aw.WorkingObjective, "current durable children resource-separation question"; got != want {
		t.Fatalf("WorkingObjective = %q, want %q", got, want)
	}
	if got, want := aw.WorkingObjectiveSource, "inferred"; got != want {
		t.Fatalf("WorkingObjectiveSource = %q, want %q", got, want)
	}
	if got, want := aw.OperationStatus, string(session.OperationStatusCompleted); got != want {
		t.Fatalf("OperationStatus = %q, want %q", got, want)
	}
	if got, want := aw.OperationObjective, "Document old Imexx SSH recall context."; got != want {
		t.Fatalf("OperationObjective = %q, want %q", got, want)
	}
}
