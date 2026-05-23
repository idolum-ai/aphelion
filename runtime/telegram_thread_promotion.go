//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) PromoteTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (session.TelegramThreadPromotionResult, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime unavailable")
	}
	if chatID == 0 || threadID <= 0 {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("thread id is required")
	}
	if err := r.requireTelegramThreadPromotionAdmin(senderID, "Promote"); err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}

	threadKey := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramThreadScopeRef(chatID, threadID)}
	unlockThread := r.lockSession(threadKey)
	defer unlockThread()

	thread, ok, err := r.store.TelegramThread(chatID, threadID)
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	if !ok {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError(fmt.Sprintf("Thread %d does not exist. Start a new side thread with `/thread <message>`.", threadID))
	}
	threadLabel := telegramThreadOperatorLabel(thread, threadID)
	if !thread.Open() {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError(fmt.Sprintf("Thread %s is closed. Start a new side thread with `/thread <message>`.", threadLabel))
	}

	handoff, created, err := r.store.CreateTelegramThreadPromotionDraft(chatID, threadID, senderID, time.Now().UTC())
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	if handoff.Status == session.TelegramThreadPromotionStatusDraft {
		packaged, err := r.populateTelegramThreadPromotionReviewPackage(thread, handoff)
		if err != nil {
			return session.TelegramThreadPromotionResult{}, err
		}
		handoff = packaged
	}
	return telegramThreadPromotionStatusResult(threadLabel, handoff, created), nil
}

func telegramThreadPromotionStatusResult(threadLabel string, handoff session.TelegramThreadPromotionHandoff, created bool) session.TelegramThreadPromotionResult {
	switch session.NormalizeTelegramThreadPromotionStatus(handoff.Status) {
	case session.TelegramThreadPromotionStatusDraft:
		return telegramThreadPromotionResult(renderTelegramThreadPromotionDraft(threadLabel, handoff, created), handoff)
	case session.TelegramThreadPromotionStatusReady:
		return telegramThreadPromotionResult(renderTelegramThreadPromotionReady(threadLabel, handoff), handoff)
	case session.TelegramThreadPromotionStatusApproved:
		return telegramThreadPromotionResult(renderTelegramThreadPromotionTerminal("Promotion handoff already approved", handoff), handoff)
	case session.TelegramThreadPromotionStatusCancelled:
		return telegramThreadPromotionResult(renderTelegramThreadPromotionTerminal("Promotion handoff already cancelled", handoff), handoff)
	case session.TelegramThreadPromotionStatusSuperseded:
		return telegramThreadPromotionResult(renderTelegramThreadPromotionTerminal("Promotion handoff superseded", handoff), handoff)
	default:
		return telegramThreadPromotionResult(renderTelegramThreadPromotionTerminal("Promotion handoff unavailable", handoff), handoff)
	}
}

func (r *Runtime) PrepareTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime unavailable")
	}
	if err := r.requireTelegramThreadPromotionAdmin(senderID, "Promote"); err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	handoff, ok, err := r.store.TelegramThreadPromotionHandoff(handoffID)
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	if !ok {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError("Promotion handoff is no longer available. Run /threads and promote again.")
	}
	if chatID != 0 && handoff.ChatID != chatID {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError("Promotion handoff belongs to another chat. Run /threads again.")
	}
	threadKey := session.SessionKey{ChatID: handoff.ChatID, UserID: 0, Scope: telegramThreadScopeRef(handoff.ChatID, handoff.ThreadID)}
	unlockThread := r.lockSession(threadKey)
	defer unlockThread()
	thread, ok, err := r.store.TelegramThread(handoff.ChatID, handoff.ThreadID)
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	if !ok {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError("Source thread is no longer available. Cancel this promotion and start again.")
	}
	threadLabel := telegramThreadOperatorLabel(thread, handoff.ThreadID)
	switch handoff.Status {
	case session.TelegramThreadPromotionStatusDraft:
		packaged, err := r.populateTelegramThreadPromotionReviewPackage(thread, handoff)
		if err != nil {
			return session.TelegramThreadPromotionResult{}, err
		}
		handoff, err = r.store.MarkTelegramThreadPromotionReady(packaged.HandoffID, time.Now().UTC())
		if err != nil {
			return session.TelegramThreadPromotionResult{}, err
		}
	case session.TelegramThreadPromotionStatusReady:
		// Idempotent ready callback.
	default:
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError(fmt.Sprintf("Promotion handoff %s is %s and cannot be marked ready.", handoff.HandoffID, handoff.Status))
	}
	return telegramThreadPromotionResult(renderTelegramThreadPromotionReady(threadLabel, handoff), handoff), nil
}

