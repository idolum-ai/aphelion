//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) recoverScheduledExternalChannelReadiness(ctx context.Context, agent core.DurableAgent, now time.Time) (recovered bool, handled bool, err error) {
	if r == nil || r.store == nil {
		return false, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	due, err := r.externalChannelReadinessRecoveryDue(agent, now)
	if err != nil || !due {
		return false, false, err
	}
	readiness := r.externalChannelReadinessForAgent(agent, now)
	if readiness.Status != externalChannelReadinessStatusBlocked {
		return false, false, nil
	}

	if readiness.RepairKind == externalChannelReadinessRepairToolLifecycleVerify {
		if verifyErr := r.verifyExternalChannelToolLifecycle(ctx, agent, readiness, now); verifyErr == nil {
			if err := r.clearExternalChannelReadinessBackoff(agent, readiness.Adapter); err != nil {
				return false, false, err
			}
			return true, false, nil
		} else {
			readiness.NextRepair = strings.TrimSpace(readiness.NextRepair + "; verifier rejected existing evidence: " + truncateRunes(verifyErr.Error(), 240))
		}
	}

	handled, err = r.recordDurableWakeChildRuntimeBlock(agent, externalChannelReadinessPreflightError{Readiness: readiness}, now)
	return false, handled, err
}

func (r *Runtime) externalChannelReadinessRecoveryDue(agent core.DurableAgent, now time.Time) (bool, error) {
	adapterName, pollInterval, ok := durableWakeExternalBackoffIdentity(agent)
	if !ok {
		return false, nil
	}
	if strings.TrimSpace(agent.ChannelKind) == scheduledReviewChannelKind && agent.ChannelConfig.ScheduledReviewConfig() != nil {
		return false, nil
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil || strings.TrimSpace(external.Adapter) == "" {
		return false, nil
	}
	pending, err := r.pendingDurableAgentParentConversation(agent.AgentID, 1)
	if err != nil {
		return false, err
	}
	if len(pending) > 0 {
		return true, nil
	}
	_, continuity, err := loadDurableAgentContinuityFromStore(r.store, agent.AgentID)
	if err != nil {
		return false, err
	}
	runtimeState := externalChannelStateForAdapter(continuity, adapterName)
	if externalChannelWakeBackoffHasActionableReadiness(runtimeState, now) {
		return true, nil
	}
	return externalChannelPollDueIgnoringBackoff(runtimeState, pollInterval, now), nil
}

func externalChannelWakeBackoffHasActionableReadiness(state core.DurableAgentExternalChannelRuntimeState, now time.Time) bool {
	if state.BackoffUntil.IsZero() || !now.UTC().Before(state.BackoffUntil.UTC()) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(state.LastStatus), "wake_blocked") {
		return false
	}
	lastErr := strings.ToLower(strings.TrimSpace(state.LastError))
	return strings.Contains(lastErr, "child_runtime_blocked") || strings.Contains(lastErr, "preflight_failed")
}

func (r *Runtime) verifyExternalChannelToolLifecycle(ctx context.Context, agent core.DurableAgent, readiness externalChannelAdapterReadiness, _ time.Time) error {
	if r == nil || r.tools == nil {
		return fmt.Errorf("tool registry unavailable")
	}
	executor, ok := r.tools.(sessionAwareToolExecutor)
	if !ok {
		return fmt.Errorf("tool registry does not support session-aware execution")
	}
	toolName := strings.TrimSpace(readiness.Adapter)
	if toolName == "" {
		toolName = externalChannelAdapter(agent)
	}
	if toolName == "" {
		return fmt.Errorf("external channel adapter is not configured")
	}
	input, err := json.Marshal(map[string]string{
		"action":    "install_set",
		"tool_name": toolName,
		"status":    string(session.ToolInstallStatusVerified),
		"installer": "aphelion",
	})
	if err != nil {
		return err
	}
	key := externalChannelReadinessRepairSessionKey(agent)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: key.UserID}
	_, err = executor.ExecuteForSessionPrincipal(ctx, actor, key, "tool_authority", json.RawMessage(input))
	if err != nil {
		return err
	}
	return nil
}

func externalChannelReadinessRepairSessionKey(agent core.DurableAgent) session.SessionKey {
	chatID := agent.ReviewTargetChatID
	if chatID == 0 {
		chatID = durableWakeSyntheticChatID(agent.AgentID)
	}
	scopeID := strings.TrimSpace(agent.ParentScopeID)
	if scopeID == "" {
		scopeID = fmt.Sprintf("%d", chatID)
	}
	scopeKind := session.ScopeKindTelegramDM
	if strings.TrimSpace(agent.ParentScopeKind) != "" {
		scopeKind = session.ScopeKind(strings.TrimSpace(agent.ParentScopeKind))
	}
	return session.SessionKey{
		ChatID: chatID,
		UserID: chatID,
		Scope:  session.ScopeRef{Kind: scopeKind, ID: scopeID},
	}
}

func (r *Runtime) clearExternalChannelReadinessBackoff(agent core.DurableAgent, adapterName string) error {
	if r == nil || r.store == nil {
		return nil
	}
	state, continuity, err := loadDurableAgentContinuityFromStore(r.store, agent.AgentID)
	if err != nil {
		return err
	}
	runtimeState := externalChannelStateForAdapter(continuity, adapterName)
	runtimeState.BackoffUntil = time.Time{}
	runtimeState.FailureCount = 0
	if strings.EqualFold(strings.TrimSpace(runtimeState.LastStatus), "wake_blocked") {
		runtimeState.LastStatus = "readiness_repaired"
		runtimeState.LastError = ""
		runtimeState.LastErrorAt = time.Time{}
	}
	continuity.ExternalChannel = encodeGenericExternalChannelState(runtimeState, adapterName)
	raw, err := continuity.Marshal()
	if err != nil {
		return err
	}
	state.StateJSON = raw
	return r.store.SaveDurableAgentState(*state)
}
