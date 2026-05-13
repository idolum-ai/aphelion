//go:build linux

package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	missionCallbackPrefix = "mission:"
	staleMissionCallback  = "This mission action is no longer active. Run /mission again."
)

type missionCallbackAction string

const (
	missionCallbackHome     missionCallbackAction = "home"
	missionCallbackShow     missionCallbackAction = "show"
	missionCallbackPropose  missionCallbackAction = "propose"
	missionCallbackPin      missionCallbackAction = "pin"
	missionCallbackUnpin    missionCallbackAction = "unpin"
	missionCallbackActive   missionCallbackAction = "active"
	missionCallbackPause    missionCallbackAction = "pause"
	missionCallbackComplete missionCallbackAction = "complete"
	missionCallbackArchive  missionCallbackAction = "archive"
	missionCallbackHealth   missionCallbackAction = "health"
)

func renderMissionHomePanel(missions []session.MissionState, working session.WorkingObjective, isAdmin bool, detailed bool) (string, [][]telegram.InlineButton) {
	details := make([]string, 0, 1+len(missions))
	if objective := strings.TrimSpace(working.Objective); objective != "" {
		details = append(details, "Working objective: "+truncateOperatorLine(objective, 180))
	} else {
		details = append(details, "Working objective: none")
	}
	if len(missions) == 0 {
		details = append(details, "No missions found.")
	} else {
		limit := len(missions)
		if limit > 12 {
			limit = 12
		}
		for i := 0; i < limit; i++ {
			mission := missions[i]
			title := firstNonEmpty(strings.TrimSpace(mission.Title), strings.TrimSpace(mission.Objective), strings.TrimSpace(mission.ID))
			line := fmt.Sprintf("%d. %s", i+1, truncateOperatorLine(title, 140))
			if status := strings.TrimSpace(string(mission.Status)); status != "" {
				line += " (" + status + ")"
			}
			if mission.Pinned {
				line += "; pinned"
			}
			details = append(details, line)
		}
	}
	panel := face.OperatorPanel{
		Title:   "Mission Ledger",
		State:   fmt.Sprintf("%d mission(s)", len(missions)),
		Why:     "Missions organize long-running intent but do not grant authority by themselves.",
		Next:    "Use buttons to inspect, propose, or update an existing mission; create new objectives in text.",
		Details: details,
	}
	rows := missionHomeRows(missions, isAdmin)
	return renderTelegramCompactPanel(panel, detailed), rows
}

func missionHomeRows(missions []session.MissionState, isAdmin bool) [][]telegram.InlineButton {
	rows := make([][]telegram.InlineButton, 0, len(missions)+2)
	limit := len(missions)
	if limit > 6 {
		limit = 6
	}
	for i := 0; i < limit; i++ {
		token := missionCallbackToken(missions[i].ID)
		rows = append(rows, []telegram.InlineButton{
			{Text: fmt.Sprintf("Show %d", i+1), CallbackData: encodeMissionCallbackData(missionCallbackShow, token)},
			{Text: fmt.Sprintf("Propose %d", i+1), CallbackData: encodeMissionCallbackData(missionCallbackPropose, token)},
		})
	}
	bottom := []telegram.InlineButton{{Text: "Refresh", CallbackData: encodeMissionCallbackData(missionCallbackHome, "")}}
	if isAdmin {
		bottom = append(bottom, telegram.InlineButton{Text: "Health", CallbackData: encodeMissionCallbackData(missionCallbackHealth, "")})
	}
	rows = append(rows, bottom)
	return rows
}

