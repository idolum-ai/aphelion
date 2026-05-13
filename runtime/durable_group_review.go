//go:build linux

package runtime

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func durableTelegramReviewArtifact(agent core.DurableAgent, policy core.DurableAgentLivePolicy, msg core.InboundMessage, replyText string) *core.DurableReviewArtifact {
	if durableTelegramChannel(agent.ChannelKind) == durableTelegramChannelDM {
		return durableTelegramDMReviewArtifact(agent, policy, msg, replyText)
	}
	return durableGroupReviewArtifact(agent, policy, msg, replyText)
}

func durableTelegramDMReviewArtifact(agent core.DurableAgent, policy core.DurableAgentLivePolicy, msg core.InboundMessage, replyText string) *core.DurableReviewArtifact {
	assessment := durableGroupAssessInteraction(msg.Text)
	allowLocalReply := durableGroupAllowsLocalReply(policy)
	triggerKinds := durableTelegramDMTriggerKinds(assessment, allowLocalReply, len(msg.Artifacts) > 0)
	shouldEscalate := !allowLocalReply || durableGroupShouldEscalate(policy, assessment)
	if !shouldEscalate {
		return nil
	}

	sender := strings.TrimSpace(msg.SenderName)
	if sender == "" && msg.SenderID != 0 {
		sender = fmt.Sprintf("user_%d", msg.SenderID)
	}
	if sender == "" {
		sender = "direct_user"
	}
	summary := strings.TrimSpace(msg.Text)
	if summary == "" {
		summary = "[no text]"
	}
	metadata := map[string]string{
		"chat_id":             strconv.FormatInt(msg.ChatID, 10),
		"chat_title":          strings.TrimSpace(msg.ChatTitle),
		"sender_id":           strconv.FormatInt(msg.SenderID, 10),
		"sender_name":         sender,
		"source_excerpt":      truncateRunes(summary, 240),
		"channel_kind":        durableTelegramChannelDM,
		"durable_agent_id":    strings.TrimSpace(agent.AgentID),
		"policy_outbound":     strings.TrimSpace(policy.OutboundMode),
		"trigger_kinds":       strings.Join(triggerKinds, ","),
		"question_detected":   boolString(assessment.DirectQuestion),
		"child_local_subject": "true",
	}
	if allowLocalReply {
		metadata["local_response"] = truncateRunes(strings.TrimSpace(replyText), 240)
	} else if strings.TrimSpace(replyText) != "" {
		metadata["draft_response"] = truncateRunes(strings.TrimSpace(replyText), 240)
	}
	if len(assessment.DriftSignals) > 0 {
		metadata["drift_detected"] = "true"
	}
	return &core.DurableReviewArtifact{
		AgentID:       strings.TrimSpace(agent.AgentID),
		Summary:       durableTelegramDMReviewSummary(sender, assessment, policy, allowLocalReply),
		IntervalLabel: strconv.FormatInt(msg.MessageID, 10),
		LocalActions:  durableTelegramDMReviewLocalActions(policy, assessment, allowLocalReply),
		Questions:     durableTelegramDMReviewQuestions(policy, assessment, allowLocalReply),
		RiskFlags:     uniqueStrings(append(append([]string{}, triggerKinds...), assessment.DriftSignals...)),
		Metadata:      metadata,
	}
}

func durableTelegramDMTriggerKinds(assessment durableGroupInteractionAssessment, allowLocalReply bool, hasArtifacts bool) []string {
	out := append([]string(nil), assessment.TriggerKinds...)
	if !allowLocalReply {
		out = append(out, "withheld_local_reply")
	}
	if hasArtifacts {
		out = append(out, "artifact_attachment")
	}
	return uniqueStrings(out)
}

func durableTelegramDMReviewSummary(sender string, assessment durableGroupInteractionAssessment, policy core.DurableAgentLivePolicy, allowLocalReply bool) string {
	switch {
	case len(assessment.DriftSignals) > 0:
		return fmt.Sprintf("Telegram DM from child-local subject %s may be pressuring durable charter drift.", sender)
	case !allowLocalReply && strings.TrimSpace(policy.OutboundMode) == "reply_with_parent_review":
		return fmt.Sprintf("Telegram DM from child-local subject %s is awaiting parent review before any reply.", sender)
	case !allowLocalReply && strings.TrimSpace(policy.OutboundMode) == "draft_only":
		return fmt.Sprintf("Telegram DM from child-local subject %s produced a local draft for parent review.", sender)
	case assessment.DirectQuestion:
		return fmt.Sprintf("Telegram DM question from child-local subject %s was surfaced for bounded review.", sender)
	default:
		return fmt.Sprintf("Telegram DM from child-local subject %s was surfaced for bounded review.", sender)
	}
}

