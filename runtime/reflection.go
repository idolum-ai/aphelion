//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

const (
	heartbeatReflectionMarker = "BEGIN_HEARTBEAT_REFLECTION"
	reflectionMemoryTag       = "[MEMORY]"
	reflectionMemoryEndTag    = "[/MEMORY]"
	reflectionKnowledgeTag    = "[KNOWLEDGE]"
	reflectionKnowledgeEndTag = "[/KNOWLEDGE]"
	reflectionDecisionsTag    = "[DECISIONS]"
	reflectionDecisionsEndTag = "[/DECISIONS]"
	reflectionQuestionsTag    = "[QUESTIONS]"
	reflectionQuestionsEndTag = "[/QUESTIONS]"
	reflectionRhizomeTag      = "[RHIZOME]"
	reflectionRhizomeEndTag   = "[/RHIZOME]"
)

type reflectionInput struct {
	Notes    []string
	Events   []session.ReviewEvent
	Semantic []memstore.SemanticHit
}

func (r *Runtime) reflectCuratedMemory(
	ctx context.Context,
	scopeRoot string,
	semanticScope string,
	semanticPrincipalID string,
	systemBlocks []agent.SystemBlock,
	since time.Time,
	now time.Time,
	events []session.ReviewEvent,
) (string, error) {
	input, err := r.loadReflectionInput(scopeRoot, semanticScope, semanticPrincipalID, since, now, events)
	if err != nil {
		return "", err
	}
	if len(input.Notes) == 0 && len(input.Events) == 0 && len(input.Semantic) == 0 {
		return "", nil
	}

	messages := []agent.Message{
		{Role: "system", Content: prompt.RenderSystemBlocks(systemBlocks), SystemBlocks: systemBlocks},
		{Role: "user", Content: renderReflectionRequest(input)},
	}
	provider := r.provider
	if slotProvider, _, ok := r.modelSlotProviderIncludingDefault(core.ModelSlotHeartbeat); ok {
		provider = slotProvider
	}
	if provider == nil {
		return "", fmt.Errorf("heartbeat reflection provider unavailable")
	}
	result, _, err := agent.RunTurn(ctx, provider, nil, &agent.Budget{
		Max:     2,
		Caution: 0.8,
		Warning: 0.9,
	}, r.reasoningOptionsForRun(session.TurnRunKindHeartbeat), messages)
	if err != nil {
		return "", err
	}

	sections := parseReflectionSections(result.Text)
	if len(sections) == 0 {
		return "", nil
	}
	scopeName := dynamicScopeName(scopeRoot)
	if r.memoryReflectionMode() == "propose" {
		proposalIDs, err := proposeMemorySections(scopeRoot, scopeName, sections, "reflection", "heartbeat_reflection", now)
		if err != nil {
			return "", err
		}
		if len(proposalIDs) == 0 {
			return "", nil
		}
		if err := r.recordReflectionInteriorSignals("heartbeat_reflection_proposed", sections, reflectionProposalEvidence(proposalIDs), now); err != nil {
			log.Printf("WARN reflection interior signal write failed: %v", err)
		}
		return fmt.Sprintf("Proposed curated memory updates for review: %s", strings.Join(proposalIDs, ", ")), nil
	}

	updatedStores := make([]string, 0, len(sections))
	for _, store := range []string{
		memstore.StoreMemory,
		memstore.StoreKnowledge,
		memstore.StoreDecisions,
		memstore.StoreQuestions,
	} {
		content := strings.TrimSpace(sections[store])
		if content == "" {
			continue
		}
		if _, err := memstore.ApplyWrite(memstore.WriteRequest{
			Root:      scopeRoot,
			Store:     store,
			Action:    "add",
			Content:   content,
			SourceTag: "reflection",
			SourceRef: "heartbeat_reflection",
			Scope:     scopeName,
		}); err != nil {
			return "", fmt.Errorf("write reflected %s memory: %w", store, err)
		}
		updatedStores = append(updatedStores, store)
	}

	if rhizomeContent := strings.TrimSpace(sections[memstore.StoreRhizome]); rhizomeContent != "" {
		if err := r.updateRhizome(scopeName, scopeRoot, rhizomeContent); err != nil {
			return "", err
		}
		updatedStores = append(updatedStores, memstore.StoreRhizome)
	}

	if len(updatedStores) == 0 {
		return "", nil
	}
	if err := r.recordReflectionInteriorSignals("heartbeat_reflection_applied", sections, nil, now); err != nil {
		log.Printf("WARN reflection interior signal write failed: %v", err)
	}
	return "Reflected curated memory updates for: " + strings.Join(updatedStores, ", "), nil
}

