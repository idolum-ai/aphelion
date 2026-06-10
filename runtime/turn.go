//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func (r *Runtime) HandleInbound(ctx context.Context, msg core.InboundMessage) (result *core.TurnResult, err error) {
	return r.handleInteractiveInbound(ctx, msg, nil)
}

type internalContinuationOptions struct {
	DeferBudgetRecoveryToWorkFailureRetry bool
}

func (r *Runtime) handleInternalContinuation(ctx context.Context, actor principal.Principal, msg core.InboundMessage) (result *core.TurnResult, err error) {
	return r.handleInternalContinuationWithOptions(ctx, actor, msg, internalContinuationOptions{})
}

func (r *Runtime) handleInternalContinuationWithOptions(ctx context.Context, actor principal.Principal, msg core.InboundMessage, opts internalContinuationOptions) (result *core.TurnResult, err error) {
	turnResult, err := r.handleInternalContinuationTurnWithOptions(ctx, actor, msg, opts)
	if turnResult == nil {
		return nil, err
	}
	return turnResult.Turn, err
}

func (r *Runtime) handleInternalContinuationTurnWithOptions(ctx context.Context, actor principal.Principal, msg core.InboundMessage, opts internalContinuationOptions) (result *turn.Result, err error) {
	if actor.TelegramUserID <= 0 && strings.TrimSpace(actor.DurableAgentID) == "" {
		return nil, ErrPrincipalDenied
	}
	msg = detachInternalContinuationIngress(msg)
	return r.handleInteractiveInboundTurnWithOptions(ctx, msg, &actor, opts)
}

func detachInternalContinuationIngress(msg core.InboundMessage) core.InboundMessage {
	msg.IngressSeq = 0
	msg.IngressQueuedAt = time.Time{}
	msg.IngressSurface = ""
	msg.IngressUpdateID = 0
	msg.Raw = nil
	return msg
}

func (r *Runtime) handleInteractiveInbound(ctx context.Context, msg core.InboundMessage, forcedActor *principal.Principal) (result *core.TurnResult, err error) {
	return r.handleInteractiveInboundWithOptions(ctx, msg, forcedActor, internalContinuationOptions{})
}

func (r *Runtime) handleInteractiveInboundWithOptions(ctx context.Context, msg core.InboundMessage, forcedActor *principal.Principal, opts internalContinuationOptions) (result *core.TurnResult, err error) {
	turnResult, err := r.handleInteractiveInboundTurnWithOptions(ctx, msg, forcedActor, opts)
	if turnResult == nil {
		return nil, err
	}
	return turnResult.Turn, err
}

func (r *Runtime) handleInteractiveInboundTurnWithOptions(ctx context.Context, msg core.InboundMessage, forcedActor *principal.Principal, opts internalContinuationOptions) (result *turn.Result, err error) {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		coreResult, err := r.handleDurableTelegramGroupInbound(ctx, msg)
		return turnResultFromCore(coreResult), err
	}
	actor := principal.Principal{}
	if forcedActor != nil {
		actor = *forcedActor
	} else {
		resolved, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
		if !ok {
			return nil, ErrPrincipalDenied
		}
		actor = resolved
	}
	if handled, result, err := r.maybeHandleTypedContinuationApproval(ctx, msg, actor); handled {
		return turnResultFromCore(result), err
	}
	stopTyping := r.startChatActionLoop(ctx, msg.ChatID, "typing")
	defer stopTyping()
	defer r.clearChatTurnPhase(msg.ChatID)

	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramInboundScopeRef(msg)}
	unlock := r.lockSession(key)
	defer unlock()

	tools := r.toolsForPrincipal(actor, key)

	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return nil, fmt.Errorf("resolve principal scope: %w", err)
	}
	if handled, result, err := r.maybeHandleOperationArtifactRequest(ctx, key, scope, msg); handled {
		return turnResultFromCore(result), err
	}
	eventAwareness := turn.EventAwareness{Origin: inboundOriginLabel(msg)}
	if msg.Origin == core.InboundOriginTurnAuthorization {
		eventAwareness.TurnAuthorizationKind = inboundOriginDetailLabel(msg)
	}
	assembler := r.interactiveDMAssembler
	if assembler == nil {
		assembler = newInteractiveDMTurnAssembler(r)
	}
	input := interactiveDMTurnAssemblyInput{
		Msg:                                   msg,
		Actor:                                 actor,
		Key:                                   key,
		Scope:                                 scope,
		Tools:                                 tools,
		EventAwareness:                        eventAwareness,
		DeferBudgetRecoveryToWorkFailureRetry: opts.DeferBudgetRecoveryToWorkFailureRetry,
	}
	if turnAssembler, ok := assembler.(interactiveDMTurnResultAssembler); ok {
		return turnAssembler.RunTurn(ctx, input)
	}
	coreResult, err := assembler.Run(ctx, input)
	return turnResultFromCore(coreResult), err
}

func turnResultFromCore(result *core.TurnResult) *turn.Result {
	if result == nil {
		return nil
	}
	return &turn.Result{
		Turn:         result,
		VisibleReply: strings.TrimSpace(result.Text),
	}
}

type faceUsageConsumer interface {
	ConsumeLastUsage() core.TokenUsage
}

func consumeFaceUsage(model face.Renderer) core.TokenUsage {
	consumer, ok := model.(faceUsageConsumer)
	if !ok {
		return core.TokenUsage{}
	}
	return consumer.ConsumeLastUsage()
}

func addTokenUsage(dst core.TokenUsage, src core.TokenUsage) core.TokenUsage {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.TotalTokens += src.TotalTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	dst.CacheCreationTokens += src.CacheCreationTokens
	return dst
}

func replaceLastAssistantWithSceneText(messages []session.Message, sceneText string) []session.Message {
	trimmed := strings.TrimSpace(sceneText)
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			messages[i].Content = trimmed
			messages[i].ContentChars = len(trimmed)
			return messages
		}
	}
	if trimmed == "" {
		return messages
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
