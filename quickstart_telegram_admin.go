//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/telegram"
)

type telegramAdminCandidate struct {
	UserID    int64
	Username  string
	FirstName string
	LastName  string
	ChatID    int64
	ChatType  string
}

func detectTelegramAdminForQuickstart(ctx context.Context, client quickstartTelegramClient, session quickstartSession, timeout time.Duration) (int64, error) {
	if client == nil {
		return 0, fmt.Errorf("telegram client is required for admin discovery")
	}
	if session.noInput {
		return 0, fmt.Errorf("--detect-admin requires confirmation and cannot run with --no-input")
	}
	if !session.allowPrompt {
		return 0, fmt.Errorf("--detect-admin requires an interactive terminal")
	}
	if timeout <= 0 {
		timeout = defaultQuickstartDetectAdminTimeout
	}

	offset, err := nextTelegramUpdateOffset(ctx, client)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(session.out, "Message the Telegram bot now from the admin account.\n")
	candidates, err := collectTelegramAdminCandidates(ctx, client, offset, timeout)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, fmt.Errorf("timed out waiting for a Telegram admin message")
	}
	if len(candidates) == 1 {
		candidate := candidates[0]
		fmt.Fprintf(session.out, "Detected %s.\n", formatTelegramAdminCandidate(candidate))
		ok, err := session.confirm("Use this Telegram user as admin? [y/N]: ")
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, fmt.Errorf("admin discovery was rejected")
		}
		return candidate.UserID, nil
	}

	fmt.Fprintf(session.out, "Detected multiple Telegram users:\n")
	for i, candidate := range candidates {
		fmt.Fprintf(session.out, "  %d. %s\n", i+1, formatTelegramAdminCandidate(candidate))
	}
	fmt.Fprintf(session.out, "Choose admin number: ")
	raw, err := session.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	choice, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || choice < 1 || choice > len(candidates) {
		return 0, fmt.Errorf("invalid admin selection")
	}
	return candidates[choice-1].UserID, nil
}

func nextTelegramUpdateOffset(ctx context.Context, client quickstartTelegramClient) (int64, error) {
	updates, err := client.GetUpdates(ctx, 0, 0)
	if err != nil {
		return 0, err
	}
	var offset int64
	for _, update := range updates {
		if update.UpdateID >= offset {
			offset = update.UpdateID + 1
		}
	}
	return offset, nil
}

func collectTelegramAdminCandidates(ctx context.Context, client quickstartTelegramClient, offset int64, timeout time.Duration) ([]telegramAdminCandidate, error) {
	deadline := time.Now().Add(timeout)
	seen := map[int64]struct{}{}
	var candidates []telegramAdminCandidate
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		pollSeconds := int(remaining / time.Second)
		if pollSeconds < 1 {
			pollSeconds = 1
		}
		if pollSeconds > 5 {
			pollSeconds = 5
		}
		updates, err := client.GetUpdates(ctx, offset, pollSeconds)
		if err != nil {
			return nil, err
		}
		for _, update := range updates {
			if update.UpdateID < offset {
				continue
			}
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			candidate, ok := telegramAdminCandidateFromUpdate(update)
			if !ok {
				continue
			}
			if _, exists := seen[candidate.UserID]; exists {
				continue
			}
			seen[candidate.UserID] = struct{}{}
			candidates = append(candidates, candidate)
		}
		if len(candidates) > 0 {
			return candidates, nil
		}
	}
	return nil, nil
}

func telegramAdminCandidateFromUpdate(update telegram.Update) (telegramAdminCandidate, bool) {
	if update.Message == nil || update.Message.From == nil || update.Message.From.ID <= 0 || update.Message.From.IsBot {
		return telegramAdminCandidate{}, false
	}
	candidate := telegramAdminCandidate{
		UserID:    update.Message.From.ID,
		Username:  strings.TrimSpace(update.Message.From.Username),
		FirstName: strings.TrimSpace(update.Message.From.FirstName),
		LastName:  strings.TrimSpace(update.Message.From.LastName),
	}
	if update.Message.Chat != nil {
		candidate.ChatID = update.Message.Chat.ID
		candidate.ChatType = strings.TrimSpace(update.Message.Chat.Type)
	}
	return candidate, true
}

func formatTelegramAdminCandidate(candidate telegramAdminCandidate) string {
	parts := []string{fmt.Sprintf("user_id=%d", candidate.UserID)}
	if candidate.Username != "" {
		parts = append(parts, "username=@"+candidate.Username)
	}
	name := strings.TrimSpace(strings.Join([]string{candidate.FirstName, candidate.LastName}, " "))
	if name != "" {
		parts = append(parts, "name="+name)
	}
	if candidate.ChatID != 0 {
		parts = append(parts, fmt.Sprintf("chat_id=%d", candidate.ChatID))
	}
	if candidate.ChatType != "" {
		parts = append(parts, "chat_type="+candidate.ChatType)
	}
	return strings.Join(parts, " ")
}
