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
	case "scheduled_review":
		scheduled := cfg.ScheduledReviewConfig()
		if scheduled == nil {
			return nil
		}
		if strings.TrimSpace(scheduled.TimeUTC) != "" {
			if _, err := time.Parse("15:04", strings.TrimSpace(scheduled.TimeUTC)); err != nil {
				return fmt.Errorf("invalid scheduled_review time_utc %q: %w", scheduled.TimeUTC, err)
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
