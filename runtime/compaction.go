//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/session"
)

const (
	compactionSummaryMaxChars = 1800
	compactionMinTurnsKept    = 2
	compactionSummaryBudget   = 512
)

type turnTokenEstimate struct {
	TurnIndex int
	Tokens    int
}

func (r *Runtime) maybeCompactSession(
	ctx context.Context,
	key session.SessionKey,
	sess *session.Session,
	systemBlocks []agent.SystemBlock,
	userText string,
	idolumProposal string,
) (*session.Session, []agent.Message, error) {
	history, err := session.ToAgentHistory(sess.Messages)
	if err != nil {
		return nil, nil, fmt.Errorf("assemble history: %w", err)
	}
	if !r.shouldCompact(systemBlocks, history, userText, idolumProposal) {
		return sess, history, nil
	}

	keepFromTurn, ok := r.compactionBoundary(sess, systemBlocks, userText, idolumProposal)
	if !ok {
		return sess, history, nil
	}

	summary := ""
	if strings.EqualFold(strings.TrimSpace(r.cfg.Sessions.CompactionStrategy), "summarize") {
		summary, err = r.buildCompactionSummary(ctx, sess, keepFromTurn)
		if err != nil {
			log.Printf("WARN compaction summary failed chat_id=%d user_id=%d keep_from=%d err=%v; falling back to truncate", key.ChatID, key.UserID, keepFromTurn, err)
		}
	}

	if err := r.store.Compact(key, summary, keepFromTurn); err != nil {
		return nil, nil, fmt.Errorf("compact session: %w", err)
	}

	reloaded, err := r.store.Load(key)
	if err != nil {
		return nil, nil, fmt.Errorf("reload compacted session: %w", err)
	}
	history, err = session.ToAgentHistory(reloaded.Messages)
	if err != nil {
		return nil, nil, fmt.Errorf("assemble compacted history: %w", err)
	}
	return reloaded, history, nil
}

func (r *Runtime) shouldCompact(systemBlocks []agent.SystemBlock, history []agent.Message, userText string, idolumProposal string) bool {
	contextWindow := r.governorContextWindow()
	if contextWindow <= 0 {
		return false
	}
	estimate := estimateInteractivePromptTokens(systemBlocks, history, userText, idolumProposal)
	threshold := int(float64(contextWindow) * r.cfg.Sessions.MaxContextRatio)
	return estimate > threshold
}

func (r *Runtime) compactionBoundary(sess *session.Session, systemBlocks []agent.SystemBlock, userText string, idolumProposal string) (int, bool) {
	turns := estimateActiveTurns(sess.Messages)
	if len(turns) <= compactionMinTurnsKept {
		return 0, false
	}

	contextWindow := r.governorContextWindow()
	targetPromptTokens := int(float64(contextWindow) * r.cfg.Sessions.CompactionRatio)
	baseTokens := estimateSystemBlocksTokens(systemBlocks) + estimateTextTokens(userText) + estimateTextTokens(idolumProposal)
	remainingBudget := targetPromptTokens - baseTokens - compactionSummaryBudget
	if remainingBudget < 0 {
		remainingBudget = 0
	}

	keepIndex := len(turns) - compactionMinTurnsKept
	acc := 0
	for i := keepIndex; i < len(turns); i++ {
		acc += turns[i].Tokens
	}
	for i := keepIndex - 1; i >= 0; i-- {
		if acc+turns[i].Tokens > remainingBudget {
			break
		}
		acc += turns[i].Tokens
		keepIndex = i
	}
	if keepIndex <= 0 {
		keepIndex = 1
	}
	if keepIndex >= len(turns) {
		return 0, false
	}
	return turns[keepIndex].TurnIndex, true
}

func (r *Runtime) buildCompactionSummary(ctx context.Context, sess *session.Session, keepFromTurn int) (string, error) {
	transcript := compactionTranscript(sess.Messages, keepFromTurn)
	if strings.TrimSpace(transcript) == "" {
		return "", nil
	}

	messages := []agent.Message{
		{
			Role: "system",
			Content: strings.Join([]string{
				"You are compacting an existing session ledger.",
				"Produce a concise assistant summary that preserves durable facts, open tasks, unresolved questions, and important tool outcomes.",
				"Do not invent anything that did not occur in the transcript.",
				fmt.Sprintf("Keep the summary under %d characters.", compactionSummaryMaxChars),
				"Return plain text only.",
			}, "\n"),
		},
		{
			Role:    "user",
			Content: transcript,
		},
	}

	resp, err := completeProvider(ctx, r.provider, messages, nil, r.reasoningOptionsForRun(session.TurnRunKindHeartbeat))
	if err != nil {
		return "", err
	}
	return clampText(strings.TrimSpace(resp.Content), compactionSummaryMaxChars), nil
}

