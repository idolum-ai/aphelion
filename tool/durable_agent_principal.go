//go:build linux

package tool

import (
	"database/sql"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func canonicalDurableAgentPrincipalIfKnown(store *session.SQLiteStore, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || store == nil {
		return value
	}
	if agentID, ok := core.DurableAgentIDFromPrincipal(value); ok {
		if _, err := store.DurableAgent(agentID); err == nil {
			return core.DurableAgentPrincipal(agentID)
		}
		return core.DurableAgentPrincipal(agentID)
	}
	if _, err := store.DurableAgent(value); err == nil {
		return core.DurableAgentPrincipal(value)
	} else if err != nil && err != sql.ErrNoRows {
		return value
	}
	return value
}

func durableAgentIDFromCapabilityPrincipal(value string) string {
	value = strings.TrimSpace(value)
	if agentID, ok := core.DurableAgentIDFromPrincipal(value); ok {
		return agentID
	}
	return value
}
