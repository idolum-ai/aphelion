//go:build linux

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const ordinaryMediaApprovalMaxSizeBytes = 20 * 1024 * 1024

func (h *telegramDecisionHandler) HandleArtifactRetentionMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	if h == nil || h.sender == nil || h.router == nil || h.broker == nil {
		return false, nil
	}
	if !hasArtifactRetentionCandidates(msg) {
		return false, nil
	}
	if hasArtifactRetentionApprovalCandidates(msg) {
		return h.handleBlockingArtifactRetentionMessage(ctx, msg)
	}
	return h.handleImmediateMediaArtifactMessage(ctx, msg)
}

func (h *telegramDecisionHandler) handleBlockingArtifactRetentionMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	ownerKey := decision.OwnerKey(msg.ChatID, msg.SenderID)
	if ownerKey == "" {
		return false, fmt.Errorf("artifact retention owner key is required")
	}
	if h.store == nil {
		result, err := h.broker.Request(ctx, decision.Request{
			Kind:          decision.KindArtifactRetention,
			ChatID:        msg.ChatID,
			SenderID:      msg.SenderID,
			MessageID:     msg.MessageID,
			Prompt:        "How should I retain this inbound file?",
			Details:       formatArtifactRetentionDetails(msg),
			Choices:       artifactRetentionChoices(),
			DefaultChoice: "session",
			Timeout:       h.artifactRetentionTimeout,
		})
		if err != nil {
			return true, err
		}
		updated := applyArtifactRetentionChoice(msg, result.Choice)
		if result.Delivery.MessageID != 0 {
			_ = editDecisionMessageClearingInlineKeyboard(ctx, h.sender, msg.ChatID, result.Delivery.MessageID, artifactRetentionResolutionText(result))
		}
		h.router.Route(ctx, updated)
		return true, nil
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return true, fmt.Errorf("marshal pending artifact retention message: %w", err)
	}
	if err := h.store.UpsertPendingArtifactRetention(session.PendingArtifactRetentionRecord{
		OwnerKey:           ownerKey,
		ChatID:             msg.ChatID,
		SenderID:           msg.SenderID,
		InboundMessageJSON: string(raw),
	}); err != nil {
		return true, err
	}

	go h.awaitArtifactRetentionDecision(context.Background(), ownerKey, decision.Request{
		Kind:          decision.KindArtifactRetention,
		ChatID:        msg.ChatID,
		SenderID:      msg.SenderID,
		MessageID:     msg.MessageID,
		Prompt:        "How should I retain this inbound file?",
		Details:       formatArtifactRetentionDetails(msg),
		Choices:       artifactRetentionChoices(),
		DefaultChoice: "session",
		Timeout:       h.artifactRetentionTimeout,
	})
	return true, nil
}

func artifactRetentionChoices() []decision.Choice {
	return []decision.Choice{
		{ID: "turn", Label: "Turn only"},
		{ID: "session", Label: "Session"},
		{ID: "local", Label: "Save locally"},
	}
}

func (h *telegramDecisionHandler) awaitArtifactRetentionDecision(ctx context.Context, ownerKey string, req decision.Request) {
	result, err := h.broker.Request(ctx, req)
	if err != nil {
		if h.store != nil {
			_ = h.store.DeletePendingArtifactRetention(ownerKey)
		}
		return
	}
	if err := h.resumePendingArtifactRetention(ctx, ownerKey, result); err != nil {
		if h.store != nil {
			_ = h.store.DeletePendingArtifactRetention(ownerKey)
		}
	}
}

func (h *telegramDecisionHandler) resumePendingArtifactRetention(ctx context.Context, ownerKey string, result decision.Result) error {
	if h == nil || h.router == nil {
		return nil
	}
	if h.store == nil {
		return nil
	}
	record, err := h.store.PendingArtifactRetention(ownerKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	defer func() { _ = h.store.DeletePendingArtifactRetention(ownerKey) }()
	var msg core.InboundMessage
	if err := json.Unmarshal([]byte(record.InboundMessageJSON), &msg); err != nil {
		return fmt.Errorf("decode pending artifact retention message: %w", err)
	}
	updated := applyArtifactRetentionChoice(msg, result.Choice)
	if result.Delivery.MessageID != 0 && h.sender != nil {
		_ = editDecisionMessageClearingInlineKeyboard(ctx, h.sender, msg.ChatID, result.Delivery.MessageID, artifactRetentionResolutionText(result))
	}
	h.router.Route(ctx, updated)
	return nil
}

func (h *telegramDecisionHandler) handleImmediateMediaArtifactMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	updated := markMediaProcessingAgentDecision(msg)
	updated = applyArtifactRetentionChoice(updated, "session")
	storedForKeep := false
	if h.store != nil && h.artifactRetentionKeeper != nil && hasPermanentArtifactKeepCandidates(msg) {
		if ownerKey := decision.OwnerKey(msg.ChatID, msg.SenderID); ownerKey != "" {
			raw, err := json.Marshal(msg)
			if err != nil {
				return true, fmt.Errorf("marshal pending permanent artifact message: %w", err)
			}
			err = h.store.UpsertPendingArtifactRetention(session.PendingArtifactRetentionRecord{
				OwnerKey:           ownerKey,
				ChatID:             msg.ChatID,
				SenderID:           msg.SenderID,
				InboundMessageJSON: string(raw),
			})
			storedForKeep = err == nil
			if err != nil {
				return true, err
			}
		}
	}
	if storedForKeep {
		_ = h.sendPermanentArtifactRetentionOffer(ctx, msg)
	}
	h.router.Route(ctx, updated)
	return true, nil
}

