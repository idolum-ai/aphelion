//go:build linux

package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) durableAgent(ctx context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("durable agent governance requires transcript store")
	}
	if err := requireAdminTool(p, "durable_agent"); err != nil {
		return "", err
	}

	var in durableAgentInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode durable_agent input: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "list":
		agents, err := r.store.ListDurableAgents()
		if err != nil {
			return "", err
		}
		return renderDurableAgentList(agents), nil
	case "create":
		return r.createDurableAgent(in, key)
	case "create_from_archetype":
		return r.createDurableAgentFromArchetype(in, key)
	case "activate":
		return r.activateDurableAgent(in)
	case "connection_test":
		return r.testDurableAgentConnection(ctx, in)
	case "policy_show":
		if strings.TrimSpace(in.AgentID) == "" {
			return "", fmt.Errorf("durable_agent agent_id is required for policy_show")
		}
		agent, err := r.resolveDurableAgent(in.AgentID)
		if err != nil {
			return "", err
		}
		history := in.History
		if history <= 0 {
			history = 5
		}
		updates, err := r.store.DurableAgentPolicyUpdates(agent.AgentID, history)
		if err != nil {
			return "", err
		}
		return renderDurableAgentPolicy(*agent, updates), nil
	case "bootstrap_show":
		if strings.TrimSpace(in.AgentID) == "" {
			return "", fmt.Errorf("durable_agent agent_id is required for bootstrap_show")
		}
		agent, err := r.resolveDurableAgent(in.AgentID)
		if err != nil {
			return "", err
		}
		history := in.History
		if history <= 0 {
			history = 5
		}
		updates, err := r.store.DurableAgentBootstrapUpdates(agent.AgentID, history)
		if err != nil {
			return "", err
		}
		return renderDurableAgentBootstrapShow(*agent, updates, core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)), nil
	case "policy_apply":
		return r.applyDurableAgentPolicy(in)
	case "bootstrap_update":
		return r.updateDurableAgentBootstrap(in, p, key)
	case "enrollment_show":
		agentID := strings.TrimSpace(in.AgentID)
		if agentID == "" {
			return "", fmt.Errorf("durable_agent agent_id is required for enrollment_show")
		}
		agent, err := r.resolveDurableAgent(agentID)
		if err != nil {
			return "", err
		}
		enrollment, err := r.store.DurableAgentRemoteEnrollment(agent.AgentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("durable agent %q has no remote enrollment; use policy_apply for ordinary autonomy/privacy/shared-context changes", agent.AgentID)
			}
			return "", err
		}
		return renderDurableAgentEnrollment(*enrollment), nil
	case "enrollment_update":
		return r.updateDurableAgentEnrollment(in)
	case "wizard_start":
		return r.startDurableAgentWizard(in, key)
	case "wizard_answer":
		return r.answerDurableAgentWizard(in)
	case "wizard_show":
		return r.showDurableAgentWizard(in)
	case "wizard_finalize":
		return r.finalizeDurableAgentWizard(in, key)
	case "wizard_cancel":
		return r.cancelDurableAgentWizard(in)
	case "archetype_list":
		return r.listDurableAgentArchetypes()
	case "archetype_show":
		return r.showDurableAgentArchetype(in)
	case "access_show":
		return r.showDurableAgentAccess(in)
	case "access_grant":
		return r.grantDurableAgentAccess(in)
	case "access_revoke":
		return r.revokeDurableAgentAccess(in)
	case "conversation_show":
		return r.showDurableAgentConversation(in)
	case "conversation_send":
		return r.sendDurableAgentConversation(in)
	case "delegation_request":
		return r.requestDurableAgentDelegation(in, p, key)
	case "delegation_report":
		return r.reportDurableAgentDelegation(in, key)
	case "memory_review":
		return r.reviewDurableAgentMemoryDelegation(in, scope)
	case "memory_delegate":
		return r.delegateDurableAgentMemory(ctx, in, p, key, scope)
	case "profile_show":
		return r.showDurableAgentProfile(in)
	case "profile_apply":
		return r.applyDurableAgentProfile(in)
	case "artifact_put":
		return r.putDurableAgentArtifact(in)
	case "artifact_list":
		return r.listDurableAgentArtifacts(in)
	case "artifact_show":
		return r.showDurableAgentArtifact(in)
	case "snapshot_create":
		return r.createDurableAgentSnapshot(in)
	case "snapshot_list":
		return r.listDurableAgentSnapshots(in)
	case "snapshot_restore":
		return r.restoreDurableAgentSnapshot(ctx, in, p, key)
	default:
		return "", fmt.Errorf("durable_agent action must be one of list|create|create_from_archetype|activate|connection_test|policy_show|bootstrap_show|policy_apply|bootstrap_update|enrollment_show|enrollment_update|wizard_start|wizard_answer|wizard_show|wizard_finalize|wizard_cancel|archetype_list|archetype_show|access_show|access_grant|access_revoke|conversation_show|conversation_send|delegation_request|delegation_report|memory_review|memory_delegate|profile_show|profile_apply|artifact_put|artifact_list|artifact_show|snapshot_create|snapshot_list|snapshot_restore")
	}
}

func (r *Registry) applyDurableAgentPolicy(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for policy_apply")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	if in.ReviewEventID > 0 {
		event, err := r.store.ReviewEventByID(in.ReviewEventID)
		if err != nil {
			return "", err
		}
		if event.SourceScope.Kind != session.ScopeKindDurableAgent || !durableAgentReviewTargetsAgent(agent.AgentID, event.SourceScope) {
			return "", fmt.Errorf("review event %d does not belong to durable agent %s", in.ReviewEventID, agent.AgentID)
		}
	}

	patch := effectiveDurableAgentPolicyPatchFromInput(in)
	policy := agent.LivePolicy
	if err := applyDurableAgentPolicyPatch(&policy, patch); err != nil {
		return "", err
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" && in.ReviewEventID > 0 {
		reason = fmt.Sprintf("ratified from review_event=%d", in.ReviewEventID)
	}
	updated, update, err := r.store.ApplyDurableAgentLivePolicy(agent.AgentID, policy, in.ReviewEventID, reason)
	if err != nil {
		return "", err
	}
	if _, err := syncDurableAgentProfileFiles(*updated, r.store); err != nil {
		return "", err
	}
	return renderDurableAgentPolicyApply(*updated, update), nil
}

func (r *Registry) updateDurableAgentBootstrap(in durableAgentInput, p principal.Principal, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for bootstrap_update")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return "", fmt.Errorf("durable_agent reason is required for bootstrap_update")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	if in.ReviewEventID > 0 {
		event, err := r.store.ReviewEventByID(in.ReviewEventID)
		if err != nil {
			return "", err
		}
		if event.SourceScope.Kind != session.ScopeKindDurableAgent || !durableAgentReviewTargetsAgent(agent.AgentID, event.SourceScope) {
			return "", fmt.Errorf("review event %d does not belong to durable agent %s", in.ReviewEventID, agent.AgentID)
		}
	}
	next, updateKind, err := r.resolveDurableAgentBootstrapUpdate(*agent, in)
	if err != nil {
		return "", err
	}
	updated, update, err := r.store.ApplyDurableAgentBootstrap(agent.AgentID, next, in.ReviewEventID, p.TelegramUserID, string(p.Role), updateKind, reason)
	if err != nil {
		return "", err
	}
	_ = key
	return renderDurableAgentBootstrapApply(*updated, update), nil
}

func (r *Registry) resolveDurableAgentBootstrapUpdate(agent core.DurableAgent, in durableAgentInput) (core.NodeLLMBootstrap, string, error) {
	profile := strings.ToLower(strings.TrimSpace(in.BootstrapProfile))
	hasExplicit := in.BootstrapLLM != nil
	switch {
	case profile != "" && hasExplicit:
		return core.NodeLLMBootstrap{}, "", fmt.Errorf("durable_agent bootstrap_update accepts either bootstrap_profile or bootstrap_llm, not both")
	case profile == "" && !hasExplicit:
		return core.NodeLLMBootstrap{}, "", fmt.Errorf("durable_agent bootstrap_update requires bootstrap_profile=inherit_parent or bootstrap_llm")
	case profile != "" && profile != "inherit_parent":
		return core.NodeLLMBootstrap{}, "", fmt.Errorf("durable_agent bootstrap_profile must be inherit_parent for bootstrap_update")
	}
	if hasExplicit {
		bootstrap := core.NormalizeNodeLLMBootstrap(*in.BootstrapLLM)
		if err := core.ValidateNodeLLMBootstrap(bootstrap); err != nil {
			return core.NodeLLMBootstrap{}, "", fmt.Errorf("durable_agent bootstrap_llm: %w", err)
		}
		return bootstrap, "explicit", nil
	}
	inherited := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	if !inherited.Configured() {
		return core.NodeLLMBootstrap{}, "", fmt.Errorf("durable_agent bootstrap_update inherit_parent requires a configured parent bootstrap")
	}
	_ = agent
	return inherited, "inherit_parent", nil
}

func (r *Registry) createDurableAgent(in durableAgentInput, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for create")
	}
	channelKind := normalizeDurableAgentChannelKind(in.ChannelKind)
	if channelKind == "" {
		return "", fmt.Errorf("durable_agent channel_kind is required for create")
	}

	existing, err := r.store.DurableAgent(agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	var agent core.DurableAgent
	if existing != nil {
		if strings.TrimSpace(existing.Status) != "" && strings.TrimSpace(existing.Status) != "draft" {
			return "", fmt.Errorf("durable agent %q already exists with status %q; use policy_apply or activate instead of create", existing.AgentID, existing.Status)
		}
		agent = *existing
	}
	agent.AgentID = agentID
	agent.ChannelKind = normalizeDurableAgentChannelKind(firstNonEmpty(channelKind, agent.ChannelKind))
	agent.ParentScopeKind = firstNonEmpty(agent.ParentScopeKind, string(key.Scope.Kind))
	agent.ParentScopeID = firstNonEmpty(agent.ParentScopeID, key.Scope.ID)
	if in.ReviewTargetChatID > 0 {
		agent.ReviewTargetChatID = in.ReviewTargetChatID
	} else if agent.ReviewTargetChatID == 0 && key.ChatID != 0 {
		agent.ReviewTargetChatID = key.ChatID
	}
	if strings.TrimSpace(in.WakeupMode) != "" {
		agent.WakeupMode = strings.TrimSpace(in.WakeupMode)
	} else if strings.TrimSpace(agent.WakeupMode) == "" && agent.ChannelKind == "external_channel" {
		agent.WakeupMode = "poll"
	}
	if strings.TrimSpace(in.NetworkPolicy) != "" {
		agent.NetworkPolicy = strings.TrimSpace(in.NetworkPolicy)
	}
	if len(in.SecretScopes) > 0 {
		agent.SecretScopes = append([]string(nil), in.SecretScopes...)
	}
	if in.TelegramUserID != 0 || len(in.TelegramUserIDs) > 0 {
		agent.AllowedTelegramUserIDs = core.NormalizeDurableAgentAllowedTelegramUserIDs(append(append([]int64(nil), in.TelegramUserID), in.TelegramUserIDs...))
	}
	patch := effectiveDurableAgentPolicyPatchFromInput(in)
	policy := agent.LivePolicy
	if strings.TrimSpace(policy.Charter) == "" &&
		len(policy.CapabilityEnvelope) == 0 &&
		strings.TrimSpace(policy.OutboundMode) == "" &&
		strings.TrimSpace(policy.DriftPolicy) == "" &&
		strings.TrimSpace(policy.PublicSurfaceMode) == "" &&
		strings.TrimSpace(policy.SharedInferenceReuse) == "" &&
		strings.TrimSpace(policy.SharedInferenceReuseScope) == "" {
		policy = defaultDurableAgentLivePolicy(agent.ChannelKind, patch.Charter)
	}
	if err := applyDurableAgentPolicyPatch(&policy, patch); err != nil {
		return "", err
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(policy)

	channelConfig, err := mergeDurableAgentChannelConfig(agent.ChannelConfig, in.ChannelConfig)
	if err != nil {
		return "", err
	}
	agent.ChannelConfig = channelConfig
	r.inheritDurableAgentBootstrapIfMissing(&agent)
	agent.Status = "draft"

	if err := r.store.UpsertDurableAgent(agent); err != nil {
		return "", err
	}
	updated, err := r.store.DurableAgent(agent.AgentID)
	if err != nil {
		return "", err
	}
	if _, err := syncDurableAgentProfileFiles(*updated, r.store); err != nil {
		return "", err
	}
	return renderDurableAgentLifecycle("create", *updated), nil
}

func (r *Registry) activateDurableAgent(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for activate")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	if err := validateDurableAgentActivation(*agent); err != nil {
		return "", err
	}
	r.inheritDurableAgentBootstrapIfMissing(agent)
	agent.Status = "active"
	if err := r.store.UpsertDurableAgent(*agent); err != nil {
		return "", err
	}
	if _, err := syncDurableAgentProfileFiles(*agent, r.store); err != nil {
		return "", err
	}
	return renderDurableAgentLifecycle("activate", *agent), nil
}

func (r *Registry) startDurableAgentWizard(in durableAgentInput, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_start")
	}
	channelKind := strings.TrimSpace(in.ChannelKind)
	if channelKind == "" {
		channelKind = "external_channel"
	}
	channelKind = normalizeDurableAgentChannelKind(channelKind)
	if channelKind != "external_channel" {
		return "", fmt.Errorf("durable_agent wizard_start currently supports channel_kind=external_channel")
	}

	createIn := in
	createIn.Action = "create"
	createIn.ChannelKind = channelKind
	if strings.TrimSpace(createIn.WakeupMode) == "" {
		createIn.WakeupMode = "poll"
	}
	if _, err := r.createDurableAgent(createIn, key); err != nil {
		return "", err
	}

	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	now := time.Now().UTC()
	wizard := seedDurableAgentWizardFromAgent(*agent, inheritedBootstrap)
	if continuity.SetupWizard != nil {
		wizard = *continuity.SetupWizard
		if wizard.StartedAt.IsZero() {
			wizard.StartedAt = now
		}
	}
	wizard.SchemaVersion = 1
	wizard.ChannelKind = channelKind
	wizard.UpdatedAt = now
	if wizard.StartedAt.IsZero() {
		wizard.StartedAt = now
	}
	missing := durableAgentWizardMissingAnswers(*agent, wizard, inheritedBootstrap)
	wizard.Missing = missing
	wizard.CurrentStep = firstWizardStep(missing)
	if len(missing) == 0 {
		wizard.Status = "ready"
	} else {
		wizard.Status = "in_progress"
	}
	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardShow(*agent, wizard, inheritedBootstrap), nil
}

