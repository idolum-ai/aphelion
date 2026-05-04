//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
	"github.com/idolum-ai/aphelion/workspace"
)

const defaultMaintenanceTurnStyle = "observant, high-agency, warm, and emotionally lucid"

// maintenanceTurnAssembler is the execution-family boundary between runtime's
// long-lived maintenance loops (heartbeat/cron/recovery) and one-turn
// assembly/execution through turn.Machine.
type maintenanceTurnAssembler interface {
	Run(ctx context.Context, input maintenanceTurnAssemblyInput) (*turn.Result, error)
}

type maintenanceTurnAssemblyInput struct {
	Species               maintenanceTurnSpecies
	RunKind               session.TurnRunKind
	Key                   session.SessionKey
	Sess                  *session.Session
	Scope                 sandbox.Scope
	Prepared              pipeline.TurnPrepareContract
	Exec                  pipeline.TurnExecutionContract
	PromptContext         *workspace.PromptContext
	HiddenInputs          hiddenInputSet
	RecoveryRuns          []session.TurnRun
	UseMaterialFloor      bool
	GovernorName          string
	FaceName              string
	Channel               string
	PrincipalRole         string
	SessionUserName       string
	RenderLatestUserInput string
	ProposalDeliveryMode  string
	RenderDeliveryMode    string
	CronJobID             string
	CurrentFaceModel      face.Renderer
	BaseGovernorAwareness prompt.RuntimeAwareness
	RuntimeAwareness      prompt.RuntimeAwareness
	PolicyFunc            func(turn.Request) turn.Policy
	ErrContext            turnCommitErrorContext
	Inbound               core.InboundMessage
	Now                   time.Time
	UseFacePort           bool
	Style                 string
}

type runtimeMaintenanceTurnAssembler struct {
	runtime *Runtime
}

func newMaintenanceTurnAssembler(runtime *Runtime) maintenanceTurnAssembler {
	return &runtimeMaintenanceTurnAssembler{runtime: runtime}
}

func (a *runtimeMaintenanceTurnAssembler) Run(ctx context.Context, input maintenanceTurnAssemblyInput) (*turn.Result, error) {
	if a == nil || a.runtime == nil {
		return nil, fmt.Errorf("maintenance turn assembler unavailable")
	}
	if input.Sess == nil {
		return nil, fmt.Errorf("maintenance turn assembly missing session")
	}

	runKind := input.RunKind
	if runKind == "" {
		runKind = maintenanceRunKind(input.Species)
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	channel := firstNonEmpty(strings.TrimSpace(input.Channel), "telegram")
	governorName := firstNonEmpty(strings.TrimSpace(input.GovernorName), a.runtime.governorName())
	faceName := firstNonEmpty(strings.TrimSpace(input.FaceName), a.runtime.faceName())
	style := firstNonEmpty(strings.TrimSpace(input.Style), defaultMaintenanceTurnStyle)
	policyFunc := input.PolicyFunc
	if policyFunc == nil {
		policyFunc = func(turn.Request) turn.Policy { return turn.Policy{} }
	}

	coordinator := &maintenanceTurnCoordinator{
		runtime:               a.runtime,
		species:               input.Species,
		key:                   input.Key,
		sess:                  input.Sess,
		scope:                 input.Scope,
		prepared:              input.Prepared,
		exec:                  input.Exec,
		promptContext:         input.PromptContext,
		hiddenInputs:          input.HiddenInputs,
		recoveryRuns:          input.RecoveryRuns,
		useMaterialFloor:      input.UseMaterialFloor,
		governorName:          governorName,
		faceName:              faceName,
		channelName:           channel,
		principalRole:         input.PrincipalRole,
		sessionUserName:       input.SessionUserName,
		renderLatestUserInput: input.RenderLatestUserInput,
		proposalDeliveryMode:  input.ProposalDeliveryMode,
		renderDeliveryMode:    input.RenderDeliveryMode,
		cronJobID:             input.CronJobID,
		currentFaceModel:      input.CurrentFaceModel,
		baseGovernorAwareness: input.BaseGovernorAwareness,
	}

	machine := &turn.Machine{
		Governor: coordinator,
		Persistence: &maintenanceTurnPersistencePort{
			runtime: a.runtime,
			key:     input.Key,
			sess:    input.Sess,
			errCtx:  input.ErrContext,
		},
		Options: turn.Options{
			GovernorName: governorName,
			FaceName:     faceName,
			Channel:      channel,
			Style:        style,
		},
		RuntimeAwareness: input.RuntimeAwareness,
		PolicyFunc:       policyFunc,
	}
	if input.UseFacePort {
		machine.Face = coordinator
	}

	return machine.Handle(ctx, turn.Request{
		RunKind:    runKind,
		SessionKey: input.Key,
		Inbound:    input.Inbound,
		Session:    input.Sess,
		Now:        input.Now,
	})
}
