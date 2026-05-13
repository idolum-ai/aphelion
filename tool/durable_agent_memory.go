//go:build linux

package tool

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableMemoryCandidate struct {
	ID          string
	SourceStore string
	TargetStore string
	Content     string
	Score       int
	Reason      string
}

func (r *Registry) reviewDurableAgentMemoryDelegation(in durableAgentInput, scope sandbox.Scope) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for memory_review")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	parentRoot, _, err := resolveMemoryRoot(scope, "shared")
	if err != nil {
		return "", err
	}
	limit := 8
	if in.MemoryDelegation != nil && in.MemoryDelegation.Limit > 0 {
		limit = in.MemoryDelegation.Limit
	}
	if limit > 20 {
		limit = 20
	}
	candidates, err := durableMemoryCandidatesForAgent(parentRoot, *agent, limit)
	if err != nil {
		return "", err
	}
	return renderDurableAgentMemoryReview(*agent, candidates), nil
}

func (r *Registry) delegateDurableAgentMemory(
	ctx context.Context,
	in durableAgentInput,
	p principal.Principal,
	key session.SessionKey,
	scope sandbox.Scope,
) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for memory_delegate")
	}
	if in.MemoryDelegation == nil {
		return "", fmt.Errorf("durable_agent memory_delegate requires memory_delegation payload")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	parentRoot, _, err := resolveMemoryRoot(scope, "shared")
	if err != nil {
		return "", err
	}
	candidates, err := durableMemoryCandidatesForAgent(parentRoot, *agent, 200)
	if err != nil {
		return "", err
	}
	entries, err := buildDurableMemoryDelegationEntries(*in.MemoryDelegation, candidates)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("durable_agent memory_delegate requires at least one candidate_id or entry")
	}
	if r.durableMemoryDelegationApprover == nil {
		return "", fmt.Errorf("durable_agent memory_delegate requires an interactive admin approval channel")
	}
	approval, err := r.durableMemoryDelegationApprover.ConfirmDurableMemoryDelegation(ctx, DurableMemoryDelegationApprovalRequest{
		Principal:  p,
		SessionKey: key,
		Agent:      *agent,
		Reason:     strings.TrimSpace(in.MemoryDelegation.Reason),
		Entries:    entries,
	})
	if err != nil {
		return "", err
	}
	if !approval.Approved {
		return renderDurableAgentMemoryDelegate(*agent, entries, approval, 0), nil
	}

	childMemoryRoot, err := durableAgentMemoryRoot(*agent, r.store)
	if err != nil {
		return "", err
	}
	applied := 0
	for _, entry := range entries {
		if _, err := memstore.ApplyWrite(memstore.WriteRequest{
			Root:      childMemoryRoot,
			Store:     entry.TargetStore,
			Action:    "add",
			Content:   entry.Content,
			SourceTag: "delegated_from_parent",
		}); err != nil {
			return "", fmt.Errorf("delegate memory entry %q to %s: %w", entry.CandidateID, entry.TargetStore, err)
		}
		applied++
	}
	return renderDurableAgentMemoryDelegate(*agent, entries, approval, applied), nil
}

