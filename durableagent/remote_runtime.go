//go:build linux

package durableagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type RemoteControlClient interface {
	Enroll(ctx context.Context) (core.DurableAgentEnrollmentResponse, error)
	Reattest(ctx context.Context) (core.DurableAgentEnrollmentResponse, error)
	PollPolicy(ctx context.Context, knownVersion int64, knownHash string) (core.DurableAgentPolicyPollResponse, error)
	UploadReviewArtifact(ctx context.Context, artifact core.DurableReviewArtifact) (core.DurableAgentReviewArtifactUploadResponse, error)
	AcknowledgePolicy(ctx context.Context, ack core.DurableAgentPolicyAcknowledgement) (core.DurableAgentPolicyAcknowledgementResponse, error)
}

type RemoteClientFactory func(bootstrap core.DurableAgentRemoteBootstrap) (RemoteControlClient, error)

type RemoteRuntimeStore interface {
	DurableAgent(agentID string) (*core.DurableAgent, error)
	UpsertDurableAgent(agent core.DurableAgent) error
	DurableAgentState(agentID string) (*core.DurableAgentState, error)
	SaveDurableAgentState(state core.DurableAgentState) error
	UpsertDurableAgentRemoteEnrollment(enrollment core.DurableAgentRemoteEnrollment) error
	DurableAgentRemoteEnrollment(agentID string) (*core.DurableAgentRemoteEnrollment, error)
}

type RemoteSyncResult struct {
	Enrolled      bool
	PolicyChanged bool
	PolicyVersion int64
}

type RemoteUploadResult struct {
	ReviewEventID int64
}

type RemoteRuntime struct {
	store     RemoteRuntimeStore
	newClient RemoteClientFactory
	clock     func() time.Time
}

func NewRemoteRuntime(store RemoteRuntimeStore, factory RemoteClientFactory) *RemoteRuntime {
	if factory == nil {
		factory = func(bootstrap core.DurableAgentRemoteBootstrap) (RemoteControlClient, error) {
			return NewHTTPClient(bootstrap)
		}
	}
	return &RemoteRuntime{
		store:     store,
		newClient: factory,
		clock:     func() time.Time { return time.Now().UTC() },
	}
}

func (r *RemoteRuntime) Sync(ctx context.Context, bootstrapPath string) (*RemoteSyncResult, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("durable agent remote runtime store is nil")
	}
	bootstrap, err := ReadRemoteBootstrap(bootstrapPath)
	if err != nil {
		return nil, err
	}
	client, err := r.newClient(bootstrap)
	if err != nil {
		return nil, err
	}

	enrollment, err := r.store.DurableAgentRemoteEnrollment(bootstrap.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		enrollment = nil
	}
	seedRemoteClientSequence(client, enrollment)

	if enrollment == nil {
		resp, err := client.Enroll(ctx)
		if err != nil {
			return nil, err
		}
		enrollment = &resp.Enrollment
		if err := r.applySnapshot(ctx, client, bootstrap, resp.Policy); err != nil {
			return nil, err
		}
		if err := r.persistRemoteEnrollment(*enrollment, client); err != nil {
			return nil, err
		}
		return &RemoteSyncResult{
			Enrolled:      true,
			PolicyChanged: true,
			PolicyVersion: resp.Policy.PolicyVersion,
		}, nil
	}

	if remoteEnrollmentNeedsReattestation(*enrollment, bootstrap) {
		resp, err := client.Reattest(ctx)
		if err != nil {
			return nil, err
		}
		enrollment = &resp.Enrollment
		if err := r.persistRemoteEnrollment(*enrollment, client); err != nil {
			return nil, err
		}
	}

	localAgent, err := r.store.DurableAgent(bootstrap.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		localAgent = nil
	}
	knownVersion := int64(0)
	knownHash := ""
	if localAgent != nil {
		knownVersion = localAgent.PolicyVersion
		knownHash = strings.TrimSpace(localAgent.PolicyHash)
	}

	pollResp, err := client.PollPolicy(ctx, knownVersion, knownHash)
	if err != nil {
		return nil, err
	}
	if err := r.persistRemoteEnrollment(*enrollment, client); err != nil {
		return nil, err
	}
	if !pollResp.Changed && localAgent != nil {
		return &RemoteSyncResult{
			Enrolled:      false,
			PolicyChanged: false,
			PolicyVersion: localAgent.PolicyVersion,
		}, nil
	}
	if err := r.applySnapshot(ctx, client, bootstrap, pollResp.Snapshot); err != nil {
		return nil, err
	}
	if err := r.persistRemoteEnrollment(*enrollment, client); err != nil {
		return nil, err
	}
	return &RemoteSyncResult{
		Enrolled:      false,
		PolicyChanged: true,
		PolicyVersion: pollResp.Snapshot.PolicyVersion,
	}, nil
}

