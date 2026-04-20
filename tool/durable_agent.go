//go:build linux

package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) durableAgent(ctx context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
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
	case "policy_apply":
		return r.applyDurableAgentPolicy(in)
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
	case "access_show":
		return r.showDurableAgentAccess(in)
	case "access_grant":
		return r.grantDurableAgentAccess(in)
	case "access_revoke":
		return r.revokeDurableAgentAccess(in)
	case "capacity_show":
		return r.showDurableAgentCapacity(in)
	case "capacity_negotiate":
		return r.negotiateDurableAgentCapacity(in)
	case "capacity_probe":
		return r.probeDurableAgentCapacity(in)
	case "capacity_attest":
		return r.attestDurableAgentCapacity(in)
	case "conversation_show":
		return r.showDurableAgentConversation(in)
	case "conversation_send":
		return r.sendDurableAgentConversation(in)
	default:
		return "", fmt.Errorf("durable_agent action must be one of list|create|activate|connection_test|policy_show|policy_apply|enrollment_show|enrollment_update|wizard_start|wizard_answer|wizard_show|wizard_finalize|wizard_cancel|access_show|access_grant|access_revoke|capacity_show|capacity_negotiate|capacity_probe|capacity_attest|conversation_show|conversation_send")
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
	if err := r.markDurableAgentCapacityStale(agent.AgentID); err != nil {
		return "", err
	}
	return renderDurableAgentPolicyApply(*updated, update), nil
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
	} else if strings.TrimSpace(agent.WakeupMode) == "" && agent.ChannelKind == "email" {
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
	agent.Status = "draft"

	if err := r.store.UpsertDurableAgent(agent); err != nil {
		return "", err
	}
	updated, err := r.store.DurableAgent(agent.AgentID)
	if err != nil {
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
	agent.Status = "active"
	if err := r.store.UpsertDurableAgent(*agent); err != nil {
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
		channelKind = "inbox"
	}
	channelKind = normalizeDurableAgentChannelKind(channelKind)
	if channelKind != "email" {
		return "", fmt.Errorf("durable_agent wizard_start currently supports channel_kind=inbox (alias: email)")
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
	now := time.Now().UTC()
	wizard := seedDurableAgentWizardFromAgent(*agent)
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
	missing := durableAgentWizardMissingAnswers(wizard)
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
	return renderDurableAgentWizardShow(*agent, wizard), nil
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

	wizard := *continuity.SetupWizard
	wizard.SchemaVersion = 1
	wizard.ChannelKind = firstNonEmpty(
		strings.TrimSpace(wizard.ChannelKind),
		normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind)),
		"email",
	)
	wizard.Answers = mergeDurableAgentWizardAnswers(wizard.Answers, *in.WizardAnswers)
	wizard.UpdatedAt = time.Now().UTC()
	if wizard.StartedAt.IsZero() {
		wizard.StartedAt = wizard.UpdatedAt
	}
	missing := durableAgentWizardMissingAnswers(wizard)
	wizard.Missing = missing
	wizard.CurrentStep = firstWizardStep(missing)
	if len(missing) == 0 {
		wizard.Status = "ready"
	} else {
		wizard.Status = "in_progress"
	}

	updatedAgent, err := applyDurableWizardAnswersToAgent(*agent, wizard.Answers)
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
	return renderDurableAgentWizardShow(*agent, wizard), nil
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
	wizard := *continuity.SetupWizard
	wizard.Missing = durableAgentWizardMissingAnswers(wizard)
	wizard.CurrentStep = firstWizardStep(wizard.Missing)
	if wizard.Status == "" {
		if len(wizard.Missing) == 0 {
			wizard.Status = "ready"
		} else {
			wizard.Status = "in_progress"
		}
	}
	return renderDurableAgentWizardShow(*agent, wizard), nil
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
	wizard := *continuity.SetupWizard
	wizard.Missing = durableAgentWizardMissingAnswers(wizard)
	if len(wizard.Missing) > 0 {
		return "", fmt.Errorf("missing wizard answers: %s", strings.Join(wizard.Missing, ", "))
	}
	wizard.CurrentStep = ""
	wizard.Status = "finalized"
	wizard.UpdatedAt = time.Now().UTC()

	updatedAgent, err := applyDurableWizardAnswersToAgent(*agent, wizard.Answers)
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
	if err := r.store.UpsertDurableAgent(updatedAgent); err != nil {
		return "", err
	}

	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardFinalize(updatedAgent, wizard), nil
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
	wizard := *continuity.SetupWizard
	wizard.Status = "cancelled"
	wizard.CurrentStep = ""
	wizard.Missing = nil
	wizard.UpdatedAt = time.Now().UTC()
	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardShow(*agent, wizard), nil
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
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for connection_test")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	switch normalizeDurableAgentChannelKind(agent.ChannelKind) {
	case "email":
		if agent.ChannelConfig.Email == nil {
			return "", fmt.Errorf("durable agent %q has no email channel_config", agent.AgentID)
		}
		args := []string{"gog"}
		if strings.TrimSpace(agent.ChannelConfig.Email.Account) != "" {
			args = append(args, "--account", strings.TrimSpace(agent.ChannelConfig.Email.Account))
		}
		args = append(args, "gmail", "search", firstNonEmpty(strings.TrimSpace(agent.ChannelConfig.Email.Query), "label:inbox"), "--json", "--results-only", "--max", "1", "--no-input")
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("durable agent connection_test failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("action: durable-agent connection test\nagent_id: %s\nchannel_kind: %s\nstatus: ok\n", agent.AgentID, agent.ChannelKind), nil
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

func (r *Registry) showDurableAgentCapacity(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for capacity_show")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	_, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	return renderDurableAgentCapacity("show", *agent, durableAgentCapacityContractFromContinuity(continuity)), nil
}

func (r *Registry) negotiateDurableAgentCapacity(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for capacity_negotiate")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	contract := durableAgentCapacityContractFromContinuity(continuity)
	contract = mergeDurableAgentCapacityContract(contract, in.CapacityContract)
	contract.LastNegotiatedAt = time.Now().UTC()
	switch {
	case durableAgentCapacityContractReadyForAttestation(contract):
		contract.Status = "provisional"
	default:
		contract.Status = "unattested"
	}
	continuity.CapabilityContract = &contract
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentCapacity("negotiate", *agent, contract), nil
}

func (r *Registry) probeDurableAgentCapacity(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for capacity_probe")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.CapabilityContract == nil {
		return "", fmt.Errorf("durable agent %q has no capability contract; use capacity_negotiate first", agent.AgentID)
	}
	contract := mergeDurableAgentCapacityContract(*continuity.CapabilityContract, in.CapacityContract)
	contract.LastProbedAt = time.Now().UTC()
	if contract.Status == "" || contract.Status == "unattested" || contract.Status == "stale" {
		if durableAgentCapacityContractReadyForAttestation(contract) {
			contract.Status = "provisional"
		} else {
			contract.Status = "unattested"
		}
	}
	continuity.CapabilityContract = &contract
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentCapacity("probe", *agent, contract), nil
}

func (r *Registry) attestDurableAgentCapacity(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for capacity_attest")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.CapabilityContract == nil {
		return "", fmt.Errorf("durable agent %q has no capability contract; use capacity_negotiate first", agent.AgentID)
	}
	contract := mergeDurableAgentCapacityContract(*continuity.CapabilityContract, in.CapacityContract)
	if !durableAgentCapacityContractReadyForAttestation(contract) {
		return "", fmt.Errorf("durable agent %q capability contract is incomplete; include child_self_assessment, success_criteria, and evidence_signals", agent.AgentID)
	}
	desiredState := "verified"
	if in.CapacityContract != nil {
		if value := normalizeDurableAgentCapacityContractState(in.CapacityContract.Status); value != "" {
			desiredState = value
		}
	}
	switch desiredState {
	case "verified", "stale":
	default:
		return "", fmt.Errorf("capacity_attest status must be verified or stale")
	}
	if desiredState == "verified" {
		if len(contract.ProbeResults) == 0 || contract.LastProbedAt.IsZero() {
			return "", fmt.Errorf("durable agent %q capability contract requires probe_results and last_probed_at before verified attestation", agent.AgentID)
		}
	}
	contract.Status = desiredState
	contract.LastAttestedAt = time.Now().UTC()
	continuity.CapabilityContract = &contract
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentCapacity("attest", *agent, contract), nil
}