func buildDurableMemoryDelegationEntries(input durableAgentMemoryDelegationInput, candidates []durableMemoryCandidate) ([]DurableMemoryDelegationEntry, error) {
	candidateIndex := make(map[string]durableMemoryCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateIndex[strings.TrimSpace(candidate.ID)] = candidate
	}

	defaultTarget, err := normalizeDurableDelegationStore(strings.TrimSpace(input.TargetStore))
	if err != nil {
		return nil, err
	}

	out := make([]DurableMemoryDelegationEntry, 0, len(input.CandidateIDs)+len(input.Entries))
	appendCandidate := func(candidate durableMemoryCandidate, explicitTarget string) error {
		target := explicitTarget
		if target == "" {
			target = defaultTarget
		}
		if target == "" {
			target = strings.TrimSpace(candidate.TargetStore)
		}
		target, err = normalizeDurableDelegationStore(target)
		if err != nil {
			return err
		}
		out = append(out, DurableMemoryDelegationEntry{
			CandidateID: strings.TrimSpace(candidate.ID),
			SourceStore: strings.TrimSpace(candidate.SourceStore),
			TargetStore: target,
			Content:     strings.TrimSpace(candidate.Content),
		})
		return nil
	}

	for _, candidateID := range input.CandidateIDs {
		candidateID = strings.TrimSpace(candidateID)
		if candidateID == "" {
			continue
		}
		candidate, ok := candidateIndex[candidateID]
		if !ok {
			return nil, fmt.Errorf("memory candidate %q was not found; run memory_review again", candidateID)
		}
		if err := appendCandidate(candidate, ""); err != nil {
			return nil, err
		}
	}
	for _, entry := range input.Entries {
		if candidateID := strings.TrimSpace(entry.CandidateID); candidateID != "" {
			candidate, ok := candidateIndex[candidateID]
			if !ok {
				return nil, fmt.Errorf("memory candidate %q was not found; run memory_review again", candidateID)
			}
			if err := appendCandidate(candidate, strings.TrimSpace(entry.TargetStore)); err != nil {
				return nil, err
			}
			continue
		}
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}
		sourceStore := strings.TrimSpace(entry.SourceStore)
		if sourceStore == "" {
			sourceStore = "knowledge"
		}
		sourceStore, err = normalizeDurableDelegationStore(sourceStore)
		if err != nil {
			return nil, err
		}
		targetStore := strings.TrimSpace(entry.TargetStore)
		if targetStore == "" {
			targetStore = defaultTarget
		}
		if targetStore == "" {
			targetStore = sourceStore
		}
		targetStore, err = normalizeDurableDelegationStore(targetStore)
		if err != nil {
			return nil, err
		}
		out = append(out, DurableMemoryDelegationEntry{
			SourceStore: sourceStore,
			TargetStore: targetStore,
			Content:     content,
		})
	}
	if len(out) == 0 {
		return nil, nil
	}
	seen := map[string]struct{}{}
	deduped := make([]DurableMemoryDelegationEntry, 0, len(out))
	for _, entry := range out {
		key := strings.ToLower(strings.TrimSpace(entry.TargetStore)) + "|" + strings.TrimSpace(entry.Content)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, entry)
	}
	return deduped, nil
}

func durableAgentMemoryRoot(agent core.DurableAgent, store *session.SQLiteStore) (string, error) {
	_, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if memoryRoot == "" && store != nil {
		if dbPath := strings.TrimSpace(store.DBPath()); dbPath != "" {
			_, memoryRoot = durableagent.DefaultLocalRoots(dbPath, strings.TrimSpace(agent.AgentID))
		}
	}
	if strings.TrimSpace(memoryRoot) == "" {
		return "", fmt.Errorf("durable agent %q has no local memory root", strings.TrimSpace(agent.AgentID))
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		return "", fmt.Errorf("create durable agent memory root: %w", err)
	}
	return memoryRoot, nil
}

func durableMemoryCandidatesForAgent(parentRoot string, agent core.DurableAgent, limit int) ([]durableMemoryCandidate, error) {
	stores := []string{memstore.StoreKnowledge, memstore.StoreDecisions, memstore.StoreQuestions, memstore.StoreMemory}
	keywords := durableMemoryKeywords(agent)
	candidates := make([]durableMemoryCandidate, 0)
	for _, store := range stores {
		entries, err := loadDurableMemoryStoreEntries(parentRoot, store)
		if err != nil {
			return nil, err
		}
		for i, entry := range entries {
			id := fmt.Sprintf("%s:%d", store, i+1)
			score, reason := scoreDurableMemoryCandidate(entry, store, keywords)
			candidates = append(candidates, durableMemoryCandidate{
				ID:          id,
				SourceStore: store,
				TargetStore: store,
				Content:     entry,
				Score:       score,
				Reason:      reason,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].SourceStore != candidates[j].SourceStore {
			return candidates[i].SourceStore < candidates[j].SourceStore
		}
		return candidates[i].ID < candidates[j].ID
	})
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	if limit == 0 {
		return nil, nil
	}
	return candidates[:limit], nil
}

func loadDurableMemoryStoreEntries(root string, store string) ([]string, error) {
	path, normalizedStore, err := memstore.ResolveStorePath(root, store)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read parent memory store %s: %w", path, err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, nil
	}
	switch normalizedStore {
	case memstore.StoreMemory:
		return parseDurableMemoryParagraphs(text), nil
	default:
		return parseDurableMemoryBullets(text), nil
	}
}