func (r *Registry) answerDurableAgentWizard(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_answer")
	}
	if in.WizardAnswers == nil {
		return "", fmt.Errorf("durable_agent wizard_answer requires wizard_answers")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.SetupWizard == nil {
		return "", fmt.Errorf("durable agent %q has no active setup wizard; use wizard_start first", agent.AgentID)
	}

	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	wizard := *continuity.SetupWizard
	wizard.SchemaVersion = 1
	wizard.ChannelKind = firstNonEmpty(
		strings.TrimSpace(wizard.ChannelKind),
		normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind)),
		"external_channel",
	)
	wizard.Answers = mergeDurableAgentWizardAnswers(wizard.Answers, *in.WizardAnswers)
	wizard.UpdatedAt = time.Now().UTC()
	if wizard.StartedAt.IsZero() {
		wizard.StartedAt = wizard.UpdatedAt
	}
	missing := durableAgentWizardMissingAnswers(*agent, wizard, inheritedBootstrap)
	wizard.Missing = missing
	wizard.CurrentStep = firstWizardStep(missing)
	if len(missing) == 0 {
		wizard.Status = "ready"
	} else {
		wizard.Status = "in_progress"
	}

	updatedAgent, err := applyDurableWizardAnswersToAgent(*agent, wizard.Answers, inheritedBootstrap)
	if err != nil {
		return "", err
	}
	if err := r.store.UpsertDurableAgent(updatedAgent); err != nil {
		return "", err
	}
	agent = &updatedAgent

	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardShow(*agent, wizard, inheritedBootstrap), nil
}

func (r *Registry) showDurableAgentWizard(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_show")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	_, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.SetupWizard == nil {
		return "", fmt.Errorf("durable agent %q has no active setup wizard; use wizard_start first", agent.AgentID)
	}
	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	wizard := *continuity.SetupWizard
	wizard.Missing = durableAgentWizardMissingAnswers(*agent, wizard, inheritedBootstrap)
	wizard.CurrentStep = firstWizardStep(wizard.Missing)
	if wizard.Status == "" {
		if len(wizard.Missing) == 0 {
			wizard.Status = "ready"
		} else {
			wizard.Status = "in_progress"
		}
	}
	return renderDurableAgentWizardShow(*agent, wizard, inheritedBootstrap), nil
}

func (r *Registry) finalizeDurableAgentWizard(in durableAgentInput, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_finalize")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.SetupWizard == nil {
		return "", fmt.Errorf("durable agent %q has no active setup wizard; use wizard_start first", agent.AgentID)
	}
	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	wizard := *continuity.SetupWizard
	wizard.Missing = durableAgentWizardMissingAnswers(*agent, wizard, inheritedBootstrap)
	if len(wizard.Missing) > 0 {
		return "", fmt.Errorf("missing wizard answers: %s", strings.Join(wizard.Missing, ", "))
	}
	wizard.CurrentStep = ""
	wizard.Status = "finalized"
	wizard.UpdatedAt = time.Now().UTC()

	updatedAgent, err := applyDurableWizardAnswersToAgent(*agent, wizard.Answers, inheritedBootstrap)
	if err != nil {
		return "", err
	}
	if updatedAgent.ReviewTargetChatID == 0 && key.ChatID != 0 {
		updatedAgent.ReviewTargetChatID = key.ChatID
	}
	if strings.TrimSpace(updatedAgent.Status) == "" {
		updatedAgent.Status = "draft"
	}
	if strings.TrimSpace(updatedAgent.Status) != "active" {
		updatedAgent.Status = "draft"
	}
	r.inheritDurableAgentBootstrapIfMissing(&updatedAgent)
	if err := r.store.UpsertDurableAgent(updatedAgent); err != nil {
		return "", err
	}
	if _, err := syncDurableAgentProfileFiles(updatedAgent, r.store); err != nil {
		return "", err
	}

	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardFinalize(updatedAgent, wizard, inheritedBootstrap), nil
}

func (r *Registry) cancelDurableAgentWizard(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_cancel")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.SetupWizard == nil {
		return "", fmt.Errorf("durable agent %q has no active setup wizard; use wizard_start first", agent.AgentID)
	}
	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	wizard := *continuity.SetupWizard
	wizard.Status = "cancelled"
	wizard.CurrentStep = ""
	wizard.Missing = nil
	wizard.UpdatedAt = time.Now().UTC()
	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardShow(*agent, wizard, inheritedBootstrap), nil
}

func (r *Registry) loadDurableAgentContinuity(agentID string) (*core.DurableAgentState, core.DurableAgentContinuityState, error) {
	state, err := r.store.DurableAgentState(agentID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, core.DurableAgentContinuityState{}, err
		}
		state = &core.DurableAgentState{AgentID: strings.TrimSpace(agentID)}
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return nil, core.DurableAgentContinuityState{}, err
	}
	return state, continuity, nil
}

func (r *Registry) saveDurableAgentContinuity(state *core.DurableAgentState, continuity core.DurableAgentContinuityState) error {
	if state == nil {
		return fmt.Errorf("durable agent continuity state is nil")
	}
	raw, err := continuity.Marshal()
	if err != nil {
		return err
	}
	state.StateJSON = raw
	return r.store.SaveDurableAgentState(*state)
}

func (r *Registry) testDurableAgentConnection(ctx context.Context, in durableAgentInput) (string, error) {
	_ = ctx
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for connection_test")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	switch normalizeDurableAgentChannelKind(agent.ChannelKind) {
	case "external_channel":
		if agent.ChannelConfig.ExternalConfig() == nil {
			return "", fmt.Errorf("durable agent %q has no external channel_config", agent.AgentID)
		}
		return fmt.Sprintf("action: durable-agent connection test\nagent_id: %s\nchannel_kind: %s\nstatus: configuration_only\nnext: grant a concrete channel/tool capability before live adapter access can be tested\n", agent.AgentID, agent.ChannelKind), nil
	default:
		return "", fmt.Errorf("durable agent %q channel %q does not support connection_test yet", agent.AgentID, agent.ChannelKind)
	}
}

func (r *Registry) updateDurableAgentEnrollment(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for enrollment_update")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	enrollment, err := r.store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("durable agent %q has no remote enrollment; use policy_apply for ordinary autonomy/privacy/shared-context changes", agent.AgentID)
		}
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(in.Operation)) {
	case "revoke":
		enrollment.Status = "revoked"
		enrollment.RevokedAt = time.Now().UTC()
	case "reactivate":
		if enrollment.Status == "decommissioned" {
			return "", fmt.Errorf("durable agent enrollment %s is decommissioned and cannot be reactivated", agentID)
		}
		enrollment.Status = "active"
		enrollment.RevokedAt = time.Time{}
	case "decommission":
		enrollment.Status = "decommissioned"
		enrollment.RevokedAt = time.Now().UTC()
	case "rotate_secret":
		secret := strings.TrimSpace(in.Secret)
		if secret == "" {
			return "", fmt.Errorf("durable_agent enrollment_update secret is required when operation=rotate_secret")
		}
		agent.ControlPlaneSecret = secret
		if err := r.store.UpsertDurableAgent(*agent); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("durable_agent enrollment_update operation must be one of revoke|reactivate|decommission|rotate_secret")
	}
	if err := r.store.UpsertDurableAgentRemoteEnrollment(*enrollment); err != nil {
		return "", err
	}
	return renderDurableAgentEnrollment(*enrollment), nil
}

func (r *Registry) showDurableAgentAccess(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for access_show")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	return renderDurableAgentAccess("show", *agent, nil, false), nil
}

func (r *Registry) grantDurableAgentAccess(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for access_grant")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	requested, err := durableAgentAccessUserIDs(in)
	if err != nil {
		return "", err
	}
	combined := append(append([]int64(nil), agent.AllowedTelegramUserIDs...), requested...)
	next := core.NormalizeDurableAgentAllowedTelegramUserIDs(combined)
	changed := !equalInt64Slices(agent.AllowedTelegramUserIDs, next)
	agent.AllowedTelegramUserIDs = next
	if changed {
		if err := r.store.UpsertDurableAgent(*agent); err != nil {
			return "", err
		}
	}
	return renderDurableAgentAccess("grant", *agent, requested, changed), nil
}

func (r *Registry) revokeDurableAgentAccess(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for access_revoke")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	requested, err := durableAgentAccessUserIDs(in)
	if err != nil {
		return "", err
	}
	remove := make(map[int64]struct{}, len(requested))
	for _, userID := range requested {
		remove[userID] = struct{}{}
	}
	next := make([]int64, 0, len(agent.AllowedTelegramUserIDs))
	for _, userID := range core.NormalizeDurableAgentAllowedTelegramUserIDs(agent.AllowedTelegramUserIDs) {
		if _, drop := remove[userID]; drop {
			continue
		}
		next = append(next, userID)
	}
	next = core.NormalizeDurableAgentAllowedTelegramUserIDs(next)
	changed := !equalInt64Slices(agent.AllowedTelegramUserIDs, next)
	agent.AllowedTelegramUserIDs = next
	if changed {
		if err := r.store.UpsertDurableAgent(*agent); err != nil {
			return "", err
		}
	}
	return renderDurableAgentAccess("revoke", *agent, requested, changed), nil
}

func (r *Registry) showDurableAgentConversation(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for conversation_show")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	_, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	return renderDurableAgentConversation("show", *agent, continuity, in.History), nil
}

func (r *Registry) sendDurableAgentConversation(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for conversation_send")
	}
	message := strings.TrimSpace(in.Message)
	if message == "" {
		return "", fmt.Errorf("durable_agent message is required for conversation_send")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	continuity = continuity.WithConversationMessage("parent", message, time.Now().UTC())
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentConversation("send", *agent, continuity, in.History), nil
}

func (r *Registry) requestDurableAgentDelegation(in durableAgentInput, actor principal.Principal, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for delegation_request")
	}
	if in.DelegationRequest == nil {
		return "", fmt.Errorf("durable_agent delegation_request requires delegation_request payload")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	payload := in.DelegationRequest
	reviewTarget := durableAgentReviewTargetChatID(*agent, payload.ReviewTargetChatID, in.ReviewTargetChatID)
	if reviewTarget == 0 {
		return "", fmt.Errorf("durable_agent delegation_request requires review_target_chat_id on the agent or payload")
	}
	agent.ReviewTargetChatID = reviewTarget

	requestID := strings.TrimSpace(payload.RequestID)
	if requestID == "" {
		requestID = generatedOperationID("cap")
	}
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(payload.Kind))
	if strings.TrimSpace(payload.Kind) != "" && kind == "" {
		return "", fmt.Errorf("durable_agent delegation_request kind is not supported")
	}
	if kind == "" {
		kind = session.CapabilityKindGenericDelegation
	}
	target := strings.TrimSpace(payload.TargetResource)
	if target == "" {
		return "", fmt.Errorf("durable_agent delegation_request requires target_resource")
	}
	purpose := strings.TrimSpace(payload.Purpose)
	if purpose == "" {
		return "", fmt.Errorf("durable_agent delegation_request requires purpose")
	}
	contract, err := normalizeCapabilityJSONBlob(payload.Contract, "contract")
	if err != nil {
		return "", err
	}
	contract, err = mergeCapabilityUpdatePlanIntoContract(contract, capabilityUpdatePlanFromDurableDelegation(agent.AgentID, *payload))
	if err != nil {
		return "", err
	}
	constraints, err := normalizeCapabilityJSONBlob(payload.Constraints, "constraints")
	if err != nil {
		return "", err
	}
	requestedBy := canonicalDurableAgentPrincipalIfKnown(r.store, firstNonEmpty(payload.RequestedBy, core.DurableAgentPrincipal(agent.AgentID)))
	requestedFor := canonicalDurableAgentPrincipalIfKnown(r.store, firstNonEmpty(payload.RequestedFor, core.DurableAgentPrincipal(agent.AgentID)))
	parentPrincipal := firstNonEmpty(payload.ParentPrincipal, durableAgentDefaultParentPrincipal(*agent))
	adminPrincipal := firstNonEmpty(payload.AdminPrincipal, toolAuthorityPrincipalDisplay(actor))
	record, err := r.store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:       requestID,
		RequestedBy:     requestedBy,
		RequestedFor:    requestedFor,
		ParentPrincipal: parentPrincipal,
		AdminPrincipal:  adminPrincipal,
		Kind:            kind,
		TargetResource:  target,
		Purpose:         purpose,
		RiskClass:       strings.TrimSpace(payload.RiskClass),
		Contract:        contract,
		Constraints:     constraints,
		ReviewStatus:    session.CapabilityReviewStatusProposed,
	})
	if err != nil {
		return "", err
	}
	if err := r.appendCapabilityEvent(key, core.ExecutionEventCapabilityRequestCreated, string(record.ReviewStatus), map[string]any{
		"request_id":        record.RequestID,
		"kind":              string(record.Kind),
		"target_resource":   record.TargetResource,
		"review_status":     string(record.ReviewStatus),
		"requested_by":      record.RequestedBy,
		"requested_for":     record.RequestedFor,
		"parent_principal":  record.ParentPrincipal,
		"requester_role":    strings.TrimSpace(string(actor.Role)),
		"requester_user_id": actor.TelegramUserID,
		"request_via":       "durable_agent.delegation_request",
		"agent_id":          agent.AgentID,
		"channel_kind":      agent.ChannelKind,
	}); err != nil {
		return "", err
	}
	reviewEventID, err := durableagent.NewRuntime(r.store).QueueReviewArtifact(*agent, durableAgentDelegationRequestArtifact(*agent, record, *payload))
	if err != nil {
		return "", err
	}
	agreement, err := r.store.UpsertDurableChildAgreement(session.DurableChildAgreement{
		AgreementID:         "agreement-" + record.RequestID,
		AgentID:             agent.AgentID,
		ParentPrincipal:     parentPrincipal,
		ChildPrincipal:      requestedFor,
		SourceSurface:       "durable_agent.delegation_request",
		SourceRequestID:     record.RequestID,
		SourceReviewEventID: reviewEventID,
		Summary:             firstNonEmpty(strings.TrimSpace(payload.Summary), purpose),
		BoundedEffect:       durableAgentDelegationAgreementBoundedEffect(record, *payload),
		Status:              session.DurableChildAgreementStatusProposed,
		ArtifactRefs:        durableAgentDelegationAgreementArtifactRefs(reviewEventID, payload.ArtifactRefs),
	})
	if err != nil {
		return "", err
	}
	return renderDurableAgentDelegationRequest(*agent, record, reviewEventID, agreement), nil
}