func renderMissionDetailPanel(mission session.MissionState, events []session.MissionEvent, detailed bool) (string, [][]telegram.InlineButton) {
	title := firstNonEmpty(strings.TrimSpace(mission.Title), strings.TrimSpace(mission.ID))
	details := []string{
		"ID: " + firstNonEmpty(strings.TrimSpace(mission.ID), "-"),
		"Objective: " + truncateOperatorLine(firstNonEmpty(strings.TrimSpace(mission.Objective), "-"), 220),
		"Pinned: " + operatorBoolLabel(mission.Pinned),
	}
	if blocked := strings.TrimSpace(mission.BlockedReason); blocked != "" {
		details = append(details, "Blocked: "+truncateOperatorLine(blocked, 180))
	}
	if len(mission.Tags) > 0 {
		details = append(details, "Tags: "+strings.Join(mission.Tags, ", "))
	}
	evidence := []string{
		"Self-summon: " + operatorBoolLabel(mission.Authority.CanSelfSummon),
		"Self-continuation: " + operatorBoolLabel(mission.Authority.CanSelfContinue),
		"Requires review: " + operatorBoolLabel(mission.Authority.RequiresUserReview),
	}
	for _, event := range events {
		line := strings.TrimSpace(event.EventType)
		if actor := strings.TrimSpace(event.Actor); actor != "" {
			line += " by " + actor
		}
		if summary := strings.TrimSpace(event.Summary); summary != "" {
			line += ": " + truncateOperatorLine(summary, 160)
		}
		evidence = append(evidence, line)
	}
	panel := face.OperatorPanel{
		Title:    "Mission: " + truncateOperatorLine(title, 90),
		State:    firstNonEmpty(strings.TrimSpace(string(mission.Status)), "unknown"),
		Why:      "Mission state is ledger context; action still needs explicit plan, lease, or grant authority.",
		Next:     "Use Propose for a bounded action, or update the mission state with buttons.",
		Details:  details,
		Evidence: evidence,
	}
	return renderTelegramCompactPanel(panel, detailed), missionDetailRows(mission)
}

func missionDetailRows(mission session.MissionState) [][]telegram.InlineButton {
	token := missionCallbackToken(mission.ID)
	rows := [][]telegram.InlineButton{{
		{Text: "All", CallbackData: encodeMissionCallbackData(missionCallbackHome, "")},
		{Text: "Propose", CallbackData: encodeMissionCallbackData(missionCallbackPropose, token)},
	}}
	if mission.Pinned {
		rows = append(rows, []telegram.InlineButton{{Text: "Unpin", CallbackData: encodeMissionCallbackData(missionCallbackUnpin, token)}})
	} else {
		rows = append(rows, []telegram.InlineButton{{Text: "Pin", CallbackData: encodeMissionCallbackData(missionCallbackPin, token)}})
	}
	switch mission.Status {
	case session.MissionStatusActive:
		rows = append(rows, []telegram.InlineButton{{Text: "Pause", CallbackData: encodeMissionCallbackData(missionCallbackPause, token)}})
	case session.MissionStatusCompleted, session.MissionStatusArchived:
	default:
		rows = append(rows, []telegram.InlineButton{{Text: "Activate", CallbackData: encodeMissionCallbackData(missionCallbackActive, token)}})
	}
	if mission.Status != session.MissionStatusCompleted && mission.Status != session.MissionStatusArchived {
		rows = append(rows, []telegram.InlineButton{
			{Text: "Complete", CallbackData: encodeMissionCallbackData(missionCallbackComplete, token)},
			{Text: "Archive", CallbackData: encodeMissionCallbackData(missionCallbackArchive, token)},
		})
	}
	return rows
}

func renderMissionHealthPanel(health session.MissionLedgerHealth) (string, [][]telegram.InlineButton) {
	panel := face.OperatorPanel{
		Title: "Mission Health",
		State: fmt.Sprintf("%d active, %d blocked", health.ActiveCount, health.BlockedCount),
		Why:   "Health highlights ledger state that may need review or cleanup.",
		Next:  "Inspect blocked missions or stale candidates before relying on the ledger.",
		Evidence: []string{
			fmt.Sprintf("Active: %d", health.ActiveCount),
			fmt.Sprintf("Pinned: %d", health.PinnedCount),
			fmt.Sprintf("Recurring: %d", health.RecurringCount),
			fmt.Sprintf("Blocked: %d", health.BlockedCount),
			fmt.Sprintf("Self-continuation enabled: %d", health.SelfContinuationEnabledCount),
			fmt.Sprintf("Stale candidates: %d", health.StaleCandidateCount),
			fmt.Sprintf("Pending handoffs: %d", health.PendingHandoffCount),
		},
	}
	return renderTelegramCompactPanel(panel, false), [][]telegram.InlineButton{{
		{Text: "All", CallbackData: encodeMissionCallbackData(missionCallbackHome, "")},
		{Text: "Refresh", CallbackData: encodeMissionCallbackData(missionCallbackHealth, "")},
	}}
}