func parseDurableMemoryParagraphs(raw string) []string {
	chunks := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n\n")
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		clean := strings.TrimSpace(chunk)
		if clean == "" || strings.HasPrefix(clean, "#") {
			continue
		}
		out = append(out, compactWhitespace(clean))
	}
	return out
}

func parseDurableMemoryBullets(raw string) []string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			line = strings.TrimSpace(line[2:])
		}
		if line == "" {
			continue
		}
		out = append(out, compactWhitespace(line))
	}
	return out
}

func durableMemoryKeywords(agent core.DurableAgent) []string {
	seed := strings.Join([]string{
		strings.TrimSpace(agent.AgentID),
		strings.TrimSpace(agent.ChannelKind),
		strings.TrimSpace(agent.LivePolicy.Charter),
		strings.Join(agent.LivePolicy.CapabilityEnvelope, " "),
	}, " ")
	parts := strings.FieldsFunc(strings.ToLower(seed), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(parts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 4 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func scoreDurableMemoryCandidate(content string, store string, keywords []string) (int, string) {
	score := 1
	matches := make([]string, 0, 4)
	lower := strings.ToLower(strings.TrimSpace(content))
	for _, keyword := range keywords {
		if keyword == "" || !strings.Contains(lower, keyword) {
			continue
		}
		score++
		if len(matches) < 3 {
			matches = append(matches, keyword)
		}
	}
	if len(matches) == 0 {
		return score, "general " + strings.TrimSpace(store) + " context"
	}
	return score, "matches child context: " + strings.Join(matches, ", ")
}

func normalizeDurableDelegationStore(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case memstore.StoreMemory:
		return memstore.StoreMemory, nil
	case memstore.StoreKnowledge:
		return memstore.StoreKnowledge, nil
	case memstore.StoreDecisions:
		return memstore.StoreDecisions, nil
	case memstore.StoreQuestions:
		return memstore.StoreQuestions, nil
	case memstore.StoreRhizome:
		return memstore.StoreRhizome, nil
	default:
		return "", fmt.Errorf("unsupported delegation store %q", value)
	}
}

func renderDurableAgentMemoryReview(agent core.DurableAgent, candidates []durableMemoryCandidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent memory review\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "candidate_count: %d\n", len(candidates))
	if len(candidates) == 0 {
		b.WriteString("candidates: -\n")
		b.WriteString("next: add parent memory context, then run memory_review again\n")
		return b.String()
	}
	b.WriteString("candidates:\n")
	for _, candidate := range candidates {
		fmt.Fprintf(
			&b,
			"- candidate_id=%s source_store=%s target_store=%s score=%d reason=%s text=%s\n",
			strings.TrimSpace(candidate.ID),
			strings.TrimSpace(candidate.SourceStore),
			strings.TrimSpace(candidate.TargetStore),
			candidate.Score,
			compactWhitespace(candidate.Reason),
			truncateCompact(candidate.Content, 180),
		)
	}
	b.WriteString("next: memory_delegate\n")
	return b.String()
}

func renderDurableAgentMemoryDelegate(agent core.DurableAgent, entries []DurableMemoryDelegationEntry, approval DurableMemoryDelegationApprovalDecision, applied int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent memory delegate\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "approved: %t\n", approval.Approved)
	fmt.Fprintf(&b, "timed_out: %t\n", approval.TimedOut)
	changed := approval.Approved && applied > 0
	fmt.Fprintf(&b, "changed: %t\n", changed)
	fmt.Fprintf(&b, "delegated_count: %d\n", applied)
	b.WriteString("entries:\n")
	for _, entry := range entries {
		fmt.Fprintf(
			&b,
			"- candidate_id=%s source_store=%s target_store=%s text=%s\n",
			firstNonEmpty(strings.TrimSpace(entry.CandidateID), "-"),
			firstNonEmpty(strings.TrimSpace(entry.SourceStore), "-"),
			firstNonEmpty(strings.TrimSpace(entry.TargetStore), "-"),
			truncateCompact(entry.Content, 180),
		)
	}
	if !approval.Approved {
		b.WriteString("next: update memory_delegation payload and request approval again\n")
	}
	return b.String()
}

func compactWhitespace(raw string) string {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func truncateCompact(raw string, limit int) string {
	clean := compactWhitespace(raw)
	if limit <= 0 || len(clean) <= limit {
		return clean
	}
	if limit <= 3 {
		return clean[:limit]
	}
	return clean[:limit-3] + "..."
}