func (r *Registry) reportDurableAgentDelegation(in durableAgentInput, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for delegation_report")
	}
	if in.DelegationReport == nil {
		return "", fmt.Errorf("durable_agent delegation_report requires delegation_report payload")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	payload := in.DelegationReport
	reviewTarget := durableAgentReviewTargetChatID(*agent, payload.ReviewTargetChatID, in.ReviewTargetChatID)
	if reviewTarget == 0 {
		return "", fmt.Errorf("durable_agent delegation_report requires review_target_chat_id on the agent or payload")
	}
	agent.ReviewTargetChatID = reviewTarget
	if strings.TrimSpace(payload.Summary) == "" &&
		strings.TrimSpace(payload.Outcome) == "" &&
		len(payload.LocalActions) == 0 &&
		len(payload.Questions) == 0 &&
		len(payload.RiskFlags) == 0 {
		return "", fmt.Errorf("durable_agent delegation_report requires summary, outcome, local_actions, questions, or risk_flags")
	}
	if requestID := strings.TrimSpace(payload.RequestID); requestID != "" {
		if _, ok, err := r.store.CapabilityRequest(requestID); err != nil {
			return "", err
		} else if !ok {
			return "", fmt.Errorf("capability request %q not found", requestID)
		}
	}
	if grantID := strings.TrimSpace(payload.GrantID); grantID != "" {
		if _, ok, err := r.store.CapabilityGrant(grantID); err != nil {
			return "", err
		} else if !ok {
			return "", fmt.Errorf("capability grant %q not found", grantID)
		}
	}
	reviewEventID, err := durableagent.NewRuntime(r.store).QueueReviewArtifact(*agent, durableAgentDelegationReportArtifact(*agent, *payload))
	if err != nil {
		return "", err
	}
	if err := r.appendCapabilityEvent(key, "capability.reported", strings.TrimSpace(payload.Status), map[string]any{
		"agent_id":        agent.AgentID,
		"request_id":      strings.TrimSpace(payload.RequestID),
		"grant_id":        strings.TrimSpace(payload.GrantID),
		"status":          strings.TrimSpace(payload.Status),
		"outcome":         strings.TrimSpace(payload.Outcome),
		"review_event_id": reviewEventID,
		"report_via":      "durable_agent.delegation_report",
	}); err != nil {
		return "", err
	}
	return renderDurableAgentDelegationReport(*agent, *payload, reviewEventID), nil
}

type durableMemoryCandidate struct {
	ID          string
	SourceStore string
	TargetStore string
	Content     string
	Score       int
	Reason      string
}

func (r *Registry) reviewDurableAgentMemoryDelegation(in durableAgentInput, scope sandbox.Scope) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for memory_review")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	parentRoot, _, err := resolveMemoryRoot(scope, "shared")
	if err != nil {
		return "", err
	}
	limit := 8
	if in.MemoryDelegation != nil && in.MemoryDelegation.Limit > 0 {
		limit = in.MemoryDelegation.Limit
	}
	if limit > 20 {
		limit = 20
	}
	candidates, err := durableMemoryCandidatesForAgent(parentRoot, *agent, limit)
	if err != nil {
		return "", err
	}
	return renderDurableAgentMemoryReview(*agent, candidates), nil
}

func (r *Registry) delegateDurableAgentMemory(
	ctx context.Context,
	in durableAgentInput,
	p principal.Principal,
	key session.SessionKey,
	scope sandbox.Scope,
) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for memory_delegate")
	}
	if in.MemoryDelegation == nil {
		return "", fmt.Errorf("durable_agent memory_delegate requires memory_delegation payload")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	parentRoot, _, err := resolveMemoryRoot(scope, "shared")
	if err != nil {
		return "", err
	}
	candidates, err := durableMemoryCandidatesForAgent(parentRoot, *agent, 200)
	if err != nil {
		return "", err
	}
	entries, err := buildDurableMemoryDelegationEntries(*in.MemoryDelegation, candidates)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("durable_agent memory_delegate requires at least one candidate_id or entry")
	}
	if r.durableMemoryDelegationApprover == nil {
		return "", fmt.Errorf("durable_agent memory_delegate requires an interactive admin approval channel")
	}
	approval, err := r.durableMemoryDelegationApprover.ConfirmDurableMemoryDelegation(ctx, DurableMemoryDelegationApprovalRequest{
		Principal:  p,
		SessionKey: key,
		Agent:      *agent,
		Reason:     strings.TrimSpace(in.MemoryDelegation.Reason),
		Entries:    entries,
	})
	if err != nil {
		return "", err
	}
	if !approval.Approved {
		return renderDurableAgentMemoryDelegate(*agent, entries, approval, 0), nil
	}

	childMemoryRoot, err := durableAgentMemoryRoot(*agent, r.store)
	if err != nil {
		return "", err
	}
	applied := 0
	for _, entry := range entries {
		if _, err := memstore.ApplyWrite(memstore.WriteRequest{
			Root:      childMemoryRoot,
			Store:     entry.TargetStore,
			Action:    "add",
			Content:   entry.Content,
			SourceTag: "delegated_from_parent",
		}); err != nil {
			return "", fmt.Errorf("delegate memory entry %q to %s: %w", entry.CandidateID, entry.TargetStore, err)
		}
		applied++
	}
	return renderDurableAgentMemoryDelegate(*agent, entries, approval, applied), nil
}

func buildDurableMemoryDelegationEntries(input durableAgentMemoryDelegationInput, candidates []durableMemoryCandidate) ([]DurableMemoryDelegationEntry, error) {
	candidateIndex := make(map[string]durableMemoryCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateIndex[strings.TrimSpace(candidate.ID)] = candidate
	}

	defaultTarget, err := normalizeDurableDelegationStore(strings.TrimSpace(input.TargetStore))
	if err != nil {
		return nil, err
	}

	out := make([]DurableMemoryDelegationEntry, 0, len(input.CandidateIDs)+len(input.Entries))
	appendCandidate := func(candidate durableMemoryCandidate, explicitTarget string) error {
		target := explicitTarget
		if target == "" {
			target = defaultTarget
		}
		if target == "" {
			target = strings.TrimSpace(candidate.TargetStore)
		}
		target, err = normalizeDurableDelegationStore(target)
		if err != nil {
			return err
		}
		out = append(out, DurableMemoryDelegationEntry{
			CandidateID: strings.TrimSpace(candidate.ID),
			SourceStore: strings.TrimSpace(candidate.SourceStore),
			TargetStore: target,
			Content:     strings.TrimSpace(candidate.Content),
		})
		return nil
	}

	for _, candidateID := range input.CandidateIDs {
		candidateID = strings.TrimSpace(candidateID)
		if candidateID == "" {
			continue
		}
		candidate, ok := candidateIndex[candidateID]
		if !ok {
			return nil, fmt.Errorf("memory candidate %q was not found; run memory_review again", candidateID)
		}
		if err := appendCandidate(candidate, ""); err != nil {
			return nil, err
		}
	}
	for _, entry := range input.Entries {
		if candidateID := strings.TrimSpace(entry.CandidateID); candidateID != "" {
			candidate, ok := candidateIndex[candidateID]
			if !ok {
				return nil, fmt.Errorf("memory candidate %q was not found; run memory_review again", candidateID)
			}
			if err := appendCandidate(candidate, strings.TrimSpace(entry.TargetStore)); err != nil {
				return nil, err
			}
			continue
		}
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}
		sourceStore := strings.TrimSpace(entry.SourceStore)
		if sourceStore == "" {
			sourceStore = "knowledge"
		}
		sourceStore, err = normalizeDurableDelegationStore(sourceStore)
		if err != nil {
			return nil, err
		}
		targetStore := strings.TrimSpace(entry.TargetStore)
		if targetStore == "" {
			targetStore = defaultTarget
		}
		if targetStore == "" {
			targetStore = sourceStore
		}
		targetStore, err = normalizeDurableDelegationStore(targetStore)
		if err != nil {
			return nil, err
		}
		out = append(out, DurableMemoryDelegationEntry{
			SourceStore: sourceStore,
			TargetStore: targetStore,
			Content:     content,
		})
	}
	if len(out) == 0 {
		return nil, nil
	}
	seen := map[string]struct{}{}
	deduped := make([]DurableMemoryDelegationEntry, 0, len(out))
	for _, entry := range out {
		key := strings.ToLower(strings.TrimSpace(entry.TargetStore)) + "|" + strings.TrimSpace(entry.Content)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, entry)
	}
	return deduped, nil
}

func durableAgentMemoryRoot(agent core.DurableAgent, store *session.SQLiteStore) (string, error) {
	_, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if memoryRoot == "" && store != nil {
		if dbPath := strings.TrimSpace(store.DBPath()); dbPath != "" {
			_, memoryRoot = durableagent.DefaultLocalRoots(dbPath, strings.TrimSpace(agent.AgentID))
		}
	}
	if strings.TrimSpace(memoryRoot) == "" {
		return "", fmt.Errorf("durable agent %q has no local memory root", strings.TrimSpace(agent.AgentID))
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		return "", fmt.Errorf("create durable agent memory root: %w", err)
	}
	return memoryRoot, nil
}

func durableMemoryCandidatesForAgent(parentRoot string, agent core.DurableAgent, limit int) ([]durableMemoryCandidate, error) {
	stores := []string{memstore.StoreKnowledge, memstore.StoreDecisions, memstore.StoreQuestions, memstore.StoreMemory}
	keywords := durableMemoryKeywords(agent)
	candidates := make([]durableMemoryCandidate, 0)
	for _, store := range stores {
		entries, err := loadDurableMemoryStoreEntries(parentRoot, store)
		if err != nil {
			return nil, err
		}
		for i, entry := range entries {
			id := fmt.Sprintf("%s:%d", store, i+1)
			score, reason := scoreDurableMemoryCandidate(entry, store, keywords)
			candidates = append(candidates, durableMemoryCandidate{
				ID:          id,
				SourceStore: store,
				TargetStore: store,
				Content:     entry,
				Score:       score,
				Reason:      reason,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].SourceStore != candidates[j].SourceStore {
			return candidates[i].SourceStore < candidates[j].SourceStore
		}
		return candidates[i].ID < candidates[j].ID
	})
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	if limit == 0 {
		return nil, nil
	}
	return candidates[:limit], nil
}

func loadDurableMemoryStoreEntries(root string, store string) ([]string, error) {
	path, normalizedStore, err := memstore.ResolveStorePath(root, store)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read parent memory store %s: %w", path, err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, nil
	}
	switch normalizedStore {
	case memstore.StoreMemory:
		return parseDurableMemoryParagraphs(text), nil
	default:
		return parseDurableMemoryBullets(text), nil
	}
}

func parseDurableMemoryParagraphs(raw string) []string {
	chunks := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n\n")
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		clean := strings.TrimSpace(chunk)
		if clean == "" || strings.HasPrefix(clean, "#") {
			continue
		}
		out = append(out, compactWhitespace(clean))
	}
	return out
}

func parseDurableMemoryBullets(raw string) []string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			line = strings.TrimSpace(line[2:])
		}
		if line == "" {
			continue
		}
		out = append(out, compactWhitespace(line))
	}
	return out
}

func durableMemoryKeywords(agent core.DurableAgent) []string {
	seed := strings.Join([]string{
		strings.TrimSpace(agent.AgentID),
		strings.TrimSpace(agent.ChannelKind),
		strings.TrimSpace(agent.LivePolicy.Charter),
		strings.Join(agent.LivePolicy.CapabilityEnvelope, " "),
	}, " ")
	parts := strings.FieldsFunc(strings.ToLower(seed), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(parts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 4 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func scoreDurableMemoryCandidate(content string, store string, keywords []string) (int, string) {
	score := 1
	matches := make([]string, 0, 4)
	lower := strings.ToLower(strings.TrimSpace(content))
	for _, keyword := range keywords {
		if keyword == "" || !strings.Contains(lower, keyword) {
			continue
		}
		score++
		if len(matches) < 3 {
			matches = append(matches, keyword)
		}
	}
	if len(matches) == 0 {
		return score, "general " + strings.TrimSpace(store) + " context"
	}
	return score, "matches child context: " + strings.Join(matches, ", ")
}

func normalizeDurableDelegationStore(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case memstore.StoreMemory:
		return memstore.StoreMemory, nil
	case memstore.StoreKnowledge:
		return memstore.StoreKnowledge, nil
	case memstore.StoreDecisions:
		return memstore.StoreDecisions, nil
	case memstore.StoreQuestions:
		return memstore.StoreQuestions, nil
	case memstore.StoreRhizome:
		return memstore.StoreRhizome, nil
	default:
		return "", fmt.Errorf("unsupported delegation store %q", value)
	}
}

func renderDurableAgentMemoryReview(agent core.DurableAgent, candidates []durableMemoryCandidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent memory review\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "candidate_count: %d\n", len(candidates))
	if len(candidates) == 0 {
		b.WriteString("candidates: -\n")
		b.WriteString("next: add parent memory context, then run memory_review again\n")
		return b.String()
	}
	b.WriteString("candidates:\n")
	for _, candidate := range candidates {
		fmt.Fprintf(
			&b,
			"- candidate_id=%s source_store=%s target_store=%s score=%d reason=%s text=%s\n",
			strings.TrimSpace(candidate.ID),
			strings.TrimSpace(candidate.SourceStore),
			strings.TrimSpace(candidate.TargetStore),
			candidate.Score,
			compactWhitespace(candidate.Reason),
			truncateCompact(candidate.Content, 180),
		)
	}
	b.WriteString("next: memory_delegate\n")
	return b.String()
}

func renderDurableAgentMemoryDelegate(agent core.DurableAgent, entries []DurableMemoryDelegationEntry, approval DurableMemoryDelegationApprovalDecision, applied int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent memory delegate\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "approved: %t\n", approval.Approved)
	fmt.Fprintf(&b, "timed_out: %t\n", approval.TimedOut)
	changed := approval.Approved && applied > 0
	fmt.Fprintf(&b, "changed: %t\n", changed)
	fmt.Fprintf(&b, "delegated_count: %d\n", applied)
	b.WriteString("entries:\n")
	for _, entry := range entries {
		fmt.Fprintf(
			&b,
			"- candidate_id=%s source_store=%s target_store=%s text=%s\n",
			firstNonEmpty(strings.TrimSpace(entry.CandidateID), "-"),
			firstNonEmpty(strings.TrimSpace(entry.SourceStore), "-"),
			firstNonEmpty(strings.TrimSpace(entry.TargetStore), "-"),
			truncateCompact(entry.Content, 180),
		)
	}
	if !approval.Approved {
		b.WriteString("next: update memory_delegation payload and request approval again\n")
	}
	return b.String()
}

