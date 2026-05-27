//go:build linux

package main

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"strings"
	"time"
)

func (s *stubCommandRouter) Stop(chatID int64) core.StopResult {
	s.stopInput = chatID
	s.stopCalls++
	return s.stop
}

func (s *stubCommandRouter) StopForMessage(msg core.InboundMessage) core.StopResult {
	copied := msg
	s.stopMessage = &copied
	if s.stopMessageResult != (core.StopResult{}) {
		return s.stopMessageResult
	}
	return s.stop
}

func (s *stubCommandRouter) MarkStreamControlStopping(streamID string, chatID int64) bool {
	s.streamStopID = streamID
	s.streamStopChatID = chatID
	s.streamStopCalls++
	if s.streamControls == nil {
		return false
	}
	return s.streamControls[streamID] == chatID
}

func (s *stubCommandRouter) New(chatID int64, senderID int64) (core.NewSessionResult, error) {
	s.newChatID = chatID
	s.newSenderID = senderID
	if s.newErr != nil {
		return core.NewSessionResult{}, s.newErr
	}
	return s.newResult, nil
}

func (s *stubCommandRouter) NewForMessage(msg core.InboundMessage) (core.NewSessionResult, error) {
	copied := msg
	s.newMessage = &copied
	if s.newErr != nil {
		return core.NewSessionResult{}, s.newErr
	}
	if s.newMessageResult != (core.NewSessionResult{}) {
		return s.newMessageResult, nil
	}
	return s.newResult, nil
}

func (s *stubCommandRouter) Detach(chatID int64, senderID int64) (core.DetachResult, error) {
	s.detachChatID = chatID
	s.detachSenderID = senderID
	if s.detachErr != nil {
		return core.DetachResult{}, s.detachErr
	}
	return s.detach, nil
}

func (s *stubCommandRouter) DetachForMessage(msg core.InboundMessage) (core.DetachResult, error) {
	copied := msg
	s.detachMessage = &copied
	if s.detachErr != nil {
		return core.DetachResult{}, s.detachErr
	}
	if s.detachMessageResult != (core.DetachResult{}) {
		return s.detachMessageResult, nil
	}
	return s.detach, nil
}

func (s *stubCommandRouter) ToggleProgressView(_ context.Context, chatID int64, senderID int64, runID int64, details bool) (bool, string, error) {
	s.toggleProgressChatID = chatID
	s.toggleProgressSenderID = senderID
	s.toggleProgressRunID = runID
	s.toggleProgressDetails = details
	if s.toggleProgressErr != nil {
		return false, "", s.toggleProgressErr
	}
	return s.toggleProgressUpdated, s.toggleProgressText, nil
}

func (s stubCommandRouter) Status(chatID int64) core.SessionStatus {
	return s.status
}

func (s stubCommandRouter) StatusChat(chatID int64) (core.ChatStatusSnapshot, error) {
	if s.statusChatErr != nil {
		return core.ChatStatusSnapshot{}, s.statusChatErr
	}
	snapshot := s.statusChat
	if snapshot.ChatID == 0 {
		snapshot.ChatID = chatID
	}
	return snapshot, nil
}

func (s *stubCommandRouter) StatusChatForMessage(msg core.InboundMessage) (core.ChatStatusSnapshot, error) {
	copied := msg
	s.statusMessage = &copied
	if s.statusChatErr != nil {
		return core.ChatStatusSnapshot{}, s.statusChatErr
	}
	snapshot := s.statusMessageSnapshot
	if snapshot.ChatID == 0 {
		snapshot.ChatID = msg.ChatID
	}
	if snapshot.SessionID == "" {
		snapshot.SessionID = telegramruntime.SessionTargetForMessage(msg).SessionID
	}
	return snapshot, nil
}

func (s stubCommandRouter) StatusSystem(senderID int64) (core.SystemStatusSnapshot, error) {
	_ = senderID
	if s.statusSystemErr != nil {
		return core.SystemStatusSnapshot{}, s.statusSystemErr
	}
	return s.statusSystem, nil
}