func durableTelegramDMReviewLocalActions(policy core.DurableAgentLivePolicy, assessment durableGroupInteractionAssessment, allowLocalReply bool) []string {
	actions := make([]string, 0, 3)
	switch {
	case allowLocalReply:
		actions = append(actions, "Replied locally within the current durable DM charter.")
	case strings.TrimSpace(policy.OutboundMode) == "reply_with_parent_review":
		actions = append(actions, "Held the direct-message reply because live policy requires parent review.")
	case strings.TrimSpace(policy.OutboundMode) == "draft_only":
		actions = append(actions, "Prepared a direct-message draft but did not reply because live policy is draft_only.")
	case strings.TrimSpace(policy.OutboundMode) == "read_only":
		actions = append(actions, "Stayed silent in direct-message lane because live policy is read_only.")
	default:
		actions = append(actions, "Did not reply locally under the current durable DM live policy.")
	}
	if len(assessment.DriftSignals) > 0 {
		actions = append(actions, "Did not widen standing role, authority, memory, or secret scope.")
	}
	if assessment.DirectQuestion && !allowLocalReply {
		actions = append(actions, "Surfaced the direct-message question upward for parent review instead of answering in-channel.")
	}
	return uniqueStrings(actions)
}

func durableTelegramDMReviewQuestions(policy core.DurableAgentLivePolicy, assessment durableGroupInteractionAssessment, allowLocalReply bool) []string {
	questions := make([]string, 0, 3)
	if len(assessment.DriftSignals) > 0 {
		questions = append(questions, "Should this durable DM child's charter or authority be adjusted in response to this pressure?")
	}
	if !allowLocalReply {
		questions = append(questions, "Approve, edit, or reject the held direct-message response?")
	} else if assessment.DirectQuestion {
		questions = append(questions, "Should this direct-message question be retained for continuity follow-up?")
	}
	return uniqueStrings(questions)
}

func durableGroupReviewArtifact(agent core.DurableAgent, policy core.DurableAgentLivePolicy, msg core.InboundMessage, replyText string) *core.DurableReviewArtifact {
	assessment := durableGroupAssessInteraction(msg.Text)
	if !durableGroupShouldEscalate(policy, assessment) {
		return nil
	}
	summary := strings.TrimSpace(msg.Text)
	if summary == "" {
		summary = "[no text]"
	}
	member := strings.TrimSpace(msg.SenderName)
	if member == "" && msg.SenderID != 0 {
		member = fmt.Sprintf("user_%d", msg.SenderID)
	}
	if member == "" {
		member = "group_member"
	}
	allowLocalReply := durableGroupAllowsLocalReply(policy)
	localActions := durableGroupReviewLocalActions(policy, assessment, allowLocalReply)
	questions := durableGroupReviewQuestions(policy, assessment)
	riskFlags := uniqueStrings(append(append([]string{}, assessment.TriggerKinds...), assessment.DriftSignals...))
	metadata := map[string]string{
		"chat_id":           strconv.FormatInt(msg.ChatID, 10),
		"chat_title":        strings.TrimSpace(msg.ChatTitle),
		"sender_id":         strconv.FormatInt(msg.SenderID, 10),
		"sender_name":       member,
		"source_excerpt":    truncateRunes(summary, 240),
		"channel_kind":      "telegram_group",
		"durable_agent_id":  strings.TrimSpace(agent.AgentID),
		"policy_outbound":   strings.TrimSpace(policy.OutboundMode),
		"trigger_kinds":     strings.Join(assessment.TriggerKinds, ","),
		"question_detected": boolString(assessment.DirectQuestion),
		"family_relevant":   boolString(assessment.FamilyRelevant),
	}
	if allowLocalReply {
		metadata["local_response"] = truncateRunes(strings.TrimSpace(replyText), 240)
	} else if strings.TrimSpace(replyText) != "" {
		metadata["draft_response"] = truncateRunes(strings.TrimSpace(replyText), 240)
	}
	if len(assessment.DriftSignals) > 0 {
		metadata["drift_detected"] = "true"
	}
	return &core.DurableReviewArtifact{
		AgentID:       strings.TrimSpace(agent.AgentID),
		Summary:       durableGroupReviewSummary(member, assessment, policy),
		IntervalLabel: strconv.FormatInt(msg.MessageID, 10),
		LocalActions:  localActions,
		Questions:     questions,
		RiskFlags:     riskFlags,
		Metadata:      metadata,
	}
}