func (h *telegramDecisionHandler) sendPermanentArtifactRetentionOffer(ctx context.Context, msg core.InboundMessage) error {
	if h == nil || h.sender == nil {
		return nil
	}
	subject := permanentArtifactKeepSubject(msg)
	text := subject.Sentence + " is available while we work with it. I won't keep it beyond that unless you ask."
	rows := [][]telegram.InlineButton{{{
		Text:         subject.Button,
		CallbackData: encodePermanentArtifactKeepCallbackData(msg.MessageID),
	}}}
	_, err := h.sender.SendInlineKeyboard(ctx, msg.ChatID, text, rows, replyToMessageID(msg.MessageID))
	return err
}

type permanentArtifactKeepCopy struct {
	Sentence     string
	Button       string
	Unavailable  string
	Stale        string
	Failed       string
	Confirmation string
}

func permanentArtifactKeepSubject(msg core.InboundMessage) permanentArtifactKeepCopy {
	counts := map[string]int{}
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if artifactRetentionCandidate(artifact) && !artifactNeedsRetentionApproval(artifact) {
			counts[artifact.Kind]++
		}
	}
	total := 0
	for _, count := range counts {
		total += count
	}
	if total == 1 {
		switch {
		case counts["audio"] == 1:
			return permanentArtifactKeepCopy{
				Sentence:     "Audio",
				Button:       "Keep audio",
				Unavailable:  "That audio is no longer available to save from this button.",
				Stale:        "That audio button is stale.",
				Failed:       "I couldn't save that audio permanently.",
				Confirmation: "Audio saved permanently.",
			}
		case counts["image"] == 1:
			return permanentArtifactKeepCopy{
				Sentence:     "Image",
				Button:       "Keep image",
				Unavailable:  "That image is no longer available to save from this button.",
				Stale:        "That image button is stale.",
				Failed:       "I couldn't save that image permanently.",
				Confirmation: "Image saved permanently.",
			}
		case counts["video"] == 1:
			return permanentArtifactKeepCopy{
				Sentence:     "Video",
				Button:       "Keep video",
				Unavailable:  "That video is no longer available to save from this button.",
				Stale:        "That video button is stale.",
				Failed:       "I couldn't save that video locally.",
				Confirmation: "Video saved locally.",
			}
		case counts["sticker"] == 1:
			return permanentArtifactKeepCopy{
				Sentence:     "Sticker",
				Button:       "Keep sticker",
				Unavailable:  "That sticker is no longer available to save from this button.",
				Stale:        "That sticker button is stale.",
				Failed:       "I couldn't save that sticker locally.",
				Confirmation: "Sticker saved locally.",
			}
		case counts["document"] == 1:
			return permanentArtifactKeepCopy{
				Sentence:     "File",
				Button:       "Keep file",
				Unavailable:  "That file is no longer available to save from this button.",
				Stale:        "That file button is stale.",
				Failed:       "I couldn't save that file locally.",
				Confirmation: "File saved locally.",
			}
		}
	}
	return permanentArtifactKeepCopy{
		Sentence:     "Media",
		Button:       "Keep media",
		Unavailable:  "That media is no longer available to save from this button.",
		Stale:        "That media button is stale.",
		Failed:       "I couldn't save that media permanently.",
		Confirmation: "Media saved permanently.",
	}
}

func hasArtifactRetentionCandidates(msg core.InboundMessage) bool {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return false
	}
	for _, raw := range msg.Artifacts {
		if artifactRetentionCandidate(core.NormalizeArtifact(raw)) {
			return true
		}
	}
	return false
}

func hasArtifactRetentionApprovalCandidates(msg core.InboundMessage) bool {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return false
	}
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if artifactRetentionCandidate(artifact) && artifactNeedsRetentionApproval(artifact) {
			return true
		}
	}
	return false
}