func (r *Runtime) CancelTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime unavailable")
	}
	if err := r.requireTelegramThreadPromotionAdmin(senderID, "Promote"); err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	handoff, ok, err := r.store.TelegramThreadPromotionHandoff(handoffID)
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	if !ok {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError("Promotion handoff is no longer available. Run /threads again.")
	}
	if chatID != 0 && handoff.ChatID != chatID {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError("Promotion handoff belongs to another chat. Run /threads again.")
	}
	updated, err := r.store.CancelTelegramThreadPromotion(handoff.HandoffID, time.Now().UTC())
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	return telegramThreadPromotionResult(renderTelegramThreadPromotionTerminal("Promotion cancelled", updated), updated), nil
}

func (r *Runtime) SupersedeTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime unavailable")
	}
	if err := r.requireTelegramThreadPromotionAdmin(senderID, "Promote"); err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	handoff, ok, err := r.store.TelegramThreadPromotionHandoff(handoffID)
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	if !ok {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError("Promotion handoff is no longer available. Run /threads again.")
	}
	if chatID != 0 && handoff.ChatID != chatID {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError("Promotion handoff belongs to another chat. Run /threads again.")
	}
	thread, ok, err := r.store.TelegramThread(handoff.ChatID, handoff.ThreadID)
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	if !ok || !thread.Open() {
		return session.TelegramThreadPromotionResult{}, telegramThreadRuntimeUserError("Source thread is not open. Cancel this promotion and start a new side thread if needed.")
	}
	if _, err := r.store.SupersedeTelegramThreadPromotion(handoff.HandoffID, time.Now().UTC()); err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	fresh, created, err := r.store.CreateTelegramThreadPromotionDraft(handoff.ChatID, handoff.ThreadID, senderID, time.Now().UTC())
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	fresh, err = r.populateTelegramThreadPromotionReviewPackage(thread, fresh)
	if err != nil {
		return session.TelegramThreadPromotionResult{}, err
	}
	text := renderTelegramThreadPromotionDraft(telegramThreadOperatorLabel(thread, handoff.ThreadID), fresh, created)
	return telegramThreadPromotionResult("Previous promotion handoff superseded.\n\n"+text, fresh), nil
}

func telegramThreadPromotionResult(text string, handoff session.TelegramThreadPromotionHandoff) session.TelegramThreadPromotionResult {
	handoff = session.NormalizeTelegramThreadPromotionHandoff(handoff)
	return session.TelegramThreadPromotionResult{
		Text:      strings.TrimSpace(text),
		HandoffID: handoff.HandoffID,
		ThreadID:  handoff.ThreadID,
		Status:    handoff.Status,
	}
}

func (r *Runtime) requireTelegramThreadPromotionAdmin(senderID int64, verb string) error {
	actor, ok := r.resolver.ResolveTelegramUser(senderID)
	if !ok {
		return ErrPrincipalDenied
	}
	if actor.Role != principal.RoleAdmin {
		return telegramThreadRuntimeUserError(strings.TrimSpace(verb) + " is admin only.")
	}
	return nil
}

func (r *Runtime) populateTelegramThreadPromotionReviewPackage(thread session.TelegramThread, handoff session.TelegramThreadPromotionHandoff) (session.TelegramThreadPromotionHandoff, error) {
	now := time.Now().UTC()
	digest := r.telegramThreadPromotionContextDigest(thread, handoff, now)
	child := telegramThreadPromotionProposedChild(thread, handoff)
	memory := telegramThreadPromotionMemoryCandidates(digest)
	resources := telegramThreadPromotionResourceCandidates(thread, handoff)
	policy := telegramThreadPromotionPolicyReview()
	handoff.ContextSummary = digest.Summary
	handoff.MemoryDigestJSON = session.EncodeTelegramThreadPromotionJSON(memory, `[]`)
	handoff.ResourceReviewJSON = session.EncodeTelegramThreadPromotionJSON(resources, `[]`)
	handoff.PolicyPatchJSON = session.EncodeTelegramThreadPromotionJSON(policy, `{}`)
	handoff.ProposedChildJSON = session.EncodeTelegramThreadPromotionJSON(child, `{}`)
	handoff.FirstTask = child.FirstTask
	handoff.ValidationJSON = session.EncodeTelegramThreadPromotionJSON([]string{
		"operator reviewed source thread digest",
		"operator reviewed proposed child spec",
		"memory candidates remain proposed; no child memory write yet",
		"resource candidates remain proposed; no capability grant yet",
		"first supervised run requires separate approval after approval/apply phase",
	}, `[]`)
	handoff.ReviewChecklistJSON = session.EncodeTelegramThreadPromotionJSON([]string{
		"review source thread digest",
		"edit or accept proposed child spec",
		"include/exclude memory candidates",
		"include/exclude resource and capability candidates",
		"review policy patch and first supervised task",
		"mark ready; approval/apply remains a separate gated action",
	}, `[]`)
	return r.store.UpdateTelegramThreadPromotionReviewPackage(handoff, now)
}

