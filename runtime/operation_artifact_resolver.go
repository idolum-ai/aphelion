//go:build linux

package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Runtime) maybeHandleOperationArtifactRequest(ctx context.Context, key session.SessionKey, scope sandbox.Scope, msg core.InboundMessage) (bool, *core.TurnResult, error) {
	if r == nil || r.store == nil || r.outbound == nil || !artifactRequestAllowedForOrigin(msg) {
		return false, nil, nil
	}
	turnEvidenceCommand := isTurnEvidenceCommand(msg.Text)
	if !turnEvidenceCommand && !looksLikeOperationArtifactSendRequest(msg.Text) {
		return false, nil, nil
	}
	state, err := r.store.OperationState(key)
	if err != nil {
		return false, nil, nil
	}
	state = session.NormalizeOperationState(state)
	if turnEvidenceCommand {
		artifact, media, ok := latestSendableWorkEvidenceArtifact(scope, state.Artifacts)
		if !ok {
			reply := "No turn evidence artifact is available for this chat."
			return r.sendOperationArtifactReply(ctx, key, msg, state, reply, nil, session.OperationArtifact{})
		}
		reply := operationArtifactReplyText(artifact, media)
		return r.sendOperationArtifactReply(ctx, key, msg, state, reply, []core.Media{media}, artifact)
	}

	artifact, media, ok := latestSendableOperationArtifact(scope, state.Artifacts, msg.Text)
	if !ok {
		return false, nil, nil
	}
	reply := operationArtifactReplyText(artifact, media)
	return r.sendOperationArtifactReply(ctx, key, msg, state, reply, []core.Media{media}, artifact)
}

func (r *Runtime) sendOperationArtifactReply(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.OperationState, reply string, media []core.Media, artifact session.OperationArtifact) (bool, *core.TurnResult, error) {
	outboundID, outboundType, err := r.sendReply(ctx, msg, reply, media, false)
	if err != nil {
		return true, &core.TurnResult{Text: reply, Media: media}, fmt.Errorf("send operation artifact: %w", err)
	}

	sess, err := r.store.Load(key)
	if err != nil {
		return true, &core.TurnResult{Text: reply, Media: media}, fmt.Errorf("load session for operation artifact reply: %w", err)
	}
	applySessionScope(sess, key)
	sess.ChatType = "dm"
	sess.UserName = msg.SenderName
	sess.OperationState = mergeSessionOperationState(sess.OperationState, state)
	sess.TurnCount++
	turnIndex := sess.TurnCount
	newMessages := []session.Message{
		{
			Role:         "user",
			Content:      msg.Text,
			ContentChars: len(msg.Text),
			TurnIndex:    turnIndex,
		},
		{
			Role:         "assistant",
			Content:      reply,
			ContentChars: len(reply),
			TurnIndex:    turnIndex,
		},
	}
	if err := r.store.Save(sess, newMessages, core.TokenUsage{}); err != nil {
		return true, &core.TurnResult{Text: reply, Media: media}, fmt.Errorf("save operation artifact reply: %w", err)
	}
	if outboundID != 0 {
		if err := r.store.RecordOutbound(key, turnIndex, outboundID, outboundType); err != nil {
			return true, &core.TurnResult{Text: reply, Media: media}, fmt.Errorf("record operation artifact reply: %w", err)
		}
	}
	payload := map[string]any{
		"message_id":   outboundID,
		"message_type": outboundType,
	}
	if strings.TrimSpace(artifact.Ref) != "" {
		payload["artifact_ref"] = artifact.Ref
		payload["artifact"] = firstNonEmpty(artifact.Label, filepath.Base(artifact.Ref))
	}
	r.recordExecutionEvent(key, core.ExecutionEventDeliveryFinalSent, "delivery", "sent", payload, time.Now().UTC())
	return true, &core.TurnResult{Text: reply, Media: media}, nil
}

func artifactRequestAllowedForOrigin(msg core.InboundMessage) bool {
	return msg.Origin == "" || msg.Origin == core.InboundOriginUser
}

func isTurnEvidenceCommand(text string) bool {
	userText := operationArtifactRequestUserText(text)
	fields := strings.Fields(strings.TrimSpace(userText))
	if len(fields) != 1 {
		return false
	}
	command := strings.ToLower(strings.TrimSpace(fields[0]))
	if at := strings.Index(command, "@"); at >= 0 {
		command = command[:at]
	}
	return command == "/turn-evidence"
}

func looksLikeOperationArtifactSendRequest(text string) bool {
	userText, replyContext := operationArtifactRequestTextParts(text)
	normalized := operationArtifactNormalizeRequestText(userText)
	if normalized == "" || !operationArtifactStartsWithSendRequest(normalized) {
		return false
	}
	if operationArtifactRequestNamesArtifact(normalized) {
		return true
	}
	if operationArtifactShortPronounRequest(normalized) && operationArtifactReplyContextNamesArtifact(replyContext) {
		return true
	}
	return false
}

func operationArtifactRequestUserText(text string) string {
	userText, _ := operationArtifactRequestTextParts(text)
	return userText
}

