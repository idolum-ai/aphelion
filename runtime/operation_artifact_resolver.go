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
	if r == nil || r.store == nil || r.outbound == nil || !looksLikeOperationArtifactSendRequest(msg.Text) {
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

func looksLikeOperationArtifactSendRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" || !(strings.Contains(lower, "send") || strings.Contains(lower, "attach") || strings.Contains(lower, "share")) {
		return false
	}
	if strings.Contains(lower, "pdf") || strings.Contains(lower, "artifact") || strings.Contains(lower, "file") || strings.Contains(lower, "report") {
		return true
	}
	fields := strings.Fields(strings.NewReplacer("?", " ", "!", " ", ".", " ", ",", " ").Replace(lower))
	for _, field := range fields {
		if field == "it" || field == "that" {
			return true
		}
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
