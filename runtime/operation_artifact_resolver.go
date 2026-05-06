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
	if r == nil || r.store == nil || r.outbound == nil || !artifactRequestAllowedForOrigin(msg) || !looksLikeOperationArtifactSendRequest(msg.Text) {
		return false, nil, nil
	}
	state, err := r.store.OperationState(key)
	if err != nil {
		return false, nil, nil
	}
	state = session.NormalizeOperationState(state)
	artifact, media, ok := latestSendableOperationArtifact(scope, state.Artifacts, msg.Text)
	if !ok {
		return false, nil, nil
	}

	reply := operationArtifactReplyText(artifact, media)
	outboundID, outboundType, err := r.sendReply(ctx, msg, reply, []core.Media{media}, false)
	if err != nil {
		return true, &core.TurnResult{Text: reply, Media: []core.Media{media}}, fmt.Errorf("send operation artifact: %w", err)
	}

	sess, err := r.store.Load(key)
	if err != nil {
		return true, &core.TurnResult{Text: reply, Media: []core.Media{media}}, fmt.Errorf("load session for operation artifact reply: %w", err)
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
		return true, &core.TurnResult{Text: reply, Media: []core.Media{media}}, fmt.Errorf("save operation artifact reply: %w", err)
	}
	if outboundID != 0 {
		if err := r.store.RecordOutbound(key, turnIndex, outboundID, outboundType); err != nil {
			return true, &core.TurnResult{Text: reply, Media: []core.Media{media}}, fmt.Errorf("record operation artifact reply: %w", err)
		}
	}
	r.recordExecutionEvent(key, core.ExecutionEventDeliveryFinalSent, "delivery", "sent", map[string]any{
		"message_id":   outboundID,
		"message_type": outboundType,
		"artifact_ref": artifact.Ref,
		"artifact":     firstNonEmpty(artifact.Label, filepath.Base(artifact.Ref)),
	}, time.Now().UTC())
	return true, &core.TurnResult{Text: reply, Media: []core.Media{media}}, nil
}

func artifactRequestAllowedForOrigin(msg core.InboundMessage) bool {
	return msg.Origin == "" || msg.Origin == core.InboundOriginUser
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
	return operationArtifactNormalizedTextNamesArtifact(normalized)
}

func operationArtifactReplyContextNamesArtifact(replyContext string) bool {
	return operationArtifactNormalizedTextNamesArtifact(operationArtifactNormalizeRequestText(replyContext))
}

func operationArtifactNormalizedTextNamesArtifact(normalized string) bool {
	for _, field := range strings.Fields(normalized) {
		switch field {
		case "pdf", "artifact", "artifacts", "file", "files", "report", "reports", "evidence", "log", "logs":
			return true
		}
	}
	return strings.Contains(normalized, ".pdf")
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
