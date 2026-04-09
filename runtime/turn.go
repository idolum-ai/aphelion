//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/workspace"
)

func (r *Runtime) HandleInbound(ctx context.Context, msg core.InboundMessage) (*core.TurnResult, error) {
	principal, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
	if !ok {
		return nil, ErrPrincipalDenied
	}

	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0}
	sess, err := r.store.Load(key)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	promptContext, err := workspace.LoadPromptContext(r.cfg.Agent, time.Now())
	if err != nil {
		return nil, fmt.Errorf("load workspace prompt context: %w", err)
	}
	systemPrompt := promptContext.Render(BaseSystemInstruction(r.cfg.Agent.Workspace, string(principal.Role)))

	sess.ChatType = "dm"
	sess.UserName = msg.SenderName
	sess.SystemPrompt = systemPrompt

	history, err := session.ToAgentHistory(sess.Messages)
	if err != nil {
		return nil, fmt.Errorf("assemble history: %w", err)
	}

	userText := strings.TrimSpace(msg.Text)
	if userText == "" {
		userText = "[empty message]"
	}

	input := make([]agent.Message, 0, len(history)+2)
	if systemPrompt != "" {
		input = append(input, agent.Message{Role: "system", Content: systemPrompt})
	}
	input = append(input, history...)
	input = append(input, agent.Message{Role: "user", Content: userText})

	result, outHistory, err := agent.RunTurn(ctx, r.provider, r.tools, &agent.Budget{
		Max:     r.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, input)
	if err != nil {
		return nil, fmt.Errorf("run turn: %w", err)
	}

	if len(outHistory) < len(input) {
		return nil, fmt.Errorf("invalid turn output: history shrank from %d to %d", len(input), len(outHistory))
	}

	sess.TurnCount++
	newMessages, err := session.NewMessagesForTurn(userText, outHistory[len(input):], sess.TurnCount)
	if err != nil {
		return nil, fmt.Errorf("convert new messages: %w", err)
	}

	if err := r.store.Save(sess, newMessages, result.TokenUsage); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	replyText := strings.TrimSpace(result.Text)
	if replyText == "" {
		replyText = "(no response)"
	}

	_, sendErr := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    replyText,
		ReplyTo: &msg.MessageID,
	})
	if sendErr != nil {
		return result, fmt.Errorf("send outbound reply: %w", sendErr)
	}

	return result, nil
}

func BaseSystemInstruction(workspaceRoot string, principalRole string) string {
	if strings.TrimSpace(principalRole) == "" {
		principalRole = "unknown"
	}
	return fmt.Sprintf(
		"You are a Linux personal assistant operating inside the workspace %q. The active principal role is %q. Use the exec tool whenever shell interaction is useful. Inspect before changing, prefer concise answers, and only claim work you actually completed.",
		workspaceRoot, principalRole,
	)
}
