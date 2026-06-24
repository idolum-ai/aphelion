//go:build linux

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type DurableAgentContinuityState struct {
	RecentInteractions []DurableAgentRecentInteraction          `json:"recent_interactions,omitempty"`
	PendingQuestions   []DurableAgentPendingQuestion            `json:"pending_questions,omitempty"`
	ReviewRefs         []DurableAgentReviewReference            `json:"review_refs,omitempty"`
	RatifiedOutcomes   []DurableAgentRatifiedOutcome            `json:"ratified_outcomes,omitempty"`
	Conversation       *DurableAgentConversationState           `json:"conversation,omitempty"`
	SetupWizard        *DurableAgentSetupWizardState            `json:"setup_wizard,omitempty"`
	EmailPending       *DurableAgentEmailPendingState           `json:"email_pending,omitempty"`
	ExternalChannel    *DurableAgentExternalChannelRuntimeState `json:"external_channel,omitempty"`
	ScheduledReview    *DurableAgentScheduledReviewRuntimeState `json:"scheduled_review,omitempty"`
}

type DurableAgentConversationState struct {
	Messages []DurableAgentConversationMessage `json:"messages,omitempty"`
}

type DurableAgentConversationMessage struct {
	MessageID      string    `json:"message_id,omitempty"`
	Role           string    `json:"role,omitempty"`
	Text           string    `json:"text,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	AcknowledgedAt time.Time `json:"acknowledged_at,omitempty"`
}

type DurableAgentRecentInteraction struct {
	Summary       string    `json:"summary,omitempty"`
	Source        string    `json:"source,omitempty"`
	TriggerKinds  []string  `json:"trigger_kinds,omitempty"`
	ReviewEventID int64     `json:"review_event_id,omitempty"`
	OccurredAt    time.Time `json:"occurred_at,omitempty"`
}

type DurableAgentPendingQuestion struct {
	Question      string    `json:"question,omitempty"`
	ReviewEventID int64     `json:"review_event_id,omitempty"`
	Status        string    `json:"status,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type DurableAgentReviewReference struct {
	ReviewEventID int64     `json:"review_event_id,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	RiskFlags     []string  `json:"risk_flags,omitempty"`
	Status        string    `json:"status,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

type DurableAgentRatifiedOutcome struct {
	Summary             string    `json:"summary,omitempty"`
	PolicyVersion       int64     `json:"policy_version,omitempty"`
	PolicyHash          string    `json:"policy_hash,omitempty"`
	SourceReviewEventID int64     `json:"source_review_event_id,omitempty"`
	AppliedAt           time.Time `json:"applied_at,omitempty"`
}

type DurableReviewArtifact struct {
	AgentID       string
	Summary       string
	IntervalLabel string
	LocalActions  []string
	Questions     []string
	RiskFlags     []string
	ArtifactRefs  []string
	Metadata      map[string]string
}

const durableAgentContinuityMaxItems = 12

const durableAgentConversationMaxItems = 48

func ParseDurableAgentContinuityState(raw string) (DurableAgentContinuityState, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DurableAgentContinuityState{}, nil
	}
	type durableAgentContinuityWire struct {
		RecentInteractions []DurableAgentRecentInteraction          `json:"recent_interactions,omitempty"`
		PendingQuestions   []DurableAgentPendingQuestion            `json:"pending_questions,omitempty"`
		ReviewRefs         []DurableAgentReviewReference            `json:"review_refs,omitempty"`
		RatifiedOutcomes   []DurableAgentRatifiedOutcome            `json:"ratified_outcomes,omitempty"`
		Conversation       *DurableAgentConversationState           `json:"conversation,omitempty"`
		SetupWizard        *DurableAgentSetupWizardState            `json:"setup_wizard,omitempty"`
		EmailPending       *DurableAgentEmailPendingState           `json:"email_pending,omitempty"`
		ExternalChannel    *DurableAgentExternalChannelRuntimeState `json:"external_channel,omitempty"`
		ScheduledReview    *DurableAgentScheduledReviewRuntimeState `json:"scheduled_review,omitempty"`
	}
	var wire durableAgentContinuityWire
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return DurableAgentContinuityState{}, err
	}
	state := DurableAgentContinuityState{
		RecentInteractions: wire.RecentInteractions,
		PendingQuestions:   wire.PendingQuestions,
		ReviewRefs:         wire.ReviewRefs,
		RatifiedOutcomes:   wire.RatifiedOutcomes,
		Conversation:       wire.Conversation,
		SetupWizard:        wire.SetupWizard,
		EmailPending:       wire.EmailPending,
		ExternalChannel:    wire.ExternalChannel,
		ScheduledReview:    wire.ScheduledReview,
	}
	return NormalizeDurableAgentContinuityState(state), nil
}