func durableGroupAllowsLocalReply(policy core.DurableAgentLivePolicy) bool {
	switch strings.TrimSpace(policy.OutboundMode) {
	case "reply_with_policy_authorization":
		return true
	case "read_only", "draft_only", "reply_with_parent_review":
		return false
	default:
		return true
	}
}

func durableGroupDriftSignals(text string) []string {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return nil
	}
	signals := make([]string, 0, 4)
	if containsAny(lower, "from now on", "always ", "every time", "permanent", "new rule", "policy", "standing role", "you should be our", "act as our", "be our") {
		signals = append(signals, "standing_role_pressure")
	}
	if containsAny(lower, "remember this", "write this down", "store this forever", "save this permanently", "make this part of your memory") {
		signals = append(signals, "durable_memory_pressure")
	}
	if containsAny(lower, "password", "api key", "secret", "token", "credential", "ssh key") {
		signals = append(signals, "secret_request_pressure")
	}
	if containsAny(lower, "tool", "run command", "deploy", "write files", "change config", "grant access", "admin rights") {
		signals = append(signals, "authority_widening_pressure")
	}
	return uniqueStrings(signals)
}

type durableGroupInteractionAssessment struct {
	DirectQuestion         bool
	FamilyRelevant         bool
	FamilyRelevantUpdate   bool
	FamilyRelevantQuestion bool
	DriftSignals           []string
	TriggerKinds           []string
}

func durableGroupAssessInteraction(text string) durableGroupInteractionAssessment {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return durableGroupInteractionAssessment{}
	}
	lower := strings.ToLower(trimmed)
	directQuestion := strings.Contains(trimmed, "?") || startsWithAnyWord(lower,
		"can", "could", "should", "would", "will", "what", "when", "where", "who", "why", "how", "do", "does", "did", "is", "are", "am",
	)
	familyRelevant := containsAny(lower,
		"tonight", "tomorrow", "weekend", "birthday", "dinner", "lunch", "breakfast", "pick up", "pickup", "drop off", "school", "doctor", "appointment",
		"hospital", "med", "medicine", "pharmacy", "airport", "flight", "trip", "travel", "visit", "guest", "family", "mom", "dad", "grandma", "grandpa",
		"kid", "kids", "child", "children", "baby", "babysit", "groceries", "errand", "house", "home", "rent", "bill", "payment", "arrive", "arriving",
		"leave", "leaving", "landed", "confirmed", "cancelled", "rescheduled", "moved",
	)
	familyRelevantUpdate := !directQuestion && containsAny(lower,
		"heads up", "fyi", "update", "confirmed", "cancelled", "rescheduled", "moved", "arriving", "leaving", "landed", "appointment", "pickup", "drop off",
		"tomorrow", "tonight", "weekend", "birthday", "flight", "airport", "visit", "hospital", "school", "doctor",
	)
	familyRelevantQuestion := directQuestion && familyRelevant
	driftSignals := durableGroupDriftSignals(trimmed)

	triggerKinds := make([]string, 0, 4)
	if len(driftSignals) > 0 {
		triggerKinds = append(triggerKinds, "drift_pressure")
	}
	if familyRelevantQuestion {
		triggerKinds = append(triggerKinds, "family_relevant_question")
	} else if directQuestion {
		triggerKinds = append(triggerKinds, "direct_question")
	}
	if familyRelevantUpdate {
		triggerKinds = append(triggerKinds, "family_relevant_update")
	}

	return durableGroupInteractionAssessment{
		DirectQuestion:         directQuestion,
		FamilyRelevant:         familyRelevant,
		FamilyRelevantUpdate:   familyRelevantUpdate,
		FamilyRelevantQuestion: familyRelevantQuestion,
		DriftSignals:           driftSignals,
		TriggerKinds:           uniqueStrings(triggerKinds),
	}
}

func durableGroupShouldEscalate(policy core.DurableAgentLivePolicy, assessment durableGroupInteractionAssessment) bool {
	if len(assessment.DriftSignals) > 0 || assessment.FamilyRelevantUpdate || assessment.FamilyRelevantQuestion {
		return true
	}
	switch strings.TrimSpace(policy.OutboundMode) {
	case "draft_only", "reply_with_parent_review":
		return assessment.DirectQuestion
	default:
		return false
	}
}