func (r *Registry) markDurableAgentCapacityStale(agentID string) error {
	state, continuity, err := r.loadDurableAgentContinuity(agentID)
	if err != nil {
		return err
	}
	if continuity.CapabilityContract == nil {
		return nil
	}
	contract := *continuity.CapabilityContract
	if strings.TrimSpace(contract.Status) != "verified" {
		return nil
	}
	contract.Status = "stale"
	continuity.CapabilityContract = &contract
	return r.saveDurableAgentContinuity(state, continuity)
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

func durableAgentCapacityContractReadyForAttestation(contract core.DurableAgentCapabilityContract) bool {
	return strings.TrimSpace(contract.ChildSelfAssessment) != "" &&
		len(contract.SuccessCriteria) > 0 &&
		len(contract.EvidenceSignals) > 0
}

func durableAgentCapacityContractFromContinuity(continuity core.DurableAgentContinuityState) core.DurableAgentCapabilityContract {
	if continuity.CapabilityContract == nil {
		return core.DurableAgentCapabilityContract{Status: "unattested"}
	}
	contract := *continuity.CapabilityContract
	if strings.TrimSpace(contract.Status) == "" {
		contract.Status = "unattested"
	}
	return contract
}

func mergeDurableAgentCapacityContract(current core.DurableAgentCapabilityContract, patch *durableAgentCapacityContractInput) core.DurableAgentCapabilityContract {
	if patch == nil {
		if strings.TrimSpace(current.Status) == "" {
			current.Status = "unattested"
		}
		return current
	}
	if status := normalizeDurableAgentCapacityContractState(patch.Status); status != "" {
		current.Status = status
	}
	if strings.TrimSpace(patch.ParentProposal) != "" {
		current.ParentProposal = strings.TrimSpace(patch.ParentProposal)
	}
	if strings.TrimSpace(patch.ChildSelfAssessment) != "" {
		current.ChildSelfAssessment = strings.TrimSpace(patch.ChildSelfAssessment)
	}
	if patch.Can != nil {
		current.Can = normalizePolicyCapabilities(patch.Can)
	}
	if patch.Cannot != nil {
		current.Cannot = normalizePolicyCapabilities(patch.Cannot)
	}
	if patch.Uncertain != nil {
		current.Uncertain = normalizePolicyCapabilities(patch.Uncertain)
	}
	if patch.SuccessCriteria != nil {
		current.SuccessCriteria = normalizePolicyCapabilities(patch.SuccessCriteria)
	}
	if patch.EvidenceSignals != nil {
		current.EvidenceSignals = normalizePolicyCapabilities(patch.EvidenceSignals)
	}
	if patch.ProbeChecklist != nil {
		current.ProbeChecklist = normalizePolicyCapabilities(patch.ProbeChecklist)
	}
	if patch.ProbeResults != nil {
		current.ProbeResults = normalizePolicyCapabilities(patch.ProbeResults)
	}
	if strings.TrimSpace(current.Status) == "" {
		current.Status = "unattested"
	}
	return current
}

func normalizeDurableAgentCapacityContractState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "unattested":
		return "unattested"
	case "provisional":
		return "provisional"
	case "verified":
		return "verified"
	case "stale":
		return "stale"
	default:
		return ""
	}
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