func completeProvider(ctx context.Context, provider agent.Provider, messages []agent.Message, tools []agent.ToolDef, opts *agent.CompleteOptions) (*agent.Response, error) {
	if withOptions, ok := provider.(agent.ProviderWithOptions); ok && opts != nil {
		return withOptions.CompleteWithOptions(ctx, messages, tools, *opts)
	}
	return provider.Complete(ctx, messages, tools)
}

func compactionTranscript(messages []session.Message, keepFromTurn int) string {
	var b strings.Builder
	for _, msg := range messages {
		if msg.Compacted || msg.TurnIndex >= keepFromTurn {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("[turn %d] %s: %s", msg.TurnIndex, msg.Role, content))
		if b.Len() >= 16000 {
			break
		}
	}
	return clampText(b.String(), 16000)
}

func estimateActiveTurns(messages []session.Message) []turnTokenEstimate {
	byTurn := make([]turnTokenEstimate, 0)
	indexByTurn := make(map[int]int)
	for _, msg := range messages {
		if msg.Compacted {
			continue
		}
		idx, ok := indexByTurn[msg.TurnIndex]
		if !ok {
			indexByTurn[msg.TurnIndex] = len(byTurn)
			byTurn = append(byTurn, turnTokenEstimate{TurnIndex: msg.TurnIndex})
			idx = len(byTurn) - 1
		}
		byTurn[idx].Tokens += estimatePersistedMessageTokens(msg)
	}
	return byTurn
}

func estimateInteractivePromptTokens(systemBlocks []agent.SystemBlock, history []agent.Message, userText string, idolumProposal string) int {
	total := estimateSystemBlocksTokens(systemBlocks) + estimateTextTokens(userText) + estimateTextTokens(idolumProposal)
	for _, msg := range history {
		total += estimateAgentMessageTokens(msg)
	}
	return total
}

func estimateSystemBlocksTokens(blocks []agent.SystemBlock) int {
	total := 0
	for _, block := range blocks {
		total += estimateTextTokens(block.Text)
	}
	return total
}

func estimatePersistedMessageTokens(msg session.Message) int {
	total := estimateTextTokens(msg.Content)
	if strings.TrimSpace(msg.ToolCalls) != "" {
		total += estimateTextTokens(msg.ToolCalls)
	}
	return total + 4
}

func estimateAgentMessageTokens(msg agent.Message) int {
	total := estimateTextTokens(msg.Content)
	if strings.TrimSpace(msg.Thinking) != "" {
		total += estimateTextTokens(msg.Thinking)
	}
	for _, call := range msg.ToolCalls {
		total += estimateTextTokens(call.Name) + estimateTextTokens(string(call.Input))
	}
	return total + 4
}

func estimateTextTokens(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	return len(trimmed)/4 + 1
}

func clampText(text string, maxChars int) string {
	trimmed := strings.TrimSpace(text)
	if maxChars <= 0 || len(trimmed) <= maxChars {
		return trimmed
	}
	if maxChars <= 1 {
		return trimmed[:maxChars]
	}
	return strings.TrimSpace(trimmed[:maxChars-1]) + "…"
}

func (r *Runtime) governorContextWindow() int {
	switch strings.ToLower(strings.TrimSpace(r.governorBackend)) {
	case "codex":
		if r.cfg.Governor.Codex.ContextWindow > 0 {
			return r.cfg.Governor.Codex.ContextWindow
		}
	default:
		switch config.EffectiveNativeProvider(r.cfg) {
		case "openai":
			if r.cfg.Providers.OpenAI.ContextWindow > 0 {
				return r.cfg.Providers.OpenAI.ContextWindow
			}
		case "openrouter":
			if r.cfg.Providers.OpenRouter.ContextWindow > 0 {
				return r.cfg.Providers.OpenRouter.ContextWindow
			}
		default:
			if r.cfg.Providers.Anthropic.ContextWindow > 0 {
				return r.cfg.Providers.Anthropic.ContextWindow
			}
		}
		if r.cfg.Providers.Anthropic.ContextWindow > 0 {
			return r.cfg.Providers.Anthropic.ContextWindow
		}
	}
	return 200000
}
