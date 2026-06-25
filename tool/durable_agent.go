//go:build linux

package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
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
	case "park":
		return r.parkDurableAgent(in, key)
	case "resume":
		return r.resumeDurableAgent(in, key)
	case "retire":
		return r.retireDurableAgent(in, key)
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
	case "wake_once":
		return r.wakeDurableAgentOnce(ctx, in, input, p, key)
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
		return "", fmt.Errorf("durable_agent action must be one of list|create|create_from_archetype|activate|park|resume|retire|connection_test|policy_show|bootstrap_show|policy_apply|bootstrap_update|enrollment_show|enrollment_update|wizard_start|wizard_answer|wizard_show|wizard_finalize|wizard_cancel|archetype_list|archetype_show|access_show|access_grant|access_revoke|conversation_show|conversation_send|wake_once|delegation_request|delegation_report|memory_review|memory_delegate|profile_show|profile_apply|artifact_put|artifact_list|artifact_show|snapshot_create|snapshot_list|snapshot_restore")
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
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(policy)
	policy = agent.LivePolicy

	reason := strings.TrimSpace(in.Reason)
	if reason == "" && in.ReviewEventID > 0 {
		reason = fmt.Sprintf("ratified from review_event=%d", in.ReviewEventID)
	}
	updated, update, err := r.store.ApplyDurableAgentLivePolicy(agent.AgentID, policy, in.ReviewEventID, reason)
	if err != nil {
		return "", err
	}
	if updated != nil {
		beforeWakeup := strings.TrimSpace(updated.WakeupMode)
		applyDurableAgentModeRuntimeDefaults(updated)
		if strings.TrimSpace(updated.WakeupMode) != beforeWakeup {
			if err := r.store.UpsertDurableAgent(*updated); err != nil {
				return "", err
			}
			refreshed, err := r.store.DurableAgent(updated.AgentID)
			if err != nil {
				return "", err
			}
			updated = refreshed
		}
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
	if err := core.ValidateDurableAgentID(agentID); err != nil {
		return "", fmt.Errorf("durable_agent create: %w", err)
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
		strings.TrimSpace(policy.Mode) == "" &&
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
	applyDurableAgentModeRuntimeDefaults(&agent)

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