func (r *RemoteRuntime) UploadReviewArtifact(ctx context.Context, bootstrapPath string, artifact core.DurableReviewArtifact) (*RemoteUploadResult, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("durable agent remote runtime store is nil")
	}
	bootstrap, err := ReadRemoteBootstrap(bootstrapPath)
	if err != nil {
		return nil, err
	}
	enrollment, err := r.store.DurableAgentRemoteEnrollment(bootstrap.AgentID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if _, err := r.Sync(ctx, bootstrapPath); err != nil {
			return nil, err
		}
		enrollment, err = r.store.DurableAgentRemoteEnrollment(bootstrap.AgentID)
		if err != nil {
			return nil, err
		}
	}

	client, err := r.newClient(bootstrap)
	if err != nil {
		return nil, err
	}
	seedRemoteClientSequence(client, enrollment)

	agent, state, err := r.localDurableAgentState(bootstrap)
	if err != nil {
		return nil, err
	}
	artifact.AgentID = firstNonEmpty(strings.TrimSpace(artifact.AgentID), bootstrap.AgentID)
	artifact, err = PrepareReviewArtifact(agent, artifact)
	if err != nil {
		return nil, err
	}
	resp, err := client.UploadReviewArtifact(ctx, artifact)
	if err != nil {
		return nil, err
	}
	if !resp.Accepted {
		return nil, fmt.Errorf("durable agent remote review artifact upload was not accepted")
	}
	if err := r.persistRemoteEnrollment(*enrollment, client); err != nil {
		return nil, err
	}
	now := r.now()
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return nil, fmt.Errorf("parse durable agent continuity state: %w", err)
	}
	continuity = continuity.WithReviewArtifact(resp.ReviewEventID, artifact, now)
	stateJSON, err := continuity.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal durable agent continuity state: %w", err)
	}
	state.StateJSON = stateJSON
	state.LastReviewAt = now
	state.Status = "active"
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return nil, err
	}
	return &RemoteUploadResult{ReviewEventID: resp.ReviewEventID}, nil
}

func (r *RemoteRuntime) applySnapshot(ctx context.Context, client RemoteControlClient, bootstrap core.DurableAgentRemoteBootstrap, snapshot core.DurableAgentPolicySnapshot) error {
	now := r.now()
	agent, state, err := r.localDurableAgentState(bootstrap)
	if err != nil {
		return err
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(snapshot.LivePolicy)
	agent.PolicyVersion = snapshot.PolicyVersion
	agent.PolicyHash = strings.TrimSpace(snapshot.PolicyHash)
	agent.PolicyIssuedAt = snapshot.IssuedAt.UTC()
	state.LastOfferedPolicyVersion = snapshot.PolicyVersion
	state.LastOfferedPolicyHash = strings.TrimSpace(snapshot.PolicyHash)
	state.LastOfferedPolicyAt = nonZeroRemotePolicyTime(snapshot.IssuedAt, now)

	ack := core.DurableAgentPolicyAcknowledgement{
		AgentID:             bootstrap.AgentID,
		AcknowledgedVersion: snapshot.PolicyVersion,
		AcknowledgedHash:    strings.TrimSpace(snapshot.PolicyHash),
		AcknowledgedAt:      now,
	}

	if err := r.store.UpsertDurableAgent(agent); err != nil {
		state.LastApplyStatus = "failed"
		state.LastApplyError = strings.TrimSpace(err.Error())
		if saveErr := r.store.SaveDurableAgentState(*state); saveErr != nil {
			return fmt.Errorf("save failed durable agent apply state: %w", saveErr)
		}
		ack.Status = "failed"
		ack.Error = state.LastApplyError
		if _, ackErr := client.AcknowledgePolicy(ctx, ack); ackErr != nil {
			return fmt.Errorf("acknowledge failed durable agent apply: %w", ackErr)
		}
		return err
	}

	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return fmt.Errorf("parse durable agent continuity state: %w", err)
	}
	summary := "Applied parent durable-agent policy snapshot."
	if snapshot.PolicyVersion > 0 && snapshot.PolicyVersion != state.LastAppliedPolicyVersion {
		continuity = continuity.WithRatifiedOutcome(summary, snapshot.PolicyVersion, snapshot.PolicyHash, 0, now)
	}
	stateJSON, err := continuity.Marshal()
	if err != nil {
		return fmt.Errorf("marshal durable agent continuity state: %w", err)
	}
	state.StateJSON = stateJSON
	state.LastAppliedPolicyVersion = snapshot.PolicyVersion
	state.LastAppliedPolicyHash = strings.TrimSpace(snapshot.PolicyHash)
	state.LastAppliedPolicyAt = now
	state.LastApplyStatus = "applied"
	state.LastApplyError = ""
	state.Status = "active"
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	ack.AppliedVersion = snapshot.PolicyVersion
	ack.AppliedHash = strings.TrimSpace(snapshot.PolicyHash)
	ack.Status = "applied"
	if _, err := client.AcknowledgePolicy(ctx, ack); err != nil {
		return err
	}

	state.LastAcknowledgedPolicyVersion = snapshot.PolicyVersion
	state.LastAcknowledgedPolicyHash = strings.TrimSpace(snapshot.PolicyHash)
	state.LastAcknowledgedPolicyAt = now
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	return nil
}

