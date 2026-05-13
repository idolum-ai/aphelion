//go:build linux

package durableagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const defaultRemoteChildPendingReviewLimit = 20

type RemoteChildStore interface {
	RemoteRuntimeStore
	PendingReviewEvents(targetChatID int64, limit int) ([]session.ReviewEvent, error)
	MarkReviewDelivered(ids []int64) error
}

type RemoteChildExecutor interface {
	Run(ctx context.Context, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, msg core.InboundMessage) error
}

type RemoteChildExecutorFunc func(ctx context.Context, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, msg core.InboundMessage) error

func (fn RemoteChildExecutorFunc) Run(ctx context.Context, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, msg core.InboundMessage) error {
	return fn(ctx, bootstrap, agent, msg)
}

type RemoteChildRunResult struct {
	Sync                    RemoteSyncResult
	UploadedReviewArtifacts int
	AcknowledgedParent      bool
}

type RemoteChildRunner struct {
	store    RemoteChildStore
	remote   *RemoteRuntime
	executor RemoteChildExecutor
}

func NewRemoteChildRunner(store RemoteChildStore, remote *RemoteRuntime, executor RemoteChildExecutor) *RemoteChildRunner {
	return &RemoteChildRunner{store: store, remote: remote, executor: executor}
}

func (r *RemoteChildRunner) RunOnce(ctx context.Context, bootstrapPath string, msg core.InboundMessage) (*RemoteChildRunResult, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("durable agent remote child store is nil")
	}
	if r.remote == nil {
		return nil, fmt.Errorf("durable agent remote runtime is nil")
	}
	if r.executor == nil {
		return nil, fmt.Errorf("durable agent remote child executor is nil")
	}

	syncResult, err := r.remote.Sync(ctx, bootstrapPath)
	if err != nil {
		return nil, err
	}
	bootstrap, err := ReadRemoteBootstrap(bootstrapPath)
	if err != nil {
		return nil, err
	}
	agent, err := r.store.DurableAgent(bootstrap.AgentID)
	if err != nil {
		return nil, err
	}
	if err := r.executor.Run(ctx, bootstrap, *agent, msg); err != nil {
		return nil, err
	}

	uploaded, err := r.uploadPendingReviewArtifacts(ctx, bootstrapPath, *agent)
	if err != nil {
		return nil, err
	}
	acked, err := r.acknowledgeParentConversationIfNeeded(ctx, bootstrapPath, syncResult.ParentConversationMessageIDs)
	if err != nil {
		return nil, err
	}
	return &RemoteChildRunResult{
		Sync:                    *syncResult,
		UploadedReviewArtifacts: uploaded,
		AcknowledgedParent:      acked,
	}, nil
}

func (r *RemoteChildRunner) RunParentConversation(ctx context.Context, bootstrapPath string) (*RemoteChildRunResult, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("durable agent remote child store is nil")
	}
	if r.remote == nil {
		return nil, fmt.Errorf("durable agent remote runtime is nil")
	}
	if r.executor == nil {
		return nil, fmt.Errorf("durable agent remote child executor is nil")
	}
	syncResult, err := r.remote.Sync(ctx, bootstrapPath)
	if err != nil {
		return nil, err
	}
	result := &RemoteChildRunResult{Sync: *syncResult}
	if len(syncResult.ParentConversationMessageIDs) == 0 {
		return result, nil
	}
	bootstrap, err := ReadRemoteBootstrap(bootstrapPath)
	if err != nil {
		return nil, err
	}
	agent, err := r.store.DurableAgent(bootstrap.AgentID)
	if err != nil {
		return nil, err
	}
	msg := core.InboundMessage{
		ChatType:       "durable_parent_conversation",
		SenderName:     "parent",
		Text:           "Durable parent conversation wake.",
		DurableAgentID: agent.AgentID,
		Timestamp:      time.Now().UTC(),
	}
	if err := r.executor.Run(ctx, bootstrap, *agent, msg); err != nil {
		return nil, err
	}
	uploaded, err := r.uploadPendingReviewArtifacts(ctx, bootstrapPath, *agent)
	if err != nil {
		return nil, err
	}
	acked, err := r.acknowledgeParentConversationIfNeeded(ctx, bootstrapPath, syncResult.ParentConversationMessageIDs)
	if err != nil {
		return nil, err
	}
	result.UploadedReviewArtifacts = uploaded
	result.AcknowledgedParent = acked
	return result, nil
}