func seedDurableAgentWizardFromAgent(agent core.DurableAgent) core.DurableAgentSetupWizardState {
	wizard := core.DurableAgentSetupWizardState{
		SchemaVersion: 1,
		ChannelKind:   strings.TrimSpace(agent.ChannelKind),
		Answers: core.DurableAgentSetupWizardAnswers{
			Charter:      strings.TrimSpace(agent.LivePolicy.Charter),
			Autonomy:     durableAgentAutonomyFromPolicy(agent.LivePolicy),
			WakeupMode:   strings.TrimSpace(agent.WakeupMode),
			Capabilities: append([]string(nil), agent.LivePolicy.CapabilityEnvelope...),
			DriftPolicy:  strings.TrimSpace(agent.LivePolicy.DriftPolicy),
		},
	}
	if agent.ChannelConfig.Email != nil {
		email := agent.ChannelConfig.Email
		wizard.Answers.Address = strings.TrimSpace(email.Address)
		wizard.Answers.Account = strings.TrimSpace(email.Account)
		wizard.Answers.Adapter = strings.TrimSpace(email.Adapter)
		wizard.Answers.Query = strings.TrimSpace(email.Query)
		wizard.Answers.PollInterval = strings.TrimSpace(email.PollInterval)
		wizard.Answers.SurfaceRules = append([]string(nil), email.SurfaceRules...)
		value := email.SummarizePDFs
		wizard.Answers.SummarizePDFs = &value
		wizard.Answers.SynthesisCadence = strings.TrimSpace(email.SynthesisCadence)
		wizard.Answers.NeverRetain = append([]string(nil), email.NeverRetain...)
	}
	return wizard
}