func (r *Runtime) telegramThreadPromotionContextDigest(thread session.TelegramThread, handoff session.TelegramThreadPromotionHandoff, now time.Time) session.TelegramThreadPromotionContextDigest {
	key := session.SessionKey{ChatID: thread.ChatID, UserID: 0, Scope: telegramThreadScopeRef(thread.ChatID, thread.ThreadID)}
	sess, err := r.store.Load(key)
	messages := []session.TelegramThreadPromotionContextMessage{}
	total := 0
	if err == nil && sess != nil {
		for i := len(sess.Messages) - 1; i >= 0 && len(messages) < 8; i-- {
			msg := sess.Messages[i]
			role := strings.ToLower(strings.TrimSpace(msg.Role))
			if role != "user" && role != "assistant" {
				continue
			}
			content := strings.Join(strings.Fields(strings.TrimSpace(msg.Content)), " ")
			if content == "" || strings.HasPrefix(content, "/") {
				continue
			}
			total++
			messages = append(messages, session.TelegramThreadPromotionContextMessage{
				ID:        fmt.Sprintf("turn:%d:%s:%d", msg.TurnIndex, role, len(messages)+1),
				Role:      role,
				TurnIndex: msg.TurnIndex,
				Excerpt:   truncateRunes(content, 260),
				CreatedAt: msg.CreatedAt,
			})
		}
		for i := len(sess.Messages) - 1 - len(messages); i >= 0; i-- {
			msg := sess.Messages[i]
			role := strings.ToLower(strings.TrimSpace(msg.Role))
			content := strings.TrimSpace(msg.Content)
			if (role == "user" || role == "assistant") && content != "" && !strings.HasPrefix(content, "/") {
				total++
			}
		}
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	preview := strings.Join(strings.Fields(strings.TrimSpace(thread.CreatedText)), " ")
	label := normalizeTelegramThreadOperatorLabel(telegramThreadOperatorLabel(thread, handoff.ThreadID), strconv.FormatInt(handoff.ThreadID, 10))
	summaryParts := []string{"Promotion candidate for thread " + label + "."}
	if preview != "" {
		summaryParts = append(summaryParts, "Source intent: "+truncateRunes(preview, 260)+".")
	}
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		summaryParts = append(summaryParts, "Recent thread context ends with "+last.Role+": "+truncateRunes(last.Excerpt, 220))
	} else {
		summaryParts = append(summaryParts, "No source-thread transcript messages were found beyond the thread prompt; review manually before applying.")
	}
	return session.TelegramThreadPromotionContextDigest{
		Summary:              strings.TrimSpace(strings.Join(summaryParts, " ")),
		SourceSessionID:      session.SessionIDForKey(key),
		SourceThreadStatus:   string(thread.Status),
		SourcePreview:        preview,
		MessageCount:         total,
		IncludedMessageCount: len(messages),
		RecentMessages:       messages,
		SourceRefs: []session.RecordReference{
			{Kind: "telegram_thread", Ref: fmt.Sprintf("%d:%d", thread.ChatID, thread.ThreadID), Label: "source thread"},
			{Kind: "session", Ref: session.SessionIDForKey(key), Label: "source thread session"},
		},
		GeneratedAt: now,
	}
}