func compactWhitespace(raw string) string {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func truncateCompact(raw string, limit int) string {
	clean := compactWhitespace(raw)
	if limit <= 0 || len(clean) <= limit {
		return clean
	}
	if limit <= 3 {
		return clean[:limit]
	}
	return clean[:limit-3] + "..."
}

func (r *Registry) showDurableAgentProfile(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for profile_show")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	memoryRoot, err := durableAgentMemoryRoot(*agent, r.store)
	if err != nil {
		return "", err
	}
	profileRoot := filepath.Join(memoryRoot, "profile")
	manifest := loadDurableAgentProfileManifest(profileRoot)
	return renderDurableAgentProfile("show", *agent, profileRoot, manifest, nil), nil
}

func (r *Registry) applyDurableAgentProfile(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for profile_apply")
	}
	if in.ProfileEdit == nil {
		return "", fmt.Errorf("durable_agent profile_apply requires profile_edit")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	reason := firstNonEmpty(strings.TrimSpace(in.ProfileEdit.Reason), strings.TrimSpace(in.Reason))
	sync, err := applyDurableAgentProfileEdit(*agent, r.store, in.ProfileEdit.TargetFile, in.ProfileEdit.Content, reason)
	if err != nil {
		return "", err
	}
	manifest := loadDurableAgentProfileManifest(sync.Root)
	return renderDurableAgentProfile("apply", *agent, sync.Root, manifest, sync.Written), nil
}

func (r *Registry) createDurableAgentSnapshot(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for snapshot_create")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	var state *core.DurableAgentState
	existingState, err := r.store.DurableAgentState(agent.AgentID)
	if err == nil {
		state = existingState
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	dbPath := strings.TrimSpace(r.store.DBPath())
	reason := strings.TrimSpace(in.Reason)
	if in.Snapshot != nil && strings.TrimSpace(in.Snapshot.Reason) != "" {
		reason = strings.TrimSpace(in.Snapshot.Reason)
	}
	manifest, err := durableagent.CreateSnapshot(*agent, state, dbPath, reason, time.Now().UTC())
	if err != nil {
		return "", err
	}
	return renderDurableAgentSnapshotCreate(*agent, *manifest), nil
}

func (r *Registry) listDurableAgentSnapshots(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for snapshot_list")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	limit := 10
	if in.Snapshot != nil && in.Snapshot.Limit > 0 {
		limit = in.Snapshot.Limit
	}
	if limit > 50 {
		limit = 50
	}
	records, err := durableagent.ListSnapshots(*agent, strings.TrimSpace(r.store.DBPath()), limit)
	if err != nil {
		return "", err
	}
	return renderDurableAgentSnapshotList(*agent, records), nil
}

func (r *Registry) restoreDurableAgentSnapshot(
	ctx context.Context,
	in durableAgentInput,
	p principal.Principal,
	key session.SessionKey,
) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for snapshot_restore")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	snapshotID := ""
	if in.Snapshot != nil {
		snapshotID = strings.TrimSpace(in.Snapshot.SnapshotID)
	}
	if snapshotID == "" {
		return "", fmt.Errorf("durable_agent snapshot.snapshot_id is required for snapshot_restore")
	}
	manifest, _, err := durableagent.LoadSnapshot(*agent, strings.TrimSpace(r.store.DBPath()), snapshotID)
	if err != nil {
		return "", err
	}
	if r.durableSnapshotRestoreApprover == nil {
		return "", fmt.Errorf("durable_agent snapshot_restore requires an interactive admin approval channel")
	}
	restoreReason := strings.TrimSpace(in.Reason)
	if in.Snapshot != nil && strings.TrimSpace(in.Snapshot.Reason) != "" {
		restoreReason = strings.TrimSpace(in.Snapshot.Reason)
	}
	approval, err := r.durableSnapshotRestoreApprover.ConfirmDurableSnapshotRestore(ctx, DurableSnapshotRestoreApprovalRequest{
		Principal:         p,
		SessionKey:        key,
		Agent:             *agent,
		SnapshotID:        strings.TrimSpace(manifest.SnapshotID),
		SnapshotReason:    firstNonEmpty(restoreReason, strings.TrimSpace(manifest.Reason)),
		SnapshotCreatedAt: manifest.CreatedAt.UTC(),
	})
	if err != nil {
		return "", err
	}
	if !approval.Approved {
		return renderDurableAgentSnapshotRestore(*agent, *manifest, approval, false), nil
	}
	restoredManifest, err := durableagent.RestoreSnapshot(*agent, strings.TrimSpace(r.store.DBPath()), snapshotID, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if err := r.store.UpsertDurableAgent(restoredManifest.Agent); err != nil {
		return "", err
	}
	if restoredManifest.State != nil {
		if err := r.store.SaveDurableAgentState(*restoredManifest.State); err != nil {
			return "", err
		}
	}
	return renderDurableAgentSnapshotRestore(*agent, *restoredManifest, approval, true), nil
}

func renderDurableAgentProfile(action string, agent core.DurableAgent, profileRoot string, manifest durableAgentProfileManifest, written []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent profile %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "profile_root: %s\n", strings.TrimSpace(profileRoot))
	fmt.Fprintf(&b, "policy_hash: %s\n", strings.TrimSpace(manifest.PolicyHash))
	fmt.Fprintf(&b, "manifest_updated_at: %s\n", strings.TrimSpace(manifest.UpdatedAt))
	if len(written) > 0 {
		fmt.Fprintf(&b, "written: %s\n", strings.Join(written, ","))
	}
	b.WriteString("files:\n")
	if len(manifest.Files) == 0 {
		b.WriteString("- none\n")
		return b.String()
	}
	for _, entry := range manifest.Files {
		fmt.Fprintf(&b, "- path=%s ownership=%s source=%s\n", entry.Path, entry.Ownership, entry.Source)
	}
	return b.String()
}

func renderDurableAgentSnapshotCreate(agent core.DurableAgent, manifest durableagent.SnapshotManifest) string {
	var b strings.Builder
	b.WriteString("action: durable-agent snapshot create\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "snapshot_id: %s\n", strings.TrimSpace(manifest.SnapshotID))
	if !manifest.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "created_at: %s\n", manifest.CreatedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "reason: %s\n", firstNonEmpty(strings.TrimSpace(manifest.Reason), "-"))
	if manifest.State != nil {
		b.WriteString("state_saved: true\n")
	} else {
		b.WriteString("state_saved: false\n")
	}
	b.WriteString("next: snapshot_list or snapshot_restore\n")
	return b.String()
}

func renderDurableAgentSnapshotList(agent core.DurableAgent, records []durableagent.SnapshotRecord) string {
	var b strings.Builder
	b.WriteString("action: durable-agent snapshot list\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "count: %d\n", len(records))
	if len(records) == 0 {
		b.WriteString("snapshots: -\n")
		b.WriteString("next: snapshot_create\n")
		return b.String()
	}
	b.WriteString("snapshots:\n")
	for _, record := range records {
		created := "-"
		if !record.CreatedAt.IsZero() {
			created = record.CreatedAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(
			&b,
			"- snapshot_id=%s created_at=%s reason=%s\n",
			strings.TrimSpace(record.SnapshotID),
			created,
			firstNonEmpty(strings.TrimSpace(record.Reason), "-"),
		)
	}
	b.WriteString("next: snapshot_restore\n")
	return b.String()
}

func renderDurableAgentSnapshotRestore(agent core.DurableAgent, manifest durableagent.SnapshotManifest, approval DurableSnapshotRestoreApprovalDecision, changed bool) string {
	var b strings.Builder
	b.WriteString("action: durable-agent snapshot restore\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "snapshot_id: %s\n", strings.TrimSpace(manifest.SnapshotID))
	if !manifest.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "snapshot_created_at: %s\n", manifest.CreatedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "approved: %t\n", approval.Approved)
	fmt.Fprintf(&b, "timed_out: %t\n", approval.TimedOut)
	fmt.Fprintf(&b, "changed: %t\n", changed)
	fmt.Fprintf(&b, "reason: %s\n", firstNonEmpty(strings.TrimSpace(manifest.Reason), "-"))
	return b.String()
}

func durableAgentAccessUserIDs(in durableAgentInput) ([]int64, error) {
	values := make([]int64, 0, len(in.TelegramUserIDs)+1)
	if in.TelegramUserID != 0 {
		values = append(values, in.TelegramUserID)
	}
	values = append(values, in.TelegramUserIDs...)
	values = core.NormalizeDurableAgentAllowedTelegramUserIDs(values)
	if len(values) == 0 {
		return nil, fmt.Errorf("durable_agent telegram_user_id or telegram_user_ids is required")
	}
	return values, nil
}

func equalInt64Slices(left []int64, right []int64) bool {
	left = core.NormalizeDurableAgentAllowedTelegramUserIDs(left)
	right = core.NormalizeDurableAgentAllowedTelegramUserIDs(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

var durableAgentWizardStepOrder = []string{
	"address",
	"adapter",
	"bootstrap_profile",
	"bootstrap_model",
	"autonomy",
	"surface_rules",
	"summarize_pdfs",
	"synthesis_cadence",
	"wakeup_mode",
	"poll_interval",
	"capabilities",
	"never_retain",
	"charter",
}

func seedDurableAgentWizardFromAgent(agent core.DurableAgent, inheritedBootstrap core.NodeLLMBootstrap) core.DurableAgentSetupWizardState {
	currentBootstrap := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	inheritedBootstrap = core.NormalizeNodeLLMBootstrap(inheritedBootstrap)
	bootstrapProfile := durableAgentWizardBootstrapProfile(currentBootstrap, inheritedBootstrap)
	bootstrapModel := ""
	if bootstrapProfile == "child_custom" {
		bootstrapModel = strings.TrimSpace(currentBootstrap.Model)
	}
	wizard := core.DurableAgentSetupWizardState{
		SchemaVersion: 1,
		ChannelKind:   strings.TrimSpace(agent.ChannelKind),
		Answers: core.DurableAgentSetupWizardAnswers{
			BootstrapProfile: bootstrapProfile,
			BootstrapModel:   bootstrapModel,
			Charter:          strings.TrimSpace(agent.LivePolicy.Charter),
			Autonomy:         durableAgentAutonomyFromPolicy(agent.LivePolicy),
			WakeupMode:       strings.TrimSpace(agent.WakeupMode),
			Capabilities:     append([]string(nil), agent.LivePolicy.CapabilityEnvelope...),
			DriftPolicy:      strings.TrimSpace(agent.LivePolicy.DriftPolicy),
		},
	}
	if external := agent.ChannelConfig.ExternalConfig(); external != nil {
		wizard.Answers.Address = strings.TrimSpace(external.Address)
		wizard.Answers.Account = strings.TrimSpace(external.Account)
		wizard.Answers.Adapter = strings.TrimSpace(external.Adapter)
		wizard.Answers.Query = strings.TrimSpace(external.Query)
		wizard.Answers.PollInterval = strings.TrimSpace(external.PollInterval)
		wizard.Answers.SurfaceRules = append([]string(nil), external.SurfaceRules...)
		value := external.SummarizePDFs
		wizard.Answers.SummarizePDFs = &value
		wizard.Answers.SynthesisCadence = strings.TrimSpace(external.SynthesisCadence)
		wizard.Answers.NeverRetain = append([]string(nil), external.NeverRetain...)
	}
	return wizard
}

func mergeDurableAgentWizardAnswers(current core.DurableAgentSetupWizardAnswers, patch durableAgentWizardAnswersInput) core.DurableAgentSetupWizardAnswers {
	current = core.NormalizeDurableAgentSetupWizardAnswers(current)
	previousProfile := strings.TrimSpace(current.BootstrapProfile)
	if strings.TrimSpace(patch.Address) != "" {
		current.Address = strings.TrimSpace(patch.Address)
	}
	if strings.TrimSpace(patch.Account) != "" {
		current.Account = strings.TrimSpace(patch.Account)
	}
	if strings.TrimSpace(patch.Adapter) != "" {
		current.Adapter = strings.TrimSpace(patch.Adapter)
	}
	if strings.TrimSpace(patch.Query) != "" {
		current.Query = strings.TrimSpace(patch.Query)
	}
	if strings.TrimSpace(patch.BootstrapProfile) != "" {
		current.BootstrapProfile = strings.TrimSpace(patch.BootstrapProfile)
		switch core.NormalizeDurableAgentSetupWizardAnswers(current).BootstrapProfile {
		case "inherit_parent":
			// Keep inherited model implicit when the parent bootstrap is selected.
			current.BootstrapModel = ""
		case "child_custom":
			if previousProfile != "child_custom" && strings.TrimSpace(patch.BootstrapModel) == "" {
				// Force an explicit child model decision when switching to child-custom mode.
				current.BootstrapModel = ""
			}
		}
	}
	if strings.TrimSpace(patch.BootstrapModel) != "" {
		current.BootstrapModel = strings.TrimSpace(patch.BootstrapModel)
	}
	if strings.TrimSpace(patch.Charter) != "" {
		current.Charter = strings.TrimSpace(patch.Charter)
	}
	if strings.TrimSpace(patch.Autonomy) != "" {
		current.Autonomy = strings.TrimSpace(patch.Autonomy)
	}
	if strings.TrimSpace(patch.WakeupMode) != "" {
		current.WakeupMode = strings.TrimSpace(patch.WakeupMode)
	}
	if strings.TrimSpace(patch.PollInterval) != "" {
		current.PollInterval = strings.TrimSpace(patch.PollInterval)
	}
	if patch.SurfaceRules != nil {
		current.SurfaceRules = normalizePolicyCapabilities(patch.SurfaceRules)
	}
	if patch.SummarizePDFs != nil {
		value := *patch.SummarizePDFs
		current.SummarizePDFs = &value
	}
	if strings.TrimSpace(patch.SynthesisCadence) != "" {
		current.SynthesisCadence = strings.TrimSpace(patch.SynthesisCadence)
	}
	if patch.Capabilities != nil {
		current.Capabilities = normalizePolicyCapabilities(patch.Capabilities)
	}
	if patch.NeverRetain != nil {
		current.NeverRetain = normalizePolicyCapabilities(patch.NeverRetain)
	}
	if strings.TrimSpace(patch.DriftPolicy) != "" {
		current.DriftPolicy = strings.TrimSpace(patch.DriftPolicy)
	}
	return core.NormalizeDurableAgentSetupWizardAnswers(current)
}

func applyDurableWizardAnswersToAgent(agent core.DurableAgent, answers core.DurableAgentSetupWizardAnswers, inheritedBootstrap core.NodeLLMBootstrap) (core.DurableAgent, error) {
	answers = core.NormalizeDurableAgentSetupWizardAnswers(answers)
	agent.ChannelKind = normalizeDurableAgentChannelKind("external_channel")
	wakeupMode := normalizeDurableChannelWakeupMode(answers.WakeupMode)
	if wakeupMode == "" && strings.TrimSpace(agent.WakeupMode) != "" {
		wakeupMode = normalizeDurableChannelWakeupMode(agent.WakeupMode)
	}
	if wakeupMode == "" {
		wakeupMode = "poll"
	}
	agent.WakeupMode = wakeupMode

	patch := effectiveDurableAgentPolicyPatch{
		Charter:     strings.TrimSpace(answers.Charter),
		Autonomy:    strings.TrimSpace(answers.Autonomy),
		DriftPolicy: strings.TrimSpace(answers.DriftPolicy),
	}
	if len(answers.Capabilities) > 0 {
		patch.Capabilities = append([]string(nil), answers.Capabilities...)
		patch.CapabilitiesSet = true
	}
	policy := agent.LivePolicy
	if strings.TrimSpace(policy.Charter) == "" &&
		len(policy.CapabilityEnvelope) == 0 &&
		strings.TrimSpace(policy.OutboundMode) == "" &&
		strings.TrimSpace(policy.DriftPolicy) == "" &&
		strings.TrimSpace(policy.PublicSurfaceMode) == "" &&
		strings.TrimSpace(policy.SharedInferenceReuse) == "" &&
		strings.TrimSpace(policy.SharedInferenceReuseScope) == "" {
		policy = defaultDurableAgentLivePolicy("external_channel", patch.Charter)
	}
	if err := applyDurableAgentPolicyPatch(&policy, patch); err != nil {
		return core.DurableAgent{}, err
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(policy)

	channelConfig := core.NormalizeDurableAgentChannelConfig(agent.ChannelConfig)
	external := channelConfig.ExternalConfig()
	if external == nil {
		external = &core.DurableAgentExternalChannelConfig{}
	} else {
		copied := *external
		external = &copied
	}
	if answers.Address != "" {
		external.Address = answers.Address
	}
	if answers.Account != "" {
		external.Account = answers.Account
	} else if strings.TrimSpace(external.Account) == "" && strings.TrimSpace(external.Address) != "" {
		external.Account = strings.TrimSpace(external.Address)
	}
	if answers.Adapter != "" {
		external.Adapter = answers.Adapter
	}
	if answers.Query != "" {
		external.Query = answers.Query
	}
	if answers.PollInterval != "" {
		external.PollInterval = answers.PollInterval
	}
	if answers.SurfaceRules != nil {
		external.SurfaceRules = append([]string(nil), answers.SurfaceRules...)
	}
	if answers.SummarizePDFs != nil {
		external.SummarizePDFs = *answers.SummarizePDFs
	}
	if answers.SynthesisCadence != "" {
		external.SynthesisCadence = answers.SynthesisCadence
	}
	if answers.NeverRetain != nil {
		external.NeverRetain = append([]string(nil), answers.NeverRetain...)
	}
	channelConfig.External = external
	channelConfig.Email = nil
	agent.ChannelConfig = core.NormalizeDurableAgentChannelConfig(channelConfig)

	bootstrap, err := durableAgentBootstrapFromWizardAnswers(agent.BootstrapLLM, answers, inheritedBootstrap)
	if err != nil {
		return core.DurableAgent{}, err
	}
	agent.BootstrapLLM = core.NormalizeNodeLLMBootstrap(bootstrap)

	if strings.TrimSpace(agent.Status) == "" {
		agent.Status = "draft"
	}
	return agent, nil
}

func durableAgentWizardMissingAnswers(agent core.DurableAgent, wizard core.DurableAgentSetupWizardState, inheritedBootstrap core.NodeLLMBootstrap) []string {
	answers := core.NormalizeDurableAgentSetupWizardAnswers(wizard.Answers)
	effectiveBootstrap := durableAgentWizardEffectiveBootstrapForAnswers(agent, answers, inheritedBootstrap)
	missing := make([]string, 0, len(durableAgentWizardStepOrder))
	if strings.TrimSpace(answers.Address) == "" {
		missing = append(missing, "address")
	}
	if strings.TrimSpace(answers.Adapter) == "" {
		missing = append(missing, "adapter")
	}
	if strings.TrimSpace(answers.BootstrapProfile) == "" {
		missing = append(missing, "bootstrap_profile")
	} else if strings.TrimSpace(answers.BootstrapProfile) == "child_custom" &&
		strings.TrimSpace(effectiveBootstrap.Backend) == "native" &&
		strings.TrimSpace(answers.BootstrapModel) == "" {
		missing = append(missing, "bootstrap_model")
	}
	if strings.TrimSpace(answers.Autonomy) == "" {
		missing = append(missing, "autonomy")
	}
	if len(answers.SurfaceRules) == 0 {
		missing = append(missing, "surface_rules")
	}
	if answers.SummarizePDFs == nil {
		missing = append(missing, "summarize_pdfs")
	}
	if strings.TrimSpace(answers.SynthesisCadence) == "" {
		missing = append(missing, "synthesis_cadence")
	}
	mode := normalizeDurableChannelWakeupMode(answers.WakeupMode)
	if mode == "" {
		missing = append(missing, "wakeup_mode")
	} else if durableChannelWakeupModeIncludesPoll(mode) && strings.TrimSpace(answers.PollInterval) == "" {
		missing = append(missing, "poll_interval")
	}
	if len(answers.Capabilities) == 0 {
		missing = append(missing, "capabilities")
	}
	if len(answers.NeverRetain) == 0 {
		missing = append(missing, "never_retain")
	}
	if strings.TrimSpace(answers.Charter) == "" {
		missing = append(missing, "charter")
	}
	return normalizePolicyCapabilities(missing)
}

func firstWizardStep(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	missingSet := make(map[string]struct{}, len(missing))
	for _, item := range missing {
		missingSet[strings.TrimSpace(item)] = struct{}{}
	}
	for _, step := range durableAgentWizardStepOrder {
		if _, ok := missingSet[step]; ok {
			return step
		}
	}
	return strings.TrimSpace(missing[0])
}

func durableAgentWizardEffectiveBootstrapForAnswers(agent core.DurableAgent, answers core.DurableAgentSetupWizardAnswers, inheritedBootstrap core.NodeLLMBootstrap) core.NodeLLMBootstrap {
	effective, err := durableAgentBootstrapFromWizardAnswers(agent.BootstrapLLM, answers, inheritedBootstrap)
	if err == nil {
		return core.NormalizeNodeLLMBootstrap(effective)
	}

	answers = core.NormalizeDurableAgentSetupWizardAnswers(answers)
	current := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	inherited := core.NormalizeNodeLLMBootstrap(inheritedBootstrap)
	switch strings.TrimSpace(answers.BootstrapProfile) {
	case "inherit_parent":
		if inherited.Configured() {
			return inherited
		}
		return current
	case "child_custom":
		if current.Configured() {
			return current
		}
		return inherited
	default:
		if inherited.Configured() {
			return inherited
		}
		return current
	}
}

func wizardQuestionForStep(step string, effectiveBootstrapBackend string) string {
	switch strings.TrimSpace(step) {
	case "address":
		return "What channel address should this child own?"
	case "adapter":
		return "Which channel adapter should be named for this channel profile?"
	case "bootstrap_profile":
		if strings.TrimSpace(effectiveBootstrapBackend) == "codex" {
			return "This child uses a codex bootstrap backend; keep parent bootstrap defaults?"
		}
		return "Should this child inherit the parent bootstrap defaults or pin a child-custom bootstrap profile?"
	case "bootstrap_model":
		return "Which model should this child pin for child-custom bootstrap?"
	case "autonomy":
		return "Should the child be observe_only, local_drafts, review_before_reply, or reply_within_charter?"
	case "surface_rules":
		return "Which signal rules should surface upward as important?"
	case "summarize_pdfs":
		return "Should PDFs be summarized automatically?"
	case "synthesis_cadence":
		return "How often should this child synthesize upward (for example 4h)?"
	case "wakeup_mode":
		return "Should wakeups be poll, push, or poll_or_push?"
	case "poll_interval":
		return "What poll interval should be used (for example 5m)?"
	case "capabilities":
		return "Which capabilities are allowed in the child charter?"
	case "never_retain":
		return "Which classes must never be retained?"
	case "charter":
		return "What is the child charter summary?"
	default:
		return ""
	}
}

func durableAgentBootstrapFromWizardAnswers(current core.NodeLLMBootstrap, answers core.DurableAgentSetupWizardAnswers, inherited core.NodeLLMBootstrap) (core.NodeLLMBootstrap, error) {
	current = core.NormalizeNodeLLMBootstrap(current)
	inherited = core.NormalizeNodeLLMBootstrap(inherited)
	answers = core.NormalizeDurableAgentSetupWizardAnswers(answers)

	profile := strings.TrimSpace(answers.BootstrapProfile)
	if profile == "" {
		profile = durableAgentWizardBootstrapProfile(current, inherited)
	}

	switch profile {
	case "inherit_parent":
		if inherited.Configured() {
			return inherited, nil
		}
		return current, nil
	case "child_custom":
		bootstrap := current
		if !bootstrap.Configured() && inherited.Configured() {
			bootstrap = inherited
		}
		if strings.TrimSpace(answers.BootstrapModel) != "" {
			if bootstrap.Backend != "native" {
				return core.NodeLLMBootstrap{}, fmt.Errorf("durable_agent bootstrap_model requires a native bootstrap backend")
			}
			bootstrap.Model = strings.TrimSpace(answers.BootstrapModel)
		}
		return core.NormalizeNodeLLMBootstrap(bootstrap), nil
	default:
		if inherited.Configured() {
			return inherited, nil
		}
		return current, nil
	}
}

func durableAgentWizardBootstrapProfile(current core.NodeLLMBootstrap, inherited core.NodeLLMBootstrap) string {
	current = core.NormalizeNodeLLMBootstrap(current)
	inherited = core.NormalizeNodeLLMBootstrap(inherited)
	if current.Configured() {
		if inherited.Configured() && durableAgentNodeBootstrapEqual(current, inherited) {
			return "inherit_parent"
		}
		return "child_custom"
	}
	if inherited.Configured() {
		return "inherit_parent"
	}
	return ""
}

func durableAgentNodeBootstrapEqual(left core.NodeLLMBootstrap, right core.NodeLLMBootstrap) bool {
	left = core.NormalizeNodeLLMBootstrap(left)
	right = core.NormalizeNodeLLMBootstrap(right)
	return left.Backend == right.Backend &&
		left.NativeProvider == right.NativeProvider &&
		left.APIKey == right.APIKey &&
		left.BaseURL == right.BaseURL &&
		left.Model == right.Model &&
		left.MaxTokens == right.MaxTokens &&
		left.CodexAuthSource == right.CodexAuthSource &&
		left.CodexHome == right.CodexHome &&
		left.CodexBaseURL == right.CodexBaseURL
}

func durableAgentWizardBootstrapFallbackSummary(bootstrap core.NodeLLMBootstrap) string {
	bootstrap = core.NormalizeNodeLLMBootstrap(bootstrap)
	switch bootstrap.Backend {
	case "native":
		return "inherits parent provider fallback chain"
	case "codex":
		return "codex backend; no provider fallback chain"
	default:
		return "n/a"
	}
}

func normalizeDurableChannelWakeupMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "poll":
		return "poll"
	case "push":
		return "push"
	case "poll_or_push", "both":
		return "poll_or_push"
	default:
		return ""
	}
}

func durableChannelWakeupModeIncludesPoll(mode string) bool {
	mode = normalizeDurableChannelWakeupMode(mode)
	return mode == "poll" || mode == "poll_or_push"
}

func normalizeDurableAgentChannelKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "external", "external_channel", "channel", "inbox", "email":
		return "external_channel"
	default:
		return strings.TrimSpace(value)
	}
}

func durableAgentWizardDisplayChannelKind(value string) string {
	switch normalizeDurableAgentChannelKind(value) {
	case "external_channel":
		return "external"
	default:
		return strings.TrimSpace(value)
	}
}

func renderDurableAgentWizardShow(agent core.DurableAgent, wizard core.DurableAgentSetupWizardState, inheritedBootstrap core.NodeLLMBootstrap) string {
	var b strings.Builder
	channelKind := normalizeDurableAgentChannelKind(firstNonEmpty(strings.TrimSpace(wizard.ChannelKind), strings.TrimSpace(agent.ChannelKind), "external_channel"))
	effectiveBootstrap, _ := durableAgentBootstrapFromWizardAnswers(agent.BootstrapLLM, wizard.Answers, inheritedBootstrap)
	profile := strings.TrimSpace(core.NormalizeDurableAgentSetupWizardAnswers(wizard.Answers).BootstrapProfile)
	if profile == "" {
		profile = durableAgentWizardBootstrapProfile(agent.BootstrapLLM, inheritedBootstrap)
	}
	b.WriteString("action: durable-agent wizard show\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "wizard_status: %s\n", firstNonEmpty(strings.TrimSpace(wizard.Status), "in_progress"))
	fmt.Fprintf(&b, "current_step: %s\n", firstNonEmpty(strings.TrimSpace(wizard.CurrentStep), "-"))
	fmt.Fprintf(&b, "missing: %s\n", firstNonEmpty(strings.Join(wizard.Missing, ","), "-"))
	if question := wizardQuestionForStep(wizard.CurrentStep, effectiveBootstrap.Backend); question != "" {
		fmt.Fprintf(&b, "next_question: %s\n", question)
	}
	fmt.Fprintf(&b, "address: %s\n", strings.TrimSpace(wizard.Answers.Address))
	fmt.Fprintf(&b, "adapter: %s\n", strings.TrimSpace(wizard.Answers.Adapter))
	fmt.Fprintf(&b, "bootstrap_profile: %s\n", profile)
	fmt.Fprintf(&b, "bootstrap_backend: %s\n", strings.TrimSpace(effectiveBootstrap.Backend))
	fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", strings.TrimSpace(effectiveBootstrap.NativeProvider))
	fmt.Fprintf(&b, "bootstrap_model: %s\n", strings.TrimSpace(effectiveBootstrap.Model))
	fmt.Fprintf(&b, "bootstrap_fallback: %s\n", durableAgentWizardBootstrapFallbackSummary(effectiveBootstrap))
	b.WriteString("bootstrap_context_seed: inherited durable prompt context (no wizard override)\n")
	fmt.Fprintf(&b, "autonomy: %s\n", strings.TrimSpace(wizard.Answers.Autonomy))
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(wizard.Answers.WakeupMode))
	fmt.Fprintf(&b, "poll_interval: %s\n", strings.TrimSpace(wizard.Answers.PollInterval))
	fmt.Fprintf(&b, "synthesis_cadence: %s\n", strings.TrimSpace(wizard.Answers.SynthesisCadence))
	fmt.Fprintf(&b, "charter: %s\n", strings.TrimSpace(wizard.Answers.Charter))
	return b.String()
}

func renderDurableAgentWizardFinalize(agent core.DurableAgent, wizard core.DurableAgentSetupWizardState, inheritedBootstrap core.NodeLLMBootstrap) string {
	var b strings.Builder
	channelKind := normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind))
	profile := strings.TrimSpace(core.NormalizeDurableAgentSetupWizardAnswers(wizard.Answers).BootstrapProfile)
	if profile == "" {
		profile = durableAgentWizardBootstrapProfile(agent.BootstrapLLM, inheritedBootstrap)
	}
	bootstrap := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	b.WriteString("action: durable-agent wizard finalize\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "draft"))
	fmt.Fprintf(&b, "wizard_status: %s\n", firstNonEmpty(strings.TrimSpace(wizard.Status), "finalized"))
	fmt.Fprintf(&b, "bootstrap_profile: %s\n", profile)
	fmt.Fprintf(&b, "bootstrap_backend: %s\n", strings.TrimSpace(bootstrap.Backend))
	fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", strings.TrimSpace(bootstrap.NativeProvider))
	fmt.Fprintf(&b, "bootstrap_model: %s\n", strings.TrimSpace(bootstrap.Model))
	fmt.Fprintf(&b, "bootstrap_fallback: %s\n", durableAgentWizardBootstrapFallbackSummary(bootstrap))
	b.WriteString("bootstrap_context_seed: inherited durable prompt context (no wizard override)\n")
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(agent.WakeupMode))
	fmt.Fprintf(&b, "outbound_mode: %s\n", strings.TrimSpace(agent.LivePolicy.OutboundMode))
	renderDurableAgentChannelConfig(&b, agent)
	b.WriteString("next: connection_test then activate\n")
	return b.String()
}