func mergeDurableAgentWizardAnswers(current core.DurableAgentSetupWizardAnswers, patch durableAgentWizardAnswersInput) core.DurableAgentSetupWizardAnswers {
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

func applyDurableWizardAnswersToAgent(agent core.DurableAgent, answers core.DurableAgentSetupWizardAnswers) (core.DurableAgent, error) {
	answers = core.NormalizeDurableAgentSetupWizardAnswers(answers)
	agent.ChannelKind = normalizeDurableAgentChannelKind("inbox")
	wakeupMode := normalizeDurableEmailWakeupMode(answers.WakeupMode)
	if wakeupMode == "" && strings.TrimSpace(agent.WakeupMode) != "" {
		wakeupMode = normalizeDurableEmailWakeupMode(agent.WakeupMode)
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
		policy = defaultDurableAgentLivePolicy("email", patch.Charter)
	}
	if err := applyDurableAgentPolicyPatch(&policy, patch); err != nil {
		return core.DurableAgent{}, err
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(policy)

	channelConfig := core.NormalizeDurableAgentChannelConfig(agent.ChannelConfig)
	if channelConfig.Email == nil {
		channelConfig.Email = &core.DurableAgentEmailChannelConfig{}
	}
	email := channelConfig.Email
	if answers.Address != "" {
		email.Address = answers.Address
	}
	if answers.Account != "" {
		email.Account = answers.Account
	} else if strings.TrimSpace(email.Account) == "" && strings.TrimSpace(email.Address) != "" {
		email.Account = strings.TrimSpace(email.Address)
	}
	if answers.Adapter != "" {
		email.Adapter = answers.Adapter
	}
	if answers.Query != "" {
		email.Query = answers.Query
	} else if strings.TrimSpace(email.Query) == "" {
		email.Query = "label:inbox"
	}
	if answers.PollInterval != "" {
		email.PollInterval = answers.PollInterval
	}
	if answers.SurfaceRules != nil {
		email.SurfaceRules = append([]string(nil), answers.SurfaceRules...)
	}
	if answers.SummarizePDFs != nil {
		email.SummarizePDFs = *answers.SummarizePDFs
	}
	if answers.SynthesisCadence != "" {
		email.SynthesisCadence = answers.SynthesisCadence
	}
	if answers.NeverRetain != nil {
		email.NeverRetain = append([]string(nil), answers.NeverRetain...)
	}
	channelConfig.Email = email
	agent.ChannelConfig = core.NormalizeDurableAgentChannelConfig(channelConfig)
	if strings.TrimSpace(agent.Status) == "" {
		agent.Status = "draft"
	}
	return agent, nil
}

func durableAgentWizardMissingAnswers(wizard core.DurableAgentSetupWizardState) []string {
	answers := core.NormalizeDurableAgentSetupWizardAnswers(wizard.Answers)
	missing := make([]string, 0, len(durableAgentWizardStepOrder))
	if strings.TrimSpace(answers.Address) == "" {
		missing = append(missing, "address")
	}
	if strings.TrimSpace(answers.Adapter) == "" {
		missing = append(missing, "adapter")
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
	mode := normalizeDurableEmailWakeupMode(answers.WakeupMode)
	if mode == "" {
		missing = append(missing, "wakeup_mode")
	} else if durableEmailWakeupModeIncludesPoll(mode) && strings.TrimSpace(answers.PollInterval) == "" {
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

func wizardQuestionForStep(step string) string {
	switch strings.TrimSpace(step) {
	case "address":
		return "What channel address should this child own?"
	case "adapter":
		return "Which channel adapter should be used (for example gog_cli for inbox/email)?"
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

func normalizeDurableEmailWakeupMode(value string) string {
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

func durableEmailWakeupModeIncludesPoll(mode string) bool {
	mode = normalizeDurableEmailWakeupMode(mode)
	return mode == "poll" || mode == "poll_or_push"
}

func normalizeDurableAgentChannelKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "inbox", "email":
		return "email"
	default:
		return strings.TrimSpace(value)
	}
}

func durableAgentWizardDisplayChannelKind(value string) string {
	switch normalizeDurableAgentChannelKind(value) {
	case "email":
		return "inbox"
	default:
		return strings.TrimSpace(value)
	}
}

func renderDurableAgentWizardShow(agent core.DurableAgent, wizard core.DurableAgentSetupWizardState) string {
	var b strings.Builder
	channelKind := normalizeDurableAgentChannelKind(firstNonEmpty(strings.TrimSpace(wizard.ChannelKind), strings.TrimSpace(agent.ChannelKind), "email"))
	b.WriteString("action: durable-agent wizard show\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "wizard_status: %s\n", firstNonEmpty(strings.TrimSpace(wizard.Status), "in_progress"))
	fmt.Fprintf(&b, "current_step: %s\n", firstNonEmpty(strings.TrimSpace(wizard.CurrentStep), "-"))
	fmt.Fprintf(&b, "missing: %s\n", firstNonEmpty(strings.Join(wizard.Missing, ","), "-"))
	if question := wizardQuestionForStep(wizard.CurrentStep); question != "" {
		fmt.Fprintf(&b, "next_question: %s\n", question)
	}
	fmt.Fprintf(&b, "address: %s\n", strings.TrimSpace(wizard.Answers.Address))
	fmt.Fprintf(&b, "adapter: %s\n", strings.TrimSpace(wizard.Answers.Adapter))
	fmt.Fprintf(&b, "autonomy: %s\n", strings.TrimSpace(wizard.Answers.Autonomy))
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(wizard.Answers.WakeupMode))
	fmt.Fprintf(&b, "poll_interval: %s\n", strings.TrimSpace(wizard.Answers.PollInterval))
	fmt.Fprintf(&b, "synthesis_cadence: %s\n", strings.TrimSpace(wizard.Answers.SynthesisCadence))
	fmt.Fprintf(&b, "charter: %s\n", strings.TrimSpace(wizard.Answers.Charter))
	return b.String()
}

func renderDurableAgentWizardFinalize(agent core.DurableAgent, wizard core.DurableAgentSetupWizardState) string {
	var b strings.Builder
	channelKind := normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind))
	b.WriteString("action: durable-agent wizard finalize\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "draft"))
	fmt.Fprintf(&b, "wizard_status: %s\n", firstNonEmpty(strings.TrimSpace(wizard.Status), "finalized"))
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
	if update.SourceReviewEventID > 0 {
		fmt.Fprintf(&b, "source_review_event_id: %d\n", update.SourceReviewEventID)
	}
	if strings.TrimSpace(update.Reason) != "" {
		fmt.Fprintf(&b, "reason: %s\n", update.Reason)
	}
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

func renderDurableAgentCapacity(action string, agent core.DurableAgent, contract core.DurableAgentCapabilityContract) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent capacity %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "capacity_state: %s\n", firstNonEmpty(strings.TrimSpace(contract.Status), "unattested"))
	fmt.Fprintf(&b, "parent_proposal: %s\n", strings.TrimSpace(contract.ParentProposal))
	fmt.Fprintf(&b, "child_self_assessment: %s\n", strings.TrimSpace(contract.ChildSelfAssessment))
	fmt.Fprintf(&b, "can: %s\n", firstNonEmpty(strings.Join(contract.Can, ","), "-"))
	fmt.Fprintf(&b, "cannot: %s\n", firstNonEmpty(strings.Join(contract.Cannot, ","), "-"))
	fmt.Fprintf(&b, "uncertain: %s\n", firstNonEmpty(strings.Join(contract.Uncertain, ","), "-"))
	fmt.Fprintf(&b, "success_criteria: %s\n", firstNonEmpty(strings.Join(contract.SuccessCriteria, ","), "-"))
	fmt.Fprintf(&b, "evidence_signals: %s\n", firstNonEmpty(strings.Join(contract.EvidenceSignals, ","), "-"))
	fmt.Fprintf(&b, "probe_checklist: %s\n", firstNonEmpty(strings.Join(contract.ProbeChecklist, ","), "-"))
	fmt.Fprintf(&b, "probe_results: %s\n", firstNonEmpty(strings.Join(contract.ProbeResults, ","), "-"))
	if !contract.LastNegotiatedAt.IsZero() {
		fmt.Fprintf(&b, "last_negotiated_at: %s\n", contract.LastNegotiatedAt.UTC().Format(time.RFC3339))
	}
	if !contract.LastProbedAt.IsZero() {
		fmt.Fprintf(&b, "last_probed_at: %s\n", contract.LastProbedAt.UTC().Format(time.RFC3339))
	}
	if !contract.LastAttestedAt.IsZero() {
		fmt.Fprintf(&b, "last_attested_at: %s\n", contract.LastAttestedAt.UTC().Format(time.RFC3339))
	}
	switch strings.TrimSpace(contract.Status) {
	case "unattested":
		b.WriteString("next: capacity_negotiate\n")
	case "provisional":
		b.WriteString("next: capacity_probe or capacity_attest\n")
	case "verified":
		b.WriteString("next: monitor and re-attest after policy drift\n")
	case "stale":
		b.WriteString("next: capacity_negotiate then capacity_attest\n")
	}
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
	fmt.Fprintf(&b, "messages: %d\n", total)
	fmt.Fprintf(&b, "pending_parent_messages: %d\n", len(continuity.PendingParentConversationMessages(0)))
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
	if b == nil || agent.ChannelConfig.Email == nil {
		return
	}
	email := agent.ChannelConfig.Email
	fmt.Fprintf(b, "channel_address: %s\n", strings.TrimSpace(email.Address))
	fmt.Fprintf(b, "channel_account: %s\n", strings.TrimSpace(email.Account))
	fmt.Fprintf(b, "channel_adapter: %s\n", strings.TrimSpace(email.Adapter))
	fmt.Fprintf(b, "channel_query: %s\n", strings.TrimSpace(email.Query))
	fmt.Fprintf(b, "channel_poll_interval: %s\n", strings.TrimSpace(email.PollInterval))
	fmt.Fprintf(b, "channel_summarize_pdfs: %t\n", email.SummarizePDFs)
	fmt.Fprintf(b, "channel_synthesis_cadence: %s\n", strings.TrimSpace(email.SynthesisCadence))
	fmt.Fprintf(b, "channel_surface_rules: %s\n", strings.Join(email.SurfaceRules, ","))
	fmt.Fprintf(b, "channel_never_retain: %s\n", strings.Join(email.NeverRetain, ","))
	fmt.Fprintf(b, "email_address: %s\n", strings.TrimSpace(email.Address))
	fmt.Fprintf(b, "email_account: %s\n", strings.TrimSpace(email.Account))
	fmt.Fprintf(b, "email_adapter: %s\n", strings.TrimSpace(email.Adapter))
	fmt.Fprintf(b, "email_query: %s\n", strings.TrimSpace(email.Query))
	fmt.Fprintf(b, "email_poll_interval: %s\n", strings.TrimSpace(email.PollInterval))
	fmt.Fprintf(b, "email_summarize_pdfs: %t\n", email.SummarizePDFs)
	fmt.Fprintf(b, "email_synthesis_cadence: %s\n", strings.TrimSpace(email.SynthesisCadence))
	fmt.Fprintf(b, "email_surface_rules: %s\n", strings.Join(email.SurfaceRules, ","))
	fmt.Fprintf(b, "email_never_retain: %s\n", strings.Join(email.NeverRetain, ","))
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
	case "email":
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
		Email *core.DurableAgentEmailChannelConfig `json:"email,omitempty"`
		Inbox *core.DurableAgentEmailChannelConfig `json:"inbox,omitempty"`
	}
	var updateRaw channelConfigInput
	if err := json.Unmarshal(raw, &updateRaw); err != nil {
		return core.DurableAgentChannelConfig{}, fmt.Errorf("decode durable_agent channel_config: %w", err)
	}
	update := core.DurableAgentChannelConfig{}
	switch {
	case updateRaw.Email != nil:
		cfg := *updateRaw.Email
		update.Email = &cfg
	case updateRaw.Inbox != nil:
		cfg := *updateRaw.Inbox
		update.Email = &cfg
	}
	update = core.NormalizeDurableAgentChannelConfig(update)
	if update.Email != nil {
		if existing.Email == nil {
			cfg := *update.Email
			existing.Email = &cfg
		} else {
			mergeDurableAgentEmailChannelConfig(existing.Email, *update.Email)
		}
	}
	return core.NormalizeDurableAgentChannelConfig(existing), nil
}

func mergeDurableAgentEmailChannelConfig(dst *core.DurableAgentEmailChannelConfig, src core.DurableAgentEmailChannelConfig) {
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
	case "email":
		email := agent.ChannelConfig.Email
		if email == nil {
			return fmt.Errorf("durable agent %q cannot activate without inbox/email channel_config", agent.AgentID)
		}
		if strings.TrimSpace(email.Address) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a channel address", agent.AgentID)
		}
		if strings.TrimSpace(email.Adapter) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a channel adapter", agent.AgentID)
		}
		if strings.TrimSpace(agent.WakeupMode) == "" {
			return fmt.Errorf("durable agent %q cannot activate without a wakeup_mode", agent.AgentID)
		}
	}
	return nil
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