func NormalizeDurableAgentContinuityState(state DurableAgentContinuityState) DurableAgentContinuityState {
	state.RecentInteractions = normalizeDurableAgentRecentInteractions(state.RecentInteractions)
	state.PendingQuestions = normalizeDurableAgentPendingQuestions(state.PendingQuestions)
	state.ReviewRefs = normalizeDurableAgentReviewReferences(state.ReviewRefs)
	state.RatifiedOutcomes = normalizeDurableAgentRatifiedOutcomes(state.RatifiedOutcomes)
	state.Conversation = normalizeDurableAgentConversationState(state.Conversation)
	state.SetupWizard = normalizeDurableAgentSetupWizardState(state.SetupWizard)
	state.EmailPending = normalizeDurableAgentEmailPendingState(state.EmailPending)
	state.ExternalChannel = normalizeDurableAgentExternalChannelRuntimeState(state.ExternalChannel)
	state.ScheduledReview = normalizeDurableAgentScheduledReviewRuntimeState(state.ScheduledReview)
	return state
}

func (s DurableAgentContinuityState) Marshal() (string, error) {
	s = NormalizeDurableAgentContinuityState(s)
	if s.IsZero() {
		return "", nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (s DurableAgentContinuityState) WithReviewArtifact(reviewEventID int64, artifact DurableReviewArtifact, at time.Time) DurableAgentContinuityState {
	s = NormalizeDurableAgentContinuityState(s)
	if reviewEventID > 0 {
		for _, ref := range s.ReviewRefs {
			if ref.ReviewEventID == reviewEventID {
				return s
			}
		}
	}
	at = at.UTC()
	summary := normalizeDurableAgentText(artifact.Summary)
	source := normalizeDurableAgentText(artifact.Metadata["sender_name"])
	triggerKinds := normalizeDurableAgentCSV(artifact.Metadata["trigger_kinds"])
	if summary != "" {
		s.RecentInteractions = prependDurableAgentRecentInteraction(s.RecentInteractions, DurableAgentRecentInteraction{
			Summary:       summary,
			Source:        source,
			TriggerKinds:  triggerKinds,
			ReviewEventID: reviewEventID,
			OccurredAt:    at,
		})
	}
	for _, question := range artifact.Questions {
		question = normalizeDurableAgentText(question)
		if question == "" {
			continue
		}
		s.PendingQuestions = upsertDurableAgentPendingQuestion(s.PendingQuestions, DurableAgentPendingQuestion{
			Question:      question,
			ReviewEventID: reviewEventID,
			Status:        "pending_review",
			CreatedAt:     at,
			UpdatedAt:     at,
		})
	}
	if reviewEventID > 0 {
		s.ReviewRefs = prependDurableAgentReviewReference(s.ReviewRefs, DurableAgentReviewReference{
			ReviewEventID: reviewEventID,
			Summary:       summary,
			RiskFlags:     normalizeDurableAgentStringSet(artifact.RiskFlags),
			Status:        "pending",
			CreatedAt:     at,
		})
	}
	return NormalizeDurableAgentContinuityState(s)
}

func (s DurableAgentContinuityState) WithRatifiedOutcome(summary string, policyVersion int64, policyHash string, sourceReviewEventID int64, appliedAt time.Time) DurableAgentContinuityState {
	s = NormalizeDurableAgentContinuityState(s)
	if policyVersion <= 0 || strings.TrimSpace(policyHash) == "" {
		return s
	}
	s.RatifiedOutcomes = prependDurableAgentRatifiedOutcome(s.RatifiedOutcomes, DurableAgentRatifiedOutcome{
		Summary:             normalizeDurableAgentText(summary),
		PolicyVersion:       policyVersion,
		PolicyHash:          strings.TrimSpace(policyHash),
		SourceReviewEventID: sourceReviewEventID,
		AppliedAt:           appliedAt.UTC(),
	})
	return NormalizeDurableAgentContinuityState(s)
}

func (s DurableAgentContinuityState) WithConversationMessage(role string, text string, createdAt time.Time) DurableAgentContinuityState {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return s.WithConversationMessages(DurableAgentConversationMessage{
		Role:      role,
		Text:      text,
		CreatedAt: createdAt,
	})
}

func (s DurableAgentContinuityState) WithConversationMessages(messages ...DurableAgentConversationMessage) DurableAgentContinuityState {
	s = NormalizeDurableAgentContinuityState(s)
	next := normalizeDurableAgentConversationMessages(messages)
	if len(next) == 0 {
		return s
	}
	if s.Conversation == nil {
		s.Conversation = &DurableAgentConversationState{}
	}
	s.Conversation.Messages = append(next, s.Conversation.Messages...)
	return NormalizeDurableAgentContinuityState(s)
}

func (s DurableAgentContinuityState) PendingParentConversationMessages(limit int) []DurableAgentConversationMessage {
	s = NormalizeDurableAgentContinuityState(s)
	if s.Conversation == nil || len(s.Conversation.Messages) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = durableAgentConversationMaxItems
	}
	out := make([]DurableAgentConversationMessage, 0, limit)
	for _, message := range s.Conversation.Messages {
		if message.Role != "parent" {
			continue
		}
		if !message.AcknowledgedAt.IsZero() {
			continue
		}
		out = append(out, message)
		if len(out) == limit {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func DurableAgentConversationMessageIDs(messages []DurableAgentConversationMessage) []string {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, 0, len(messages))
	for _, message := range normalizeDurableAgentConversationMessages(messages) {
		if message.MessageID == "" {
			continue
		}
		ids = append(ids, message.MessageID)
	}
	return normalizeDurableAgentStringSet(ids)
}

func (s DurableAgentContinuityState) AcknowledgeParentConversationMessageIDs(messageIDs []string, at time.Time) (DurableAgentContinuityState, error) {
	s = NormalizeDurableAgentContinuityState(s)
	messageIDs = normalizeDurableAgentStringSet(messageIDs)
	if len(messageIDs) == 0 {
		return s, fmt.Errorf("durable agent parent conversation acknowledgement must include message_ids")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	byID := map[string]int{}
	if s.Conversation != nil {
		byID = make(map[string]int, len(s.Conversation.Messages))
		for i, message := range s.Conversation.Messages {
			if message.MessageID == "" {
				continue
			}
			byID[message.MessageID] = i
		}
	}
	for _, messageID := range messageIDs {
		idx, ok := byID[messageID]
		if !ok {
			return s, fmt.Errorf("durable agent parent conversation acknowledgement references unknown message_id %q", messageID)
		}
		if s.Conversation.Messages[idx].Role != "parent" {
			return s, fmt.Errorf("durable agent parent conversation acknowledgement references non-parent message_id %q", messageID)
		}
	}
	updated := false
	for _, messageID := range messageIDs {
		message := &s.Conversation.Messages[byID[messageID]]
		if !message.AcknowledgedAt.IsZero() {
			continue
		}
		message.AcknowledgedAt = at.UTC()
		updated = true
	}
	if !updated {
		return s, nil
	}
	return NormalizeDurableAgentContinuityState(s), nil
}

func normalizeDurableAgentRecentInteractions(values []DurableAgentRecentInteraction) []DurableAgentRecentInteraction {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentRecentInteraction, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Summary = normalizeDurableAgentText(value.Summary)
		value.Source = normalizeDurableAgentText(value.Source)
		value.TriggerKinds = normalizeDurableAgentStringSet(value.TriggerKinds)
		if value.Summary == "" {
			continue
		}
		key := durableAgentInteractionKey(value.ReviewEventID, value.Summary)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentContinuityMaxItems {
			break
		}
	}
	return out
}

func normalizeDurableAgentPendingQuestions(values []DurableAgentPendingQuestion) []DurableAgentPendingQuestion {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentPendingQuestion, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Question = normalizeDurableAgentText(value.Question)
		value.Status = normalizeDurableAgentPendingQuestionStatus(value.Status)
		if value.Question == "" {
			continue
		}
		key := durableAgentPendingQuestionKey(value.ReviewEventID, value.Question)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentContinuityMaxItems {
			break
		}
	}
	return out
}

func normalizeDurableAgentReviewReferences(values []DurableAgentReviewReference) []DurableAgentReviewReference {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentReviewReference, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		value.Summary = normalizeDurableAgentText(value.Summary)
		value.RiskFlags = normalizeDurableAgentStringSet(value.RiskFlags)
		value.Status = normalizeDurableAgentReviewStatus(value.Status)
		if value.ReviewEventID <= 0 {
			continue
		}
		if _, ok := seen[value.ReviewEventID]; ok {
			continue
		}
		seen[value.ReviewEventID] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentContinuityMaxItems {
			break
		}
	}
	return out
}

func normalizeDurableAgentRatifiedOutcomes(values []DurableAgentRatifiedOutcome) []DurableAgentRatifiedOutcome {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentRatifiedOutcome, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Summary = normalizeDurableAgentText(value.Summary)
		value.PolicyHash = strings.TrimSpace(value.PolicyHash)
		if value.PolicyVersion <= 0 || value.PolicyHash == "" {
			continue
		}
		key := fmt.Sprintf("%d:%s", value.PolicyVersion, value.PolicyHash)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentContinuityMaxItems {
			break
		}
	}
	return out
}

