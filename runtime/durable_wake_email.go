//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const durableEmailWakeChatType = "durable_email_wake"

var runDurableEmailGogSearch = runDurableEmailGogSearchCommand

type durableEmailWakeAdapter struct{}

func newDurableEmailWakeAdapter() durableWakeIngressAdapter {
	return durableEmailWakeAdapter{}
}

func (durableEmailWakeAdapter) Name() string {
	return "email"
}

func (durableEmailWakeAdapter) Supports(agent core.DurableAgent) bool {
	return strings.EqualFold(strings.TrimSpace(agent.ChannelKind), "email")
}

func (durableEmailWakeAdapter) Prepare(ctx context.Context, rt *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error) {
	if rt == nil || rt.store == nil {
		return nil, fmt.Errorf("email wake runtime is unavailable")
	}
	if strings.ToLower(strings.TrimSpace(agent.Status)) != "active" {
		return nil, nil
	}
	if mode := strings.TrimSpace(agent.WakeupMode); mode != "" && !strings.EqualFold(mode, "poll") {
		return nil, nil
	}
	if agent.ChannelConfig.Email == nil {
		return nil, fmt.Errorf("email wake agent %q has no email channel_config", strings.TrimSpace(agent.AgentID))
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	pending, err := rt.pendingDurableAgentParentConversation(strings.TrimSpace(agent.AgentID), 5)
	if err != nil {
		return nil, fmt.Errorf("load email parent conversation queue: %w", err)
	}
	if len(pending) == 0 {
		due, err := durableEmailWakeDue(rt, agent, now)
		if err != nil {
			return nil, err
		}
		if !due {
			return nil, nil
		}
	}

	threadsJSON, err := runDurableEmailGogSearch(ctx, agent)
	if err != nil {
		return nil, err
	}
	threadsJSON = strings.TrimSpace(threadsJSON)
	if threadsJSON == "" {
		threadsJSON = "[]"
	}

	key := session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	return &durableWakeTurnPlan{
		Channel:         "email",
		AuditChannel:    "email:gog_cli",
		Key:             key,
		SessionChatType: durableEmailWakeChatType,
		SessionUserName: "email-adapter",
		Inbound: core.InboundMessage{
			ChatID:         key.ChatID,
			ChatType:       durableEmailWakeChatType,
			ChatTitle:      "durable-email-wake",
			SenderName:     "email-adapter",
			Text:           durableEmailWakePrompt(agent, pending, threadsJSON),
			MessageID:      durableWakeMessageID(now),
			DurableAgentID: strings.TrimSpace(agent.AgentID),
			Timestamp:      now,
		},
		PromptContextErrHint: "load durable email wake prompt context",
		PolicyReason:         "mapped from durable email channel policy",
		PersistenceErrCtx: turnCommitErrorContext{
			ConvertMessages: "convert durable email wake messages",
			LoadPlanState:   "load durable email wake plan state before save",
			LoadOperation:   "load durable email wake operation state before save",
			SaveSession:     "save durable email wake session",
			RecordOutbound:  "record durable email wake outbound reply",
		},
		SendErrCtx:   "send durable email wake reply",
		RecordErrCtx: "record durable email wake outbound reply",
		GovernorContext: func(agent core.DurableAgent, policy core.DurableAgentLivePolicy, _ core.InboundMessage, pending []core.DurableAgentConversationMessage) string {
			lines := []string{
				"You are handling a durable-agent email wake through the configured gog_cli adapter.",
				"The email payload was fetched read-only by the parent runtime before this child turn.",
				"Do not claim to reply, mark read, archive, delete, unsubscribe, or mutate mailbox state.",
				"Summarize only what is present in the email payload and pending parent guidance.",
			}
			if charter := strings.TrimSpace(policy.Charter); charter != "" {
				lines = append(lines, "Charter: "+charter)
			}
			lines = append(lines, "Durable agent id: "+strings.TrimSpace(agent.AgentID))
			lines = append(lines, "Channel kind: "+strings.TrimSpace(agent.ChannelKind))
			lines = append(lines, durableParentConversationGovernorLines(pending)...)
			return strings.Join(lines, "\n")
		},
	}, nil
}

func durableEmailWakeDue(rt *Runtime, agent core.DurableAgent, now time.Time) (bool, error) {
	if rt == nil || rt.store == nil {
		return false, fmt.Errorf("email wake runtime is unavailable")
	}
	interval := durableEmailPollInterval(agent)
	state, err := rt.store.DurableAgentState(strings.TrimSpace(agent.AgentID))
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, fmt.Errorf("load durable email wake state: %w", err)
	}
	if state == nil || state.LastWakeAt.IsZero() {
		return true, nil
	}
	return !now.Before(state.LastWakeAt.Add(interval)), nil
}

func durableEmailPollInterval(agent core.DurableAgent) time.Duration {
	if agent.ChannelConfig.Email != nil {
		if raw := strings.TrimSpace(agent.ChannelConfig.Email.PollInterval); raw != "" {
			if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 5 * time.Minute
}

func durableEmailWakePrompt(agent core.DurableAgent, messages []core.DurableAgentConversationMessage, threadsJSON string) string {
	email := agent.ChannelConfig.Email
	lines := []string{
		"Durable email wake.",
		"Agent: " + strings.TrimSpace(agent.AgentID),
		"Channel: email",
	}
	if email != nil {
		lines = append(lines,
			"Adapter: "+strings.TrimSpace(email.Adapter),
			"Account: "+strings.TrimSpace(email.Account),
			"Query: "+firstNonEmpty(strings.TrimSpace(email.Query), "label:inbox"),
		)
		if len(email.SurfaceRules) > 0 {
			lines = append(lines, "Surface rules: "+strings.Join(email.SurfaceRules, ", "))
		}
		if len(email.NeverRetain) > 0 {
			lines = append(lines, "Never retain: "+strings.Join(email.NeverRetain, ", "))
		}
	}
	lines = append(lines, fmt.Sprintf("Pending parent messages: %d", len(messages)))
	for i, message := range messages {
		text := truncateRunes(strings.TrimSpace(message.Text), 300)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("Parent message %d: %s", i+1, text))
	}
	lines = append(lines,
		"Read-only gog_cli search results JSON:",
		truncateRunes(threadsJSON, 6000),
	)
	return strings.Join(lines, "\n")
}

func runDurableEmailGogSearchCommand(ctx context.Context, agent core.DurableAgent) (string, error) {
	if agent.ChannelConfig.Email == nil {
		return "", fmt.Errorf("email wake agent %q has no email channel_config", strings.TrimSpace(agent.AgentID))
	}
	email := agent.ChannelConfig.Email
	if !strings.EqualFold(strings.TrimSpace(email.Adapter), "gog_cli") {
		return "", fmt.Errorf("email wake adapter %q is not supported", strings.TrimSpace(email.Adapter))
	}
	args := []string{"gmail", "search"}
	if account := strings.TrimSpace(email.Account); account != "" {
		args = append([]string{"--account", account}, args...)
	}
	args = append(args, firstNonEmpty(strings.TrimSpace(email.Query), "label:inbox"), "--json", "--results-only", "--max", "10", "--no-input")
	cmd := exec.CommandContext(ctx, "gog", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("email gog_cli search failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