func (r *RemoteRuntime) localDurableAgentState(bootstrap core.DurableAgentRemoteBootstrap) (core.DurableAgent, *core.DurableAgentState, error) {
	agent, err := r.store.DurableAgent(bootstrap.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return core.DurableAgent{}, nil, err
	}
	if errors.Is(err, sql.ErrNoRows) || agent == nil {
		agent = &core.DurableAgent{
			ReviewTargetChatID: bootstrap.ReviewTargetChatID,
			AgentID:            bootstrap.AgentID,
			ParentAgentID:      bootstrap.ParentAgentID,
			ChannelKind:        bootstrap.ChannelKind,
			BootstrapCeiling:   bootstrap.BootstrapCeiling,
			BootstrapLLM:       bootstrap.BootstrapLLM,
			LocalStorageRoots:  append([]string(nil), bootstrap.LocalStorageRoots...),
			SecretScopes:       append([]string(nil), bootstrap.SecretScopes...),
			NetworkPolicy:      bootstrap.NetworkPolicy,
			WakeupMode:         "remote_control_plane",
			Status:             "active",
		}
	}

	state, err := r.store.DurableAgentState(bootstrap.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return core.DurableAgent{}, nil, err
	}
	if errors.Is(err, sql.ErrNoRows) || state == nil {
		state = &core.DurableAgentState{AgentID: bootstrap.AgentID}
	}
	return *agent, state, nil
}

func (r *RemoteRuntime) persistRemoteEnrollment(enrollment core.DurableAgentRemoteEnrollment, client RemoteControlClient) error {
	enrollment = core.NormalizeDurableAgentRemoteEnrollment(enrollment)
	enrollment.LastSequence = remoteClientSequence(client)
	enrollment.LastSeenAt = r.now()
	if enrollment.Status == "" {
		enrollment.Status = "active"
	}
	return r.store.UpsertDurableAgentRemoteEnrollment(enrollment)
}

func (r *RemoteRuntime) now() time.Time {
	if r != nil && r.clock != nil {
		return r.clock().UTC()
	}
	return time.Now().UTC()
}

func seedRemoteClientSequence(client RemoteControlClient, enrollment *core.DurableAgentRemoteEnrollment) {
	httpClient, ok := client.(*HTTPClient)
	if !ok || enrollment == nil {
		return
	}
	httpClient.mu.Lock()
	defer httpClient.mu.Unlock()
	httpClient.sequence = enrollment.LastSequence
}

func remoteClientSequence(client RemoteControlClient) int64 {
	httpClient, ok := client.(*HTTPClient)
	if !ok {
		return 0
	}
	httpClient.mu.Lock()
	defer httpClient.mu.Unlock()
	return httpClient.sequence
}

func nonZeroRemotePolicyTime(value time.Time, fallback time.Time) time.Time {
	if !value.IsZero() {
		return value.UTC()
	}
	return fallback.UTC()
}

func remoteEnrollmentNeedsReattestation(enrollment core.DurableAgentRemoteEnrollment, bootstrap core.DurableAgentRemoteBootstrap) bool {
	enrollment = core.NormalizeDurableAgentRemoteEnrollment(enrollment)
	bootstrap = core.NormalizeDurableAgentRemoteBootstrap(bootstrap)
	return strings.TrimSpace(enrollment.ParentControlURL) != strings.TrimSpace(bootstrap.ParentControlURL) ||
		strings.TrimSpace(enrollment.KeyFingerprint) != strings.TrimSpace(bootstrap.KeyFingerprint) ||
		strings.TrimSpace(enrollment.ProtocolVersion) != strings.TrimSpace(bootstrap.ProtocolVersion)
}
