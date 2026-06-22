//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
	"github.com/idolum-ai/aphelion/workspace"
)

type maintenanceTurnSpecies string

const (
	maintenanceTurnHeartbeat maintenanceTurnSpecies = "heartbeat"
	maintenanceTurnCron      maintenanceTurnSpecies = "cron"
	maintenanceTurnRecovery  maintenanceTurnSpecies = "recovery"
)

type maintenanceTurnCoordinator struct {
	runtime               *Runtime
	species               maintenanceTurnSpecies
	key                   session.SessionKey
	sess                  *session.Session
	scope                 sandbox.Scope
	prepared              pipeline.TurnPrepareContract
	exec                  pipeline.TurnExecutionContract
	promptContext         *workspace.PromptContext
	hiddenInputs          hiddenInputSet
	recoveryRuns          []session.TurnRun
	useMaterialFloor      bool
	governorName          string
	faceName              string
	channelName           string
	principalRole         string
	sessionUserName       string
	renderLatestUserInput string
	proposalDeliveryMode  string
	renderDeliveryMode    string
	scheduledJobID        string
	scheduledJobKind      string
	cronJobID             string
	currentFaceModel      face.Renderer
	baseGovernorAwareness prompt.RuntimeAwareness
	lastRunID             int64
	lastGovernor          *turn.GovernorResult
}

func (c *maintenanceTurnCoordinator) Propose(ctx context.Context, req turn.FaceProposalRequest) (*turn.FaceProposalResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("maintenance coordinator unavailable")
	}
	if c.species != maintenanceTurnHeartbeat {
		return &turn.FaceProposalResult{}, nil
	}
	proposer, ok := c.currentFaceModel.(face.Proposer)
	if c.currentFaceModel == nil || !ok || c.runtime.faceBackend == face.BackendFloorFallback {
		return &turn.FaceProposalResult{}, nil
	}

	awareness := req.Runtime
	awareness.ArtifactMode = "scene"
	if strings.TrimSpace(awareness.DeliveryMode) == "" {
		awareness.DeliveryMode = firstNonEmpty(strings.TrimSpace(c.proposalDeliveryMode), "heartbeat_proposal")
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "proposal"
	}
	proposal, err := proposer.Propose(ctx, face.ProposalRequest{
		GovernorName:      req.GovernorName,
		FaceName:          req.FaceName,
		Channel:           req.Channel,
		Mode:              mode,
		PrincipalRole:     req.PrincipalRole,
		WorkspaceRoot:     faceWorkspaceRoot(c.scope),
		LatestUserInput:   req.LatestUserInput,
		PriorProposal:     "",
		BrokerageFeedback: "",
		Runtime:           awareness,
	})
	if err != nil {
		log.Printf("WARN heartbeat idolum proposal failed backend=%s err=%v", c.runtime.faceBackend, err)
		return &turn.FaceProposalResult{}, nil
	}
	return &turn.FaceProposalResult{
		Note:  strings.TrimSpace(proposal),
		Usage: consumeFaceUsage(c.currentFaceModel),
	}, nil
}