func renderDurableAgentList(agents []core.DurableAgent) string {
	var b strings.Builder
	b.WriteString("[DURABLE_AGENTS]\n")
	fmt.Fprintf(&b, "count: %d\n", len(agents))
	if len(agents) == 0 {
		b.WriteString("no_agents\n[/DURABLE_AGENTS]")
		return b.String()
	}
	for i, agent := range agents {
		fmt.Fprintf(&b, "%d. agent_id=%s channel=%s status=%s policy_version=%d outbound_mode=%s allowed_users=%s\n",
			i+1,
			strings.TrimSpace(agent.AgentID),
			durableAgentWizardDisplayChannelKind(strings.TrimSpace(agent.ChannelKind)),
			firstNonEmpty(strings.TrimSpace(agent.Status), "active"),
			agent.PolicyVersion,
			strings.TrimSpace(agent.LivePolicy.OutboundMode),
			formatDurableAgentTelegramUserIDs(agent.AllowedTelegramUserIDs),
		)
	}
	b.WriteString("[/DURABLE_AGENTS]")
	return b.String()
}

func renderDurableAgentBootstrapShow(agent core.DurableAgent, updates []session.DurableAgentBootstrapUpdate, inherited core.NodeLLMBootstrap) string {
	var b strings.Builder
	b.WriteString("action: durable-agent bootstrap show\n")
	fmt.Fprintf(&b, "agent_id: %s\n", agent.AgentID)
	fmt.Fprintf(&b, "bootstrap_source_hint: %s\n", durableAgentBootstrapSourceHint(agent.BootstrapLLM, inherited))
	fmt.Fprintf(&b, "bootstrap_llm_backend: %s\n", agent.BootstrapLLM.Backend)
	fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", agent.BootstrapLLM.NativeProvider)
	fmt.Fprintf(&b, "bootstrap_model: %s\n", agent.BootstrapLLM.Model)
	if strings.TrimSpace(agent.BootstrapLLM.CodexHome) != "" {
		fmt.Fprintf(&b, "bootstrap_codex_home: %s\n", agent.BootstrapLLM.CodexHome)
	}
	fmt.Fprintf(&b, "parent_bootstrap_backend: %s\n", inherited.Backend)
	if inherited.Configured() && strings.TrimSpace(inherited.CodexHome) != "" {
		fmt.Fprintf(&b, "parent_bootstrap_codex_home: %s\n", inherited.CodexHome)
	}
	fmt.Fprintf(&b, "history_count: %d\n", len(updates))
	for _, update := range updates {
		fmt.Fprintf(&b, "- bootstrap_update id=%d kind=%s actor_role=%s", update.ID, strings.TrimSpace(update.UpdateKind), strings.TrimSpace(update.ActorRole))
		if update.ActorUserID > 0 {
			fmt.Fprintf(&b, " actor_user_id=%d", update.ActorUserID)
		}
		if update.SourceReviewEventID > 0 {
			fmt.Fprintf(&b, " review_event=%d", update.SourceReviewEventID)
		}
		if strings.TrimSpace(update.Reason) != "" {
			fmt.Fprintf(&b, " reason=%s", update.Reason)
		}
		fmt.Fprintf(&b, " applied_at=%s\n", update.AppliedAt.UTC().Format(time.RFC3339Nano))
	}
	return b.String()
}