func (s *stubCommandRouter) AutonomyStatus(chatID int64, senderID int64) (core.AutonomyStatusSnapshot, error) {
	s.autonomyChatID = chatID
	s.autonomySenderID = senderID
	_ = senderID
	if s.autonomyStatusErr != nil {
		return core.AutonomyStatusSnapshot{}, s.autonomyStatusErr
	}
	if strings.TrimSpace(s.autonomyStatus.DefaultMode) != "" || strings.TrimSpace(s.autonomyStatus.Ceiling) != "" {
		return s.autonomyStatus, nil
	}
	return core.AutonomyStatusSnapshot{
		DefaultMode:         "ask_first",
		Ceiling:             "leased",
		AllowLiveOverrides:  true,
		MaxOverrideDuration: 4 * time.Hour,
		Source:              "test",
		AuthorityBehavior:   "approvals require an open auto-mode window",
	}, nil
}

func (s *stubCommandRouter) AutonomyStatusForMessage(msg core.InboundMessage) (core.AutonomyStatusSnapshot, error) {
	copied := msg
	s.autonomyStatusMessage = &copied
	s.autonomyChatID = msg.ChatID
	s.autonomySenderID = msg.SenderID
	if s.autonomyStatusErr != nil {
		return core.AutonomyStatusSnapshot{}, s.autonomyStatusErr
	}
	if strings.TrimSpace(s.autonomyStatus.DefaultMode) != "" || strings.TrimSpace(s.autonomyStatus.Ceiling) != "" {
		return s.autonomyStatus, nil
	}
	return core.AutonomyStatusSnapshot{
		DefaultMode:         "ask_first",
		Ceiling:             "leased",
		AllowLiveOverrides:  true,
		MaxOverrideDuration: 4 * time.Hour,
		Source:              "test",
		AuthorityBehavior:   "approvals require an open auto-mode window",
	}, nil
}

func (s stubCommandRouter) StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error) {
	_ = senderID
	if s.statusDurablesErr != nil {
		return core.DurableAgentsStatusSnapshot{}, s.statusDurablesErr
	}
	return s.statusDurables, nil
}

func (s stubCommandRouter) StatusReadableSummary(ctx context.Context, view string, statusText string) string {
	_ = ctx
	_ = view
	_ = statusText
	return s.statusReadableSummary
}

func (s *stubCommandRouter) TailnetStatus(ctx context.Context, senderID int64) (core.TailnetStatusSnapshot, error) {
	_ = ctx
	s.tailnetStatusSenderID = senderID
	if s.tailnetStatusErr != nil {
		return core.TailnetStatusSnapshot{}, s.tailnetStatusErr
	}
	if strings.TrimSpace(s.tailnetStatus.Status) != "" || s.tailnetStatus.GeneratedAt.IsZero() == false {
		return s.tailnetStatus, nil
	}
	return core.TailnetStatusSnapshot{
		Enabled: false,
		Backend: "disabled",
		Status:  "disabled",
		Summary: "Tailscale integration is disabled.",
	}, nil
}

func (s *stubCommandRouter) TailnetSurfaces(senderID int64) ([]core.TailnetSurfaceStatus, error) {
	s.tailnetSurfacesSenderID = senderID
	if s.tailnetSurfacesErr != nil {
		return nil, s.tailnetSurfacesErr
	}
	return append([]core.TailnetSurfaceStatus(nil), s.tailnetSurfaces...), nil
}

func (s *stubCommandRouter) TailnetGrantBindings(senderID int64) ([]core.TailnetGrantBindingStatus, error) {
	s.tailnetGrantBindingsSenderID = senderID
	if s.tailnetGrantBindingsErr != nil {
		return nil, s.tailnetGrantBindingsErr
	}
	return append([]core.TailnetGrantBindingStatus(nil), s.tailnetGrantBindings...), nil
}

func (s *stubCommandRouter) RevokeTailnetSurface(ctx context.Context, senderID int64, surfaceID string, reason string) (core.TailnetSurfaceStatus, bool, error) {
	_ = ctx
	s.revokeTailnetSurfaceSenderID = senderID
	s.revokeTailnetSurfaceID = surfaceID
	s.revokeTailnetSurfaceReason = reason
	if s.revokeTailnetSurfaceErr != nil {
		return core.TailnetSurfaceStatus{}, false, s.revokeTailnetSurfaceErr
	}
	if strings.TrimSpace(s.revokeTailnetSurfaceReturn.SurfaceID) != "" || s.revokeTailnetSurfaceOK {
		return s.revokeTailnetSurfaceReturn, s.revokeTailnetSurfaceOK, nil
	}
	return core.TailnetSurfaceStatus{SurfaceID: surfaceID, Status: "revoked"}, true, nil
}

func (s stubCommandRouter) CurrentEfforts() (string, string) {
	return s.personaEffort, s.governorEffort
}
