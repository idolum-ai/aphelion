//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (s *SQLiteStore) UpsertDurableAgent(agent core.DurableAgent) error {
	_, err := upsertDurableAgentExec(s.db, agent)
	return err
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
	if _, err := exec.Exec(`DELETE FROM durable_agent_tombstones WHERE agent_id = ?`, agent.AgentID); err != nil {
		return core.DurableAgent{}, fmt.Errorf("clear durable agent tombstone: %w", err)
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

func (s *SQLiteStore) DeleteDurableAgent(agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("delete durable agent: agent_id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("delete durable agent: begin tombstone transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM durable_agents WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("delete durable agent: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO durable_agent_tombstones(agent_id, reason, created_at, updated_at)
		VALUES (?, 'deleted', datetime('now'), datetime('now'))
		ON CONFLICT(agent_id) DO UPDATE SET reason = excluded.reason, updated_at = excluded.updated_at
	`, agentID); err != nil {
		return fmt.Errorf("delete durable agent tombstone: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete durable agent: commit tombstone transaction: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DurableAgentTombstoned(agentID string) (bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false, nil
	}
	var existing string
	err := s.db.QueryRow(`SELECT agent_id FROM durable_agent_tombstones WHERE agent_id = ?`, agentID).Scan(&existing)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("query durable agent tombstone: %w", err)
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
