//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
	"github.com/idolum-ai/aphelion/workspace"
)

const defaultInteractiveLikeTurnStyle = "observant, high-agency, warm, and emotionally lucid"

type interactiveLikeAssemblyInput struct {
	Scope                sandbox.Scope
	Key                  session.SessionKey
	Msg                  core.InboundMessage
	PrepareInbound       *core.InboundMessage
	RunKind              session.TurnRunKind
	Channel              string
	PrincipalRole        string
	AuditChannel         string
	EventAwareness       turn.EventAwareness
	PromptContextErrHint string
	PolicyReason         string
	GovernorName         string
	FaceName             string
	Style                string
}

type interactiveLikeAssembly struct {
	Now                   time.Time
	Sess                  *session.Session
	Prepared              pipeline.TurnPrepareContract
	Exec                  pipeline.TurnExecutionContract
	FacePolicy            pipeline.FacePolicy
	UseMaterialFloor      bool
	PromptContext         *workspace.PromptContext
	HiddenInputs          hiddenInputSet
	BaseGovernorAwareness prompt.RuntimeAwareness
	Audit                 *turnAuditRecorder
	Machine               *turn.Machine
}

func (r *Runtime) assembleInteractiveLikeTurn(ctx context.Context, input interactiveLikeAssemblyInput) (interactiveLikeAssembly, error) {
	out := interactiveLikeAssembly{}
	if r == nil || r.store == nil {
		return out, fmt.Errorf("runtime unavailable")
	}

	runKind := input.RunKind
	if runKind == "" {
		runKind = session.TurnRunKindInteractive
	}
	channel := firstNonEmpty(strings.TrimSpace(input.Channel), "telegram")
	governorName := firstNonEmpty(strings.TrimSpace(input.GovernorName), r.governorName())
	faceName := firstNonEmpty(strings.TrimSpace(input.FaceName), r.faceName())
	style := firstNonEmpty(strings.TrimSpace(input.Style), defaultInteractiveLikeTurnStyle)
	policyReason := firstNonEmpty(strings.TrimSpace(input.PolicyReason), "mapped from pipeline interactive face policy")
	promptContextErr := firstNonEmpty(strings.TrimSpace(input.PromptContextErrHint), "load workspace prompt context")
	auditChannel := firstNonEmpty(strings.TrimSpace(input.AuditChannel), channel)
	principalRole := strings.TrimSpace(input.PrincipalRole)

	prepareInbound := input.Msg
	if input.PrepareInbound != nil {
		prepareInbound = *input.PrepareInbound
	}

	sess, err := r.store.Load(input.Key)
	if err != nil {
		return out, fmt.Errorf("load session: %w", err)
	}
	applySessionScope(sess, input.Key)

	now := time.Now().UTC()
	prepared, err := r.prepareInboundTurn(ctx, input.Scope, prepareInbound)
	if err != nil {
		return out, err
	}
	applyMediaIntentPolicy(sess.LastFloorMetadata, prepareInbound, &prepared, nil)
	facePolicy := pipeline.DecideInteractiveFacePolicy(prepared.LedgerText)
	useMaterialFloor := pipeline.ShouldUseMaterialFloorContract(facePolicy)
	exec := r.executionForTurn(prepared)

	promptContext, err := r.promptContextForScope(input.Scope, now)
	if err != nil {
		return out, fmt.Errorf("%s: %w", promptContextErr, err)
	}

	hiddenInputs := r.assembleInteractiveHiddenInputs(ctx, input.Key, input.Scope, now, prepared.LedgerText, sess.LastFloorMetadata)
	hiddenInputs.addCoreAll(prepared.ArtifactDecisionInputs)
	hiddenInputs = r.withInteriorSignalState(input.Key, hiddenInputs, now, false)
	planEvents, _ := r.store.PlanEvents(input.Key, 20)
	workingObjective, _ := r.store.WorkingObjective(input.Key)
	baseGovernorAwareness := turn.ApplyContinuationAwareness(
		turn.ApplyOperationAwareness(
			turn.ApplyPlanAwarenessWithEvents(
				turn.ApplyWorkingObjectiveAwareness(
					turn.ApplyEventAwareness(
						turn.ApplyHiddenInputAwareness(r.governorRuntimeAwareness(input.Scope, runKind, channel, exec), hiddenInputs.toTurnAwareness()),
						input.EventAwareness,
					),
					workingObjective,
				),
				sess.PlanState,
				planEvents,
			),
			sess.OperationState,
		),
		sess.ContinuationState,
	)
	baseGovernorAwareness = r.applyEvidenceHydrationAwareness(ctx, baseGovernorAwareness, input.Key, runKind, prepared.LedgerText, sess, now)
	baseGovernorAwareness = r.applyReplyModalityAwareness(baseGovernorAwareness, prepared)
	if useMaterialFloor {
		baseGovernorAwareness.ArtifactMode = "floor"
	}

	machine := &turn.Machine{
		Governor: nil,
		Face:     nil,
		Options: turn.Options{
			GovernorName: governorName,
			FaceName:     faceName,
			Channel:      channel,
			Style:        style,
		},
		RuntimeAwareness: baseGovernorAwareness,
		PolicyFunc: func(turn.Request) turn.Policy {
			return turn.Policy{
				Brokerage: false,
				Proposal:  facePolicy.Proposal,
				Render:    facePolicy.Render,
				Reason:    policyReason,
			}
		},
	}

	out.Now = now
	out.Sess = sess
	out.Prepared = prepared
	out.Exec = exec
	out.FacePolicy = facePolicy
	out.UseMaterialFloor = useMaterialFloor
	out.PromptContext = promptContext
	out.HiddenInputs = hiddenInputs
	out.BaseGovernorAwareness = baseGovernorAwareness
	out.Audit = newTurnAuditRecorder(input.Key, auditChannel, principalRole, prepared.LedgerText)
	out.Machine = machine
	return out, nil
}
