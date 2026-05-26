//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"log"
	"strconv"
	"strings"
	"time"
)

func (c telegramCommandControl) RunDurableWizard(ctx context.Context, chatID int64, senderID int64, action string, agentID string, wizardAnswers map[string]any) (string, error) {
	if c.durableTools == nil {
		return "", fmt.Errorf("durable wizard controls are unavailable")
	}
	if !c.CanRestart(senderID) {
		return "", fmt.Errorf("durable wizard controls are admin only")
	}

	actor := principal.Principal{
		Role:           principal.RoleAdmin,
		TelegramUserID: senderID,
	}
	if c.resolver != nil {
		resolved, ok := c.resolver.ResolveTelegramUser(senderID)
		if !ok || resolved.Role != principal.RoleAdmin {
			return "", fmt.Errorf("durable wizard controls are admin only")
		}
		actor = resolved
	}

	request := map[string]any{
		"action":   strings.TrimSpace(action),
		"agent_id": strings.TrimSpace(agentID),
	}
	if len(wizardAnswers) > 0 {
		request["wizard_answers"] = wizardAnswers
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	key := session.SessionKey{
		ChatID: chatID,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   strconv.FormatInt(chatID, 10),
		},
	}
	return c.durableTools.ExecuteForSessionPrincipal(ctx, actor, key, "durable_agent", payload)
}

func (c telegramCommandControl) DurableAgentsList(senderID int64) ([]core.DurableAgentStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return nil, fmt.Errorf("durable-agent controls are admin only")
	}
	if c.rt == nil {
		return nil, nil
	}
	snapshot, err := c.rt.DurableAgentsStatusSnapshot()
	if err != nil {
		return nil, err
	}
	return append([]core.DurableAgentStatusSnapshot(nil), snapshot.Agents...), nil
}

func (c telegramCommandControl) StartDurableAgentConversation(ctx context.Context, chatID int64, senderID int64, agentID string) (string, error) {
	return c.SendDurableAgentParentMessage(ctx, chatID, senderID, agentID, "Scheduled parent-child check-in from /agents. Share current status, blockers, and concrete next actions.")
}

func (c telegramCommandControl) SendDurableAgentParentMessage(ctx context.Context, chatID int64, senderID int64, agentID string, message string) (string, error) {
	if c.durableTools == nil {
		return "", fmt.Errorf("durable-agent controls are unavailable")
	}
	if !c.CanRestart(senderID) {
		return "", fmt.Errorf("durable-agent controls are admin only")
	}

	actor := principal.Principal{
		Role:           principal.RoleAdmin,
		TelegramUserID: senderID,
	}
	if c.resolver != nil {
		resolved, ok := c.resolver.ResolveTelegramUser(senderID)
		if !ok || resolved.Role != principal.RoleAdmin {
			return "", fmt.Errorf("durable-agent controls are admin only")
		}
		actor = resolved
	}

	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", fmt.Errorf("durable agent id is required")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", fmt.Errorf("durable agent message is required")
	}
	request := map[string]any{
		"action":   "conversation_send",
		"agent_id": agentID,
		"message":  message,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	key := session.SessionKey{
		ChatID: chatID,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   strconv.FormatInt(chatID, 10),
		},
	}
	if _, err := c.durableTools.ExecuteForSessionPrincipal(ctx, actor, key, "durable_agent", payload); err != nil {
		return "", err
	}

	wakeStatus := "queued for next child wake"
	if c.rt != nil {
		go func(agent string) {
			wakeCtx, cancel := newTurnContext(context.Background(), turnTimeout)
			defer cancel()
			if err := c.rt.RunDurableAgentChildWake(wakeCtx, agent, time.Now().UTC()); err != nil {
				log.Printf("WARN durable-agent background wake failed agent_id=%s err=%v", strings.TrimSpace(agent), err)
			}
		}(agentID)
		wakeStatus = "wake attempt started"
	}
	return fmt.Sprintf("Started background conversation with durable agent %s (%s).", agentID, wakeStatus), nil
}

func (c telegramCommandControl) DurableAgentLifecycleAction(ctx context.Context, chatID int64, senderID int64, agentID string, action string) (string, error) {
	if c.durableTools == nil {
		return "", fmt.Errorf("durable-agent controls are unavailable")
	}
	if !c.CanRestart(senderID) {
		return "", fmt.Errorf("durable-agent controls are admin only")
	}
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "park", "resume", "retire":
	default:
		return "", fmt.Errorf("durable-agent lifecycle action must be park, resume, or retire")
	}
	actor := principal.Principal{
		Role:           principal.RoleAdmin,
		TelegramUserID: senderID,
	}
	if c.resolver != nil {
		resolved, ok := c.resolver.ResolveTelegramUser(senderID)
		if !ok || resolved.Role != principal.RoleAdmin {
			return "", fmt.Errorf("durable-agent controls are admin only")
		}
		actor = resolved
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", fmt.Errorf("durable agent id is required")
	}
	payload, err := json.Marshal(map[string]any{
		"action":   action,
		"agent_id": agentID,
		"reason":   "telegram /agents " + action,
	})
	if err != nil {
		return "", err
	}
	key := session.SessionKey{
		ChatID: chatID,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   strconv.FormatInt(chatID, 10),
		},
	}
	return c.durableTools.ExecuteForSessionPrincipal(ctx, actor, key, "durable_agent", payload)
}