func hasPermanentArtifactKeepCandidates(msg core.InboundMessage) bool {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return false
	}
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if artifactRetentionCandidate(artifact) && !artifactNeedsRetentionApproval(artifact) {
			return true
		}
	}
	return false
}

func artifactRetentionCandidate(artifact core.Artifact) bool {
	artifact = core.NormalizeArtifact(artifact)
	if strings.TrimSpace(artifact.Channel) != "telegram" {
		return false
	}
	if strings.TrimSpace(artifact.RemoteID) == "" && len(artifact.Data) == 0 {
		return false
	}
	if artifact.Kind == "structured" {
		return false
	}
	return strings.TrimSpace(artifact.Metadata["aphelion_retention_choice"]) == ""
}

func artifactNeedsRetentionApproval(artifact core.Artifact) bool {
	artifact = core.NormalizeArtifact(artifact)
	if artifact.Kind == "archive" || artifact.HasCapability("quarantine_for_review") {
		return true
	}
	if artifact.SizeBytes > ordinaryMediaApprovalMaxSizeBytes {
		return true
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(artifact.Filename)))
	text := strings.ToLower(strings.Join([]string{
		artifact.Filename,
		artifact.MimeType,
		artifact.SourceType,
		artifact.Subtype,
	}, " "))
	if artifactLooksLikeArchive(ext, text) || artifactLooksLikeExecutable(ext, text) || artifactLooksLikeSecret(ext, text) {
		return true
	}
	if artifact.Kind == "document" && artifact.Subtype == "" && !ordinaryDocumentArtifact(ext, text) {
		return true
	}
	return false
}

func artifactLooksLikeArchive(ext string, text string) bool {
	switch ext {
	case ".zip", ".tar", ".tgz", ".gz", ".bz2", ".xz", ".rar", ".7z":
		return true
	}
	return strings.Contains(text, "application/zip") ||
		strings.Contains(text, "application/x-tar") ||
		strings.Contains(text, "application/x-7z") ||
		strings.Contains(text, "application/x-rar")
}

func artifactLooksLikeExecutable(ext string, text string) bool {
	switch ext {
	case ".exe", ".msi", ".dmg", ".pkg", ".deb", ".rpm", ".apk", ".app", ".bat", ".cmd", ".ps1", ".scr", ".com", ".bin", ".so", ".dll":
		return true
	}
	return strings.Contains(text, "application/x-msdownload") ||
		strings.Contains(text, "application/x-executable") ||
		strings.Contains(text, "application/vnd.android.package-archive")
}

func artifactLooksLikeSecret(ext string, text string) bool {
	switch ext {
	case ".pem", ".p12", ".pfx", ".key":
		return true
	}
	for _, marker := range []string{
		".env",
		"id_rsa",
		"id_dsa",
		"id_ecdsa",
		"id_ed25519",
		"private_key",
		"credential",
		"credentials",
		"secret",
		"token",
		"oauth",
		"password",
		"passwd",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func ordinaryDocumentArtifact(ext string, text string) bool {
	switch ext {
	case ".pdf", ".txt", ".md", ".markdown", ".csv", ".tsv", ".json", ".yaml", ".yml", ".toml", ".xml", ".log", ".rtf", ".doc", ".docx", ".odt", ".xls", ".xlsx", ".ods", ".ppt", ".pptx", ".odp":
		return true
	}
	return strings.HasPrefix(text, "text/") ||
		strings.Contains(text, "application/pdf") ||
		strings.Contains(text, "application/json") ||
		strings.Contains(text, "application/xml") ||
		strings.Contains(text, "officedocument") ||
		strings.Contains(text, "opendocument")
}

func formatArtifactRetentionDetails(msg core.InboundMessage) string {
	items := make([]string, 0, len(msg.Artifacts))
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if strings.TrimSpace(artifact.Channel) != "telegram" || artifact.Kind == "structured" {
			continue
		}
		label := strings.TrimSpace(artifact.Filename)
		if label == "" {
			label = strings.TrimSpace(artifact.Kind)
			if label == "" {
				label = strings.TrimSpace(artifact.SourceType)
			}
			if label == "" {
				label = "artifact"
			}
		}
		items = append(items, "- "+label)
	}
	if len(items) == 0 {
		return "Choose how long I should keep the inbound artifact after processing."
	}
	return strings.Join([]string{
		"Choose how long I should keep this inbound artifact after processing.",
		"",
		"Artifacts:",
		strings.Join(items, "\n"),
	}, "\n")
}

func applyArtifactRetentionChoice(msg core.InboundMessage, choice string) core.InboundMessage {
	choice = strings.TrimSpace(choice)
	out := msg
	out.Artifacts = make([]core.Artifact, 0, len(msg.Artifacts))
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if strings.TrimSpace(artifact.Channel) == "telegram" && artifact.Kind != "structured" && (strings.TrimSpace(artifact.RemoteID) != "" || len(artifact.Data) > 0) {
			if artifact.Metadata == nil {
				artifact.Metadata = map[string]string{}
			}
			artifact.Metadata["aphelion_retention_choice"] = choice
			switch choice {
			case "turn":
				artifact.DefaultRetention = "ephemeral"
				artifact.Metadata["aphelion_materialize"] = "memory_only"
			case "local":
				artifact.DefaultRetention = "child_local"
				artifact.RetentionCeiling = "child_local"
				artifact.Metadata["aphelion_materialize"] = "local"
			default:
				artifact.DefaultRetention = "session_reference"
				artifact.Metadata["aphelion_materialize"] = "local"
			}
		}
		out.Artifacts = append(out.Artifacts, core.NormalizeArtifact(artifact))
	}
	return out
}

