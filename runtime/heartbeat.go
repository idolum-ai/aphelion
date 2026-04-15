//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

const heartbeatSessionChatID int64 = -1

func (r *Runtime) StartHeartbeatLoop(ctx context.Context, logger func(string, ...any)) {
	if r == nil || !r.cfg.Heartbeat.Enabled {
		return
	}
	if logger == nil {
		logger = log.Printf
	}

	cadence, err := time.ParseDuration(strings.TrimSpace(r.cfg.Heartbeat.Every))
	if err != nil || cadence <= 0 {
		logger("WARN heartbeat disabled due to invalid cadence: %q err=%v", r.cfg.Heartbeat.Every, err)
		return
	}

	go runPeriodic(ctx, cadence, func(runCtx context.Context) {
		if err := r.runHeartbeatOnce(runCtx, time.Now()); err != nil {
			logger("WARN heartbeat failed: %v", err)
		}
	})
}

func (r *Runtime) runHeartbeatOnce(ctx context.Context, now time.Time) (err error) {
	targetChatID, deliver := r.resolveHeartbeatTarget(now)
	if targetChatID == 0 {
		return nil
	}

	events, err := r.store.PendingReviewEvents(targetChatID, maxReviewEventsPerTurn)
	if err != nil {
		return fmt.Errorf("load pending review events: %w", err)
	}

	scope, err := r.scopeForPrincipal(principal.Principal{Role: principal.RoleAdmin})
	if err != nil {
		return fmt.Errorf("resolve heartbeat scope: %w", err)
	}

	maintenanceKey := session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}
	unlockMaintenance := r.lockSession(maintenanceKey)
	defer unlockMaintenance()

	maintenanceSession, err := r.store.Load(maintenanceKey)
	if err != nil {
		return fmt.Errorf("load heartbeat session: %w", err)
	}
	applySessionScope(maintenanceSession, maintenanceKey)
	lastMaintenanceAt := maintenanceSession.UpdatedAt
	if maintenanceSession.TurnCount == 0 {
		lastMaintenanceAt = time.Time{}
	}

	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		return fmt.Errorf("load workspace prompt context: %w", err)
	}
	hiddenInputs := r.assembleHeartbeatHiddenInputs(ctx, scope, now, deliver, events)
	governorAwareness := r.withHiddenInputAwareness(r.governorRuntimeAwareness(scope, session.TurnRunKindHeartbeat, "system", pipeline.GovernorExecution{}), hiddenInputs)
	governorPrompt := prompt.GovernorRequest{
		GovernorName:    prompt.DefaultGovernorName,
		GovernorBackend: r.governorBackend,
		PrincipalRole:   "admin",
		WorkspaceRoot:   scope.WorkingRoot,
		Workspace:       promptContext,
		Runtime:         governorAwareness,
	}
	systemBlocks := prompt.BuildGovernorPromptBlocks(governorPrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)

	reflectionSummary := ""
	if r.heartbeatShouldReflect(lastMaintenanceAt, now) {
		var reflectionErr error
		semanticScope := "shared"
		semanticPrincipalID := ""
		if scope.Principal.Role == principal.RoleApprovedUser {
			semanticScope = "principal"
			if scope.Principal.TelegramUserID > 0 {
				semanticPrincipalID = strconv.FormatInt(scope.Principal.TelegramUserID, 10)
			}
		}
		reflectionSummary, reflectionErr = r.reflectCuratedMemory(ctx, dynamicPromptRoot(scope), semanticScope, semanticPrincipalID, systemBlocks, lastMaintenanceAt, now, events)
		if reflectionErr != nil {
			log.Printf("WARN heartbeat reflection failed: %v", reflectionErr)
		}
	}
	eligibleForOutreach := deliver && hiddenInputs.ReflectiveOutreachEligible()
	if len(events) == 0 && !eligibleForOutreach {
		if strings.TrimSpace(reflectionSummary) == "" {
			return nil
		}

		maintenanceSession.ChatType = "system"
		maintenanceSession.UserName = "heartbeat"
		maintenanceSession.SystemPrompt = systemPrompt
		if err := r.store.Save(maintenanceSession, appendSyntheticTurn(maintenanceSession, "[heartbeat reflection]", reflectionSummary, reflectionSummary, ""), core.TokenUsage{}); err != nil {
			return fmt.Errorf("save heartbeat reflection note: %w", err)
		}
		return nil
	}

	history, err := session.ToAgentHistory(maintenanceSession.Messages)
	if err != nil {
		return fmt.Errorf("assemble heartbeat history: %w", err)
	}

	requestText := renderHeartbeatRequest(targetChatID, events, deliver, hiddenInputs)
	monitor := r.startTurnMonitor(maintenanceKey, session.TurnRunKindHeartbeat, requestText, nil, nil)
	defer monitor.Finish(ctx, err)
	input := make([]agent.Message, 0, len(history)+2)
	if systemPrompt != "" {
		input = append(input, agent.Message{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks})
	}
	currentFaceModel := r.currentFaceRenderer()
	if eligibleForOutreach && r.faceBackend != face.BackendFloorFallback {
		if proposer, ok := currentFaceModel.(face.Proposer); ok {
			faceAwareness := r.withHiddenInputAwareness(r.governorRuntimeAwareness(scope, session.TurnRunKindHeartbeat, "telegram", pipeline.GovernorExecution{}), hiddenInputs)
			faceAwareness.ArtifactMode = "scene"
			faceAwareness.DeliveryMode = "heartbeat_proposal"
			proposal, proposalErr := proposer.Propose(ctx, face.ProposalRequest{
				GovernorName:    prompt.DefaultGovernorName,
				FaceName:        face.DefaultFaceName,
				Channel:         "telegram",
				Mode:            "proposal",
				PrincipalRole:   "admin",
				WorkspaceRoot:   faceWorkspaceRoot(scope),
				LatestUserInput: requestText,
				Runtime:         faceAwareness,
			})
			if proposalErr != nil {
				log.Printf("WARN heartbeat idolum proposal failed backend=%s err=%v", r.faceBackend, proposalErr)
			} else if advisory := prompt.RenderIdolumProposalForGovernor(face.DefaultFaceName, strings.TrimSpace(proposal)); advisory != "" {
				input = append(input, agent.Message{Role: "system", Content: advisory})
			}
		}
	}
	input = append(input, history...)
	input = append(input, agent.Message{Role: "user", Content: requestText})

	result, outHistory, err := agent.RunTurn(ctx, r.provider, nil, &agent.Budget{
		Max:     r.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, r.reasoningOptionsForRun(session.TurnRunKindHeartbeat), input)
	if err != nil {
		return fmt.Errorf("run heartbeat turn: %w", err)
	}
	if len(outHistory) < len(input) {
		return fmt.Errorf("invalid heartbeat output: history shrank from %d to %d", len(input), len(outHistory))
	}

	materialFloor, floorText, _ := governorMaterialArtifact(result.Text, true)
	if floorText == "" {
		return nil
	}
	floorMetadata := encodeFloorMetadata(hiddenInputs.Metadata())

	maintenanceSession.ChatType = "system"
	maintenanceSession.UserName = "heartbeat"
	maintenanceSession.SystemPrompt = systemPrompt
	maintenanceSession.TurnCount++
	maintenanceSession.LastFloorText = floorText
	maintenanceSession.LastFloorMetadata = floorMetadata
	newMessages, err := session.NewMessagesForTurn(requestText, outHistory[len(input):], maintenanceSession.TurnCount)
	if err != nil {
		return fmt.Errorf("convert heartbeat messages: %w", err)
	}
	newMessages = setLastAssistantFloor(newMessages, floorText)
	newMessages = setLastAssistantFloorMetadata(newMessages, floorMetadata)
	if err := r.store.Save(maintenanceSession, newMessages, result.TokenUsage); err != nil {
		return fmt.Errorf("save heartbeat session: %w", err)
	}

	if !eligibleForOutreach {
		return nil
	}

	replyText := face.SerializeFloorFallback(materialFloor, floorText, face.FallbackOptions{Channel: "telegram"})
	if r.faceBackend != face.BackendFloorFallback && currentFaceModel != nil {
		faceAwareness := r.withHiddenInputAwareness(r.governorRuntimeAwareness(scope, session.TurnRunKindHeartbeat, "telegram", pipeline.GovernorExecution{}), hiddenInputs)
		faceAwareness.DeliveryMode = "heartbeat_delivery"
		renderedReply, renderErr := currentFaceModel.Render(ctx, face.RenderRequest{
			GovernorName:    prompt.DefaultGovernorName,
			FaceName:        face.DefaultFaceName,
			Channel:         "telegram",
			PrincipalRole:   "admin",
			WorkspaceRoot:   faceWorkspaceRoot(scope),
			FloorText:       floorText,
			MaterialFloor:   materialFloor,
			LatestUserInput: "[heartbeat]",
			Runtime:         faceAwareness,
		})
		if renderErr != nil {
			log.Printf("WARN heartbeat face render failed backend=%s err=%v; using floor_fallback serializer", r.faceBackend, renderErr)
		} else if strings.TrimSpace(renderedReply) != "" {
			replyText = strings.TrimSpace(renderedReply)
		}
	}

	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID: targetChatID,
		Text:   replyText,
	})
	if err != nil {
		return fmt.Errorf("send heartbeat outbound: %w", err)
	}

	adminKey := session.SessionKey{ChatID: targetChatID, UserID: 0, Scope: telegramDMScopeRef(targetChatID)}
	unlockAdmin := r.lockSession(adminKey)
	defer unlockAdmin()

	adminSession, err := r.store.Load(adminKey)
	if err != nil {
		return fmt.Errorf("load admin target session: %w", err)
	}
	applySessionScope(adminSession, adminKey)
	adminSession.ChatType = "dm"
	adminSession.SystemPrompt = systemPrompt
	if err := r.store.Save(adminSession, appendAssistantTurn(adminSession, replyText, floorText, floorMetadata), core.TokenUsage{}); err != nil {
		return fmt.Errorf("save heartbeat admin session: %w", err)
	}
	if err := r.store.RecordOutbound(adminKey, adminSession.TurnCount, msgID, "heartbeat"); err != nil {
		return fmt.Errorf("record heartbeat outbound: %w", err)
	}

	ids := make([]int64, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	if err := r.store.MarkReviewDelivered(ids); err != nil {
		return fmt.Errorf("mark heartbeat review events delivered: %w", err)
	}

	return nil
}