func (r *Runtime) updateRhizome(scopeName string, scopeRoot string, raw string) error {
	lines := strings.Split(raw, "\n")
	recorded := false
	for _, line := range lines {
		concepts := parseRhizomeConceptLine(line)
		if len(concepts) < 2 {
			continue
		}
		if err := r.store.RecordRhizomeEvent(scopeName, "heartbeat_reflection", 1.0, concepts); err != nil {
			return fmt.Errorf("record rhizome event: %w", err)
		}
		recorded = true
	}
	if !recorded {
		return nil
	}

	edges, err := r.store.TopRhizomeEdges(scopeName, 12)
	if err != nil {
		return fmt.Errorf("load rhizome projection: %w", err)
	}
	path, _, err := memstore.ResolveStorePath(scopeRoot, memstore.StoreRhizome)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create rhizome dir: %w", err)
	}
	return os.WriteFile(path, []byte(renderRhizomeProjection(edges)), 0o600)
}

func (r *Runtime) loadReflectionInput(scopeRoot string, semanticScope string, semanticPrincipalID string, since time.Time, now time.Time, events []session.ReviewEvent) (*reflectionInput, error) {
	out := &reflectionInput{
		Events: append([]session.ReviewEvent(nil), events...),
	}

	if !r.cfg.Agent.DailyNotes {
		return out, nil
	}
	notesDir := strings.TrimSpace(r.cfg.Agent.DailyNotesDir)
	if notesDir == "" {
		return out, nil
	}

	paths := []string{
		filepath.Join(scopeRoot, filepath.FromSlash(notesDir), now.Format("2006-01-02")+".md"),
		filepath.Join(scopeRoot, filepath.FromSlash(notesDir), now.AddDate(0, 0, -1).Format("2006-01-02")+".md"),
	}

	type note struct {
		path    string
		content string
	}
	notes := make([]note, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat daily note %s: %w", path, err)
		}
		if !since.IsZero() && !info.ModTime().After(since) {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read daily note %s: %w", path, err)
		}
		content := strings.TrimSpace(string(raw))
		if content == "" {
			continue
		}
		rel, _ := filepath.Rel(scopeRoot, path)
		notes = append(notes, note{
			path:    filepath.ToSlash(rel),
			content: content,
		})
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].path < notes[j].path })
	for _, note := range notes {
		out.Notes = append(out.Notes, "### "+note.path+"\n"+note.content)
	}
	if r.semantic != nil && r.semantic.Enabled() {
		query := semanticReflectionQuery(out)
		if strings.TrimSpace(query) != "" {
			hits, err := r.semantic.Search(context.Background(), memstore.SemanticSearchRequest{
				Root:        scopeRoot,
				Scope:       semanticScope,
				PrincipalID: semanticPrincipalID,
				Query:       query,
				Mode:        memstore.SemanticModeHeartbeat,
				Now:         now,
			})
			if err != nil {
				return nil, fmt.Errorf("semantic reflection search: %w", err)
			}
			out.Semantic = hits
		}
	}
	return out, nil
}

func semanticReflectionQuery(input *reflectionInput) string {
	if input == nil {
		return ""
	}
	parts := make([]string, 0, len(input.Notes)+len(input.Events))
	for _, note := range input.Notes {
		parts = append(parts, note)
	}
	for _, event := range input.Events {
		if summary := strings.TrimSpace(event.Summary); summary != "" {
			parts = append(parts, summary)
		}
	}
	joined := strings.Join(parts, "\n")
	joined = strings.TrimSpace(joined)
	if len(joined) > 3000 {
		joined = joined[:3000]
	}
	return joined
}