func operationArtifactRequestTextParts(text string) (string, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	const replyContextMarker = "\n\nReply context:\n"
	if idx := strings.Index(text, replyContextMarker); idx >= 0 {
		return strings.TrimSpace(text[:idx]), strings.TrimSpace(text[idx+len(replyContextMarker):])
	}
	if strings.HasPrefix(text, "Reply context:\n") {
		return "", strings.TrimSpace(strings.TrimPrefix(text, "Reply context:\n"))
	}
	return text, ""
}

func operationArtifactNormalizeRequestText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"?", " ",
		"!", " ",
		".", " ",
		",", " ",
		":", " ",
		";", " ",
		"\n", " ",
		"\t", " ",
	)
	return strings.Join(strings.Fields(replacer.Replace(text)), " ")
}

func operationArtifactStartsWithSendRequest(normalized string) bool {
	if normalized == "" {
		return false
	}
	for _, prefix := range []string{
		"send ",
		"attach ",
		"share ",
		"please send ",
		"please attach ",
		"please share ",
		"can you send ",
		"can you attach ",
		"can you share ",
		"could you send ",
		"could you attach ",
		"could you share ",
		"would you send ",
		"would you attach ",
		"would you share ",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func operationArtifactRequestNamesArtifact(normalized string) bool {
	if operationArtifactNormalizedTextNamesWorkEvidence(normalized) {
		return false
	}
	return operationArtifactNormalizedTextNamesArtifact(normalized)
}

func operationArtifactReplyContextNamesArtifact(replyContext string) bool {
	return operationArtifactNormalizedTextNamesArtifact(operationArtifactNormalizeRequestText(replyContext))
}

func operationArtifactNormalizedTextNamesArtifact(normalized string) bool {
	if operationArtifactNormalizedTextNamesWorkEvidence(normalized) {
		return false
	}
	for _, field := range strings.Fields(normalized) {
		switch field {
		case "pdf", "artifact", "artifacts", "file", "files", "report", "reports", "log", "logs":
			return true
		}
	}
	return strings.Contains(normalized, ".pdf")
}

func operationArtifactNormalizedTextNamesWorkEvidence(normalized string) bool {
	return strings.Contains(normalized, "work evidence") || strings.Contains(normalized, "turn evidence") || strings.TrimSpace(normalized) == "evidence"
}

func operationArtifactShortPronounRequest(normalized string) bool {
	fields := strings.Fields(normalized)
	if len(fields) < 2 || len(fields) > 4 {
		return false
	}
	verbIndex := 0
	if fields[0] == "please" {
		verbIndex = 1
	}
	if verbIndex >= len(fields) {
		return false
	}
	switch fields[verbIndex] {
	case "send", "attach", "share":
	default:
		return false
	}
	for _, field := range fields[verbIndex+1:] {
		if field == "me" || field == "the" {
			continue
		}
		return field == "it" || field == "that"
	}
	return false
}

func latestSendableOperationArtifact(scope sandbox.Scope, artifacts []session.OperationArtifact, requestText string) (session.OperationArtifact, core.Media, bool) {
	wantPDF := strings.Contains(strings.ToLower(requestText), "pdf")
	for i := len(artifacts) - 1; i >= 0; i-- {
		artifact := artifacts[i]
		if operationArtifactIsWorkEvidence(artifact) {
			continue
		}
		ref := strings.TrimSpace(artifact.Ref)
		if ref == "" {
			continue
		}
		if wantPDF && !operationArtifactLooksLikePDF(artifact) {
			continue
		}
		media, ok := normalizeOutboundReplyMediaPath(scope, ref, false)
		if !ok {
			continue
		}
		return artifact, media, true
	}
	return session.OperationArtifact{}, core.Media{}, false
}

func latestSendableWorkEvidenceArtifact(scope sandbox.Scope, artifacts []session.OperationArtifact) (session.OperationArtifact, core.Media, bool) {
	for i := len(artifacts) - 1; i >= 0; i-- {
		artifact := artifacts[i]
		if !operationArtifactIsWorkEvidence(artifact) {
			continue
		}
		media, ok := normalizeOutboundReplyMediaPath(scope, artifact.Ref, false)
		if !ok {
			continue
		}
		return artifact, media, true
	}
	return session.OperationArtifact{}, core.Media{}, false
}

func operationArtifactIsWorkEvidence(artifact session.OperationArtifact) bool {
	label := strings.ToLower(strings.TrimSpace(artifact.Label))
	ref := strings.ToLower(filepath.ToSlash(strings.TrimSpace(artifact.Ref)))
	return label == "work evidence" || strings.Contains(ref, "/work-evidence/") || strings.Contains(ref, "memory/work-evidence/")
}

func operationArtifactLooksLikePDF(artifact session.OperationArtifact) bool {
	joined := strings.ToLower(strings.TrimSpace(artifact.Label) + " " + strings.TrimSpace(artifact.Ref))
	return strings.Contains(joined, ".pdf") || strings.Contains(joined, "pdf")
}

func operationArtifactReplyText(artifact session.OperationArtifact, media core.Media) string {
	label := strings.TrimSpace(artifact.Label)
	if label == "" {
		label = strings.TrimSpace(media.Filename)
	}
	if label == "" {
		return "Sending the latest operation artifact."
	}
	return "Sending " + label + "."
}