func normalizeDurableAgentConversationState(state *DurableAgentConversationState) *DurableAgentConversationState {
	if state == nil {
		return nil
	}
	normalized := DurableAgentConversationState{
		Messages: normalizeDurableAgentConversationMessages(state.Messages),
	}
	if len(normalized.Messages) == 0 {
		return nil
	}
	return &normalized
}

func normalizeDurableAgentConversationMessages(values []DurableAgentConversationMessage) []DurableAgentConversationMessage {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentConversationMessage, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Role = normalizeDurableAgentConversationRole(value.Role)
		value.Text = clampDurableAgentField(value.Text, 1200)
		value.CreatedAt = value.CreatedAt.UTC()
		value.AcknowledgedAt = value.AcknowledgedAt.UTC()
		if value.Role == "" || value.Text == "" {
			continue
		}
		value.MessageID = strings.TrimSpace(value.MessageID)
		if value.MessageID == "" {
			value.MessageID = durableAgentConversationMessageID(value.Role, value.Text, value.CreatedAt)
		}
		if value.Role != "parent" {
			value.AcknowledgedAt = time.Time{}
		}
		key := value.MessageID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentConversationMaxItems {
			break
		}
	}
	return out
}

func durableAgentConversationMessageID(role string, text string, createdAt time.Time) string {
	role = normalizeDurableAgentConversationRole(role)
	text = clampDurableAgentField(text, 1200)
	if role == "" || text == "" {
		return ""
	}
	created := "zero"
	if !createdAt.IsZero() {
		created = createdAt.UTC().Format(time.RFC3339Nano)
	}
	sum := sha256.Sum256([]byte(role + "\x00" + text + "\x00" + created))
	return "dcm_" + hex.EncodeToString(sum[:16])
}