func telegramThreadPromotionProposedChild(thread session.TelegramThread, handoff session.TelegramThreadPromotionHandoff) session.TelegramThreadPromotionProposedChild {
	label := telegramThreadPromotionChildLabel(thread, handoff)
	return session.TelegramThreadPromotionProposedChild{
		AgentID:     telegramThreadPromotionAgentID(thread.ChatID, thread.ThreadID),
		Label:       label,
		ChannelKind: "local",
		Charter:     "Continue the work from source thread " + normalizeTelegramThreadOperatorLabel(telegramThreadOperatorLabel(thread, handoff.ThreadID), strconv.FormatInt(handoff.ThreadID, 10)) + " using only the approved promotion handoff. Ask before external effects, memory writes, grants, deploys, restarts, or public/contact actions.",
		Autonomy:    "review_before_reply",
		Visibility:  "parent_relay_only",
		WakeupMode:  "manual",
		FirstTask:   telegramThreadPromotionRuntimeFirstTask(thread, handoff),
		NonGoals: []string{
			"no raw parent memory inheritance",
			"no child memory writes during review-package phase",
			"no capability grants during review-package phase",
			"no live child first run until separately approved",
		},
	}
}

func telegramThreadPromotionChildLabel(thread session.TelegramThread, handoff session.TelegramThreadPromotionHandoff) string {
	preview := strings.Join(strings.Fields(strings.TrimSpace(thread.CreatedText)), " ")
	if preview == "" {
		preview = "Thread " + normalizeTelegramThreadOperatorLabel(telegramThreadOperatorLabel(thread, handoff.ThreadID), strconv.FormatInt(handoff.ThreadID, 10))
	}
	return truncateRunes(preview, 48)
}

func telegramThreadPromotionAgentID(chatID int64, threadID int64) string {
	chat := strconv.FormatInt(chatID, 10)
	chat = strings.TrimPrefix(chat, "-")
	return "thread-" + chat + "-" + strconv.FormatInt(threadID, 10)
}

func telegramThreadPromotionRuntimeFirstTask(thread session.TelegramThread, handoff session.TelegramThreadPromotionHandoff) string {
	label := normalizeTelegramThreadOperatorLabel(telegramThreadOperatorLabel(thread, handoff.ThreadID), strconv.FormatInt(handoff.ThreadID, 10))
	preview := strings.Join(strings.Fields(strings.TrimSpace(thread.CreatedText)), " ")
	if preview == "" {
		preview = "review the source thread context"
	}
	return "Read the approved promotion handoff for source thread " + label + ", restate the charter, boundaries, memory/resource decisions, and first useful next step, then stop for parent review before taking external action. Source intent: " + truncateRunes(preview, 500)
}

func telegramThreadPromotionMemoryCandidates(digest session.TelegramThreadPromotionContextDigest) []session.TelegramThreadPromotionMemoryCandidate {
	out := []session.TelegramThreadPromotionMemoryCandidate{}
	if strings.TrimSpace(digest.SourcePreview) != "" {
		out = append(out, session.TelegramThreadPromotionMemoryCandidate{
			CandidateID: "source-preview",
			Source:      "telegram_thread.created_text",
			Label:       "source thread prompt",
			Excerpt:     truncateRunes(digest.SourcePreview, 220),
			Decision:    "review_required",
			Reason:      "candidate only; not written to child memory",
		})
	}
	for i, msg := range digest.RecentMessages {
		if len(out) >= 4 {
			break
		}
		out = append(out, session.TelegramThreadPromotionMemoryCandidate{
			CandidateID: fmt.Sprintf("recent-message-%d", i+1),
			Source:      "telegram_thread.session",
			Label:       msg.Role + " turn " + strconv.Itoa(msg.TurnIndex),
			Excerpt:     truncateRunes(msg.Excerpt, 220),
			Decision:    "review_required",
			Reason:      "candidate only; not written to child memory",
		})
	}
	return out
}

func telegramThreadPromotionResourceCandidates(thread session.TelegramThread, _ session.TelegramThreadPromotionHandoff) []session.TelegramThreadPromotionResourceCandidate {
	return []session.TelegramThreadPromotionResourceCandidate{
		{
			CandidateID:    "source-thread-read",
			Kind:           "generic_delegation",
			TargetResource: fmt.Sprintf("telegram_thread:%d:%d", thread.ChatID, thread.ThreadID),
			Purpose:        "allow the proposed child to read the approved promotion handoff and source-thread digest",
			RiskClass:      "low",
			Decision:       "review_required",
			Reason:         "does not grant tools, filesystem, network, credentials, or external accounts",
		},
	}
}

func telegramThreadPromotionPolicyReview() session.TelegramThreadPromotionPolicyReview {
	return session.TelegramThreadPromotionPolicyReview{
		Mode:          "local",
		Autonomy:      "review_before_reply",
		Visibility:    "parent_relay_only",
		SharedContext: "isolated",
		DriftPolicy:   "admin_review",
		Capabilities:  []string{"read_approved_promotion_handoff"},
		StopConditions: []string{
			"external account or public contact requested",
			"credential or token access requested",
			"filesystem/tool/network capability needed",
			"memory write requested",
			"deploy, restart, commit, push, or destructive action requested",
		},
	}
}