func (c *maintenanceTurnCoordinator) Execute(ctx context.Context, req turn.GovernorRequest) (*turn.GovernorResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("maintenance coordinator unavailable")
	}
	runKind := req.RunKind
	if runKind == "" {
		runKind = maintenanceRunKind(c.species)
	}

	governorName := c.coordinatorGovernorName()
	principalRole := c.principalRoleOrDefault()

	c.sess.ChatType = "system"
	c.sess.UserName = c.sessionUserNameOrDefault()

	governorPrompt := prompt.GovernorRequest{
		GovernorName:    governorName,
		GovernorBackend: c.exec.Backend,
		PrincipalRole:   principalRole,
		WorkspaceRoot:   c.scope.WorkingRoot,
		Workspace:       c.promptContext,
		Runtime:         c.baseGovernorAwareness,
	}
	systemBlocks := prompt.BuildGovernorPromptBlocks(governorPrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)
	c.sess.SystemPrompt = systemPrompt

	history, err := session.ToAgentHistory(c.sess.Messages)
	if err != nil {
		return nil, fmt.Errorf("assemble %s history: %w", c.species, err)
	}

	monitor, err := c.runtime.startTurnMonitor(ctx, c.key, runKind, c.prepared.LedgerText, nil, nil, core.InboundMessage{})
	if err != nil {
		return nil, err
	}
	ctx = monitor.Context()
	c.lastRunID = monitor.runID
	var monitorErr error
	defer monitor.Finish(ctx, monitorErr)

	input := make([]agent.Message, 0, len(history)+3)
	if systemPrompt != "" {
		input = append(input, agent.Message{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks})
	}
	if note := strings.TrimSpace(req.FaceNote); note != "" {
		if advisory := prompt.RenderIdolumProposalForGovernor(c.coordinatorFaceName(), note); advisory != "" {
			input = append(input, agent.Message{Role: "system", Content: advisory})
		}
	}
	input = append(input, history...)
	input = append(input, agent.Message{Role: "user", Content: c.prepared.UserText})

	turnResult, outHistory, runErr := agent.RunTurn(ctx, c.exec.Provider, nil, &agent.Budget{
		Max:     c.runtime.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, c.runtime.reasoningOptionsForRun(runKind), input)
	if runErr != nil {
		monitorErr = fmt.Errorf("run %s turn: %w", c.species, runErr)
		return nil, monitorErr
	}
	if len(outHistory) < len(input) {
		monitorErr = fmt.Errorf("invalid %s output: history shrank from %d to %d", c.species, len(input), len(outHistory))
		return nil, monitorErr
	}

	c.sess.TurnCount++

	materialFloor := core.MaterialPacket{}
	floorText := ""
	materialStructured := false
	switch c.species {
	case maintenanceTurnRecovery:
		floorText = strings.TrimSpace(turnResult.Text)
		if floorText == "" {
			floorText = fallbackRecoverySummary(c.recoveryRuns)
		}
	default:
		materialFloor, floorText, materialStructured = pipeline.BuildFloorFromGovernor(turnResult.Text, c.useMaterialFloor)
		if err := c.runtime.recordMaterialFloorJudgmentUse(c.key, c.lastRunID, turnResult.Text, materialFloor, floorText, materialStructured, time.Now().UTC()); err != nil {
			return nil, err
		}
	}

	floorMetadata := ""
	if c.species == maintenanceTurnHeartbeat {
		floorMetadata = encodeFloorMetadata(c.hiddenInputs.Metadata())
	}

	governorResult := &turn.GovernorResult{
		Turn:            turnResult,
		OutHistory:      outHistory,
		HistoryInputLen: len(input),
		FloorText:       floorText,
		FloorMetadata:   floorMetadata,
		MaterialFloor:   materialFloor,
		Prepared:        c.prepared,
	}
	c.lastGovernor = governorResult
	return governorResult, nil
}

func (c *maintenanceTurnCoordinator) turnRunID() int64 {
	if c == nil {
		return 0
	}
	return c.lastRunID
}

func (c *maintenanceTurnCoordinator) Render(ctx context.Context, req turn.FaceRenderRequest) (*turn.FaceRenderResult, error) {
	if c == nil || c.runtime == nil {
		return nil, fmt.Errorf("maintenance coordinator unavailable")
	}
	if c.lastGovernor == nil {
		return &turn.FaceRenderResult{}, nil
	}

	replyText := pipeline.SerializeFloorFallback(c.lastGovernor.MaterialFloor, c.lastGovernor.FloorText, pipeline.FallbackOptions{
		Channel: c.requestChannel(),
	})
	if maintenanceRunKindSkipsFaceRender(maintenanceRunKind(c.species)) {
		return &turn.FaceRenderResult{Text: strings.TrimSpace(replyText)}, nil
	}
	if c.currentFaceModel == nil || c.runtime.faceBackend == face.BackendFloorFallback {
		return &turn.FaceRenderResult{Text: strings.TrimSpace(replyText)}, nil
	}

	faceAwareness := req.Runtime
	if strings.TrimSpace(faceAwareness.DeliveryMode) == "" {
		faceAwareness.DeliveryMode = c.renderDeliveryMode
	}
	latestInput := strings.TrimSpace(c.renderLatestUserInput)
	if latestInput == "" {
		latestInput = req.LatestUserInput
	}

	renderedReply, err := c.currentFaceModel.Render(ctx, face.RenderRequest{
		GovernorName:    req.GovernorName,
		FaceName:        req.FaceName,
		Channel:         req.Channel,
		PrincipalRole:   req.PrincipalRole,
		WorkspaceRoot:   faceWorkspaceRoot(c.scope),
		FloorText:       c.lastGovernor.FloorText,
		MaterialFloor:   c.lastGovernor.MaterialFloor,
		LatestUserInput: latestInput,
		Runtime:         faceAwareness,
	})
	if err != nil {
		switch c.species {
		case maintenanceTurnHeartbeat:
			log.Printf("WARN heartbeat face render failed backend=%s err=%v; using floor_fallback serializer", c.runtime.faceBackend, err)
		case maintenanceTurnCron:
			log.Printf("WARN cron face render failed backend=%s job=%s err=%v; using floor_fallback serializer", c.runtime.faceBackend, firstNonEmpty(c.scheduledJobID, c.cronJobID), err)
		default:
			log.Printf("WARN maintenance face render failed backend=%s err=%v; using floor_fallback serializer", c.runtime.faceBackend, err)
		}
	} else if strings.TrimSpace(renderedReply) != "" {
		replyText = strings.TrimSpace(renderedReply)
	}

	return &turn.FaceRenderResult{
		Text:  strings.TrimSpace(replyText),
		Usage: consumeFaceUsage(c.currentFaceModel),
	}, nil
}

