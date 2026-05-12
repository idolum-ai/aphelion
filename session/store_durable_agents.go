//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (s *SQLiteStore) UpsertDurableAgent(agent core.DurableAgent) error {
	_, err := upsertDurableAgentExec(s.db, agent)
	return err
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type sqlQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func upsertDurableAgentExec(exec sqlExecer, agent core.DurableAgent) (core.DurableAgent, error) {
	agent.AgentID = strings.TrimSpace(agent.AgentID)
	if agent.AgentID == "" {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent: agent_id is required")
	}
	if err := core.ValidateDurableAgentID(agent.AgentID); err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent: %w", err)
	}
	agent.ChannelKind = strings.TrimSpace(agent.ChannelKind)
	if agent.ChannelKind == "" {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent: channel_kind is required")
	}
	agent.ChannelConfig = core.NormalizeDurableAgentChannelConfig(agent.ChannelConfig)
	if strings.TrimSpace(agent.Status) == "" {
		agent.Status = "active"
	}
	if agent.BootstrapCeiling.IsZero() {
		agent.BootstrapCeiling = core.DefaultDurableAgentBootstrapCeiling(agent.ChannelKind, agent.LivePolicy)
	}
	if err := validateDurableAgentChannelConfig(agent.ChannelKind, agent.ChannelConfig); err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent channel_config: %w", err)
	}
	requiresBootstrap := strings.TrimSpace(agent.Status) != "draft" && strings.TrimSpace(agent.ChannelKind) != "external_channel"
	if requiresBootstrap {
		if err := core.ValidateNodeLLMBootstrap(agent.BootstrapLLM); err != nil {
			return core.DurableAgent{}, fmt.Errorf("upsert durable agent bootstrap_llm: %w", err)
		}
	}
	agent.BootstrapCeiling = core.NormalizeDurableAgentBootstrapCeiling(agent.BootstrapCeiling)
	agent.BootstrapLLM = core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	if err := core.ValidateDurableAgentLivePolicyWithinCeiling(agent.LivePolicy, agent.BootstrapCeiling); err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent live_policy: %w", err)
	}
	agent.AllowedTelegramUserIDs = core.NormalizeDurableAgentAllowedTelegramUserIDs(agent.AllowedTelegramUserIDs)

	livePolicyJSON, policyHash, err := marshalDurableAgentLivePolicy(agent.LivePolicy)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent live_policy: %w", err)
	}
	channelConfigJSON, err := marshalDurableAgentChannelConfig(agent.ChannelConfig)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent channel_config: %w", err)
	}
	bootstrapCeilingJSON, err := marshalDurableAgentBootstrapCeiling(agent.BootstrapCeiling)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent bootstrap_ceiling: %w", err)
	}
	bootstrapProviderJSON, err := marshalDurableAgentBootstrapLLM(agent.BootstrapLLM)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent bootstrap_llm: %w", err)
	}
	storageRootsJSON, err := marshalStringSlice(agent.LocalStorageRoots)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent local_storage_roots: %w", err)
	}
	secretScopesJSON, err := marshalStringSlice(agent.SecretScopes)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent secret_scopes: %w", err)
	}
	allowedTelegramUserIDsJSON, err := marshalInt64Slice(agent.AllowedTelegramUserIDs)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent allowed_telegram_user_ids: %w", err)
	}

	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(agent.CreatedAt, now).UTC().Format(time.RFC3339Nano)
	updatedAt := now.UTC().Format(time.RFC3339Nano)
	policyVersion := agent.PolicyVersion
	if policyVersion <= 0 {
		policyVersion = 1
	}
	policyIssuedAt := nonZeroTimeOrNow(agent.PolicyIssuedAt, now)
	_, err = exec.Exec(`
		INSERT INTO durable_agents(
			agent_id, parent_agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id,
			channel_kind, live_policy_json, channel_config_json, bootstrap_ceiling_json, bootstrap_provider_json, control_plane_secret, policy_version, policy_hash, policy_issued_at,
			local_storage_roots_json, network_policy, wakeup_mode, secret_scopes_json, allowed_telegram_user_ids_json, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			parent_agent_id = excluded.parent_agent_id,
			parent_scope_kind = excluded.parent_scope_kind,
			parent_scope_id = excluded.parent_scope_id,
			review_target_chat_id = excluded.review_target_chat_id,
			channel_kind = excluded.channel_kind,
			live_policy_json = excluded.live_policy_json,
			channel_config_json = excluded.channel_config_json,
			bootstrap_ceiling_json = excluded.bootstrap_ceiling_json,
			bootstrap_provider_json = excluded.bootstrap_provider_json,
			control_plane_secret = excluded.control_plane_secret,
			policy_version = excluded.policy_version,
			policy_hash = excluded.policy_hash,
			policy_issued_at = excluded.policy_issued_at,
			local_storage_roots_json = excluded.local_storage_roots_json,
			network_policy = excluded.network_policy,
			wakeup_mode = excluded.wakeup_mode,
			secret_scopes_json = excluded.secret_scopes_json,
			allowed_telegram_user_ids_json = excluded.allowed_telegram_user_ids_json,
			status = excluded.status,
			updated_at = excluded.updated_at
	`,
		agent.AgentID, nullableString(agent.ParentAgentID), nullableString(agent.ParentScopeKind), nullableString(agent.ParentScopeID), agent.ReviewTargetChatID,
		agent.ChannelKind, livePolicyJSON, channelConfigJSON, bootstrapCeilingJSON, bootstrapProviderJSON, strings.TrimSpace(agent.ControlPlaneSecret), policyVersion, policyHash, nullableTime(policyIssuedAt), string(storageRootsJSON),
		nullableString(agent.NetworkPolicy), nullableString(agent.WakeupMode), string(secretScopesJSON), string(allowedTelegramUserIDsJSON), nullableString(agent.Status), createdAt, updatedAt,
	)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent: %w", err)
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	agent.ChannelConfig = core.NormalizeDurableAgentChannelConfig(agent.ChannelConfig)
	agent.BootstrapCeiling = core.NormalizeDurableAgentBootstrapCeiling(agent.BootstrapCeiling)
	agent.PolicyVersion = policyVersion
	agent.PolicyHash = policyHash
	agent.PolicyIssuedAt = policyIssuedAt
	agent.CreatedAt = mustParseSQLiteTime(createdAt)
	agent.UpdatedAt = mustParseSQLiteTime(updatedAt)
	return agent, nil
}