func encodeMissionCallbackData(action missionCallbackAction, token string) string {
	action = missionCallbackAction(strings.TrimSpace(string(action)))
	if token == "" {
		return missionCallbackPrefix + string(action)
	}
	return missionCallbackPrefix + string(action) + ":" + strings.TrimSpace(token)
}

func decodeMissionCallbackData(data string) (missionCallbackAction, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, missionCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, missionCallbackPrefix))
	if payload == "" {
		return "", "", false
	}
	actionRaw, token, _ := strings.Cut(payload, ":")
	action := missionCallbackAction(strings.TrimSpace(actionRaw))
	switch action {
	case missionCallbackHome, missionCallbackHealth:
		return action, "", token == ""
	case missionCallbackShow, missionCallbackPropose, missionCallbackPin, missionCallbackUnpin, missionCallbackActive, missionCallbackPause, missionCallbackComplete, missionCallbackArchive:
		token = strings.TrimSpace(token)
		return action, token, token != ""
	default:
		return "", "", false
	}
}

func handleMissionCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action missionCallbackAction, token string) (bool, error) {
	chatID := int64(0)
	messageID := int64(0)
	senderID := int64(0)
	if cb.Message != nil {
		messageID = cb.Message.MessageID
		if cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
		}
	}
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if chatID == 0 || messageID == 0 || senderID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleMissionCallback); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}

	missions, working, isAdmin, err := router.MissionHome(ctx, chatID, senderID)
	if err != nil {
		return true, err
	}
	if action == missionCallbackHome {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		text, rows := renderMissionHomePanel(missions, working, isAdmin, false)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows)
	}
	if action == missionCallbackHealth {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		health, err := router.MissionLedgerHealth(ctx, senderID)
		if err != nil {
			return true, err
		}
		text, rows := renderMissionHealthPanel(health)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows)
	}
	missionID, ok := resolveMissionCallbackToken(missions, token)
	if !ok {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleMissionCallback); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	if action == missionCallbackPropose {
		proposal, err := router.MissionActionProposal(ctx, chatID, senderID, missionID)
		if err != nil {
			return true, err
		}
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, renderActionProposalPrompt(proposal), "", actionProposalButtonRows(proposal.ID))
	}
	var mission session.MissionState
	var events []session.MissionEvent
	switch action {
	case missionCallbackShow:
		mission, events, err = router.MissionDetails(ctx, chatID, senderID, missionID)
	case missionCallbackPin:
		mission, err = router.SetMissionPinned(ctx, chatID, senderID, missionID, true)
	case missionCallbackUnpin:
		mission, err = router.SetMissionPinned(ctx, chatID, senderID, missionID, false)
	case missionCallbackActive:
		mission, err = router.UpdateMissionStatus(ctx, chatID, senderID, missionID, session.MissionStatusActive)
	case missionCallbackPause:
		mission, err = router.UpdateMissionStatus(ctx, chatID, senderID, missionID, session.MissionStatusDormant)
	case missionCallbackComplete:
		mission, err = router.UpdateMissionStatus(ctx, chatID, senderID, missionID, session.MissionStatusCompleted)
	case missionCallbackArchive:
		mission, err = router.UpdateMissionStatus(ctx, chatID, senderID, missionID, session.MissionStatusArchived)
	default:
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if len(events) == 0 {
		mission, events, err = router.MissionDetails(ctx, chatID, senderID, mission.ID)
		if err != nil {
			return true, err
		}
	}
	text, rows := renderMissionDetailPanel(mission, events, false)
	return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows)
}

func missionCallbackToken(missionID string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(missionID)))
	return hex.EncodeToString(sum[:])[:12]
}

func resolveMissionCallbackToken(missions []session.MissionState, token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	var found string
	for _, mission := range missions {
		if missionCallbackToken(mission.ID) != token {
			continue
		}
		if found != "" {
			return "", false
		}
		found = strings.TrimSpace(mission.ID)
	}
	return found, found != ""
}
