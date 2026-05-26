//go:build linux

package telegramcontrol

import (
	"context"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"time"
)

func (c CommandControl) MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
	if c.Runtime == nil {
		return core.MemoryReviewSnapshot{
			GeneratedAt: time.Now().UTC(),
			Source:      core.NormalizeMemoryReviewSource(string(source)),
			Query:       "",
		}, nil
	}
	return c.Runtime.MemoryReviewSnapshot(ctx, chatID, senderID, core.NormalizeMemoryReviewSource(string(source)))
}

func (c CommandControl) MemoryReviewSnapshotForMessage(ctx context.Context, msg core.InboundMessage, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
	if c.Runtime == nil {
		return core.MemoryReviewSnapshot{
			GeneratedAt: time.Now().UTC(),
			Source:      core.NormalizeMemoryReviewSource(string(source)),
			Query:       "",
		}, nil
	}
	return c.Runtime.MemoryReviewSnapshotForKey(ctx, SessionKeyForMessage(msg), msg.SenderID, core.NormalizeMemoryReviewSource(string(source)))
}

func (c CommandControl) MissionCommand(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	if c.Runtime == nil {
		return "Mission Ledger is unavailable.", nil
	}
	return c.Runtime.MissionCommand(ctx, chatID, senderID, args)
}

func (c CommandControl) MissionHome(ctx context.Context, chatID int64, senderID int64) ([]session.MissionState, session.WorkingObjective, bool, error) {
	if c.Runtime == nil {
		return nil, session.WorkingObjective{}, false, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.Runtime.MissionHome(ctx, chatID, senderID)
}

func (c CommandControl) MissionDetails(ctx context.Context, chatID int64, senderID int64, missionID string) (session.MissionState, []session.MissionEvent, error) {
	if c.Runtime == nil {
		return session.MissionState{}, nil, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.Runtime.MissionDetails(ctx, chatID, senderID, missionID)
}

func (c CommandControl) SetMissionPinned(ctx context.Context, chatID int64, senderID int64, missionID string, pinned bool) (session.MissionState, error) {
	if c.Runtime == nil {
		return session.MissionState{}, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.Runtime.SetMissionPinned(ctx, chatID, senderID, missionID, pinned)
}

func (c CommandControl) UpdateMissionStatus(ctx context.Context, chatID int64, senderID int64, missionID string, status session.MissionStatus) (session.MissionState, error) {
	if c.Runtime == nil {
		return session.MissionState{}, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.Runtime.UpdateMissionStatus(ctx, chatID, senderID, missionID, status)
}

func (c CommandControl) MissionLedgerHealth(ctx context.Context, senderID int64) (session.MissionLedgerHealth, error) {
	if c.Runtime == nil {
		return session.MissionLedgerHealth{}, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.Runtime.MissionLedgerHealth(ctx, senderID)
}

func (c CommandControl) MissionActionProposal(ctx context.Context, chatID int64, senderID int64, missionID string) (session.ActionProposal, error) {
	if c.Runtime == nil {
		return session.ActionProposal{}, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.Runtime.MissionActionProposal(ctx, chatID, senderID, missionID)
}

func (c CommandControl) ApplyMissionActionProposalDecision(ctx context.Context, chatID int64, senderID int64, missionID string, choice string) (session.MissionState, bool, error) {
	if c.Runtime == nil {
		return session.MissionState{}, false, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.Runtime.ApplyMissionActionProposalDecision(ctx, chatID, senderID, missionID, choice)
}

func (c CommandControl) MissionAskPrompt(ctx context.Context, senderID int64, promptID string) (session.MissionAskPrompt, bool, error) {
	if c.Runtime == nil {
		return session.MissionAskPrompt{}, false, fmt.Errorf("Mission Question is unavailable.")
	}
	return c.Runtime.MissionAskPrompt(ctx, senderID, promptID)
}

func (c CommandControl) ResolveMissionAskPrompt(ctx context.Context, senderID int64, promptID string, status session.MissionAskStatus, summary string) (session.MissionAskPrompt, error) {
	if c.Runtime == nil {
		return session.MissionAskPrompt{}, fmt.Errorf("Mission Question is unavailable.")
	}
	return c.Runtime.ResolveMissionAskPrompt(ctx, senderID, promptID, status, summary)
}