func (c *maintenanceTurnCoordinator) requestChannel() string {
	if c == nil {
		return "telegram"
	}
	if trimmed := strings.TrimSpace(c.channelName); trimmed != "" {
		return trimmed
	}
	return "telegram"
}

func (c *maintenanceTurnCoordinator) coordinatorGovernorName() string {
	if c == nil {
		return prompt.DefaultGovernorName
	}
	if trimmed := strings.TrimSpace(c.governorName); trimmed != "" {
		return trimmed
	}
	if c.runtime != nil {
		return c.runtime.governorName()
	}
	return prompt.DefaultGovernorName
}

func (c *maintenanceTurnCoordinator) coordinatorFaceName() string {
	if c == nil {
		return face.DefaultFaceName
	}
	if trimmed := strings.TrimSpace(c.faceName); trimmed != "" {
		return trimmed
	}
	if c.runtime != nil {
		return c.runtime.faceName()
	}
	return face.DefaultFaceName
}

func (c *maintenanceTurnCoordinator) principalRoleOrDefault() string {
	if c == nil {
		return "admin"
	}
	if trimmed := strings.TrimSpace(c.principalRole); trimmed != "" {
		return trimmed
	}
	return "admin"
}

func (c *maintenanceTurnCoordinator) sessionUserNameOrDefault() string {
	if c == nil {
		return "maintenance"
	}
	if trimmed := strings.TrimSpace(c.sessionUserName); trimmed != "" {
		return trimmed
	}
	return "maintenance"
}

func maintenanceRunKind(species maintenanceTurnSpecies) session.TurnRunKind {
	switch species {
	case maintenanceTurnHeartbeat:
		return session.TurnRunKindHeartbeat
	case maintenanceTurnCron:
		return session.TurnRunKindCron
	case maintenanceTurnRecovery:
		return session.TurnRunKindRecovery
	default:
		return session.TurnRunKindInteractive
	}
}

func maintenanceRunKindSkipsFaceRender(kind session.TurnRunKind) bool {
	switch kind {
	case session.TurnRunKindHeartbeat, session.TurnRunKindCron, session.TurnRunKindRecovery:
		return true
	default:
		return false
	}
}

type maintenanceTurnPersistencePort struct {
	runtime     *Runtime
	key         session.SessionKey
	sess        *session.Session
	runIDSource interface {
		turnRunID() int64
	}
	errCtx turnCommitErrorContext
}

func (p *maintenanceTurnPersistencePort) Persist(ctx context.Context, req turn.CommitRequest) (*turn.CommitResult, error) {
	if p == nil || p.runtime == nil {
		return nil, fmt.Errorf("maintenance persistence port unavailable")
	}
	if req.Result == nil || req.Result.Turn == nil {
		return nil, fmt.Errorf("maintenance persistence request missing result")
	}
	if strings.TrimSpace(req.Result.FloorText) == "" {
		return &turn.CommitResult{}, nil
	}

	result, err := p.runtime.persistTurn(ctx, turnCommitInput{
		Key:             p.key,
		RunID:           p.currentRunID(),
		RunKind:         req.Request.RunKind,
		Sess:            p.sess,
		Prepared:        req.Result.Prepared,
		OutHistory:      req.Result.OutHistory,
		HistoryInputLen: req.Result.HistoryInputLen,
		Result:          req.Result.Turn,
		FloorText:       req.Result.FloorText,
		FloorMetadata:   req.Result.FloorMetadata,
		ReplyText:       req.Result.FloorText,
		StreamedReply:   false,
		OutboundID:      0,
		OutboundType:    "",
		RecordOutbound:  false,
		Audit:           nil,
		ErrCtx:          p.errCtx,
	})
	if err != nil {
		return nil, err
	}
	return &turn.CommitResult{Persisted: result.Committed}, nil
}

func (p *maintenanceTurnPersistencePort) currentRunID() int64 {
	if p == nil || p.runIDSource == nil {
		return 0
	}
	return p.runIDSource.turnRunID()
}