func markMediaProcessingAgentDecision(msg core.InboundMessage) core.InboundMessage {
	out := msg
	out.Artifacts = make([]core.Artifact, 0, len(msg.Artifacts))
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if strings.TrimSpace(artifact.Channel) == "telegram" && (artifact.Kind == "audio" || artifact.Kind == "video") && (strings.TrimSpace(artifact.RemoteID) != "" || len(artifact.Data) > 0) {
			if artifact.Metadata == nil {
				artifact.Metadata = map[string]string{}
			}
			artifact.Metadata[core.ArtifactMetadataMediaProcessingChoice] = "agent"
		}
		out.Artifacts = append(out.Artifacts, core.NormalizeArtifact(artifact))
	}
	return out
}

func artifactRetentionResolutionText(result decision.Result) string {
	if result.TimedOut {
		return "Keeping the file for this session by default."
	}
	switch strings.TrimSpace(result.Choice) {
	case "turn":
		return "Got it — I’ll use the file for this turn only."
	case "local":
		return "Got it — I’ll save the file locally for longer work."
	default:
		return "Got it — I’ll keep the file for this session."
	}
}

func encodePermanentArtifactKeepCallbackData(messageID int64) string {
	return "media_keep:" + strconv.FormatInt(messageID, 10)
}

func decodePermanentArtifactKeepCallbackData(data string) (int64, bool) {
	trimmed := strings.TrimSpace(data)
	prefix := ""
	for _, candidate := range []string{"media_keep:", "audio_keep:"} {
		if strings.HasPrefix(trimmed, candidate) {
			prefix = candidate
			break
		}
	}
	if prefix == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *telegramDecisionHandler) handlePermanentArtifactKeepCallback(ctx context.Context, cb telegram.CallbackQuery, sourceMessageID int64) error {
	chatID := callbackChatID(cb)
	senderID := callbackSenderID(cb)
	if chatID == 0 || senderID == 0 || h == nil || h.store == nil || h.artifactRetentionKeeper == nil {
		return h.answerPermanentArtifactKeepCallback(ctx, cb, "I can't save that media from this prompt.")
	}
	record, err := h.store.PendingArtifactRetention(decision.OwnerKey(chatID, senderID))
	if err != nil {
		if err == sql.ErrNoRows {
			return h.answerPermanentArtifactKeepCallback(ctx, cb, "That media is no longer available to save from this button.")
		}
		return err
	}
	var msg core.InboundMessage
	if err := json.Unmarshal([]byte(record.InboundMessageJSON), &msg); err != nil {
		return fmt.Errorf("decode pending permanent artifact message: %w", err)
	}
	subject := permanentArtifactKeepSubject(msg)
	if sourceMessageID != 0 && msg.MessageID != 0 && msg.MessageID != sourceMessageID {
		return h.answerPermanentArtifactKeepCallback(ctx, cb, subject.Stale)
	}
	if err := h.artifactRetentionKeeper.KeepTelegramArtifactsPermanently(ctx, msg); err != nil {
		return h.answerPermanentArtifactKeepCallback(ctx, cb, subject.Failed)
	}
	_ = h.store.DeletePendingArtifactRetention(decision.OwnerKey(chatID, senderID))
	if cb.Message != nil && cb.Message.MessageID != 0 {
		_ = editDecisionMessageClearingInlineKeyboard(ctx, h.sender, chatID, cb.Message.MessageID, subject.Confirmation)
	}
	return h.answerPermanentArtifactKeepCallback(ctx, cb, "Saved.")
}

func (h *telegramDecisionHandler) answerPermanentArtifactKeepCallback(ctx context.Context, cb telegram.CallbackQuery, text string) error {
	if h == nil || h.sender == nil {
		return nil
	}
	if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return err
	}
	return nil
}
