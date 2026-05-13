//go:build linux

package tool

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func durableAgentReviewTargetsAgent(agentID string, scope session.ScopeRef) bool {
	agentID = strings.TrimSpace(agentID)
	return strings.TrimSpace(scope.DurableAgentID) == agentID || strings.TrimSpace(scope.ID) == agentID
}

func (r *Registry) resolveDurableAgent(raw string) (*core.DurableAgent, error) {
	agentID := strings.TrimSpace(raw)
	if agentID == "" {
		return nil, fmt.Errorf("durable_agent agent_id is required")
	}
	agent, err := r.store.DurableAgent(agentID)
	if err == nil {
		return agent, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	agents, listErr := r.store.ListDurableAgents()
	if listErr != nil {
		return nil, err
	}
	if matched := findDurableAgentCandidate(agents, agentID); matched != nil {
		return matched, nil
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("durable agent %q not found and no durable agents are registered", agentID)
	}
	return nil, fmt.Errorf("durable agent %q not found; available agent_ids: %s", agentID, strings.Join(durableAgentIDOptions(agents), ", "))
}

func findDurableAgentCandidate(agents []core.DurableAgent, raw string) *core.DurableAgent {
	normalized := normalizeDurableAgentReference(raw)
	if normalized == "" {
		return nil
	}
	var exact *core.DurableAgent
	exactCount := 0
	for i := range agents {
		if normalizeDurableAgentReference(agents[i].AgentID) == normalized {
			exact = &agents[i]
			exactCount++
		}
	}
	if exactCount == 1 {
		return exact
	}

	var fuzzy *core.DurableAgent
	fuzzyCount := 0
	for i := range agents {
		candidate := normalizeDurableAgentReference(agents[i].AgentID)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, normalized) || strings.Contains(normalized, candidate) {
			fuzzy = &agents[i]
			fuzzyCount++
		}
	}
	if fuzzyCount == 1 {
		return fuzzy
	}
	return nil
}

func durableAgentIDOptions(agents []core.DurableAgent) []string {
	if len(agents) == 0 {
		return nil
	}
	out := make([]string, 0, len(agents))
	for _, agent := range agents {
		id := strings.TrimSpace(agent.AgentID)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