func (r *Runtime) resolveHeartbeatTarget(now time.Time) (int64, bool) {
	adminIDs := uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)
	if len(adminIDs) == 0 {
		return 0, false
	}

	target := strings.TrimSpace(r.cfg.Heartbeat.Target)
	deliver := r.heartbeatWithinActiveHours(now)
	switch target {
	case "", "last":
		if targetChatID := r.lastActiveAdminChat(adminIDs); targetChatID != 0 {
			return targetChatID, deliver
		}
		return adminIDs[0], deliver
	case "none":
		return adminIDs[0], false
	default:
		id, err := parseAdminChatID(target)
		if err != nil {
			return adminIDs[0], false
		}
		return id, deliver
	}
}

func (r *Runtime) heartbeatShouldReflect(lastMaintenanceAt time.Time, now time.Time) bool {
	if r == nil || !r.cfg.Memory.Reflection.Enabled {
		return false
	}
	if lastMaintenanceAt.IsZero() {
		return true
	}
	every, err := time.ParseDuration(strings.TrimSpace(r.cfg.Memory.Reflection.Every))
	if err != nil || every <= 0 {
		return true
	}
	return now.Sub(lastMaintenanceAt) >= every
}

func (r *Runtime) lastActiveAdminChat(adminIDs []int64) int64 {
	keys, err := r.store.ListActive(30 * 24 * time.Hour)
	if err != nil {
		return 0
	}
	adminSet := make(map[int64]struct{}, len(adminIDs))
	for _, id := range adminIDs {
		adminSet[id] = struct{}{}
	}
	for _, key := range keys {
		if key.UserID != 0 || key.ChatID <= 0 {
			continue
		}
		if _, ok := adminSet[key.ChatID]; ok {
			return key.ChatID
		}
	}
	return 0
}

