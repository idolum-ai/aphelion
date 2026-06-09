//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) sendContinuationBlockedNotice(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState) error {
	if r == nil || r.outbound == nil {
		return nil
	}
	text := strings.TrimSpace(r.renderContinuationBlockedNotice(ctx, key, msg, state))
	if text == "" {
		return nil
	}
	_, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID: msg.ChatID,
		Text:   text,
	})
	if err != nil {
		return fmt.Errorf("send continuation blocked notice: %w", err)
	}
	return nil
}

func (r *Runtime) renderContinuationBlockedNotice(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState) string {
	governorName := prompt.DefaultGovernorName
	if r != nil {
		governorName = r.governorName()
	}
	fallback := renderContinuationBlockedFallback(state, governorName)
	if r == nil {
		return fallback
	}
	if r.faceBackend == face.BackendFloorFallback {
		return fallback
	}
	renderer := r.currentFaceRenderer()
	if renderer == nil {
		return fallback
	}
	faceName := r.faceName()
	workspaceRoot := ""
	if r.cfg != nil {
		workspaceRoot = strings.TrimSpace(r.cfg.Agent.PromptRoot)
	}

	rendered, err := renderer.Render(ctx, face.RenderRequest{
		GovernorName:    governorName,
		FaceName:        faceName,
		Channel:         "telegram",
		Mode:            "repair",
		PrincipalRole:   "approved_user",
		WorkspaceRoot:   workspaceRoot,
		FloorText:       fallback,
		LatestUserInput: strings.TrimSpace(msg.Text),
		CandidateReply:  fallback,
		RepairNotes: []string{
			continuationFaceRepairIdentityNote(faceName),
			"Explain why continuation is unavailable right now.",
		},
		Runtime: prompt.RuntimeAwareness{
			ContinuationStatus:         string(state.Status),
			ContinuationActive:         state.Active(),
			ContinuationPersonaIntent:  string(state.PersonaIntent.Decision),
			ContinuationPersonaWhy:     state.PersonaIntent.Rationale,
			ContinuationGovernorIntent: string(state.GovernorIntent.Decision),
			ContinuationGovernorWhy:    state.GovernorIntent.Rationale,
			ContinuationRatified:       state.GovernorIntent.Ratified,
			ContinuationBlockedReason:  state.HandshakeBlockedReason,
		},
	})
	if err != nil {
		return fallback
	}
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return fallback
	}
	grounded, note := r.groundContinuationBlockedNoticeWithExecutionEvidence(key, state, rendered)
	if note != "" {
		log.Printf("WARN continuation blocked notice grounding fallback chat_id=%d note=%s", key.ChatID, note)
	}
	return grounded
}

func (r *Runtime) groundContinuationBlockedNoticeWithExecutionEvidence(
	key session.SessionKey,
	state session.ContinuationState,
	candidate string,
) (string, string) {
	candidate = strings.TrimSpace(candidate)
	governorName := prompt.DefaultGovernorName
	if r != nil {
		governorName = r.governorName()
	}
	fallback := renderContinuationBlockedFallback(state, governorName)
	if candidate == "" {
		return fallback, "rendered continuation blocked notice is empty"
	}
	if r == nil || r.store == nil {
		return candidate, ""
	}
	events, err := r.store.LatestExecutionEventsBySession(key, 300)
	if err != nil || len(events) == 0 {
		return fallback, "continuation evidence is unavailable; " + continuationOperationalStateNote
	}
	latestType := ""
	for _, event := range events {
		eventType := strings.TrimSpace(event.EventType)
		switch eventType {
		case core.ExecutionEventContinuationOffered,
			core.ExecutionEventContinuationApproved,
			core.ExecutionEventContinuationRevoked,
			core.ExecutionEventContinuationConsumed,
			core.ExecutionEventContinuationBlocked:
			latestType = eventType
		}
	}
	if latestType != core.ExecutionEventContinuationBlocked {
		return fallback, fmt.Sprintf("blocked notice is not grounded by blocked continuation event (latest=%s); %s", latestType, continuationOperationalStateNote)
	}
	if strings.TrimSpace(state.HandshakeBlockedReason) == "" {
		return fallback, "blocked notice state has no blocked reason"
	}
	return candidate, ""
}

func renderContinuationBlockedFallback(state session.ContinuationState, governorName string) string {
	reason := strings.TrimSpace(state.HandshakeBlockedReason)
	switch reason {
	case "persona_intent_missing":
		return "I need a clearer next step before continuing."
	case "persona_rationale_missing":
		return "I need a clearer reason to continue before taking the next step."
	case "persona_not_willing":
		return "I am holding here instead of continuing automatically."
	case "governor_intent_missing":
		return "I need a safer approval path before continuing."
	case "governor_rationale_missing":
		return "I need a clearer approval reason before continuing."
	case "governor_not_ratified":
		return "I need approval before continuing."
	case "governor_not_willing":
		return "I am holding here until there is a safer next step."
	default:
		return "I need a clearer or safer next step before continuing."
	}
}

func continuationBlockedFallbackGovernorName(governorName string) string {
	if trimmed := strings.TrimSpace(governorName); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(prompt.DefaultGovernorName); trimmed != "" {
		return trimmed
	}
	return "System"
}
