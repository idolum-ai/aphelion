//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type sqlQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
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