func (c telegramCommandControl) QueueDurableAgentAnalyze(ctx context.Context, msg core.InboundMessage) (string, error) {
	if !c.CanRestart(msg.SenderID) {
		return "", fmt.Errorf("durable-agent controls are admin only")
	}
	if c.rt == nil {
		return "", fmt.Errorf("runtime is unavailable")
	}
	snapshot, err := c.rt.DurableAgentsStatusSnapshot()
	if err != nil {
		return "", err
	}
	if err := c.recordDurableAgentAnalyzeAccepted(msg); err != nil {
		return "", err
	}
	queued := msg
	queued.DurableAgentID = ""
	queued.TelegramThreadID = 0
	queued.Text = renderDurableAgentAnalyzeQuest(snapshot)
	queued.Raw = nil
	if err := c.RouteAccepted(ctx, queued); err != nil {
		return "", err
	}
	return "Agent board analysis queued.", nil
}

func (c telegramCommandControl) recordDurableAgentAnalyzeAccepted(msg core.InboundMessage) error {
	if c.store == nil || strings.TrimSpace(msg.IngressSurface) == "" || msg.IngressUpdateID <= 0 {
		return nil
	}
	encoded := ""
	if raw, err := json.Marshal(msg); err == nil {
		encoded = string(raw)
	}
	now := time.Now().UTC()
	_, err := c.store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     strings.TrimSpace(msg.IngressSurface),
		UpdateID:    msg.IngressUpdateID,
		UpdateKind:  "callback_agents_analyze",
		ChatID:      msg.ChatID,
		SenderID:    msg.SenderID,
		MessageID:   msg.MessageID,
		SessionID:   core.SessionIDForInboundMessage(msg),
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: encoded,
		AcceptedAt:  now,
		UpdatedAt:   now,
	})
	return err
}

func (c telegramCommandControl) RecordTelegramAgentCallbackMessage(chatID int64, agentID string, messageID int64, surface string) error {
	if c.store == nil {
		return nil
	}
	return c.store.RecordTelegramAgentMessage(chatID, messageID, agentID, surface, time.Now().UTC())
}

func (c telegramCommandControl) TelegramAgentIDForReplyMessage(chatID int64, replyMessageID int64) (string, bool, error) {
	if c.store == nil {
		return "", false, nil
	}
	return c.store.TelegramAgentIDForReplyMessage(chatID, replyMessageID)
}

func renderDurableAgentAnalyzeQuest(snapshot core.DurableAgentsStatusSnapshot) string {
	var b strings.Builder
	b.WriteString("Analyze the durable-agent board as a compact operator triage note for the main chat.\n")
	b.WriteString("Do not wake children, retire agents, merge child memory, or mutate authority.\n")
	b.WriteString("Return: quick read, needs action, safe to park/retire candidates, blockers, and one suggested next move.\n\n")
	fmt.Fprintf(&b, "total_agents: %d\nactive_agents: %d\ndormant_agents: %d\ndegraded_agents: %d\ninactive_agents: %d\n", snapshot.TotalAgents, snapshot.ActiveAgents, snapshot.DormantAgents, snapshot.DegradedAgents, snapshot.InactiveAgents)
	limit := len(snapshot.Agents)
	if limit > 12 {
		limit = 12
	}
	for i := 0; i < limit; i++ {
		agent := snapshot.Agents[i]
		fmt.Fprintf(&b, "\nagent: %s\nstatus: %s\nhealth: %s\nchannel: %s\nwake_mode: %s\npolicy_version: %d\n", agent.AgentID, agent.Status, agent.Health, agent.ChannelKind, agent.WakeupMode, agent.PolicyVersion)
		if reason := strings.TrimSpace(agent.ChildRuntimeBlockedReason); reason != "" {
			fmt.Fprintf(&b, "blocked: %s\n", reason)
		}
		if repair := strings.TrimSpace(agent.ChildRuntimeRepairHint); repair != "" {
			fmt.Fprintf(&b, "repair_hint: %s\n", repair)
		}
		if enrollment := strings.TrimSpace(agent.EnrollmentStatus); enrollment != "" {
			fmt.Fprintf(&b, "enrollment: %s\n", enrollment)
		}
		if agent.LastWakeAt.IsZero() {
			b.WriteString("last_wake: none\n")
		} else {
			fmt.Fprintf(&b, "last_wake: %s\n", agent.LastWakeAt.UTC().Format(time.RFC3339))
		}
	}
	if len(snapshot.Agents) > limit {
		fmt.Fprintf(&b, "\nomitted_agents: %d\n", len(snapshot.Agents)-limit)
	}
	return strings.TrimSpace(b.String())
}
