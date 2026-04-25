//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	memstore "github.com/idolum-ai/aphelion/memory"
)

func (r *Runtime) memoryReflectionMode() string {
	if r == nil || r.cfg == nil {
		return "propose"
	}
	return normalizeMemoryWriteMode(r.cfg.Memory.WritePolicy.ReflectionWrites, "propose")
}

func (r *Runtime) memoryAggressiveMode() string {
	if r == nil || r.cfg == nil {
		return "propose"
	}
	return normalizeMemoryWriteMode(r.cfg.Memory.WritePolicy.AggressiveWrites, "propose")
}

func normalizeMemoryWriteMode(value string, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "apply", "propose":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return fallback
	}
}

func proposeMemorySections(scopeRoot string, scopeName string, sections map[string]string, sourceKind string, reason string, now time.Time) ([]string, error) {
	if strings.TrimSpace(scopeRoot) == "" || len(sections) == 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var proposalIDs []string
	for _, store := range []string{memstore.StoreMemory, memstore.StoreKnowledge, memstore.StoreDecisions, memstore.StoreQuestions, memstore.StoreRhizome} {
		content := strings.TrimSpace(sections[store])
		if content == "" {
			continue
		}
		proposal, err := memstore.CreateProposal(memstore.ProposalRequest{
			Root:       scopeRoot,
			Scope:      scopeName,
			Store:      store,
			SourceKind: sourceKind,
			Reason:     reason,
			Content:    content,
			Now:        now,
		})
		if err != nil {
			return nil, fmt.Errorf("propose %s memory: %w", store, err)
		}
		proposalIDs = append(proposalIDs, proposal.ID)
	}
	return proposalIDs, nil
}