func (r *Runtime) heartbeatWithinActiveHours(now time.Time) bool {
	start := strings.TrimSpace(r.cfg.Heartbeat.ActiveHours.Start)
	end := strings.TrimSpace(r.cfg.Heartbeat.ActiveHours.End)
	if start == "" || end == "" {
		return true
	}

	loc := time.UTC
	if zone := strings.TrimSpace(r.cfg.Heartbeat.ActiveHours.Timezone); zone != "" {
		loaded, err := time.LoadLocation(zone)
		if err == nil {
			loc = loaded
		}
	}
	localNow := now.In(loc)

	startTOD, err := time.Parse("15:04", start)
	if err != nil {
		return true
	}
	endTOD, err := time.Parse("15:04", end)
	if err != nil {
		return true
	}

	startTime := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), startTOD.Hour(), startTOD.Minute(), 0, 0, loc)
	endTime := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), endTOD.Hour(), endTOD.Minute(), 0, 0, loc)

	if !endTime.After(startTime) {
		if localNow.Before(endTime) {
			startTime = startTime.Add(-24 * time.Hour)
		} else {
			endTime = endTime.Add(24 * time.Hour)
		}
	}
	return !localNow.Before(startTime) && localNow.Before(endTime)
}

func renderHeartbeatRequest(targetChatID int64, events []session.ReviewEvent, deliver bool, hiddenInputs hiddenInputSet) string {
	lines := []string{
		"Heartbeat maintenance turn.",
		fmt.Sprintf("Target admin chat: %d", targetChatID),
		fmt.Sprintf("Delivery allowed this turn: %t", deliver),
		fmt.Sprintf("Reflective outreach eligible this turn: %t", hiddenInputs.ReflectiveOutreachEligible()),
	}
	if len(events) == 0 {
		lines = append(lines,
			"There are no pending review events this turn.",
			"Produce a concise maintenance floor from hidden-input convergence and latent state alone.",
		)
	} else {
		lines = append(lines,
			"Produce a concise maintenance floor from the pending review events.",
			"Pending review events:",
		)
	}
	lines = append(lines, "If reflective outreach is not eligible, keep the floor internal; the runtime may stay silent.")
	if hiddenInputs.Active() {
		lines = append(lines, fmt.Sprintf("Hidden input categories: %s", strings.Join(hiddenInputs.Categories(), ", ")))
		if summary := hiddenInputs.ProvenanceSummary(); summary != "" {
			lines = append(lines, "Hidden input provenance: "+summary)
		}
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].ID < events[j].ID
	})
	for _, event := range events {
		lines = append(lines, fmt.Sprintf(
			"- event=%d source_chat=%d source_user=%d role=%s turns=%d-%d summary=%s",
			event.ID,
			event.SourceChatID,
			event.SourceUserID,
			event.SourceRole,
			event.TurnFrom,
			event.TurnTo,
			strings.TrimSpace(event.Summary),
		))
	}
	return strings.Join(lines, "\n")
}

func parseAdminChatID(raw string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("admin chat id must be positive")
	}
	return value, nil
}