func (r *RemoteChildRunner) acknowledgeParentConversationIfNeeded(ctx context.Context, bootstrapPath string, messageIDs []string) (bool, error) {
	if len(messageIDs) == 0 {
		return false, nil
	}
	if err := r.remote.AcknowledgeParentConversation(ctx, bootstrapPath, messageIDs); err != nil {
		return false, err
	}
	return true, nil
}

func (r *RemoteChildRunner) uploadPendingReviewArtifacts(ctx context.Context, bootstrapPath string, agent core.DurableAgent) (int, error) {
	events, err := r.store.PendingReviewEvents(agent.ReviewTargetChatID, defaultRemoteChildPendingReviewLimit)
	if err != nil {
		return 0, err
	}
	delivered := make([]int64, 0, len(events))
	uploaded := 0
	for _, event := range events {
		if event.SourceScope.Kind != session.ScopeKindDurableAgent || strings.TrimSpace(event.SourceScope.DurableAgentID) != strings.TrimSpace(agent.AgentID) {
			continue
		}
		artifact, err := durableReviewArtifactFromEvent(event)
		if err != nil {
			return uploaded, err
		}
		if _, err := r.remote.UploadReviewArtifact(ctx, bootstrapPath, artifact); err != nil {
			return uploaded, err
		}
		delivered = append(delivered, event.ID)
		uploaded++
	}
	if len(delivered) > 0 {
		if err := r.store.MarkReviewDelivered(delivered); err != nil {
			return uploaded, err
		}
	}
	return uploaded, nil
}

func durableReviewArtifactFromEvent(event session.ReviewEvent) (core.DurableReviewArtifact, error) {
	var payload struct {
		AgentID       string            `json:"agent_id,omitempty"`
		Summary       string            `json:"summary,omitempty"`
		IntervalLabel string            `json:"interval_label,omitempty"`
		LocalActions  []string          `json:"local_actions,omitempty"`
		Questions     []string          `json:"questions,omitempty"`
		RiskFlags     []string          `json:"risk_flags,omitempty"`
		ArtifactRefs  []string          `json:"artifact_refs,omitempty"`
		Metadata      map[string]string `json:"metadata,omitempty"`
	}
	raw := strings.TrimSpace(event.MetadataJSON)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return core.DurableReviewArtifact{}, fmt.Errorf("decode durable review event metadata: %w", err)
		}
	}
	summary := strings.TrimSpace(payload.Summary)
	if summary == "" {
		summary = extractDurableReviewSummary(event.Summary)
	}
	return core.DurableReviewArtifact{
		AgentID:       firstNonEmpty(strings.TrimSpace(payload.AgentID), strings.TrimSpace(event.SourceScope.DurableAgentID)),
		Summary:       summary,
		IntervalLabel: strings.TrimSpace(payload.IntervalLabel),
		LocalActions:  cloneStrings(payload.LocalActions),
		Questions:     cloneStrings(payload.Questions),
		RiskFlags:     cloneStrings(payload.RiskFlags),
		ArtifactRefs:  cloneStrings(payload.ArtifactRefs),
		Metadata:      cloneStringMap(payload.Metadata),
	}, nil
}

func extractDurableReviewSummary(summary string) string {
	for _, line := range strings.Split(summary, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "summary:") {
			return strings.TrimSpace(strings.TrimSpace(line[len("summary:"):]))
		}
	}
	return strings.TrimSpace(summary)
}
