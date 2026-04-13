//go:build linux

package durableagent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const defaultReviewSummaryMaxChars = 700

type Store interface {
	EnqueueReviewEvent(event session.ReviewEvent) error
	SaveDurableAgentState(state core.DurableAgentState) error
	DurableAgentState(agentID string) (*core.DurableAgentState, error)
}

type Runtime struct {
	store Store
}

func NewRuntime(store Store) *Runtime {
	return &Runtime{store: store}
}

func (r *Runtime) QueueReviewArtifact(agent core.DurableAgent, artifact core.DurableReviewArtifact) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("durable agent runtime store is nil")
	}
	agent.AgentID = strings.TrimSpace(agent.AgentID)
	if agent.AgentID == "" {
		return fmt.Errorf("queue durable review artifact: agent_id is required")
	}
	if agent.ReviewTargetChatID == 0 {
		return fmt.Errorf("queue durable review artifact: review_target_chat_id is required")
	}

	artifact.AgentID = firstNonEmpty(strings.TrimSpace(artifact.AgentID), agent.AgentID)
	artifact, err := PrepareReviewArtifact(agent, artifact)
	if err != nil {
		return fmt.Errorf("prepare durable review artifact: %w", err)
	}
	summary := buildReviewSummary(agent, artifact, defaultReviewSummaryMaxChars)
	metadataJSON, err := marshalArtifactMetadata(artifact)
	if err != nil {
		return fmt.Errorf("queue durable review artifact metadata: %w", err)
	}

	event := session.ReviewEvent{
		SourceRole:        "durable_agent",
		SourceScope:       sourceScope(agent),
		TargetAdminChatID: agent.ReviewTargetChatID,
		TargetScope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   strconv.FormatInt(agent.ReviewTargetChatID, 10),
		},
		Summary:      summary,
		MetadataJSON: metadataJSON,
	}
	if err := r.store.EnqueueReviewEvent(event); err != nil {
		return err
	}

	state, err := r.store.DurableAgentState(agent.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: agent.AgentID}
	}
	state.LastReviewAt = time.Now().UTC()
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	return nil
}

func sourceScope(agent core.DurableAgent) session.ScopeRef {
	return session.NormalizeScopeRef(session.ScopeRef{
		Kind:            session.ScopeKindDurableAgent,
		ID:              agent.AgentID,
		DurableAgentID:  agent.AgentID,
		ParentScopeKind: session.ScopeKind(strings.TrimSpace(agent.ParentScopeKind)),
		ParentScopeID:   strings.TrimSpace(agent.ParentScopeID),
	})
}

func buildReviewSummary(agent core.DurableAgent, artifact core.DurableReviewArtifact, maxChars int) string {
	parts := []string{
		fmt.Sprintf("durable_agent=%s", strings.TrimSpace(agent.AgentID)),
	}
	if channel := strings.TrimSpace(agent.ChannelKind); channel != "" {
		parts = append(parts, "channel="+channel)
	}
	if parent := strings.TrimSpace(agent.ParentScopeKind); parent != "" || strings.TrimSpace(agent.ParentScopeID) != "" {
		parentRef := session.NormalizeScopeRef(session.ScopeRef{
			Kind: session.ScopeKind(parent),
			ID:   strings.TrimSpace(agent.ParentScopeID),
		})
		if !parentRef.IsZero() {
			parts = append(parts, "parent="+parentRef.String())
		}
	}
	if interval := strings.TrimSpace(artifact.IntervalLabel); interval != "" {
		parts = append(parts, "interval="+interval)
	}

	lines := []string{strings.Join(parts, " ")}
	if summary := normalizeWhitespace(artifact.Summary); summary != "" {
		lines = append(lines, "summary: "+summary)
	}
	if len(artifact.LocalActions) > 0 {
		lines = append(lines, "local: "+normalizeWhitespace(strings.Join(artifact.LocalActions, "; ")))
	}
	if len(artifact.Questions) > 0 {
		lines = append(lines, "questions: "+normalizeWhitespace(strings.Join(artifact.Questions, "; ")))
	}
	if len(artifact.RiskFlags) > 0 {
		lines = append(lines, "risks: "+normalizeWhitespace(strings.Join(artifact.RiskFlags, "; ")))
	}
	return clampChars(strings.Join(lines, "\n"), maxChars)
}

func marshalArtifactMetadata(artifact core.DurableReviewArtifact) (string, error) {
	payload := struct {
		AgentID       string            `json:"agent_id,omitempty"`
		IntervalLabel string            `json:"interval_label,omitempty"`
		LocalActions  []string          `json:"local_actions,omitempty"`
		Questions     []string          `json:"questions,omitempty"`
		RiskFlags     []string          `json:"risk_flags,omitempty"`
		ArtifactRefs  []string          `json:"artifact_refs,omitempty"`
		Metadata      map[string]string `json:"metadata,omitempty"`
	}{
		AgentID:       strings.TrimSpace(artifact.AgentID),
		IntervalLabel: strings.TrimSpace(artifact.IntervalLabel),
		LocalActions:  cloneStrings(artifact.LocalActions),
		Questions:     cloneStrings(artifact.Questions),
		RiskFlags:     cloneStrings(artifact.RiskFlags),
		ArtifactRefs:  cloneStrings(artifact.ArtifactRefs),
		Metadata:      artifact.Metadata,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func clampChars(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit <= 3 {
		return string(r[:limit])
	}
	return string(r[:limit-3]) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