func durableAgentBootstrapSourceHint(current core.NodeLLMBootstrap, inherited core.NodeLLMBootstrap) string {
	current = core.NormalizeNodeLLMBootstrap(current)
	inherited = core.NormalizeNodeLLMBootstrap(inherited)
	switch {
	case !current.Configured():
		return "unset"
	case inherited.Configured() && durableAgentNodeBootstrapEqual(current, inherited):
		return "matches_parent_copy"
	default:
		return "pinned_or_diverged"
	}
}

func renderDurableAgentPolicy(agent core.DurableAgent, updates []session.DurableAgentPolicyUpdate) string {
	var b strings.Builder
	channelKind := normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind))
	b.WriteString("action: durable-agent policy show\n")
	fmt.Fprintf(&b, "agent_id: %s\n", agent.AgentID)
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "active"))
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(agent.WakeupMode))
	fmt.Fprintf(&b, "policy_version: %d\n", agent.PolicyVersion)
	fmt.Fprintf(&b, "policy_hash: %s\n", agent.PolicyHash)
	if !agent.PolicyIssuedAt.IsZero() {
		fmt.Fprintf(&b, "policy_issued_at: %s\n", agent.PolicyIssuedAt.UTC().Format(time.RFC3339Nano))
	}
	fmt.Fprintf(&b, "autonomy: %s\n", durableAgentAutonomyFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "visibility: %s\n", durableAgentVisibilityFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "shared_context: %s\n", durableAgentSharedContextFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "charter: %s\n", agent.LivePolicy.Charter)
	fmt.Fprintf(&b, "capabilities: %s\n", strings.Join(agent.LivePolicy.CapabilityEnvelope, ","))
	fmt.Fprintf(&b, "outbound_mode: %s\n", agent.LivePolicy.OutboundMode)
	fmt.Fprintf(&b, "drift_policy: %s\n", agent.LivePolicy.DriftPolicy)
	fmt.Fprintf(&b, "public_surface_mode: %s\n", agent.LivePolicy.PublicSurfaceMode)
	fmt.Fprintf(&b, "shared_inference_reuse: %s\n", agent.LivePolicy.SharedInferenceReuse)
	fmt.Fprintf(&b, "shared_inference_reuse_scope: %s\n", agent.LivePolicy.SharedInferenceReuseScope)
	if strings.TrimSpace(agent.LivePolicy.TailnetMode) != "" {
		fmt.Fprintf(&b, "tailnet_mode: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetMode))
		fmt.Fprintf(&b, "tailnet_hostname: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetHostname))
		fmt.Fprintf(&b, "tailnet_tags: %s\n", strings.Join(agent.LivePolicy.TailnetTags, ","))
		fmt.Fprintf(&b, "tailnet_surface_policy: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetSurfacePolicy))
	}
	fmt.Fprintf(&b, "allowed_telegram_user_ids: %s\n", formatDurableAgentTelegramUserIDs(agent.AllowedTelegramUserIDs))
	fmt.Fprintf(&b, "bootstrap_capabilities: %s\n", strings.Join(agent.BootstrapCeiling.CapabilityEnvelope, ","))
	fmt.Fprintf(&b, "bootstrap_allowed_outbound_modes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedOutboundModes, ","))
	fmt.Fprintf(&b, "bootstrap_allowed_public_surface_modes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedPublicSurfaceModes, ","))
	fmt.Fprintf(&b, "bootstrap_allowed_shared_inference_reuse: %s\n", strings.Join(agent.BootstrapCeiling.AllowedSharedInferenceReuse, ","))
	fmt.Fprintf(&b, "bootstrap_allowed_shared_inference_scopes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedSharedInferenceScopes, ","))
	fmt.Fprintf(&b, "bootstrap_llm_backend: %s\n", agent.BootstrapLLM.Backend)
	fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", agent.BootstrapLLM.NativeProvider)
	fmt.Fprintf(&b, "bootstrap_model: %s\n", agent.BootstrapLLM.Model)
	if strings.TrimSpace(agent.BootstrapLLM.CodexHome) != "" {
		fmt.Fprintf(&b, "bootstrap_codex_home: %s\n", agent.BootstrapLLM.CodexHome)
	}
	renderDurableAgentChannelConfig(&b, agent)
	fmt.Fprintf(&b, "policy_updates: %d\n", len(updates))
	for _, update := range updates {
		fmt.Fprintf(&b, "- id=%d previous=%d new=%d", update.ID, update.PreviousVersion, update.NewVersion)
		if update.SourceReviewEventID > 0 {
			fmt.Fprintf(&b, " review_event=%d", update.SourceReviewEventID)
		}
		if strings.TrimSpace(update.Reason) != "" {
			fmt.Fprintf(&b, " reason=%s", update.Reason)
		}
		fmt.Fprintf(&b, " applied_at=%s\n", update.AppliedAt.UTC().Format(time.RFC3339Nano))
	}
	return b.String()
}

func renderDurableAgentPolicyApply(agent core.DurableAgent, update *session.DurableAgentPolicyUpdate) string {
	var b strings.Builder
	b.WriteString("action: durable-agent policy apply\n")
	fmt.Fprintf(&b, "agent_id: %s\n", agent.AgentID)
	if update == nil {
		b.WriteString("changed: false\n")
		fmt.Fprintf(&b, "policy_version: %d\n", agent.PolicyVersion)
		fmt.Fprintf(&b, "policy_hash: %s\n", agent.PolicyHash)
		return b.String()
	}
	b.WriteString("changed: true\n")
	fmt.Fprintf(&b, "policy_version: %d\n", agent.PolicyVersion)
	fmt.Fprintf(&b, "policy_hash: %s\n", agent.PolicyHash)
	fmt.Fprintf(&b, "autonomy: %s\n", durableAgentAutonomyFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "visibility: %s\n", durableAgentVisibilityFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "shared_context: %s\n", durableAgentSharedContextFromPolicy(agent.LivePolicy))
	if strings.TrimSpace(agent.LivePolicy.TailnetMode) != "" {
		fmt.Fprintf(&b, "tailnet_mode: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetMode))
		fmt.Fprintf(&b, "tailnet_hostname: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetHostname))
		fmt.Fprintf(&b, "tailnet_surface_policy: %s\n", strings.TrimSpace(agent.LivePolicy.TailnetSurfacePolicy))
	}
	if update.SourceReviewEventID > 0 {
		fmt.Fprintf(&b, "source_review_event_id: %d\n", update.SourceReviewEventID)
	}
	if strings.TrimSpace(update.Reason) != "" {
		fmt.Fprintf(&b, "reason: %s\n", update.Reason)
	}
	return b.String()
}

func renderDurableAgentBootstrapApply(agent core.DurableAgent, update *session.DurableAgentBootstrapUpdate) string {
	var b strings.Builder
	b.WriteString("action: durable-agent bootstrap update\n")
	fmt.Fprintf(&b, "agent_id: %s\n", agent.AgentID)
	if update == nil {
		b.WriteString("changed: false\n")
		fmt.Fprintf(&b, "bootstrap_llm_backend: %s\n", agent.BootstrapLLM.Backend)
		fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", agent.BootstrapLLM.NativeProvider)
		fmt.Fprintf(&b, "bootstrap_model: %s\n", agent.BootstrapLLM.Model)
		if strings.TrimSpace(agent.BootstrapLLM.CodexHome) != "" {
			fmt.Fprintf(&b, "bootstrap_codex_home: %s\n", agent.BootstrapLLM.CodexHome)
		}
		return b.String()
	}
	b.WriteString("changed: true\n")
	fmt.Fprintf(&b, "update_id: %d\n", update.ID)
	fmt.Fprintf(&b, "update_kind: %s\n", update.UpdateKind)
	fmt.Fprintf(&b, "previous_bootstrap_backend: %s\n", update.PreviousBootstrap.Backend)
	fmt.Fprintf(&b, "new_bootstrap_backend: %s\n", update.NewBootstrap.Backend)
	fmt.Fprintf(&b, "new_bootstrap_native_provider: %s\n", update.NewBootstrap.NativeProvider)
	fmt.Fprintf(&b, "new_bootstrap_model: %s\n", update.NewBootstrap.Model)
	if strings.TrimSpace(update.NewBootstrap.CodexHome) != "" {
		fmt.Fprintf(&b, "new_bootstrap_codex_home: %s\n", update.NewBootstrap.CodexHome)
	}
	if update.SourceReviewEventID > 0 {
		fmt.Fprintf(&b, "source_review_event_id: %d\n", update.SourceReviewEventID)
	}
	if update.ActorUserID > 0 {
		fmt.Fprintf(&b, "actor_user_id: %d\n", update.ActorUserID)
	}
	if strings.TrimSpace(update.ActorRole) != "" {
		fmt.Fprintf(&b, "actor_role: %s\n", update.ActorRole)
	}
	if strings.TrimSpace(update.Reason) != "" {
		fmt.Fprintf(&b, "reason: %s\n", update.Reason)
	}
	b.WriteString("note: next durable child wake uses the updated bootstrap\n")
	return b.String()
}

func renderDurableAgentEnrollment(enrollment core.DurableAgentRemoteEnrollment) string {
	var b strings.Builder
	b.WriteString("action: durable-agent enrollment\n")
	fmt.Fprintf(&b, "agent_id: %s\n", enrollment.AgentID)
	fmt.Fprintf(&b, "status: %s\n", enrollment.Status)
	fmt.Fprintf(&b, "parent_control_url: %s\n", enrollment.ParentControlURL)
	fmt.Fprintf(&b, "key_fingerprint: %s\n", enrollment.KeyFingerprint)
	fmt.Fprintf(&b, "protocol_version: %s\n", enrollment.ProtocolVersion)
	fmt.Fprintf(&b, "last_sequence: %d\n", enrollment.LastSequence)
	if !enrollment.EnrolledAt.IsZero() {
		fmt.Fprintf(&b, "enrolled_at: %s\n", enrollment.EnrolledAt.UTC().Format(time.RFC3339))
	}
	if !enrollment.LastSeenAt.IsZero() {
		fmt.Fprintf(&b, "last_seen_at: %s\n", enrollment.LastSeenAt.UTC().Format(time.RFC3339))
	}
	if !enrollment.RevokedAt.IsZero() {
		fmt.Fprintf(&b, "revoked_at: %s\n", enrollment.RevokedAt.UTC().Format(time.RFC3339))
	}
	return b.String()
}

func renderDurableAgentLifecycle(action string, agent core.DurableAgent) string {
	var b strings.Builder
	channelKind := normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind))
	fmt.Fprintf(&b, "action: durable-agent %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "active"))
	fmt.Fprintf(&b, "review_target_chat_id: %d\n", agent.ReviewTargetChatID)
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(agent.WakeupMode))
	fmt.Fprintf(&b, "outbound_mode: %s\n", strings.TrimSpace(agent.LivePolicy.OutboundMode))
	fmt.Fprintf(&b, "allowed_telegram_user_ids: %s\n", formatDurableAgentTelegramUserIDs(agent.AllowedTelegramUserIDs))
	renderDurableAgentChannelConfig(&b, agent)
	return b.String()
}

func renderDurableAgentAccess(action string, agent core.DurableAgent, requested []int64, changed bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent access %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	if len(requested) > 0 {
		fmt.Fprintf(&b, "requested_user_ids: %s\n", formatDurableAgentTelegramUserIDs(requested))
	}
	fmt.Fprintf(&b, "changed: %t\n", changed)
	fmt.Fprintf(&b, "allowed_telegram_user_ids: %s\n", formatDurableAgentTelegramUserIDs(agent.AllowedTelegramUserIDs))
	return b.String()
}

func renderDurableAgentConversation(action string, agent core.DurableAgent, continuity core.DurableAgentContinuityState, history int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent conversation %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	total := 0
	if continuity.Conversation != nil {
		total = len(continuity.Conversation.Messages)
	}
	threadState, lastParentAt, lastChildAt, lastParentAckAt, lastChildError := durableAgentConversationState(continuity)
	fmt.Fprintf(&b, "messages: %d\n", total)
	fmt.Fprintf(&b, "pending_parent_messages: %d\n", len(continuity.PendingParentConversationMessages(0)))
	fmt.Fprintf(&b, "thread_state: %s\n", threadState)
	if !lastParentAt.IsZero() {
		fmt.Fprintf(&b, "last_parent_message_at: %s\n", lastParentAt.UTC().Format(time.RFC3339))
	}
	if !lastChildAt.IsZero() {
		fmt.Fprintf(&b, "last_child_message_at: %s\n", lastChildAt.UTC().Format(time.RFC3339))
	}
	if !lastParentAckAt.IsZero() {
		fmt.Fprintf(&b, "last_parent_acknowledged_at: %s\n", lastParentAckAt.UTC().Format(time.RFC3339))
	}
	if lastChildError != "" {
		fmt.Fprintf(&b, "last_child_error: %s\n", truncateCompact(lastChildError, 220))
	}
	window := durableAgentConversationWindow(continuity, history)
	if len(window) == 0 {
		b.WriteString("conversation: -\n")
		b.WriteString("next: conversation_send\n")
		return b.String()
	}
	b.WriteString("conversation:\n")
	for _, message := range window {
		ts := "-"
		if !message.CreatedAt.IsZero() {
			ts = message.CreatedAt.UTC().Format(time.RFC3339)
		}
		line := fmt.Sprintf("- [%s] %s: %s", ts, message.Role, message.Text)
		if message.Role == "parent" && !message.AcknowledgedAt.IsZero() {
			line += " (acknowledged)"
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("next: conversation_send\n")
	return b.String()
}

func durableAgentDelegationAgreementBoundedEffect(record session.CapabilityRequest, input durableAgentDelegationRequestInput) string {
	parts := make([]string, 0, 4)
	if strings.TrimSpace(input.UpdateReason) != "" {
		parts = append(parts, "update_reason="+strings.TrimSpace(input.UpdateReason))
	}
	if strings.TrimSpace(record.TargetResource) != "" {
		parts = append(parts, "target="+strings.TrimSpace(record.TargetResource))
	}
	if len(input.GrantActions) > 0 {
		parts = append(parts, "grant_actions="+strings.Join(normalizePolicyCapabilities(input.GrantActions), ","))
	}
	if strings.TrimSpace(record.Purpose) != "" {
		parts = append(parts, "purpose="+strings.TrimSpace(record.Purpose))
	}
	return strings.Join(parts, "; ")
}

func durableAgentDelegationAgreementArtifactRefs(reviewEventID int64, refs []string) []session.RecordReference {
	out := make([]session.RecordReference, 0, len(refs)+1)
	if reviewEventID > 0 {
		out = append(out, session.RecordReference{Kind: "review_event", Ref: fmt.Sprintf("%d", reviewEventID), Label: "delegation request"})
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		out = append(out, session.RecordReference{Kind: "artifact", Ref: ref})
	}
	return session.NormalizeRecordReferences(out)
}

func renderDurableAgentDelegationRequest(agent core.DurableAgent, record session.CapabilityRequest, reviewEventID int64, agreement session.DurableChildAgreement) string {
	record = session.NormalizeCapabilityRequest(record)
	var b strings.Builder
	b.WriteString("action: durable-agent delegation request\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "request_id: %s\n", record.RequestID)
	fmt.Fprintf(&b, "review_event_id: %d\n", reviewEventID)
	if strings.TrimSpace(agreement.AgreementID) != "" {
		fmt.Fprintf(&b, "agreement_id: %s\n", agreement.AgreementID)
		fmt.Fprintf(&b, "agreement_status: %s\n", agreement.Status)
	}
	b.WriteString("canonical_surface: capability_request\n")
	b.WriteString("agreement_surface: durable_child_agreement\n")
	fmt.Fprintf(&b, "kind: %s\n", record.Kind)
	fmt.Fprintf(&b, "target_resource: %s\n", record.TargetResource)
	fmt.Fprintf(&b, "review_status: %s\n", record.ReviewStatus)
	fmt.Fprintf(&b, "requested_by: %s\n", firstNonEmpty(record.RequestedBy, "-"))
	fmt.Fprintf(&b, "requested_for: %s\n", firstNonEmpty(record.RequestedFor, "-"))
	if record.ParentPrincipal != "" {
		fmt.Fprintf(&b, "parent_principal: %s\n", record.ParentPrincipal)
	}
	if record.AdminPrincipal != "" {
		fmt.Fprintf(&b, "admin_principal: %s\n", record.AdminPrincipal)
	}
	if record.RiskClass != "" {
		fmt.Fprintf(&b, "risk_class: %s\n", record.RiskClass)
	}
	if record.Purpose != "" {
		fmt.Fprintf(&b, "purpose: %s\n", record.Purpose)
	}
	if plan, ok, err := capabilityUpdatePlanFromContract(record.Contract); err == nil && ok {
		b.WriteString("capability_update_plan: present\n")
		if plan.AgentID != "" {
			fmt.Fprintf(&b, "policy_agent_id: %s\n", plan.AgentID)
		}
		if capabilityUpdatePlanHasDurablePolicyPatch(plan) {
			b.WriteString("policy_update_on_grant: true\n")
		}
	}
	b.WriteString("next: capability_authority request_review, then grant_set if approved\n")
	return b.String()
}

func renderDurableAgentDelegationReport(agent core.DurableAgent, input durableAgentDelegationReportInput, reviewEventID int64) string {
	var b strings.Builder
	b.WriteString("action: durable-agent delegation report\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "review_event_id: %d\n", reviewEventID)
	if requestID := strings.TrimSpace(input.RequestID); requestID != "" {
		fmt.Fprintf(&b, "request_id: %s\n", requestID)
	}
	if grantID := strings.TrimSpace(input.GrantID); grantID != "" {
		fmt.Fprintf(&b, "grant_id: %s\n", grantID)
	}
	if status := strings.TrimSpace(input.Status); status != "" {
		fmt.Fprintf(&b, "status: %s\n", status)
	}
	if outcome := strings.TrimSpace(input.Outcome); outcome != "" {
		fmt.Fprintf(&b, "outcome: %s\n", outcome)
	}
	b.WriteString("next: review queued artifact and update capability grant/request if needed\n")
	return b.String()
}

func durableAgentDelegationRequestArtifact(agent core.DurableAgent, record session.CapabilityRequest, input durableAgentDelegationRequestInput) core.DurableReviewArtifact {
	record = session.NormalizeCapabilityRequest(record)
	metadata := cloneDurableAgentDelegationMetadata(input.Metadata)
	putDurableAgentDelegationMetadata(metadata, "delegation_surface", "durable_agent.delegation_request")
	putDurableAgentDelegationMetadata(metadata, "capability_request_id", record.RequestID)
	putDurableAgentDelegationMetadata(metadata, "capability_kind", string(record.Kind))
	putDurableAgentDelegationMetadata(metadata, "target_resource", record.TargetResource)
	putDurableAgentDelegationMetadata(metadata, "requested_by", record.RequestedBy)
	putDurableAgentDelegationMetadata(metadata, "requested_for", record.RequestedFor)
	putDurableAgentDelegationMetadata(metadata, "review_status", string(record.ReviewStatus))
	putDurableAgentDelegationMetadata(metadata, "purpose", record.Purpose)
	if plan, ok, err := capabilityUpdatePlanFromContract(record.Contract); err == nil && ok {
		putDurableAgentDelegationMetadata(metadata, "capability_update_plan", "present")
		if plan.AgentID != "" {
			putDurableAgentDelegationMetadata(metadata, "policy_agent_id", plan.AgentID)
		}
		if capabilityUpdatePlanHasDurablePolicyPatch(plan) {
			putDurableAgentDelegationMetadata(metadata, "policy_update_on_grant", "true")
		}
	}

	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		summary = fmt.Sprintf("Delegation request %s: %s requests %s access to %s. Purpose: %s", record.RequestID, firstNonEmpty(record.RequestedFor, agent.AgentID), record.Kind, record.TargetResource, record.Purpose)
	}
	localActions := normalizeDurableAgentDelegationStrings(input.LocalActions)
	localActions = appendDurableAgentDelegationString(localActions, fmt.Sprintf("created capability_request %s with review_status %s", record.RequestID, record.ReviewStatus))
	questions := normalizeDurableAgentDelegationStrings(input.Questions)
	if len(questions) == 0 {
		questions = append(questions, fmt.Sprintf("Review capability_request %s and approve, reject, or grant allowed actions if acceptable.", record.RequestID))
	}
	riskFlags := normalizeDurableAgentDelegationStrings(input.RiskFlags)
	if record.RiskClass != "" {
		riskFlags = appendDurableAgentDelegationString(riskFlags, "risk_class:"+record.RiskClass)
	}
	return core.DurableReviewArtifact{
		AgentID:      agent.AgentID,
		Summary:      summary,
		LocalActions: localActions,
		Questions:    questions,
		RiskFlags:    riskFlags,
		ArtifactRefs: normalizeDurableAgentDelegationStrings(input.ArtifactRefs),
		Metadata:     metadata,
	}
}

func durableAgentDelegationReportArtifact(agent core.DurableAgent, input durableAgentDelegationReportInput) core.DurableReviewArtifact {
	metadata := cloneDurableAgentDelegationMetadata(input.Metadata)
	putDurableAgentDelegationMetadata(metadata, "delegation_surface", "durable_agent.delegation_report")
	putDurableAgentDelegationMetadata(metadata, "capability_request_id", input.RequestID)
	putDurableAgentDelegationMetadata(metadata, "capability_grant_id", input.GrantID)
	putDurableAgentDelegationMetadata(metadata, "status", input.Status)
	putDurableAgentDelegationMetadata(metadata, "outcome", input.Outcome)

	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		summary = fmt.Sprintf("Delegation report from %s: %s", strings.TrimSpace(agent.AgentID), firstNonEmpty(input.Outcome, input.Status, "needs review"))
	}
	return core.DurableReviewArtifact{
		AgentID:      agent.AgentID,
		Summary:      summary,
		LocalActions: normalizeDurableAgentDelegationStrings(input.LocalActions),
		Questions:    normalizeDurableAgentDelegationStrings(input.Questions),
		RiskFlags:    normalizeDurableAgentDelegationStrings(input.RiskFlags),
		ArtifactRefs: normalizeDurableAgentDelegationStrings(input.ArtifactRefs),
		Metadata:     metadata,
	}
}

func durableAgentReviewTargetChatID(agent core.DurableAgent, overrides ...int64) int64 {
	for _, value := range overrides {
		if value > 0 {
			return value
		}
	}
	return agent.ReviewTargetChatID
}

func durableAgentDefaultParentPrincipal(agent core.DurableAgent) string {
	kind := strings.TrimSpace(agent.ParentScopeKind)
	id := strings.TrimSpace(agent.ParentScopeID)
	if kind == "" || id == "" {
		return ""
	}
	switch session.ScopeKind(kind) {
	case session.ScopeKindTelegramDM:
		return "telegram:" + id
	case session.ScopeKindDurableAgent:
		return id
	default:
		return kind + ":" + id
	}
}

func cloneDurableAgentDelegationMetadata(input map[string]string) map[string]string {
	out := make(map[string]string, len(input)+8)
	for key, value := range input {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func putDurableAgentDelegationMetadata(metadata map[string]string, key string, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	metadata[key] = value
}

func normalizeDurableAgentDelegationStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendDurableAgentDelegationString(out, value)
	}
	return out
}

func appendDurableAgentDelegationString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func durableAgentConversationWindow(continuity core.DurableAgentContinuityState, history int) []core.DurableAgentConversationMessage {
	continuity = core.NormalizeDurableAgentContinuityState(continuity)
	if continuity.Conversation == nil || len(continuity.Conversation.Messages) == 0 {
		return nil
	}
	limit := history
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	if limit > len(continuity.Conversation.Messages) {
		limit = len(continuity.Conversation.Messages)
	}
	out := make([]core.DurableAgentConversationMessage, 0, limit)
	out = append(out, continuity.Conversation.Messages[:limit]...)
	return out
}

func durableAgentConversationState(continuity core.DurableAgentContinuityState) (state string, lastParentAt, lastChildAt, lastParentAckAt time.Time, lastChildError string) {
	continuity = core.NormalizeDurableAgentContinuityState(continuity)
	pending := len(continuity.PendingParentConversationMessages(0))
	if continuity.Conversation == nil || len(continuity.Conversation.Messages) == 0 {
		if pending > 0 {
			return "awaiting_child_pickup", time.Time{}, time.Time{}, time.Time{}, ""
		}
		return "idle", time.Time{}, time.Time{}, time.Time{}, ""
	}

	for _, message := range continuity.Conversation.Messages {
		switch strings.TrimSpace(message.Role) {
		case "parent":
			if lastParentAt.IsZero() {
				lastParentAt = message.CreatedAt.UTC()
			}
			if !message.AcknowledgedAt.IsZero() && lastParentAckAt.IsZero() {
				lastParentAckAt = message.AcknowledgedAt.UTC()
			}
		case "child":
			if lastChildAt.IsZero() {
				lastChildAt = message.CreatedAt.UTC()
			}
			if lastChildError == "" && durableAgentMessageIsInferenceUnavailable(message.Text) {
				lastChildError = strings.TrimSpace(message.Text)
			}
		}
	}

	switch {
	case pending > 0 && lastChildError != "":
		state = "retrying_after_inference_failure"
	case pending > 0:
		state = "awaiting_child_pickup"
	case !lastChildAt.IsZero() && lastChildError != "":
		state = "child_blocked_inference"
	case !lastChildAt.IsZero():
		state = "awaiting_parent_guidance"
	default:
		state = "conversation_open"
	}
	return state, lastParentAt, lastChildAt, lastParentAckAt, lastChildError
}

func durableAgentMessageIsInferenceUnavailable(text string) bool {
	text = strings.TrimSpace(text)
	return strings.Contains(text, "Inference backend is unavailable.") ||
		strings.Contains(text, "Inference backends are unavailable after retries and fallback.") ||
		strings.Contains(text, "Inference backends are unavailable after provider fallback attempts.")
}

func formatDurableAgentTelegramUserIDs(values []int64) string {
	values = core.NormalizeDurableAgentAllowedTelegramUserIDs(values)
	if len(values) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ",")
}

func renderDurableAgentChannelConfig(b *strings.Builder, agent core.DurableAgent) {
	if b == nil {
		return
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		return
	}
	fmt.Fprintf(b, "channel_address: %s\n", strings.TrimSpace(external.Address))
	fmt.Fprintf(b, "channel_account: %s\n", strings.TrimSpace(external.Account))
	fmt.Fprintf(b, "channel_adapter: %s\n", strings.TrimSpace(external.Adapter))
	fmt.Fprintf(b, "channel_query: %s\n", strings.TrimSpace(external.Query))
	fmt.Fprintf(b, "channel_poll_interval: %s\n", strings.TrimSpace(external.PollInterval))
	fmt.Fprintf(b, "channel_summarize_pdfs: %t\n", external.SummarizePDFs)
	fmt.Fprintf(b, "channel_synthesis_cadence: %s\n", strings.TrimSpace(external.SynthesisCadence))
	fmt.Fprintf(b, "channel_surface_rules: %s\n", strings.Join(external.SurfaceRules, ","))
	fmt.Fprintf(b, "channel_never_retain: %s\n", strings.Join(external.NeverRetain, ","))
}

func durableAgentReviewTargetsAgent(agentID string, scope session.ScopeRef) bool {
	agentID = strings.TrimSpace(agentID)
	return strings.TrimSpace(scope.DurableAgentID) == agentID || strings.TrimSpace(scope.ID) == agentID
}

func (r *Registry) resolveDurableAgent(raw string) (*core.DurableAgent, error) {
	agentID := strings.TrimSpace(raw)
	if agentID == "" {
		return nil, fmt.Errorf("durable_agent agent_id is required")
	}
	agent, err := r.store.DurableAgent(agentID)
	if err == nil {
		return agent, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	agents, listErr := r.store.ListDurableAgents()
	if listErr != nil {
		return nil, err
	}
	if matched := findDurableAgentCandidate(agents, agentID); matched != nil {
		return matched, nil
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("durable agent %q not found and no durable agents are registered", agentID)
	}
	return nil, fmt.Errorf("durable agent %q not found; available agent_ids: %s", agentID, strings.Join(durableAgentIDOptions(agents), ", "))
}

func findDurableAgentCandidate(agents []core.DurableAgent, raw string) *core.DurableAgent {
	normalized := normalizeDurableAgentReference(raw)
	if normalized == "" {
		return nil
	}
	var exact *core.DurableAgent
	exactCount := 0
	for i := range agents {
		if normalizeDurableAgentReference(agents[i].AgentID) == normalized {
			exact = &agents[i]
			exactCount++
		}
	}
	if exactCount == 1 {
		return exact
	}

	var fuzzy *core.DurableAgent
	fuzzyCount := 0
	for i := range agents {
		candidate := normalizeDurableAgentReference(agents[i].AgentID)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, normalized) || strings.Contains(normalized, candidate) {
			fuzzy = &agents[i]
			fuzzyCount++
		}
	}
	if fuzzyCount == 1 {
		return fuzzy
	}
	return nil
}

func durableAgentIDOptions(agents []core.DurableAgent) []string {
	if len(agents) == 0 {
		return nil
	}
	out := make([]string, 0, len(agents))
	for _, agent := range agents {
		id := strings.TrimSpace(agent.AgentID)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

type effectiveDurableAgentPolicyPatch struct {
	Charter                   string
	Autonomy                  string
	Visibility                string
	SharedContext             string
	Capabilities              []string
	CapabilitiesSet           bool
	DriftPolicy               string
	OutboundMode              string
	PublicSurfaceMode         string
	SharedInferenceReuse      string
	SharedInferenceReuseScope string
	TailnetMode               string
	TailnetHostname           string
	TailnetTags               []string
	TailnetTagsSet            bool
	TailnetSurfacePolicy      string
}

func effectiveDurableAgentPolicyPatchFromInput(in durableAgentInput) effectiveDurableAgentPolicyPatch {
	patch := effectiveDurableAgentPolicyPatch{}

	if in.PolicyPatch != nil {
		patch.Charter = strings.TrimSpace(in.PolicyPatch.Charter)
		patch.Autonomy = strings.TrimSpace(in.PolicyPatch.Autonomy)
		patch.Visibility = strings.TrimSpace(in.PolicyPatch.Visibility)
		patch.SharedContext = strings.TrimSpace(in.PolicyPatch.SharedContext)
		patch.DriftPolicy = strings.TrimSpace(in.PolicyPatch.DriftPolicy)
		if in.PolicyPatch.Capabilities != nil {
			patch.Capabilities = normalizePolicyCapabilities(in.PolicyPatch.Capabilities)
			patch.CapabilitiesSet = true
		}
	}
	if patch.Charter == "" {
		patch.Charter = strings.TrimSpace(in.Charter)
	}
	if patch.Autonomy == "" {
		patch.Autonomy = strings.TrimSpace(in.Autonomy)
	}
	if patch.Visibility == "" {
		patch.Visibility = strings.TrimSpace(in.Visibility)
	}
	if patch.SharedContext == "" {
		patch.SharedContext = strings.TrimSpace(in.SharedContext)
	}
	if !patch.CapabilitiesSet && in.Capabilities != nil {
		patch.Capabilities = normalizePolicyCapabilities(in.Capabilities)
		patch.CapabilitiesSet = true
	}
	if patch.DriftPolicy == "" {
		patch.DriftPolicy = strings.TrimSpace(in.DriftPolicy)
	}

	if in.PolicyOverrides != nil {
		patch.OutboundMode = strings.TrimSpace(in.PolicyOverrides.OutboundMode)
		patch.PublicSurfaceMode = strings.TrimSpace(in.PolicyOverrides.PublicSurfaceMode)
		patch.SharedInferenceReuse = strings.TrimSpace(in.PolicyOverrides.SharedInferenceReuse)
		patch.SharedInferenceReuseScope = strings.TrimSpace(in.PolicyOverrides.SharedInferenceReuseScope)
		patch.TailnetMode = strings.TrimSpace(in.PolicyOverrides.TailnetMode)
		patch.TailnetHostname = strings.TrimSpace(in.PolicyOverrides.TailnetHostname)
		if in.PolicyOverrides.TailnetTags != nil {
			patch.TailnetTags = normalizePolicyCapabilities(in.PolicyOverrides.TailnetTags)
			patch.TailnetTagsSet = true
		}
		patch.TailnetSurfacePolicy = strings.TrimSpace(in.PolicyOverrides.TailnetSurfacePolicy)
	}
	if patch.OutboundMode == "" {
		patch.OutboundMode = strings.TrimSpace(in.OutboundMode)
	}
	if patch.PublicSurfaceMode == "" {
		patch.PublicSurfaceMode = strings.TrimSpace(in.PublicSurfaceMode)
	}
	if patch.SharedInferenceReuse == "" {
		patch.SharedInferenceReuse = strings.TrimSpace(in.SharedInferenceReuse)
	}
	if patch.SharedInferenceReuseScope == "" {
		patch.SharedInferenceReuseScope = strings.TrimSpace(in.SharedInferenceReuseScope)
	}
	return patch
}

func applyDurableAgentPolicyPatch(policy *core.DurableAgentLivePolicy, patch effectiveDurableAgentPolicyPatch) error {
	if policy == nil {
		return nil
	}
	if patch.Charter != "" {
		policy.Charter = patch.Charter
	}
	if patch.Autonomy != "" {
		mode, err := durableAgentAutonomyToOutboundMode(patch.Autonomy)
		if err != nil {
			return err
		}
		policy.OutboundMode = mode
	}
	if patch.Visibility != "" {
		mode, err := durableAgentVisibilityToPublicSurfaceMode(patch.Visibility)
		if err != nil {
			return err
		}
		policy.PublicSurfaceMode = mode
	}
	if patch.SharedContext != "" {
		reuse, scope, err := durableAgentSharedContextToReuse(patch.SharedContext)
		if err != nil {
			return err
		}
		policy.SharedInferenceReuse = reuse
		policy.SharedInferenceReuseScope = scope
	}
	if patch.CapabilitiesSet {
		policy.CapabilityEnvelope = append([]string(nil), patch.Capabilities...)
	}
	if patch.DriftPolicy != "" {
		policy.DriftPolicy = patch.DriftPolicy
	}
	if patch.OutboundMode != "" {
		policy.OutboundMode = patch.OutboundMode
	}
	if patch.PublicSurfaceMode != "" {
		policy.PublicSurfaceMode = patch.PublicSurfaceMode
	}
	if patch.SharedInferenceReuse != "" {
		policy.SharedInferenceReuse = patch.SharedInferenceReuse
	}
	if patch.SharedInferenceReuseScope != "" {
		policy.SharedInferenceReuseScope = patch.SharedInferenceReuseScope
	}
	if patch.TailnetMode != "" {
		policy.TailnetMode = patch.TailnetMode
	}
	if patch.TailnetHostname != "" {
		policy.TailnetHostname = patch.TailnetHostname
	}
	if patch.TailnetTagsSet {
		policy.TailnetTags = append([]string(nil), patch.TailnetTags...)
	}
	if patch.TailnetSurfacePolicy != "" {
		policy.TailnetSurfacePolicy = patch.TailnetSurfacePolicy
	}
	return nil
}

func normalizePolicyCapabilities(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func normalizeDurableAgentReference(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.ReplaceAll(raw, "durable", " ")
	raw = strings.ReplaceAll(raw, "agent", " ")
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func durableAgentAutonomyToOutboundMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "observe_only":
		return "read_only", nil
	case "local_drafts":
		return "draft_only", nil
	case "review_before_reply":
		return "reply_with_parent_review", nil
	case "reply_within_charter":
		return "reply_with_policy_authorization", nil
	default:
		return "", fmt.Errorf("durable_agent autonomy must be one of observe_only|local_drafts|review_before_reply|reply_within_charter")
	}
}

func durableAgentVisibilityToPublicSurfaceMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "private":
		return "none", nil
	case "parent_relay_only":
		return "explicit_parent_relay_only", nil
	case "public_channel":
		return "channel_transcript", nil
	default:
		return "", fmt.Errorf("durable_agent visibility must be one of private|parent_relay_only|public_channel")
	}
}

func durableAgentSharedContextToReuse(value string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "isolated":
		return "disabled", "public_prefix_only", nil
	case "public_only":
		return "allowed", "public_prefix_only", nil
	default:
		return "", "", fmt.Errorf("durable_agent shared_context must be one of isolated|public_only")
	}
}

func defaultDurableAgentLivePolicy(channelKind string, charter string) core.DurableAgentLivePolicy {
	switch normalizeDurableAgentChannelKind(channelKind) {
	case "external_channel":
		return core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:                   strings.TrimSpace(charter),
			CapabilityEnvelope:        []string{"read_channel", "bounded_review_artifact", "summarize_pdf"},
			OutboundMode:              "read_only",
			DriftPolicy:               "admin_review",
			PublicSurfaceMode:         "explicit_parent_relay_only",
			SharedInferenceReuse:      "disabled",
			SharedInferenceReuseScope: "public_prefix_only",
		})
	default:
		return core.DefaultTelegramGroupLivePolicy(charter)
	}
}

func mergeDurableAgentChannelConfig(existing core.DurableAgentChannelConfig, raw json.RawMessage) (core.DurableAgentChannelConfig, error) {
	existing = core.NormalizeDurableAgentChannelConfig(existing)
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return existing, nil
	}
	type channelConfigInput struct {
		External *core.DurableAgentExternalChannelConfig `json:"external,omitempty"`
		Channel  *core.DurableAgentExternalChannelConfig `json:"channel,omitempty"`
		Email    *core.DurableAgentEmailChannelConfig    `json:"email,omitempty"`
		Inbox    *core.DurableAgentEmailChannelConfig    `json:"inbox,omitempty"`
	}
	var updateRaw channelConfigInput
	if err := json.Unmarshal(raw, &updateRaw); err != nil {
		return core.DurableAgentChannelConfig{}, fmt.Errorf("decode durable_agent channel_config: %w", err)
	}
	update := core.DurableAgentChannelConfig{}
	switch {
	case updateRaw.External != nil:
		cfg := *updateRaw.External
		update.External = &cfg
	case updateRaw.Channel != nil:
		cfg := *updateRaw.Channel
		update.External = &cfg
	case updateRaw.Email != nil:
		cfg := *updateRaw.Email
		update.External = &cfg
	case updateRaw.Inbox != nil:
		cfg := *updateRaw.Inbox
		update.External = &cfg
	}
	update = core.NormalizeDurableAgentChannelConfig(update)
	if external := update.ExternalConfig(); external != nil {
		if existingExternal := existing.ExternalConfig(); existingExternal == nil {
			cfg := *external
			existing.External = &cfg
			existing.Email = nil
		} else {
			cfg := *existingExternal
			mergeDurableAgentExternalChannelConfig(&cfg, *external)
			existing.External = &cfg
			existing.Email = nil
		}
	}
	return core.NormalizeDurableAgentChannelConfig(existing), nil
}

func mergeDurableAgentExternalChannelConfig(dst *core.DurableAgentExternalChannelConfig, src core.DurableAgentExternalChannelConfig) {
	if dst == nil {
		return
	}
	if strings.TrimSpace(src.Address) != "" {
		dst.Address = strings.TrimSpace(src.Address)
	}
	if strings.TrimSpace(src.Account) != "" {
		dst.Account = strings.TrimSpace(src.Account)
	}
	if strings.TrimSpace(src.Adapter) != "" {
		dst.Adapter = strings.TrimSpace(src.Adapter)
	}
	if strings.TrimSpace(src.Query) != "" {
		dst.Query = strings.TrimSpace(src.Query)
	}
	if strings.TrimSpace(src.PollInterval) != "" {
		dst.PollInterval = strings.TrimSpace(src.PollInterval)
	}
	if len(src.SurfaceRules) > 0 {
		dst.SurfaceRules = append([]string(nil), src.SurfaceRules...)
	}
	if src.SummarizePDFs {
		dst.SummarizePDFs = true
	}
	if strings.TrimSpace(src.SynthesisCadence) != "" {
		dst.SynthesisCadence = strings.TrimSpace(src.SynthesisCadence)
	}
	if len(src.NeverRetain) > 0 {
		dst.NeverRetain = append([]string(nil), src.NeverRetain...)
	}
}

func validateDurableAgentActivation(agent core.DurableAgent) error {
	switch normalizeDurableAgentChannelKind(agent.ChannelKind) {
	case "external_channel":
		external := agent.ChannelConfig.ExternalConfig()
		if external == nil {
			return fmt.Errorf("durable agent %q cannot activate without external channel_config", agent.AgentID)
		}
		if strings.TrimSpace(external.Address) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a channel address", agent.AgentID)
		}
		if strings.TrimSpace(external.Adapter) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a channel adapter", agent.AgentID)
		}
		if strings.TrimSpace(agent.WakeupMode) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a wakeup_mode", agent.AgentID)
		}
	}
	return nil
}

