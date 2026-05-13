//go:build linux

package session

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestDurableAgentRegistryAndStateRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:              "help the family group without mutating the house",
			CapabilityEnvelope:   []string{"read_channel", "draft_reply", "synthesize_review"},
			OutboundMode:         "draft_only",
			DriftPolicy:          "admin_ratified",
			PublicSurfaceMode:    "explicit_parent_relay_only",
			TailnetMode:          "tsnet",
			TailnetHostname:      "family-child",
			TailnetTags:          []string{"tag:aphelion-child", "tag:family"},
			TailnetSurfacePolicy: "private_status",
		},
		BootstrapCeiling: core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"read_channel", "draft_reply", "synthesize_review", "bounded_review_artifact"},
			AllowedOutboundModes:         []string{"draft_only", "read_only"},
			AllowedPublicSurfaceModes:    []string{"explicit_parent_relay_only", "none"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		},
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			BaseURL:        "https://openrouter.example.test",
			Model:          "openrouter/group-model",
			MaxTokens:      256,
		},
		ControlPlaneSecret:     "group-control-secret",
		LocalStorageRoots:      []string{"/tmp/family-group"},
		NetworkPolicy:          "restricted",
		WakeupMode:             "event",
		SecretScopes:           []string{"telegram_bot"},
		AllowedTelegramUserIDs: []int64{2002, 2001, 2001},
		Status:                 "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	got, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if got.AgentID != agent.AgentID || got.ChannelKind != agent.ChannelKind {
		t.Fatalf("DurableAgent() = %#v, want agent %q kind %q", got, agent.AgentID, agent.ChannelKind)
	}
	if len(got.LivePolicy.CapabilityEnvelope) != 3 || got.LivePolicy.CapabilityEnvelope[2] != "synthesize_review" {
		t.Fatalf("CapabilityEnvelope = %#v, want preserved capabilities", got.LivePolicy.CapabilityEnvelope)
	}
	if len(got.SecretScopes) != 1 || got.SecretScopes[0] != "telegram_bot" {
		t.Fatalf("SecretScopes = %#v, want telegram_bot", got.SecretScopes)
	}
	if len(got.AllowedTelegramUserIDs) != 2 || got.AllowedTelegramUserIDs[0] != 2001 || got.AllowedTelegramUserIDs[1] != 2002 {
		t.Fatalf("AllowedTelegramUserIDs = %#v, want [2001 2002]", got.AllowedTelegramUserIDs)
	}
	if len(got.BootstrapCeiling.AllowedOutboundModes) != 2 || got.BootstrapCeiling.AllowedOutboundModes[0] != "draft_only" {
		t.Fatalf("BootstrapCeiling.AllowedOutboundModes = %#v, want preserved ceiling", got.BootstrapCeiling.AllowedOutboundModes)
	}
	if got.LivePolicy.OutboundMode != "draft_only" {
		t.Fatalf("OutboundMode = %q, want draft_only", got.LivePolicy.OutboundMode)
	}
	if got.LivePolicy.TailnetMode != "tsnet" || got.LivePolicy.TailnetHostname != "family-child" || got.LivePolicy.TailnetSurfacePolicy != "private_status" {
		t.Fatalf("tailnet declaration = %#v, want tsnet family-child private_status", got.LivePolicy)
	}
	if len(got.LivePolicy.TailnetTags) != 2 || got.LivePolicy.TailnetTags[0] != "tag:aphelion-child" || got.LivePolicy.TailnetTags[1] != "tag:family" {
		t.Fatalf("TailnetTags = %#v, want persisted tags", got.LivePolicy.TailnetTags)
	}
	if got.BootstrapLLM.Backend != "native" {
		t.Fatalf("BootstrapLLM.Backend = %q, want native", got.BootstrapLLM.Backend)
	}
	if got.BootstrapLLM.NativeProvider != "openrouter" {
		t.Fatalf("BootstrapLLM.NativeProvider = %q, want openrouter", got.BootstrapLLM.NativeProvider)
	}
	if got.BootstrapLLM.APIKey != "sk-or-group" {
		t.Fatalf("BootstrapLLM.APIKey = %q, want sk-or-group", got.BootstrapLLM.APIKey)
	}
	if got.ControlPlaneSecret != "group-control-secret" {
		t.Fatalf("ControlPlaneSecret = %q, want group-control-secret", got.ControlPlaneSecret)
	}
	if got.PolicyVersion != 1 {
		t.Fatalf("PolicyVersion = %d, want 1", got.PolicyVersion)
	}
	if got.PolicyHash == "" {
		t.Fatal("PolicyHash is empty, want derived policy hash")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps = created:%v updated:%v, want populated", got.CreatedAt, got.UpdatedAt)
	}

	if err := store.SetDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "ratified updated charter",
		CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
		OutboundMode:       "read_only",
		DriftPolicy:        "admin_review",
	}); err != nil {
		t.Fatalf("SetDurableAgentLivePolicy() err = %v", err)
	}
	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent(updated) err = %v", err)
	}
	if updated.LivePolicy.Charter != "ratified updated charter" {
		t.Fatalf("updated charter = %q, want ratified updated charter", updated.LivePolicy.Charter)
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated PolicyVersion = %d, want 2", updated.PolicyVersion)
	}
	if updated.PolicyHash == got.PolicyHash {
		t.Fatal("updated PolicyHash did not change after live policy update")
	}
	if updated.BootstrapCeiling.AllowedSharedInferenceReuse[0] != "disabled" {
		t.Fatalf("updated BootstrapCeiling.AllowedSharedInferenceReuse = %#v, want preserved disabled ceiling", updated.BootstrapCeiling.AllowedSharedInferenceReuse)
	}

	listed, err := store.ListDurableAgents()
	if err != nil {
		t.Fatalf("ListDurableAgents() err = %v", err)
	}
	if len(listed) != 1 || listed[0].AgentID != agent.AgentID {
		t.Fatalf("ListDurableAgents() = %#v, want single family-group agent", listed)
	}

	state := core.DurableAgentState{
		AgentID:      agent.AgentID,
		Cursor:       "msg-42",
		Status:       "dormant",
		StateJSON:    `{"last_sender":"alice"}`,
		LastWakeAt:   time.Now().UTC().Add(-5 * time.Minute).Round(0),
		LastReviewAt: time.Now().UTC().Round(0),
	}
	if err := store.SaveDurableAgentState(state); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	gotState, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if gotState.Cursor != state.Cursor || gotState.Status != state.Status {
		t.Fatalf("DurableAgentState() = %#v, want cursor/status preserved", gotState)
	}
	if gotState.StateJSON != state.StateJSON {
		t.Fatalf("StateJSON = %q, want %q", gotState.StateJSON, state.StateJSON)
	}

	if err := store.DeleteDurableAgent(agent.AgentID); err != nil {
		t.Fatalf("DeleteDurableAgent() err = %v", err)
	}
	if _, err := store.DurableAgent(agent.AgentID); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("DurableAgent() after delete err = %v, want no rows", err)
	}
	if _, err := store.DurableAgentState(agent.AgentID); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("DurableAgentState() after delete err = %v, want no rows", err)
	}
}