func durableGroupReviewSummary(member string, assessment durableGroupInteractionAssessment, policy core.DurableAgentLivePolicy) string {
	switch {
	case len(assessment.DriftSignals) > 0:
		return fmt.Sprintf("Telegram group pressure from %s may be pushing the durable child beyond its standing charter.", member)
	case assessment.FamilyRelevantQuestion && strings.TrimSpace(policy.OutboundMode) == "reply_with_parent_review":
		return fmt.Sprintf("Family-relevant question from %s is awaiting parent review before any reply.", member)
	case assessment.FamilyRelevantQuestion && strings.TrimSpace(policy.OutboundMode) == "draft_only":
		return fmt.Sprintf("Family-relevant question from %s produced a local draft that still needs parent review.", member)
	case assessment.FamilyRelevantQuestion:
		return fmt.Sprintf("Family-relevant question from %s may need parent visibility or follow-up.", member)
	case assessment.FamilyRelevantUpdate:
		return fmt.Sprintf("Family-relevant update from %s may matter for durable continuity.", member)
	case assessment.DirectQuestion && strings.TrimSpace(policy.OutboundMode) == "reply_with_parent_review":
		return fmt.Sprintf("Direct group question from %s is awaiting parent review before any reply.", member)
	case assessment.DirectQuestion && strings.TrimSpace(policy.OutboundMode) == "draft_only":
		return fmt.Sprintf("Direct group question from %s produced a local draft that still needs parent review.", member)
	default:
		return fmt.Sprintf("Group interaction from %s was surfaced for parent review.", member)
	}
}

func durableGroupReviewLocalActions(policy core.DurableAgentLivePolicy, assessment durableGroupInteractionAssessment, allowLocalReply bool) []string {
	actions := make([]string, 0, 3)
	switch {
	case allowLocalReply:
		actions = append(actions, "Replied locally within the current charter.")
	case strings.TrimSpace(policy.OutboundMode) == "reply_with_parent_review":
		actions = append(actions, "Held the reply because live policy requires parent review.")
	case strings.TrimSpace(policy.OutboundMode) == "draft_only":
		actions = append(actions, "Prepared a local draft but did not reply because live policy is draft_only.")
	case strings.TrimSpace(policy.OutboundMode) == "read_only":
		actions = append(actions, "Stayed silent because live policy is read_only.")
	default:
		actions = append(actions, "Did not reply locally under the current live policy.")
	}
	if len(assessment.DriftSignals) > 0 {
		actions = append(actions, "Did not widen standing role, authority, memory, or secret scope.")
	}
	if assessment.FamilyRelevantUpdate {
		actions = append(actions, "Surfaced the update upward for bounded continuity review.")
	}
	if assessment.DirectQuestion && !allowLocalReply {
		actions = append(actions, "Surfaced the question upward for parent review instead of answering in-channel.")
	}
	return uniqueStrings(actions)
}

func durableGroupReviewQuestions(policy core.DurableAgentLivePolicy, assessment durableGroupInteractionAssessment) []string {
	questions := make([]string, 0, 3)
	if len(assessment.DriftSignals) > 0 {
		questions = append(questions, "Should the durable child's charter, standing role, or authority change in response to this pressure?")
	}
	if assessment.FamilyRelevantQuestion {
		if strings.TrimSpace(policy.OutboundMode) == "reply_with_parent_review" || strings.TrimSpace(policy.OutboundMode) == "draft_only" {
			questions = append(questions, "Approve, edit, or reject the held reply to this family-relevant question?")
		} else {
			questions = append(questions, "Should this family-relevant question be retained for continuity or follow-up?")
		}
	}
	if assessment.FamilyRelevantUpdate {
		questions = append(questions, "Should this family-relevant update be retained in durable continuity or promoted upward?")
	}
	if assessment.DirectQuestion && !assessment.FamilyRelevantQuestion && (strings.TrimSpace(policy.OutboundMode) == "reply_with_parent_review" || strings.TrimSpace(policy.OutboundMode) == "draft_only") {
		questions = append(questions, "Approve, edit, or reject the held reply to this question?")
	}
	return uniqueStrings(questions)
}

func startsWithAnyWord(text string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if text == prefix || strings.HasPrefix(text, prefix+" ") || strings.HasPrefix(text, prefix+"?") {
			return true
		}
	}
	return false
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func containsAny(text string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
