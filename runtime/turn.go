//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/workspace"
)

const maxReviewEventsPerTurn = 10

func (r *Runtime) HandleInbound(ctx context.Context, msg core.InboundMessage) (*core.TurnResult, error) {
	principal, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
	if !ok {
		return nil, ErrPrincipalDenied
	}
	tools := r.toolsForPrincipal(principal)

	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0}
	sess, err := r.store.Load(key)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	promptContext, err := workspace.LoadPromptContext(r.cfg.Agent, time.Now())
	if err != nil {
		return nil, fmt.Errorf("load workspace prompt context: %w", err)
	}
	systemPrompt := prompt.BuildGovernorPrompt(prompt.GovernorRequest{
		GovernorName:    prompt.DefaultGovernorName,
		GovernorBackend: r.governorBackend,
		PrincipalRole:   string(principal.Role),
		WorkspaceRoot:   r.cfg.Agent.Workspace,
		ToolManifest:    toolManifest(tools),
		Workspace:       promptContext,
	})

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

	result, outHistory, err := agent.RunTurn(ctx, r.provider, tools, &agent.Budget{
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
	canonicalReply := face.CanonicalOrFallback(result.Text)
	replyText := canonicalReply
	if r.faceBackend != face.BackendGovernorPassthrough && r.faceModel != nil {
		renderedReply, renderErr := r.faceModel.Render(ctx, face.RenderRequest{
			GovernorName:    prompt.DefaultGovernorName,
			FaceName:        face.DefaultFaceName,
			Channel:         "telegram",
			PrincipalRole:   string(principal.Role),
			WorkspaceRoot:   r.cfg.Agent.Workspace,
			CanonicalReply:  canonicalReply,
			LatestUserInput: userText,
		})
		if renderErr != nil {
			log.Printf("WARN face render failed backend=%s err=%v; using governor_passthrough", r.faceBackend, renderErr)
		} else {
			replyText = strings.TrimSpace(renderedReply)
			if replyText == "" {
				replyText = canonicalReply
			}
		}
	}

	newMessages, err := session.NewMessagesForTurn(userText, outHistory[len(input):], sess.TurnCount)
	if err != nil {
		return nil, fmt.Errorf("convert new messages: %w", err)
	}
	newMessages = replaceLastAssistantWithRenderedReply(newMessages, replyText)
	sess.LastCanonicalReply = canonicalReply

	if err := r.store.Save(sess, newMessages, result.TokenUsage); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	_, sendErr := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    replyText,
		ReplyTo: &msg.MessageID,
	})
	if sendErr != nil {
		return result, fmt.Errorf("send outbound reply: %w", sendErr)
	}

	if principal.Role == "admin" {
		if err := r.deliverReviewEvents(ctx, msg.ChatID); err != nil {
			return result, fmt.Errorf("deliver review events: %w", err)
		}
	}

	return result, nil
}

func replaceLastAssistantWithRenderedReply(messages []session.Message, renderedReply string) []session.Message {
	trimmed := strings.TrimSpace(renderedReply)
	if trimmed == "" {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			messages[i].Content = trimmed
			messages[i].ContentChars = len(trimmed)
			return messages
		}
	}

	turnIndex := 0
	if len(messages) > 0 {
		turnIndex = messages[len(messages)-1].TurnIndex
	}
	return append(messages, session.Message{
		Role:         "assistant",
		Content:      trimmed,
		ContentChars: len(trimmed),
		TurnIndex:    turnIndex,
	})
}

func (r *Runtime) deliverReviewEvents(ctx context.Context, adminChatID int64) error {
	events, err := r.store.PendingReviewEvents(adminChatID, maxReviewEventsPerTurn)
	if err != nil {
		return err
	}
	for _, event := range events {
		if _, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
			ChatID: adminChatID,
			Text:   formatReviewEventMessage(event),
		}); err != nil {
			return err
		}
		if err := r.store.MarkReviewDelivered([]int64{event.ID}); err != nil {
			return err
		}
	}
	return nil
}

func formatReviewEventMessage(event session.ReviewEvent) string {
	turnRange := "n/a"
	if event.TurnFrom > 0 && event.TurnTo >= event.TurnFrom {
		turnRange = fmt.Sprintf("%d-%d", event.TurnFrom, event.TurnTo)
	} else if event.TurnFrom > 0 {
		turnRange = fmt.Sprintf("%d", event.TurnFrom)
	}

	return fmt.Sprintf(
		"[Review Digest]\nsource_chat=%d source_user=%d source_role=%s turns=%s\n\n%s",
		event.SourceChatID,
		event.SourceUserID,
		event.SourceRole,
		turnRange,
		strings.TrimSpace(event.Summary),
	)
}

func toolManifest(registry agent.ToolRegistry) string {
	if registry == nil {
		return ""
	}
	type manifestProvider interface {
		Manifest() string
	}
	if provider, ok := registry.(manifestProvider); ok {
		return provider.Manifest()
	}
	return renderToolManifest(registry.Definitions())
}

func renderToolManifest(defs []agent.ToolDef) string {
	if len(defs) == 0 {
		return ""
	}

	names := make([]string, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