func TestDurableAgentRegistryRejectsInvalidAgentID(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:     "../escape",
		ChannelKind: "external_channel",
		Status:      "active",
	})
	if err == nil {
		t.Fatal("UpsertDurableAgent() err = nil, want invalid agent id error")
	}
	if !strings.Contains(err.Error(), "path separators") {
		t.Fatalf("UpsertDurableAgent() err = %v, want path separator context", err)
	}
}

func TestDurableAgentExternalChannelConfigRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review an external child channel and surface important items.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact", "summarize_pdf"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		ChannelConfig: core.DurableAgentChannelConfig{
			External: &core.DurableAgentExternalChannelConfig{
				Address:          "idolum@example.com",
				Account:          "idolum@example.com",
				Adapter:          "child_adapter",
				Query:            "topic:important newer_than:7d",
				PollInterval:     "5m",
				SurfaceRules:     []string{"job opportunity", "external inquiry"},
				SummarizePDFs:    true,
				SynthesisCadence: "4h",
				NeverRetain:      []string{"oauth_token", "password"},
			},
		},
		WakeupMode: "poll",
		Status:     "draft",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	got, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	external := got.ChannelConfig.ExternalConfig()
	if external == nil {
		t.Fatal("ChannelConfig.ExternalConfig() = nil, want persisted external channel config")
	}
	if external.Address != "idolum@example.com" {
		t.Fatalf("ChannelConfig.ExternalConfig().Address = %q, want idolum@example.com", external.Address)
	}
	if external.Adapter != "child_adapter" {
		t.Fatalf("ChannelConfig.ExternalConfig().Adapter = %q, want child_adapter", external.Adapter)
	}
	if external.PollInterval != "5m" {
		t.Fatalf("ChannelConfig.ExternalConfig().PollInterval = %q, want 5m", external.PollInterval)
	}
	if !external.SummarizePDFs {
		t.Fatal("ChannelConfig.ExternalConfig().SummarizePDFs = false, want true")
	}
	if len(external.SurfaceRules) != 2 || external.SurfaceRules[0] != "job opportunity" {
		t.Fatalf("ChannelConfig.ExternalConfig().SurfaceRules = %#v, want persisted surface rules", external.SurfaceRules)
	}
	if len(external.NeverRetain) != 2 || external.NeverRetain[1] != "password" {
		t.Fatalf("ChannelConfig.ExternalConfig().NeverRetain = %#v, want persisted never-retain classes", external.NeverRetain)
	}
}

func TestDurableAgentStateSplitRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "state-split-agent",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Observe and report bounded research updates.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-test",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	wakeAt := time.Now().UTC().Add(-10 * time.Minute).Round(0)
	reviewAt := time.Now().UTC().Add(-2 * time.Minute).Round(0)
	runtimeState := core.DurableAgentRuntimeState{
		AgentID:         agent.AgentID,
		Cursor:          "message-42",
		Status:          "awake",
		StateJSON:       `{"continuity":"ok"}`,
		LastApplyStatus: "pending",
		LastApplyError:  "",
		LastWakeAt:      wakeAt,
		LastReviewAt:    reviewAt,
	}
	if err := store.SaveDurableAgentRuntimeState(runtimeState); err != nil {
		t.Fatalf("SaveDurableAgentRuntimeState() err = %v", err)
	}

	issuedAt := time.Now().UTC().Add(-30 * time.Minute).Round(0)
	identityState := core.DurableAgentIdentityState{
		AgentID:                       agent.AgentID,
		LastOfferedPolicyVersion:      3,
		LastOfferedPolicyHash:         "hash-offered",
		LastOfferedPolicyAt:           issuedAt,
		LastAcknowledgedPolicyVersion: 3,
		LastAcknowledgedPolicyHash:    "hash-ack",
		LastAcknowledgedPolicyAt:      issuedAt.Add(2 * time.Minute),
		LastAppliedPolicyVersion:      3,
		LastAppliedPolicyHash:         "hash-applied",
		LastAppliedPolicyAt:           issuedAt.Add(3 * time.Minute),
	}
	if err := store.SaveDurableAgentIdentityState(identityState); err != nil {
		t.Fatalf("SaveDurableAgentIdentityState() err = %v", err)
	}

	gotRuntime, err := store.DurableAgentRuntimeState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRuntimeState() err = %v", err)
	}
	if gotRuntime.Cursor != runtimeState.Cursor || gotRuntime.Status != runtimeState.Status {
		t.Fatalf("DurableAgentRuntimeState() = %#v, want cursor/status preserved", gotRuntime)
	}
	if gotRuntime.LastApplyStatus != "pending" {
		t.Fatalf("DurableAgentRuntimeState().LastApplyStatus = %q, want pending", gotRuntime.LastApplyStatus)
	}

	gotIdentity, err := store.DurableAgentIdentityState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentIdentityState() err = %v", err)
	}
	if gotIdentity.LastAppliedPolicyVersion != 3 {
		t.Fatalf("DurableAgentIdentityState().LastAppliedPolicyVersion = %d, want 3", gotIdentity.LastAppliedPolicyVersion)
	}
	if gotIdentity.LastAppliedPolicyHash != "hash-applied" {
		t.Fatalf("DurableAgentIdentityState().LastAppliedPolicyHash = %q, want hash-applied", gotIdentity.LastAppliedPolicyHash)
	}

	combined, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if combined.Cursor != runtimeState.Cursor {
		t.Fatalf("DurableAgentState().Cursor = %q, want %q", combined.Cursor, runtimeState.Cursor)
	}
	if combined.LastAppliedPolicyVersion != identityState.LastAppliedPolicyVersion {
		t.Fatalf("DurableAgentState().LastAppliedPolicyVersion = %d, want %d", combined.LastAppliedPolicyVersion, identityState.LastAppliedPolicyVersion)
	}

	runtimeState.Status = "dormant"
	runtimeState.DormantAt = time.Now().UTC().Round(0)
	runtimeState.LastApplyStatus = "failed"
	runtimeState.LastApplyError = "child runtime unavailable"
	if err := store.SaveDurableAgentRuntimeState(runtimeState); err != nil {
		t.Fatalf("SaveDurableAgentRuntimeState(update) err = %v", err)
	}

	identityAfterRuntimeUpdate, err := store.DurableAgentIdentityState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentIdentityState(after runtime update) err = %v", err)
	}
	if identityAfterRuntimeUpdate.LastAppliedPolicyVersion != identityState.LastAppliedPolicyVersion {
		t.Fatalf("identity changed by runtime update: got %d want %d", identityAfterRuntimeUpdate.LastAppliedPolicyVersion, identityState.LastAppliedPolicyVersion)
	}

	identityState.LastOfferedPolicyVersion = 4
	identityState.LastOfferedPolicyHash = "hash-offered-4"
	identityState.LastOfferedPolicyAt = time.Now().UTC().Round(0)
	if err := store.SaveDurableAgentIdentityState(identityState); err != nil {
		t.Fatalf("SaveDurableAgentIdentityState(update) err = %v", err)
	}

	runtimeAfterIdentityUpdate, err := store.DurableAgentRuntimeState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRuntimeState(after identity update) err = %v", err)
	}
	if runtimeAfterIdentityUpdate.Status != runtimeState.Status || runtimeAfterIdentityUpdate.LastApplyStatus != runtimeState.LastApplyStatus {
		t.Fatalf("runtime changed by identity update: got %#v want status=%q apply_status=%q", runtimeAfterIdentityUpdate, runtimeState.Status, runtimeState.LastApplyStatus)
	}
}