func renderTelegramThreadPromotionDraft(threadLabel string, handoff session.TelegramThreadPromotionHandoff, created bool) string {
	threadLabel = normalizeTelegramThreadOperatorLabel(threadLabel, strconv.FormatInt(handoff.ThreadID, 10))
	var b strings.Builder
	if created {
		fmt.Fprintf(&b, "Promotion draft created for thread %s.\n\n", threadLabel)
	} else {
		fmt.Fprintf(&b, "Promotion draft already exists for thread %s.\n\n", threadLabel)
	}
	renderTelegramThreadPromotionReviewPackage(&b, threadLabel, handoff)
	b.WriteString("\nNext: review this package, then tap Ready when it is suitable for a separate approval/apply gate.")
	b.WriteString("\n\nThis draft does not create a durable child, transfer memory, grant resources, or run work.")
	return strings.TrimSpace(b.String())
}

func renderTelegramThreadPromotionReady(threadLabel string, handoff session.TelegramThreadPromotionHandoff) string {
	threadLabel = normalizeTelegramThreadOperatorLabel(threadLabel, strconv.FormatInt(handoff.ThreadID, 10))
	var b strings.Builder
	fmt.Fprintf(&b, "Promotion handoff ready for thread %s.\n\n", threadLabel)
	renderTelegramThreadPromotionReviewPackage(&b, threadLabel, handoff)
	b.WriteString("\nNext gate: approve/apply may create a durable child and must remain separately authorized.")
	b.WriteString("\n\nNo durable child, memory write, capability grant, or first run happened in this step.")
	return strings.TrimSpace(b.String())
}

func renderTelegramThreadPromotionTerminal(prefix string, handoff session.TelegramThreadPromotionHandoff) string {
	return strings.TrimSpace(fmt.Sprintf("%s.\n\nHandoff: %s\nStatus: %s\n\nNo durable child, memory write, capability grant, or work run happened.", strings.TrimSpace(prefix), strings.TrimSpace(handoff.HandoffID), strings.TrimSpace(string(handoff.Status))))
}

func renderTelegramThreadPromotionReviewPackage(b *strings.Builder, threadLabel string, handoff session.TelegramThreadPromotionHandoff) {
	fmt.Fprintf(b, "Handoff: %s\n", strings.TrimSpace(handoff.HandoffID))
	fmt.Fprintf(b, "Status: %s\n", strings.TrimSpace(string(handoff.Status)))
	if child, ok := session.DecodeTelegramThreadPromotionProposedChild(handoff.ProposedChildJSON); ok {
		fmt.Fprintf(b, "Proposed child: %s (%s)\n", firstNonEmpty(strings.TrimSpace(child.Label), strings.TrimSpace(child.AgentID)), strings.TrimSpace(child.Autonomy))
		if strings.TrimSpace(child.FirstTask) != "" {
			fmt.Fprintf(b, "First task: %s\n", truncateRunes(child.FirstTask, 260))
		}
	}
	if summary := strings.TrimSpace(handoff.ContextSummary); summary != "" {
		fmt.Fprintf(b, "Context digest: %s\n", truncateRunes(strings.Join(strings.Fields(summary), " "), 320))
	}
	if memory, ok := session.DecodeTelegramThreadPromotionMemoryCandidates(handoff.MemoryDigestJSON); ok {
		fmt.Fprintf(b, "Memory candidates: %d proposed; none written\n", len(memory))
	}
	if resources, ok := session.DecodeTelegramThreadPromotionResourceCandidates(handoff.ResourceReviewJSON); ok {
		fmt.Fprintf(b, "Resource candidates: %d proposed; no grants created\n", len(resources))
	}
	if policy, ok := session.DecodeTelegramThreadPromotionPolicyReview(handoff.PolicyPatchJSON); ok && strings.TrimSpace(policy.Autonomy) != "" {
		fmt.Fprintf(b, "Policy: %s / %s / shared_context=%s\n", strings.TrimSpace(policy.Autonomy), strings.TrimSpace(policy.Visibility), strings.TrimSpace(policy.SharedContext))
	}
	if validation := strings.TrimSpace(handoff.ValidationJSON); validation != "" && validation != "[]" {
		fmt.Fprintf(b, "Validation: %s\n", truncateRunes(strings.Join(strings.Fields(validation), " "), 220))
	}
	_ = threadLabel
}
