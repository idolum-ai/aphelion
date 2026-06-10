//go:build linux

package telegramcontrol

import (
	"context"
	"fmt"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/session"
)

func (c CommandControl) QueueClarification(ctx context.Context, msg core.InboundMessage) error {
	updateKind := "callback_clarification"
	switch msg.IngressSurface {
	case telegramruntime.ContextClarificationIngressSurface:
		updateKind = "callback_context_clarification"
	case telegramruntime.MemoryClarificationIngressSurface:
		updateKind = "callback_memory_clarification"
	case telegramruntime.MissionClarificationIngressSurface:
		updateKind = "callback_mission_clarification"
	}
	if err := recordTelegramCallbackWorkAccepted(c.Store, msg, updateKind); err != nil {
		return err
	}
	return c.RouteAccepted(ctx, msg)
}

func (c CommandControl) QueueMissionClarification(ctx context.Context, msg core.InboundMessage, promptID string) error {
	if c.Runtime == nil {
		return fmt.Errorf("Mission Question is unavailable.")
	}
	msg.IngressSurface = telegramruntime.MissionClarificationIngressSurface
	if err := recordTelegramCallbackWorkAccepted(c.Store, msg, "callback_mission_clarification"); err != nil {
		return err
	}
	if _, err := c.Runtime.ResolveMissionAskPrompt(ctx, msg.SenderID, promptID, session.MissionAskStatusAsked, "mission clarification queued"); err != nil {
		return err
	}
	return c.RouteAccepted(ctx, msg)
}

func (c CommandControl) ReentryRecommendation(ctx context.Context, senderID int64, recommendationID string) (session.ReentryRecommendation, bool, error) {
	if c.Runtime == nil {
		return session.ReentryRecommendation{}, false, fmt.Errorf("Re-entry recommendation is unavailable.")
	}
	return c.Runtime.ReentryRecommendation(ctx, senderID, recommendationID)
}

func (c CommandControl) IgnoreReentryRecommendation(ctx context.Context, senderID int64, recommendationID string) (session.ReentryRecommendation, error) {
	if c.Runtime == nil {
		return session.ReentryRecommendation{}, fmt.Errorf("Re-entry recommendation is unavailable.")
	}
	return c.Runtime.IgnoreReentryRecommendation(ctx, senderID, recommendationID)
}

func (c CommandControl) QueueReentryRecommendation(ctx context.Context, msg core.InboundMessage, recommendationID string, candidateID string) (session.ReentryRecommendation, session.ReentryRecommendationCandidate, bool, error) {
	if c.Runtime == nil {
		return session.ReentryRecommendation{}, session.ReentryRecommendationCandidate{}, false, fmt.Errorf("Re-entry recommendation is unavailable.")
	}
	record, candidate, selected, err := c.Runtime.PrepareReentryRecommendationSelection(ctx, msg.SenderID, recommendationID, candidateID)
	if err != nil || !selected {
		return record, candidate, selected, err
	}
	msg.IngressSurface = telegramruntime.ReentryRecommendationIngressSurface
	if err := recordTelegramCallbackWorkAccepted(c.Store, msg, "callback_reentry_recommendation"); err != nil {
		return record, candidate, false, err
	}
	record, candidate, selected, err = c.Runtime.ConfirmReentryRecommendationSelection(ctx, msg.SenderID, recommendationID, candidateID)
	if err != nil || !selected {
		return record, candidate, selected, err
	}
	return record, candidate, true, c.RouteAccepted(ctx, msg)
}