func TestApplyDurableAgentLivePolicyRejectsBootstrapCeilingWidening(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"group_reply", "bounded_review_artifact"},
			AllowedOutboundModes:         []string{"read_only", "draft_only"},
			AllowedPublicSurfaceModes:    []string{"none"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		},
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, _, err := store.ApplyDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "Observe and surface bounded family coordination.",
		CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
		OutboundMode:       "reply_with_policy_authorization",
		DriftPolicy:        "admin_review",
	}, 0, "attempted widening")
	if err == nil {
		t.Fatal("ApplyDurableAgentLivePolicy() err = nil, want bootstrap ceiling violation")
	}
	if !strings.Contains(err.Error(), "bootstrap ceiling") {
		t.Fatalf("ApplyDurableAgentLivePolicy() err = %v, want bootstrap ceiling violation", err)
	}

	got, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if got.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("LivePolicy.OutboundMode = %q, want unchanged read_only", got.LivePolicy.OutboundMode)
	}
	if got.PolicyVersion != 1 {
		t.Fatalf("PolicyVersion = %d, want unchanged 1", got.PolicyVersion)
	}
}

func TestApplyDurableAgentLivePolicyTracksOfferedStateAndRatifiedOutcome(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	updated, update, err := store.ApplyDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "Observe and surface family coordination, but allow reviewed drafting.",
		CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
		OutboundMode:       "draft_only",
		DriftPolicy:        "admin_review",
	}, 42, "ratified family-group narrowing")
	if err != nil {
		t.Fatalf("ApplyDurableAgentLivePolicy() err = %v", err)
	}
	if update == nil {
		t.Fatal("ApplyDurableAgentLivePolicy() update = nil, want policy update record")
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated.PolicyVersion = %d, want 2", updated.PolicyVersion)
	}

	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if state.LastOfferedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastOfferedPolicyVersion = %d, want %d", state.LastOfferedPolicyVersion, updated.PolicyVersion)
	}
	if state.LastOfferedPolicyHash != updated.PolicyHash {
		t.Fatalf("LastOfferedPolicyHash = %q, want %q", state.LastOfferedPolicyHash, updated.PolicyHash)
	}
	if state.LastApplyStatus != "pending" {
		t.Fatalf("LastApplyStatus = %q, want pending", state.LastApplyStatus)
	}
	if state.LastApplyError != "" {
		t.Fatalf("LastApplyError = %q, want empty", state.LastApplyError)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if len(continuity.RatifiedOutcomes) != 1 {
		t.Fatalf("RatifiedOutcomes len = %d, want 1", len(continuity.RatifiedOutcomes))
	}
	if continuity.RatifiedOutcomes[0].PolicyVersion != updated.PolicyVersion {
		t.Fatalf("RatifiedOutcomes[0].PolicyVersion = %d, want %d", continuity.RatifiedOutcomes[0].PolicyVersion, updated.PolicyVersion)
	}
	if continuity.RatifiedOutcomes[0].SourceReviewEventID != 42 {
		t.Fatalf("RatifiedOutcomes[0].SourceReviewEventID = %d, want 42", continuity.RatifiedOutcomes[0].SourceReviewEventID)
	}
	if !strings.Contains(continuity.RatifiedOutcomes[0].Summary, "ratified family-group narrowing") {
		t.Fatalf("RatifiedOutcomes[0].Summary = %q, want operator reason", continuity.RatifiedOutcomes[0].Summary)
	}
}