func renderReflectionRequest(input *reflectionInput) string {
	var b strings.Builder
	b.WriteString(heartbeatReflectionMarker)
	b.WriteString("\n## Role\n")
	b.WriteString("You are Aphelion's heartbeat reflection distiller.\n")
	b.WriteString("## Goal\n")
	b.WriteString("Distill the material below into compact curated memory updates that are durable, reusable, or likely to matter across future turns.\n")
	b.WriteString("## Success Criteria\n")
	b.WriteString("- Most material is ignored.\n")
	b.WriteString("- Kept items are specific, reusable, and grounded in supplied notes, review events, or semantic context.\n")
	b.WriteString("- Stable personal/context facts go to MEMORY.\n")
	b.WriteString("- Structured reusable facts go to KNOWLEDGE, with provenance tags when helpful.\n")
	b.WriteString("- Changed commitments or durable lessons go to DECISIONS.\n")
	b.WriteString("- Open threads worth carrying go to QUESTIONS.\n")
	b.WriteString("- Genuine concept links go to RHIZOME, not summaries.\n")
	b.WriteString("## Output\n")
	b.WriteString("Output only the tagged sections below. Omit prose outside the tags. Leave a section empty when there is nothing durable for it.\n")
	b.WriteString("## Stop Rules\n")
	b.WriteString("- Do not restate transient chatter, one-off pleasantries, or obvious summaries of the immediate exchange.\n")
	b.WriteString("- Do not invent facts, preferences, decisions, or unresolved questions beyond the supplied material.\n")
	b.WriteString("- If evidence is too thin, leave the matching section empty.\n\n")
	b.WriteString(reflectionMemoryTag + "\n" + reflectionMemoryEndTag + "\n")
	b.WriteString(reflectionKnowledgeTag + "\n" + reflectionKnowledgeEndTag + "\n")
	b.WriteString(reflectionDecisionsTag + "\n" + reflectionDecisionsEndTag + "\n")
	b.WriteString(reflectionQuestionsTag + "\n" + reflectionQuestionsEndTag + "\n")
	b.WriteString(reflectionRhizomeTag + "\n" + reflectionRhizomeEndTag + "\n\n")
	if len(input.Notes) > 0 {
		b.WriteString("## Daily Notes\n")
		for _, note := range input.Notes {
			b.WriteString(note)
			b.WriteString("\n\n")
		}
	}
	if len(input.Events) > 0 {
		b.WriteString("## Review Events\n")
		for _, event := range input.Events {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(event.Summary))
			b.WriteString("\n")
		}
	}
	if len(input.Semantic) > 0 {
		b.WriteString("\n## Semantic Context\n")
		for i, hit := range input.Semantic {
			provenance := strings.TrimSpace(hit.Provenance)
			if provenance == "" {
				provenance = "native"
			}
			fmt.Fprintf(&b, "%d. source=%s scope=%s", i+1, hit.Source, hit.Scope)
			if strings.TrimSpace(hit.PrincipalID) != "" {
				fmt.Fprintf(&b, " principal=%s", hit.PrincipalID)
			}
			fmt.Fprintf(&b, " kind=%s provenance=%s score=%.2f\n", hit.Kind, provenance, hit.Score)
			b.WriteString(hit.Excerpt)
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func parseReflectionSections(raw string) map[string]string {
	return map[string]string{
		memstore.StoreMemory:    extractTaggedSection(raw, reflectionMemoryTag, reflectionMemoryEndTag),
		memstore.StoreKnowledge: extractTaggedSection(raw, reflectionKnowledgeTag, reflectionKnowledgeEndTag),
		memstore.StoreDecisions: extractTaggedSection(raw, reflectionDecisionsTag, reflectionDecisionsEndTag),
		memstore.StoreQuestions: extractTaggedSection(raw, reflectionQuestionsTag, reflectionQuestionsEndTag),
		memstore.StoreRhizome:   extractTaggedSection(raw, reflectionRhizomeTag, reflectionRhizomeEndTag),
	}
}

func parseRhizomeConceptLine(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "-")
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	switch {
	case strings.Contains(line, "<->"):
		return splitRhizomeConcepts(line, "<->")
	case strings.Contains(line, "->"):
		return splitRhizomeConcepts(line, "->")
	case strings.Contains(line, ";"):
		return splitRhizomeFields(line, ";")
	case strings.Contains(line, ","):
		return splitRhizomeFields(line, ",")
	default:
		return nil
	}
}

func splitRhizomeConcepts(raw string, sep string) []string {
	parts := strings.Split(raw, sep)
	return splitRhizomeParts(parts)
}

func splitRhizomeFields(raw string, sep string) []string {
	parts := strings.Split(raw, sep)
	return splitRhizomeParts(parts)
}

func splitRhizomeParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func renderRhizomeProjection(edges []session.RhizomeEdge) string {
	lines := []string{
		"# rhizome.md",
		"",
		"Associative field projected from the internal rhizome graph.",
		"",
	}
	if len(edges) == 0 {
		lines = append(lines, "No strong associations yet.")
		return strings.Join(lines, "\n") + "\n"
	}
	for _, edge := range edges {
		lines = append(lines,
			fmt.Sprintf("- %s <-> %s [strength: %.2f, count: %d, state: %s]",
				edge.LeftConcept,
				edge.RightConcept,
				edge.Strength,
				edge.RecurrenceCount,
				edge.DecayState,
			),
		)
	}
	return strings.Join(lines, "\n") + "\n"
}

func dynamicScopeName(scopeRoot string) string {
	if strings.TrimSpace(scopeRoot) == "" {
		return "shared"
	}
	return filepath.Clean(scopeRoot)
}

func extractTaggedSection(raw string, startTag string, endTag string) string {
	start := strings.Index(raw, startTag)
	if start < 0 {
		return ""
	}
	start += len(startTag)
	end := strings.Index(raw[start:], endTag)
	if end < 0 {
		return ""
	}
	content := strings.TrimSpace(raw[start : start+end])
	switch strings.ToLower(content) {
	case "", "(none)", "none", "n/a":
		return ""
	default:
		return content
	}
}