func (r *Registry) inheritDurableAgentBootstrapIfMissing(agent *core.DurableAgent) {
	if r == nil || agent == nil {
		return
	}
	if core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM).Configured() {
		return
	}
	inherited := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	if !inherited.Configured() {
		return
	}
	agent.BootstrapLLM = inherited
}

func durableAgentAutonomyFromPolicy(policy core.DurableAgentLivePolicy) string {
	switch strings.TrimSpace(policy.OutboundMode) {
	case "read_only":
		return "observe_only"
	case "draft_only":
		return "local_drafts"
	case "reply_with_parent_review":
		return "review_before_reply"
	case "reply_with_policy_authorization":
		return "reply_within_charter"
	default:
		return ""
	}
}

func durableAgentVisibilityFromPolicy(policy core.DurableAgentLivePolicy) string {
	switch strings.TrimSpace(policy.PublicSurfaceMode) {
	case "none":
		return "private"
	case "explicit_parent_relay_only":
		return "parent_relay_only"
	case "channel_transcript":
		return "public_channel"
	default:
		return ""
	}
}

func durableAgentSharedContextFromPolicy(policy core.DurableAgentLivePolicy) string {
	if strings.TrimSpace(policy.SharedInferenceReuse) == "allowed" {
		return "public_only"
	}
	return "isolated"
}