func (s *SQLiteStore) DurableAgent(agentID string) (*core.DurableAgent, error) {
	return queryDurableAgent(s.db, strings.TrimSpace(agentID))
}

func (s *SQLiteStore) SetDurableAgentLivePolicy(agentID string, policy core.DurableAgentLivePolicy) error {
	agent, err := s.DurableAgent(agentID)
	if err != nil {
		return err
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(policy)
	agent.PolicyVersion++
	if agent.PolicyVersion <= 0 {
		agent.PolicyVersion = 1
	}
	agent.PolicyIssuedAt = time.Now().UTC()
	updated, err := upsertDurableAgentExec(s.db, *agent)
	if err != nil {
		return err
	}
	state, err := s.DurableAgentState(agent.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: agent.AgentID}
	}
	state.LastOfferedPolicyVersion = updated.PolicyVersion
	state.LastOfferedPolicyHash = updated.PolicyHash
	state.LastOfferedPolicyAt = updated.PolicyIssuedAt
	state.LastApplyStatus = "pending"
	state.LastApplyError = ""
	return s.SaveDurableAgentState(*state)
}

func (s *SQLiteStore) ApplyDurableAgentLivePolicy(agentID string, policy core.DurableAgentLivePolicy, sourceReviewEventID int64, reason string) (*core.DurableAgent, *DurableAgentPolicyUpdate, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("begin apply durable agent live policy tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	agent, err := queryDurableAgent(tx, agentID)
	if err != nil {
		return nil, nil, err
	}
	nextPolicy := core.NormalizeDurableAgentLivePolicy(policy)
	nextPolicyHash, err := core.DurableAgentPolicyHash(nextPolicy)
	if err != nil {
		return nil, nil, fmt.Errorf("hash durable agent live policy: %w", err)
	}
	if agent.PolicyHash == "" {
		agent.PolicyHash, err = core.DurableAgentPolicyHash(agent.LivePolicy)
		if err != nil {
			return nil, nil, fmt.Errorf("hash current durable agent live policy: %w", err)
		}
	}
	if agent.PolicyHash == nextPolicyHash {
		if err := tx.Commit(); err != nil {
			return nil, nil, fmt.Errorf("commit no-op durable agent live policy apply: %w", err)
		}
		return agent, nil, nil
	}
	previousVersion := agent.PolicyVersion
	agent.LivePolicy = nextPolicy
	agent.PolicyVersion++
	if agent.PolicyVersion <= 0 {
		agent.PolicyVersion = 1
	}
	agent.PolicyIssuedAt = time.Now().UTC()
	updated, err := upsertDurableAgentExec(tx, *agent)
	if err != nil {
		return nil, nil, err
	}

	policyJSON, policyHash, err := marshalDurableAgentLivePolicy(updated.LivePolicy)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal applied durable agent live policy: %w", err)
	}
	now := time.Now().UTC()
	res, err := tx.Exec(`
		INSERT INTO durable_agent_policy_updates(
			agent_id, source_review_event_id, previous_version, new_version, policy_hash, policy_json, reason, applied_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		updated.AgentID, maxInt64(sourceReviewEventID, 0), previousVersion, updated.PolicyVersion, policyHash, policyJSON, nullableString(reason), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("insert durable agent policy update: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, nil, fmt.Errorf("durable agent policy update last insert id: %w", err)
	}
	state, err := queryDurableAgentState(tx, updated.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("load durable agent state for policy apply: %w", err)
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: updated.AgentID}
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("parse durable agent continuity for policy apply: %w", err)
	}
	summary := strings.TrimSpace(reason)
	if summary == "" {
		summary = "Ratified durable-agent live policy update."
	}
	continuity = continuity.WithRatifiedOutcome(summary, updated.PolicyVersion, policyHash, maxInt64(sourceReviewEventID, 0), now)
	stateJSON, err := continuity.Marshal()
	if err != nil {
		return nil, nil, fmt.Errorf("marshal durable agent continuity for policy apply: %w", err)
	}
	state.StateJSON = stateJSON
	state.LastOfferedPolicyVersion = updated.PolicyVersion
	state.LastOfferedPolicyHash = policyHash
	state.LastOfferedPolicyAt = now
	state.LastApplyStatus = "pending"
	state.LastApplyError = ""
	if err := saveDurableAgentRuntimeStateExec(tx, core.DurableAgentRuntimeStateFrom(*state)); err != nil {
		return nil, nil, fmt.Errorf("save durable agent runtime state for policy apply: %w", err)
	}
	if err := saveDurableAgentIdentityStateExec(tx, core.DurableAgentIdentityStateFrom(*state)); err != nil {
		return nil, nil, fmt.Errorf("save durable agent identity state for policy apply: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit durable agent live policy apply: %w", err)
	}
	return &updated, &DurableAgentPolicyUpdate{
		ID:                  id,
		AgentID:             updated.AgentID,
		SourceReviewEventID: maxInt64(sourceReviewEventID, 0),
		PreviousVersion:     previousVersion,
		NewVersion:          updated.PolicyVersion,
		PolicyHash:          policyHash,
		PolicyJSON:          policyJSON,
		Reason:              strings.TrimSpace(reason),
		AppliedAt:           now,
	}, nil
}

func (s *SQLiteStore) DurableAgentPolicyUpdates(agentID string, limit int) ([]DurableAgentPolicyUpdate, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("durable agent policy updates: agent_id is required")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT id, agent_id, source_review_event_id, previous_version, new_version, policy_hash, policy_json, reason, applied_at
		FROM durable_agent_policy_updates
		WHERE agent_id = ?
		ORDER BY applied_at DESC, id DESC
		LIMIT ?
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("query durable agent policy updates: %w", err)
	}
	defer rows.Close()
	var updates []DurableAgentPolicyUpdate
	for rows.Next() {
		update, err := scanDurableAgentPolicyUpdate(rows)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable agent policy updates: %w", err)
	}
	return updates, nil
}

func (s *SQLiteStore) ApplyDurableAgentBootstrap(agentID string, next core.NodeLLMBootstrap, sourceReviewEventID int64, actorUserID int64, actorRole string, updateKind string, reason string) (*core.DurableAgent, *DurableAgentBootstrapUpdate, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("begin apply durable agent bootstrap tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	agent, err := queryDurableAgent(tx, agentID)
	if err != nil {
		return nil, nil, err
	}
	previous := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	next = core.NormalizeNodeLLMBootstrap(next)
	if err := core.ValidateNodeLLMBootstrap(next); err != nil {
		return nil, nil, fmt.Errorf("validate durable agent bootstrap_llm: %w", err)
	}
	if previous == next {
		if err := tx.Commit(); err != nil {
			return nil, nil, fmt.Errorf("commit no-op durable agent bootstrap apply: %w", err)
		}
		return agent, nil, nil
	}

	agent.BootstrapLLM = next
	updated, err := upsertDurableAgentExec(tx, *agent)
	if err != nil {
		return nil, nil, err
	}
	previousAudit := redactDurableAgentBootstrapSecrets(previous)
	newAudit := redactDurableAgentBootstrapSecrets(updated.BootstrapLLM)
	prevJSON, err := marshalDurableAgentBootstrapLLM(previousAudit)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal previous durable agent bootstrap: %w", err)
	}
	nextJSON, err := marshalDurableAgentBootstrapLLM(newAudit)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal updated durable agent bootstrap: %w", err)
	}
	now := time.Now().UTC()
	res, err := tx.Exec(`
		INSERT INTO durable_agent_bootstrap_updates(
			agent_id, source_review_event_id, actor_user_id, actor_role, update_kind, previous_bootstrap_json, new_bootstrap_json, reason, applied_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		updated.AgentID, maxInt64(sourceReviewEventID, 0), maxInt64(actorUserID, 0), strings.TrimSpace(actorRole), strings.TrimSpace(updateKind), prevJSON, nextJSON, nullableString(reason), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("insert durable agent bootstrap update: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, nil, fmt.Errorf("durable agent bootstrap update last insert id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit durable agent bootstrap apply: %w", err)
	}
	return &updated, &DurableAgentBootstrapUpdate{
		ID:                  id,
		AgentID:             updated.AgentID,
		SourceReviewEventID: maxInt64(sourceReviewEventID, 0),
		ActorUserID:         maxInt64(actorUserID, 0),
		ActorRole:           strings.TrimSpace(actorRole),
		UpdateKind:          strings.TrimSpace(updateKind),
		PreviousBootstrap:   previousAudit,
		NewBootstrap:        newAudit,
		Reason:              strings.TrimSpace(reason),
		AppliedAt:           now,
	}, nil
}

func (s *SQLiteStore) DurableAgentBootstrapUpdates(agentID string, limit int) ([]DurableAgentBootstrapUpdate, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("durable agent bootstrap updates: agent_id is required")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT id, agent_id, source_review_event_id, actor_user_id, actor_role, update_kind, previous_bootstrap_json, new_bootstrap_json, reason, applied_at
		FROM durable_agent_bootstrap_updates
		WHERE agent_id = ?
		ORDER BY applied_at DESC, id DESC
		LIMIT ?
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("query durable agent bootstrap updates: %w", err)
	}
	defer rows.Close()
	var updates []DurableAgentBootstrapUpdate
	for rows.Next() {
		update, err := scanDurableAgentBootstrapUpdate(rows)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable agent bootstrap updates: %w", err)
	}
	return updates, nil
}

func (s *SQLiteStore) UpsertDurableAgentRemoteEnrollment(enrollment core.DurableAgentRemoteEnrollment) error {
	enrollment = core.NormalizeDurableAgentRemoteEnrollment(enrollment)
	if enrollment.AgentID == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: agent_id is required")
	}
	if enrollment.ParentControlURL == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: parent_control_url is required")
	}
	if enrollment.KeyFingerprint == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: key_fingerprint is required")
	}
	now := time.Now().UTC()
	if enrollment.EnrolledAt.IsZero() {
		enrollment.EnrolledAt = now
	}
	_, err := s.db.Exec(`
		INSERT INTO durable_agent_remote_enrollments(
			agent_id, parent_control_url, key_fingerprint, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			parent_control_url = excluded.parent_control_url,
			key_fingerprint = excluded.key_fingerprint,
			protocol_version = excluded.protocol_version,
			status = excluded.status,
			last_sequence = excluded.last_sequence,
			enrolled_at = excluded.enrolled_at,
			last_seen_at = excluded.last_seen_at,
			revoked_at = excluded.revoked_at,
			updated_at = excluded.updated_at
	`,
		enrollment.AgentID, enrollment.ParentControlURL, enrollment.KeyFingerprint, enrollment.ProtocolVersion, enrollment.Status,
		maxInt64(enrollment.LastSequence, 0), nullableTime(enrollment.EnrolledAt), nullableTime(enrollment.LastSeenAt), nullableTime(enrollment.RevokedAt), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert durable agent remote enrollment: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DurableAgentRemoteEnrollment(agentID string) (*core.DurableAgentRemoteEnrollment, error) {
	rows, err := s.db.Query(`
		SELECT agent_id, parent_control_url, key_fingerprint, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at
		FROM durable_agent_remote_enrollments
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent remote enrollment: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	enrollment, err := scanDurableAgentRemoteEnrollment(rows)
	if err != nil {
		return nil, err
	}
	return &enrollment, nil
}

func (s *SQLiteStore) AcceptDurableAgentControlEnvelope(envelope core.DurableAgentControlEnvelope, receivedAt time.Time) error {
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	if err := core.ValidateDurableAgentControlEnvelope(envelope); err != nil {
		return err
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin durable agent control envelope tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	enrollment, err := queryDurableAgentRemoteEnrollment(tx, envelope.AgentID)
	if err != nil {
		return err
	}
	if enrollment.Status != "active" {
		return fmt.Errorf("durable agent remote enrollment %s is not active", enrollment.AgentID)
	}
	_, err = tx.Exec(`
		INSERT INTO durable_agent_control_receipts(agent_id, message_id, message_kind, sequence, received_at)
		VALUES (?, ?, ?, ?, ?)
	`,
		envelope.AgentID, envelope.MessageID, envelope.MessageKind, envelope.Sequence, receivedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("replay durable agent control envelope for %s", envelope.AgentID)
		}
		return fmt.Errorf("insert durable agent control receipt: %w", err)
	}
	if envelope.Sequence <= enrollment.LastSequence {
		return fmt.Errorf("out-of-order durable agent control envelope for %s", enrollment.AgentID)
	}
	enrollment.LastSequence = envelope.Sequence
	enrollment.LastSeenAt = receivedAt.UTC()
	if err := upsertDurableAgentRemoteEnrollmentExec(tx, *enrollment); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit durable agent control envelope tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListDurableAgents() ([]core.DurableAgent, error) {
	rows, err := s.db.Query(`
		SELECT
			agent_id, parent_agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id,
			channel_kind, live_policy_json, COALESCE(channel_config_json, ''), COALESCE(bootstrap_ceiling_json, ''), COALESCE(bootstrap_provider_json, ''), COALESCE(control_plane_secret, ''), policy_version, policy_hash, policy_issued_at, local_storage_roots_json, network_policy,
			wakeup_mode, secret_scopes_json, COALESCE(allowed_telegram_user_ids_json, '[]'), status, created_at, updated_at
		FROM durable_agents
		ORDER BY created_at ASC, agent_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list durable agents: %w", err)
	}
	defer rows.Close()

	var agents []core.DurableAgent
	for rows.Next() {
		agent, err := scanDurableAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable agents: %w", err)
	}
	return agents, nil
}

func (s *SQLiteStore) SaveDurableAgentState(state core.DurableAgentState) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin save durable agent state tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := saveDurableAgentRuntimeStateExec(tx, core.DurableAgentRuntimeStateFrom(state)); err != nil {
		return err
	}
	if err := saveDurableAgentIdentityStateExec(tx, core.DurableAgentIdentityStateFrom(state)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save durable agent state tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveDurableAgentRuntimeState(state core.DurableAgentRuntimeState) error {
	return saveDurableAgentRuntimeStateExec(s.db, state)
}

func (s *SQLiteStore) TryMarkDurableAgentAwake(agentID string, cursor string, now time.Time, staleAfter time.Duration) (bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false, fmt.Errorf("mark durable agent awake: agent_id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if staleAfter <= 0 {
		staleAfter = 30 * time.Minute
	}
	cutoff := now.Add(-staleAfter).UTC().Format(time.RFC3339Nano)
	nowRaw := now.Format(time.RFC3339Nano)

	result, err := s.db.Exec(`
		UPDATE durable_agent_state
		SET cursor = ?, status = 'awake', last_wake_at = ?, dormant_at = NULL, updated_at = ?
		WHERE agent_id = ?
		  AND (
			COALESCE(status, '') <> 'awake'
			OR COALESCE(last_wake_at, updated_at, '') = ''
			OR COALESCE(last_wake_at, updated_at) <= ?
		  )
	`, nullableString(cursor), nowRaw, nowRaw, agentID, cutoff)
	if err != nil {
		return false, fmt.Errorf("mark durable agent awake: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows > 0 {
		return true, nil
	}

	result, err = s.db.Exec(`
		INSERT INTO durable_agent_state(agent_id, cursor, status, state_json, last_wake_at, dormant_at, updated_at)
		VALUES (?, ?, 'awake', '', ?, NULL, ?)
		ON CONFLICT(agent_id) DO NOTHING
	`, agentID, nullableString(cursor), nowRaw, nowRaw)
	if err != nil {
		return false, fmt.Errorf("mark durable agent awake: %w", err)
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

func (s *SQLiteStore) DurableAgentReviewEventCountSince(agentID string, since time.Time) (int, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || since.IsZero() {
		return 0, nil
	}
	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(1)
		FROM review_events
		WHERE source_durable_agent_id = ?
		  AND created_at >= ?
	`, agentID, since.UTC().Format(time.RFC3339Nano)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count durable agent review events: %w", err)
	}
	return count, nil
}

func saveDurableAgentRuntimeStateExec(exec sqlExecer, state core.DurableAgentRuntimeState) error {
	state.AgentID = strings.TrimSpace(state.AgentID)
	if state.AgentID == "" {
		return fmt.Errorf("save durable agent runtime state: agent_id is required")
	}
	now := time.Now().UTC()
	_, err := exec.Exec(`
		INSERT INTO durable_agent_state(
			agent_id, cursor, status, state_json,
			last_apply_status, last_apply_error,
			last_wake_at, last_review_at, dormant_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			cursor = excluded.cursor,
			status = excluded.status,
			state_json = excluded.state_json,
			last_apply_status = excluded.last_apply_status,
			last_apply_error = excluded.last_apply_error,
			last_wake_at = excluded.last_wake_at,
			last_review_at = excluded.last_review_at,
			dormant_at = excluded.dormant_at,
			updated_at = excluded.updated_at
	`,
		state.AgentID, nullableString(state.Cursor), nullableString(state.Status), nullableString(state.StateJSON),
		strings.TrimSpace(state.LastApplyStatus), strings.TrimSpace(state.LastApplyError),
		nullableTime(state.LastWakeAt), nullableTime(state.LastReviewAt), nullableTime(state.DormantAt), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save durable agent runtime state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveDurableAgentIdentityState(state core.DurableAgentIdentityState) error {
	return saveDurableAgentIdentityStateExec(s.db, state)
}

func saveDurableAgentIdentityStateExec(exec sqlExecer, state core.DurableAgentIdentityState) error {
	state.AgentID = strings.TrimSpace(state.AgentID)
	if state.AgentID == "" {
		return fmt.Errorf("save durable agent identity state: agent_id is required")
	}
	now := time.Now().UTC()
	_, err := exec.Exec(`
		INSERT INTO durable_agent_identity_state(
			agent_id,
			last_offered_policy_version, last_offered_policy_hash, last_offered_policy_at,
			last_acknowledged_policy_version, last_acknowledged_policy_hash, last_acknowledged_policy_at,
			last_applied_policy_version, last_applied_policy_hash, last_applied_policy_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			last_offered_policy_version = excluded.last_offered_policy_version,
			last_offered_policy_hash = excluded.last_offered_policy_hash,
			last_offered_policy_at = excluded.last_offered_policy_at,
			last_acknowledged_policy_version = excluded.last_acknowledged_policy_version,
			last_acknowledged_policy_hash = excluded.last_acknowledged_policy_hash,
			last_acknowledged_policy_at = excluded.last_acknowledged_policy_at,
			last_applied_policy_version = excluded.last_applied_policy_version,
			last_applied_policy_hash = excluded.last_applied_policy_hash,
			last_applied_policy_at = excluded.last_applied_policy_at,
			updated_at = excluded.updated_at
	`,
		state.AgentID,
		state.LastOfferedPolicyVersion, strings.TrimSpace(state.LastOfferedPolicyHash), nullableTime(state.LastOfferedPolicyAt),
		state.LastAcknowledgedPolicyVersion, strings.TrimSpace(state.LastAcknowledgedPolicyHash), nullableTime(state.LastAcknowledgedPolicyAt),
		state.LastAppliedPolicyVersion, strings.TrimSpace(state.LastAppliedPolicyHash), nullableTime(state.LastAppliedPolicyAt),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save durable agent identity state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DurableAgentRuntimeState(agentID string) (*core.DurableAgentRuntimeState, error) {
	return queryDurableAgentRuntimeState(s.db, agentID)
}

func queryDurableAgentRuntimeState(queryer sqlQueryer, agentID string) (*core.DurableAgentRuntimeState, error) {
	rows, err := queryer.Query(`
		SELECT
			agent_id, cursor, status, state_json,
			last_apply_status, last_apply_error,
			last_wake_at, last_review_at, dormant_at, updated_at
		FROM durable_agent_state
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent runtime state: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	state, err := scanDurableAgentRuntimeState(rows)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *SQLiteStore) DurableAgentIdentityState(agentID string) (*core.DurableAgentIdentityState, error) {
	return queryDurableAgentIdentityState(s.db, agentID)
}

func queryDurableAgentIdentityState(queryer sqlQueryer, agentID string) (*core.DurableAgentIdentityState, error) {
	rows, err := queryer.Query(`
		SELECT
			agent_id,
			last_offered_policy_version, last_offered_policy_hash, last_offered_policy_at,
			last_acknowledged_policy_version, last_acknowledged_policy_hash, last_acknowledged_policy_at,
			last_applied_policy_version, last_applied_policy_hash, last_applied_policy_at,
			updated_at
		FROM durable_agent_identity_state
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent identity state: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	state, err := scanDurableAgentIdentityState(rows)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *SQLiteStore) DurableAgentState(agentID string) (*core.DurableAgentState, error) {
	return queryDurableAgentState(s.db, agentID)
}

func queryDurableAgentState(queryer sqlQueryer, agentID string) (*core.DurableAgentState, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, sql.ErrNoRows
	}

	runtimeState, runtimeErr := queryDurableAgentRuntimeState(queryer, agentID)
	if runtimeErr != nil && !errors.Is(runtimeErr, sql.ErrNoRows) {
		return nil, runtimeErr
	}
	identityState, identityErr := queryDurableAgentIdentityState(queryer, agentID)
	if identityErr != nil && !errors.Is(identityErr, sql.ErrNoRows) {
		return nil, identityErr
	}
	if errors.Is(runtimeErr, sql.ErrNoRows) && errors.Is(identityErr, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}

	state := core.DurableAgentState{AgentID: agentID}
	if runtimeState != nil {
		state.Cursor = runtimeState.Cursor
		state.Status = runtimeState.Status
		state.StateJSON = runtimeState.StateJSON
		state.LastApplyStatus = runtimeState.LastApplyStatus
		state.LastApplyError = runtimeState.LastApplyError
		state.LastWakeAt = runtimeState.LastWakeAt
		state.LastReviewAt = runtimeState.LastReviewAt
		state.DormantAt = runtimeState.DormantAt
		state.UpdatedAt = runtimeState.UpdatedAt
	}
	if identityState != nil {
		state.LastOfferedPolicyVersion = identityState.LastOfferedPolicyVersion
		state.LastOfferedPolicyHash = identityState.LastOfferedPolicyHash
		state.LastOfferedPolicyAt = identityState.LastOfferedPolicyAt
		state.LastAcknowledgedPolicyVersion = identityState.LastAcknowledgedPolicyVersion
		state.LastAcknowledgedPolicyHash = identityState.LastAcknowledgedPolicyHash
		state.LastAcknowledgedPolicyAt = identityState.LastAcknowledgedPolicyAt
		state.LastAppliedPolicyVersion = identityState.LastAppliedPolicyVersion
		state.LastAppliedPolicyHash = identityState.LastAppliedPolicyHash
		state.LastAppliedPolicyAt = identityState.LastAppliedPolicyAt
		if state.UpdatedAt.IsZero() || (!identityState.UpdatedAt.IsZero() && identityState.UpdatedAt.After(state.UpdatedAt)) {
			state.UpdatedAt = identityState.UpdatedAt
		}
	}
	return &state, nil
}

func (s *SQLiteStore) DeleteDurableAgent(agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("delete durable agent: agent_id is required")
	}
	if _, err := s.db.Exec(`DELETE FROM durable_agents WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("delete durable agent: %w", err)
	}
	return nil
}

func queryDurableAgent(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, agentID string) (*core.DurableAgent, error) {
	rows, err := q.Query(`
		SELECT
			agent_id, parent_agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id,
			channel_kind, live_policy_json, COALESCE(channel_config_json, ''), COALESCE(bootstrap_ceiling_json, ''), COALESCE(bootstrap_provider_json, ''), COALESCE(control_plane_secret, ''), policy_version, policy_hash, policy_issued_at, local_storage_roots_json, network_policy,
			wakeup_mode, secret_scopes_json, COALESCE(allowed_telegram_user_ids_json, '[]'), status, created_at, updated_at
		FROM durable_agents
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	agent, err := scanDurableAgent(rows)
	if err != nil {
		return nil, err
	}
	return &agent, nil
}

func upsertDurableAgentRemoteEnrollmentExec(exec sqlExecer, enrollment core.DurableAgentRemoteEnrollment) error {
	enrollment = core.NormalizeDurableAgentRemoteEnrollment(enrollment)
	if enrollment.AgentID == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: agent_id is required")
	}
	if enrollment.ParentControlURL == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: parent_control_url is required")
	}
	if enrollment.KeyFingerprint == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: key_fingerprint is required")
	}
	now := time.Now().UTC()
	if enrollment.EnrolledAt.IsZero() {
		enrollment.EnrolledAt = now
	}
	_, err := exec.Exec(`
		INSERT INTO durable_agent_remote_enrollments(
			agent_id, parent_control_url, key_fingerprint, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			parent_control_url = excluded.parent_control_url,
			key_fingerprint = excluded.key_fingerprint,
			protocol_version = excluded.protocol_version,
			status = excluded.status,
			last_sequence = excluded.last_sequence,
			enrolled_at = excluded.enrolled_at,
			last_seen_at = excluded.last_seen_at,
			revoked_at = excluded.revoked_at,
			updated_at = excluded.updated_at
	`,
		enrollment.AgentID, enrollment.ParentControlURL, enrollment.KeyFingerprint, enrollment.ProtocolVersion, enrollment.Status,
		maxInt64(enrollment.LastSequence, 0), nullableTime(enrollment.EnrolledAt), nullableTime(enrollment.LastSeenAt), nullableTime(enrollment.RevokedAt), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert durable agent remote enrollment: %w", err)
	}
	return nil
}

func queryDurableAgentRemoteEnrollment(q sqlQueryer, agentID string) (*core.DurableAgentRemoteEnrollment, error) {
	rows, err := q.Query(`
		SELECT agent_id, parent_control_url, key_fingerprint, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at
		FROM durable_agent_remote_enrollments
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent remote enrollment: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	enrollment, err := scanDurableAgentRemoteEnrollment(rows)
	if err != nil {
		return nil, err
	}
	return &enrollment, nil
}

func marshalDurableAgentLivePolicy(policy core.DurableAgentLivePolicy) (string, string, error) {
	normalized := core.NormalizeDurableAgentLivePolicy(policy)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", "", err
	}
	hash, err := core.DurableAgentPolicyHash(normalized)
	if err != nil {
		return "", "", err
	}
	return string(raw), hash, nil
}

func marshalDurableAgentChannelConfig(cfg core.DurableAgentChannelConfig) (string, error) {
	normalized := core.NormalizeDurableAgentChannelConfig(cfg)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalDurableAgentBootstrapCeiling(ceiling core.DurableAgentBootstrapCeiling) (string, error) {
	normalized := core.NormalizeDurableAgentBootstrapCeiling(ceiling)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalDurableAgentBootstrapLLM(bootstrap core.NodeLLMBootstrap) (string, error) {
	normalized := core.NormalizeNodeLLMBootstrap(bootstrap)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func redactDurableAgentBootstrapSecrets(bootstrap core.NodeLLMBootstrap) core.NodeLLMBootstrap {
	redacted := core.NormalizeNodeLLMBootstrap(bootstrap)
	redacted.APIKey = ""
	return redacted
}

func unmarshalDurableAgentLivePolicy(raw string) (core.DurableAgentLivePolicy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{}), nil
	}
	var policy core.DurableAgentLivePolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return core.DurableAgentLivePolicy{}, err
	}
	return core.NormalizeDurableAgentLivePolicy(policy), nil
}

func unmarshalDurableAgentChannelConfig(raw string) (core.DurableAgentChannelConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.NormalizeDurableAgentChannelConfig(core.DurableAgentChannelConfig{}), nil
	}
	var cfg core.DurableAgentChannelConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return core.DurableAgentChannelConfig{}, err
	}
	return core.NormalizeDurableAgentChannelConfig(cfg), nil
}

func unmarshalDurableAgentBootstrapCeiling(raw string) (core.DurableAgentBootstrapCeiling, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.NormalizeDurableAgentBootstrapCeiling(core.DurableAgentBootstrapCeiling{}), nil
	}
	var ceiling core.DurableAgentBootstrapCeiling
	if err := json.Unmarshal([]byte(raw), &ceiling); err != nil {
		return core.DurableAgentBootstrapCeiling{}, err
	}
	return core.NormalizeDurableAgentBootstrapCeiling(ceiling), nil
}

func unmarshalDurableAgentBootstrapLLM(raw string) (core.NodeLLMBootstrap, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.NormalizeNodeLLMBootstrap(core.NodeLLMBootstrap{}), nil
	}
	var bootstrap core.NodeLLMBootstrap
	if err := json.Unmarshal([]byte(raw), &bootstrap); err != nil {
		return core.NodeLLMBootstrap{}, err
	}
	return core.NormalizeNodeLLMBootstrap(bootstrap), nil
}

func unmarshalStringSlice(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func unmarshalInt64Slice(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var values []int64
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return core.NormalizeDurableAgentAllowedTelegramUserIDs(values), nil
}

func scanDurableAgent(scanner interface{ Scan(dest ...any) error }) (core.DurableAgent, error) {
	var (
		agent                 core.DurableAgent
		parentAgentID         sql.NullString
		parentScopeKind       sql.NullString
		parentScopeID         sql.NullString
		livePolicyJSON        string
		channelConfigJSON     string
		bootstrapCeilingJSON  string
		bootstrapProviderJSON string
		controlPlaneSecret    sql.NullString
		policyVersion         int64
		policyHash            string
		policyIssuedAt        sql.NullString
		storageRootsJSON      string
		networkPolicy         sql.NullString
		wakeupMode            sql.NullString
		secretScopesJSON      string
		allowedUserIDsJSON    string
		status                sql.NullString
		createdAtRaw          string
		updatedAtRaw          string
	)
	if err := scanner.Scan(
		&agent.AgentID, &parentAgentID, &parentScopeKind, &parentScopeID, &agent.ReviewTargetChatID,
		&agent.ChannelKind, &livePolicyJSON, &channelConfigJSON, &bootstrapCeilingJSON, &bootstrapProviderJSON, &controlPlaneSecret, &policyVersion, &policyHash, &policyIssuedAt, &storageRootsJSON, &networkPolicy,
		&wakeupMode, &secretScopesJSON, &allowedUserIDsJSON, &status, &createdAtRaw, &updatedAtRaw,
	); err != nil {
		return core.DurableAgent{}, fmt.Errorf("scan durable agent: %w", err)
	}
	var err error
	agent.ParentAgentID = nullToString(parentAgentID)
	agent.ParentScopeKind = nullToString(parentScopeKind)
	agent.ParentScopeID = nullToString(parentScopeID)
	agent.LivePolicy, err = unmarshalDurableAgentLivePolicy(livePolicyJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent live policy: %w", err)
	}
	agent.ChannelConfig, err = unmarshalDurableAgentChannelConfig(channelConfigJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent channel config: %w", err)
	}
	agent.BootstrapCeiling, err = unmarshalDurableAgentBootstrapCeiling(bootstrapCeilingJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent bootstrap ceiling: %w", err)
	}
	agent.BootstrapLLM, err = unmarshalDurableAgentBootstrapLLM(bootstrapProviderJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent bootstrap llm: %w", err)
	}
	agent.ControlPlaneSecret = nullToString(controlPlaneSecret)
	if agent.BootstrapCeiling.IsZero() {
		agent.BootstrapCeiling = core.DefaultDurableAgentBootstrapCeiling(agent.ChannelKind, agent.LivePolicy)
	}
	agent.PolicyVersion = policyVersion
	agent.PolicyHash = strings.TrimSpace(policyHash)
	if agent.PolicyHash == "" {
		agent.PolicyHash, err = core.DurableAgentPolicyHash(agent.LivePolicy)
		if err != nil {
			return core.DurableAgent{}, fmt.Errorf("hash durable agent live policy: %w", err)
		}
	}
	if policyIssuedAt.Valid && strings.TrimSpace(policyIssuedAt.String) != "" {
		agent.PolicyIssuedAt, err = parseSQLiteTime(policyIssuedAt.String)
		if err != nil {
			return core.DurableAgent{}, fmt.Errorf("parse durable agent policy_issued_at: %w", err)
		}
	}
	agent.NetworkPolicy = nullToString(networkPolicy)
	agent.WakeupMode = nullToString(wakeupMode)
	agent.Status = nullToString(status)
	agent.LocalStorageRoots, err = unmarshalStringSlice(storageRootsJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent storage roots: %w", err)
	}
	agent.SecretScopes, err = unmarshalStringSlice(secretScopesJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent secret scopes: %w", err)
	}
	agent.AllowedTelegramUserIDs, err = unmarshalInt64Slice(allowedUserIDsJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent allowed telegram user ids: %w", err)
	}
	agent.CreatedAt, err = parseSQLiteTime(createdAtRaw)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("parse durable agent created_at: %w", err)
	}
	agent.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("parse durable agent updated_at: %w", err)
	}
	return agent, nil
}

func validateDurableAgentChannelConfig(channelKind string, cfg core.DurableAgentChannelConfig) error {
	cfg = core.NormalizeDurableAgentChannelConfig(cfg)
	switch strings.TrimSpace(channelKind) {
	case "external_channel":
		external := cfg.ExternalConfig()
		if external == nil {
			return nil
		}
		if strings.TrimSpace(external.PollInterval) != "" {
			if _, err := time.ParseDuration(strings.TrimSpace(external.PollInterval)); err != nil {
				return fmt.Errorf("invalid channel poll_interval %q: %w", external.PollInterval, err)
			}
		}
	}
	return nil
}

func scanDurableAgentPolicyUpdate(scanner interface{ Scan(dest ...any) error }) (DurableAgentPolicyUpdate, error) {
	var (
		update       DurableAgentPolicyUpdate
		reason       sql.NullString
		appliedAtRaw string
	)
	if err := scanner.Scan(&update.ID, &update.AgentID, &update.SourceReviewEventID, &update.PreviousVersion, &update.NewVersion, &update.PolicyHash, &update.PolicyJSON, &reason, &appliedAtRaw); err != nil {
		return DurableAgentPolicyUpdate{}, fmt.Errorf("scan durable agent policy update: %w", err)
	}
	update.Reason = nullToString(reason)
	appliedAt, err := parseSQLiteTime(appliedAtRaw)
	if err != nil {
		return DurableAgentPolicyUpdate{}, fmt.Errorf("parse durable agent policy update applied_at: %w", err)
	}
	update.AppliedAt = appliedAt
	return update, nil
}

func scanDurableAgentBootstrapUpdate(scanner interface{ Scan(dest ...any) error }) (DurableAgentBootstrapUpdate, error) {
	var (
		update                DurableAgentBootstrapUpdate
		actorRole             sql.NullString
		updateKind            sql.NullString
		previousBootstrapJSON string
		newBootstrapJSON      string
		reason                sql.NullString
		appliedAtRaw          string
	)
	if err := scanner.Scan(&update.ID, &update.AgentID, &update.SourceReviewEventID, &update.ActorUserID, &actorRole, &updateKind, &previousBootstrapJSON, &newBootstrapJSON, &reason, &appliedAtRaw); err != nil {
		return DurableAgentBootstrapUpdate{}, fmt.Errorf("scan durable agent bootstrap update: %w", err)
	}
	var err error
	update.ActorRole = nullToString(actorRole)
	update.UpdateKind = nullToString(updateKind)
	update.PreviousBootstrap, err = unmarshalDurableAgentBootstrapLLM(previousBootstrapJSON)
	if err != nil {
		return DurableAgentBootstrapUpdate{}, fmt.Errorf("decode previous durable agent bootstrap update: %w", err)
	}
	update.NewBootstrap, err = unmarshalDurableAgentBootstrapLLM(newBootstrapJSON)
	if err != nil {
		return DurableAgentBootstrapUpdate{}, fmt.Errorf("decode new durable agent bootstrap update: %w", err)
	}
	update.Reason = nullToString(reason)
	appliedAt, err := parseSQLiteTime(appliedAtRaw)
	if err != nil {
		return DurableAgentBootstrapUpdate{}, fmt.Errorf("parse durable agent bootstrap update applied_at: %w", err)
	}
	update.AppliedAt = appliedAt
	return update, nil
}

func scanDurableAgentRuntimeState(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentRuntimeState, error) {
	var (
		state         core.DurableAgentRuntimeState
		cursorRaw     sql.NullString
		statusRaw     sql.NullString
		stateJSONRaw  sql.NullString
		lastStatusRaw sql.NullString
		lastErrorRaw  sql.NullString
		lastWakeAtRaw sql.NullString
		lastReviewRaw sql.NullString
		dormantAtRaw  sql.NullString
		updatedAtRaw  string
	)
	if err := scanner.Scan(
		&state.AgentID, &cursorRaw, &statusRaw, &stateJSONRaw,
		&lastStatusRaw, &lastErrorRaw,
		&lastWakeAtRaw, &lastReviewRaw, &dormantAtRaw, &updatedAtRaw,
	); err != nil {
		return core.DurableAgentRuntimeState{}, fmt.Errorf("scan durable agent runtime state: %w", err)
	}
	state.Cursor = nullToString(cursorRaw)
	state.Status = nullToString(statusRaw)
	state.StateJSON = nullToString(stateJSONRaw)
	state.LastApplyStatus = nullToString(lastStatusRaw)
	state.LastApplyError = nullToString(lastErrorRaw)
	var err error
	if lastWakeAtRaw.Valid && lastWakeAtRaw.String != "" {
		state.LastWakeAt, err = parseSQLiteTime(lastWakeAtRaw.String)
		if err != nil {
			return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime last_wake_at: %w", err)
		}
	}
	if lastReviewRaw.Valid && lastReviewRaw.String != "" {
		state.LastReviewAt, err = parseSQLiteTime(lastReviewRaw.String)
		if err != nil {
			return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime last_review_at: %w", err)
		}
	}
	if dormantAtRaw.Valid && dormantAtRaw.String != "" {
		state.DormantAt, err = parseSQLiteTime(dormantAtRaw.String)
		if err != nil {
			return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime dormant_at: %w", err)
		}
	}
	state.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime updated_at: %w", err)
	}
	return state, nil
}

func scanDurableAgentIdentityState(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentIdentityState, error) {
	var (
		state                         core.DurableAgentIdentityState
		lastOfferedPolicyHashRaw      sql.NullString
		lastOfferedPolicyAtRaw        sql.NullString
		lastAcknowledgedPolicyHashRaw sql.NullString
		lastAcknowledgedPolicyAtRaw   sql.NullString
		lastAppliedPolicyHashRaw      sql.NullString
		lastAppliedPolicyAtRaw        sql.NullString
		updatedAtRaw                  string
	)
	if err := scanner.Scan(
		&state.AgentID,
		&state.LastOfferedPolicyVersion, &lastOfferedPolicyHashRaw, &lastOfferedPolicyAtRaw,
		&state.LastAcknowledgedPolicyVersion, &lastAcknowledgedPolicyHashRaw, &lastAcknowledgedPolicyAtRaw,
		&state.LastAppliedPolicyVersion, &lastAppliedPolicyHashRaw, &lastAppliedPolicyAtRaw,
		&updatedAtRaw,
	); err != nil {
		return core.DurableAgentIdentityState{}, fmt.Errorf("scan durable agent identity state: %w", err)
	}
	state.LastOfferedPolicyHash = nullToString(lastOfferedPolicyHashRaw)
	state.LastAcknowledgedPolicyHash = nullToString(lastAcknowledgedPolicyHashRaw)
	state.LastAppliedPolicyHash = nullToString(lastAppliedPolicyHashRaw)
	var err error
	if lastOfferedPolicyAtRaw.Valid && lastOfferedPolicyAtRaw.String != "" {
		state.LastOfferedPolicyAt, err = parseSQLiteTime(lastOfferedPolicyAtRaw.String)
		if err != nil {
			return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity last_offered_policy_at: %w", err)
		}
	}
	if lastAcknowledgedPolicyAtRaw.Valid && lastAcknowledgedPolicyAtRaw.String != "" {
		state.LastAcknowledgedPolicyAt, err = parseSQLiteTime(lastAcknowledgedPolicyAtRaw.String)
		if err != nil {
			return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity last_acknowledged_policy_at: %w", err)
		}
	}
	if lastAppliedPolicyAtRaw.Valid && lastAppliedPolicyAtRaw.String != "" {
		state.LastAppliedPolicyAt, err = parseSQLiteTime(lastAppliedPolicyAtRaw.String)
		if err != nil {
			return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity last_applied_policy_at: %w", err)
		}
	}
	state.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity updated_at: %w", err)
	}
	return state, nil
}

func scanDurableAgentRemoteEnrollment(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentRemoteEnrollment, error) {
	var (
		enrollment      core.DurableAgentRemoteEnrollment
		protocolVersion sql.NullString
		statusRaw       sql.NullString
		enrolledAtRaw   sql.NullString
		lastSeenAtRaw   sql.NullString
		revokedAtRaw    sql.NullString
	)
	if err := scanner.Scan(
		&enrollment.AgentID, &enrollment.ParentControlURL, &enrollment.KeyFingerprint, &protocolVersion, &statusRaw, &enrollment.LastSequence, &enrolledAtRaw, &lastSeenAtRaw, &revokedAtRaw,
	); err != nil {
		return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("scan durable agent remote enrollment: %w", err)
	}
	enrollment.ProtocolVersion = nullToString(protocolVersion)
	enrollment.Status = nullToString(statusRaw)
	var err error
	if enrolledAtRaw.Valid && enrolledAtRaw.String != "" {
		enrollment.EnrolledAt, err = parseSQLiteTime(enrolledAtRaw.String)
		if err != nil {
			return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("parse durable agent remote enrollment enrolled_at: %w", err)
		}
	}
	if lastSeenAtRaw.Valid && lastSeenAtRaw.String != "" {
		enrollment.LastSeenAt, err = parseSQLiteTime(lastSeenAtRaw.String)
		if err != nil {
			return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("parse durable agent remote enrollment last_seen_at: %w", err)
		}
	}
	if revokedAtRaw.Valid && revokedAtRaw.String != "" {
		enrollment.RevokedAt, err = parseSQLiteTime(revokedAtRaw.String)
		if err != nil {
			return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("parse durable agent remote enrollment revoked_at: %w", err)
		}
	}
	return core.NormalizeDurableAgentRemoteEnrollment(enrollment), nil
}
