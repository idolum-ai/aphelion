//go:build linux

package runtime

import (
	"context"
	"fmt"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

// interactiveDMTurnAssembler is the species-boundary handoff between runtime's
// long-lived shell concerns (identity, scope, locks, transport) and one-turn
// construction/execution for interactive DM turns.
type interactiveDMTurnAssembler interface {
	Run(ctx context.Context, input interactiveDMTurnAssemblyInput) (*core.TurnResult, error)
}

type interactiveDMTurnAssemblyInput struct {
	Msg                                   core.InboundMessage
	Actor                                 principal.Principal
	Key                                   session.SessionKey
	Scope                                 sandbox.Scope
	Tools                                 agent.ToolRegistry
	EventAwareness                        turn.EventAwareness
	DeferBudgetRecoveryToWorkFailureRetry bool
}

type runtimeInteractiveDMTurnAssembler struct {
	runtime *Runtime
}

func newInteractiveDMTurnAssembler(runtime *Runtime) interactiveDMTurnAssembler {
	return &runtimeInteractiveDMTurnAssembler{runtime: runtime}
}

func (a *runtimeInteractiveDMTurnAssembler) Run(ctx context.Context, input interactiveDMTurnAssemblyInput) (*core.TurnResult, error) {
	if a == nil || a.runtime == nil {
		return nil, fmt.Errorf("interactive dm turn assembler unavailable")
	}
	return a.runtime.runInteractiveDMTurn(ctx, input)
}

func (r *Runtime) runInteractiveDMTurn(ctx context.Context, input interactiveDMTurnAssemblyInput) (*core.TurnResult, error) {
	assembled, err := r.assembleInteractiveLikeTurn(ctx, interactiveLikeAssemblyInput{
		Scope:                input.Scope,
		Key:                  input.Key,
		Msg:                  input.Msg,
		RunKind:              session.TurnRunKindInteractive,
		Channel:              "telegram",
		PrincipalRole:        string(input.Actor.Role),
		AuditChannel:         "telegram",
		EventAwareness:       input.EventAwareness,
		PromptContextErrHint: "load workspace prompt context",
		PolicyReason:         "mapped from pipeline interactive face policy",
	})
	if err != nil {
		return nil, err
	}

	now := assembled.Now
	sess := assembled.Sess
	prepared := assembled.Prepared
	facePolicy := assembled.FacePolicy
	useMaterialFloor := assembled.UseMaterialFloor
	exec := assembled.Exec
	promptContext := assembled.PromptContext
	hiddenInputs := assembled.HiddenInputs
	baseGovernorAwareness := assembled.BaseGovernorAwareness
	audit := assembled.Audit
	machine := assembled.Machine
	defer r.emitTurnAudit(audit)

	msg := input.Msg
	key := input.Key
	r.recordWorkingObjectiveForInbound(key, msg)
	actor := input.Actor

	sess.ChatType = "dm"
	sess.UserName = msg.SenderName
	turnState := newInteractiveTurnState(sess)
	coordinator := &interactiveTurnCoordinator{
		runtime:               r,
		actor:                 actor,
		scope:                 input.Scope,
		msg:                   msg,
		key:                   key,
		state:                 turnState,
		prepared:              prepared,
		exec:                  exec,
		facePolicy:            facePolicy,
		useMaterialFloor:      useMaterialFloor,
		governorName:          machine.Options.GovernorName,
		faceName:              machine.Options.FaceName,
		channelName:           machine.Options.Channel,
		principalRole:         string(actor.Role),
		hiddenInputs:          hiddenInputs,
		promptContext:         promptContext,
		tools:                 input.Tools,
		currentFaceModel:      r.currentFaceRenderer(),
		baseGovernorAwareness: baseGovernorAwareness,
		audit:                 audit,
	}
	machine.Governor = coordinator
	machine.Face = coordinator
	machine.Persistence = &turnPersistencePort{
		runtime:      r,
		key:          key,
		scope:        input.Scope,
		sess:         sess,
		sessionState: turnState,
		runIDSource:  turnState,
		msg:          msg,
		actor:        actor,
		errCtx: turnCommitErrorContext{
			ConvertMessages: "convert new messages",
			LoadPlanState:   "load plan state before save",
			LoadOperation:   "load operation state before save",
			SaveSession:     "save session",
			RecordOutbound:  "record outbound reply",
		},
		audit: audit,
	}
	machine.Delivery = &turnDeliveryPort{
		runtime:                               r,
		key:                                   key,
		sess:                                  sess,
		sessionState:                          turnState,
		msg:                                   msg,
		inboundWasVoice:                       prepared.InboundWasVoice,
		deliver:                               true,
		recordOutbound:                        true,
		audit:                                 audit,
		sendErrCtx:                            "send outbound reply",
		recordErrCtx:                          "record outbound reply",
		deferBudgetRecoveryToWorkFailureRetry: input.DeferBudgetRecoveryToWorkFailureRetry,
		hooks: turnCommitHooks{
			QueueReviewEvents: func(result *turn.Result) error {
				if !shouldGenerateReviewEvent(actor, key) {
					return nil
				}
				turnSess := turnState.session()
				if turnSess == nil {
					return fmt.Errorf("queue review events: turn session unavailable")
				}
				sceneText, toolLog := interactiveReviewEventPayload(result)
				ledgerText := interactivePreparedLedgerText(prepared.LedgerText, result)
				return r.enqueueReviewEventsForTurn(
					actor,
					msg,
					turnSess.TurnCount,
					ledgerText,
					sceneText,
					toolLog,
				)
			},
			DeliverReviewEvents: func(*turn.Result) error {
				if actor.Role != principal.RoleAdmin {
					return nil
				}
				turnSess := turnState.session()
				if turnSess == nil {
					return fmt.Errorf("deliver review events: turn session unavailable")
				}
				return r.deliverReviewEvents(ctx, key, turnSess)
			},
			PostReplyContinuationUI: func(postCtx context.Context, result *turn.Result) error {
				ledgerText := interactivePreparedLedgerText(prepared.LedgerText, result)
				materialized, err := r.materializePendingOperationProposalApproval(postCtx, key, msg, ledgerText, result)
				if err != nil || materialized {
					return err
				}
				inferred, err := r.maybeInferOrganicOperationProposal(postCtx, key, msg, ledgerText, result)
				if err != nil {
					return err
				}
				if inferred {
					_, err := r.materializePendingOperationProposalApproval(postCtx, key, msg, ledgerText, result)
					return err
				}
				goalInferred, err := r.maybeInferGoalContinuationProposal(postCtx, key, msg, ledgerText, result)
				if err != nil {
					return err
				}
				if goalInferred {
					_, err := r.materializePendingOperationProposalApproval(postCtx, key, msg, ledgerText, result)
					return err
				}
				if err := r.offerContinuationApproval(postCtx, key, msg, ledgerText, result); err != nil {
					return err
				}
				return r.maybeOfferMissionAsk(postCtx, key, msg, ledgerText, result)
			},
		},
	}

	turnResult, err := machine.Handle(ctx, turn.Request{
		RunKind:          session.TurnRunKindInteractive,
		SessionKey:       key,
		Inbound:          msg,
		Session:          sess,
		InboundWasVoice:  prepared.InboundWasVoice,
		ReplyWithVoice:   r.preparedReplyWithVoice(prepared),
		Now:              now,
		PreparedUserText: prepared.LedgerText,
	})
	if err != nil && (turnResult == nil || !turnResult.Commit.Persisted) {
		return nil, err
	}
	if turnResult == nil || turnResult.Turn == nil {
		return nil, fmt.Errorf("interactive turn did not return a result")
	}
	if err != nil {
		return turnResult.Turn, err
	}
	return turnResult.Turn, nil
}