func normalizeDurableAgentConversationRole(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "parent":
		return "parent"
	case "child":
		return "child"
	case "system":
		return "system"
	default:
		return ""
	}
}

func prependDurableAgentRecentInteraction(values []DurableAgentRecentInteraction, next DurableAgentRecentInteraction) []DurableAgentRecentInteraction {
	return append([]DurableAgentRecentInteraction{next}, values...)
}

func prependDurableAgentReviewReference(values []DurableAgentReviewReference, next DurableAgentReviewReference) []DurableAgentReviewReference {
	return append([]DurableAgentReviewReference{next}, values...)
}

func prependDurableAgentRatifiedOutcome(values []DurableAgentRatifiedOutcome, next DurableAgentRatifiedOutcome) []DurableAgentRatifiedOutcome {
	return append([]DurableAgentRatifiedOutcome{next}, values...)
}

func upsertDurableAgentPendingQuestion(values []DurableAgentPendingQuestion, next DurableAgentPendingQuestion) []DurableAgentPendingQuestion {
	key := durableAgentPendingQuestionKey(next.ReviewEventID, next.Question)
	out := make([]DurableAgentPendingQuestion, 0, len(values)+1)
	out = append(out, next)
	for _, existing := range values {
		if durableAgentPendingQuestionKey(existing.ReviewEventID, existing.Question) == key {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func normalizeDurableAgentPendingQuestionStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "answered", "dismissed", "ratified":
		return strings.TrimSpace(value)
	default:
		return "pending_review"
	}
}

func normalizeDurableAgentReviewStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "delivered", "dismissed":
		return strings.TrimSpace(value)
	default:
		return "pending"
	}
}

func durableAgentInteractionKey(reviewEventID int64, summary string) string {
	if reviewEventID > 0 {
		return fmt.Sprintf("review:%d", reviewEventID)
	}
	return normalizeDurableAgentText(summary)
}

func durableAgentPendingQuestionKey(reviewEventID int64, question string) string {
	if reviewEventID > 0 {
		return fmt.Sprintf("%d:%s", reviewEventID, normalizeDurableAgentText(question))
	}
	return normalizeDurableAgentText(question)
}
