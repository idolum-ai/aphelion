//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

const (
	hiddenInputSemanticRecurrence = "semantic_recurrence"
	hiddenInputUnresolvedMemory   = "unresolved_memory_state"
	hiddenInputTemporalPressure   = "temporal_pressure"
	hiddenInputRetainedArtifacts  = "retained_artifact_context"
)

type hiddenInput struct {
	Category string
	Summary  string
	Claim    *core.InterpretationClaim
}

type hiddenInputSet struct {
	Inputs []hiddenInput
}

func (s *hiddenInputSet) add(category string, summary string) {
	s.addWithClaim(category, summary, nil)
}

func (s *hiddenInputSet) addWithClaim(category string, summary string, claim *core.InterpretationClaim) {
	category = strings.TrimSpace(category)
	summary = strings.TrimSpace(summary)
	if category == "" || summary == "" {
		return
	}
	for _, input := range s.Inputs {
		if input.Category == category {
			return
		}
	}
	var normalized *core.InterpretationClaim
	if claim != nil {
		value := core.NormalizeInterpretationClaim(*claim)
		if value.Active() {
			normalized = &value
		}
	}
	s.Inputs = append(s.Inputs, hiddenInput{Category: category, Summary: summary, Claim: normalized})
}

func (s *hiddenInputSet) addCore(input core.HiddenInput) {
	s.addWithClaim(input.Category, input.Summary, input.Claim)
}

func (s *hiddenInputSet) addCoreAll(inputs []core.HiddenInput) {
	for _, input := range inputs {
		s.addCore(input)
	}
}

func (s hiddenInputSet) Active() bool {
	return len(s.Inputs) > 0
}

func (s hiddenInputSet) Categories() []string {
	out := make([]string, 0, len(s.Inputs))
	for _, input := range s.Inputs {
		if strings.TrimSpace(input.Category) != "" {
			out = append(out, input.Category)
		}
	}
	sort.Strings(out)
	return out
}

func (s hiddenInputSet) has(category string) bool {
	for _, input := range s.Inputs {
		if input.Category == category {
			return true
		}
	}
	return false
}

func (s hiddenInputSet) ProvenanceSummary() string {
	if len(s.Inputs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(s.Inputs))
	for _, input := range s.Inputs {
		if strings.TrimSpace(input.Summary) == "" {
			continue
		}
		parts = append(parts, input.Summary)
	}
	if len(parts) == 0 {
		return ""
	}
	summary := strings.Join(parts, "; ")
	if len(summary) > 280 {
		summary = summary[:277] + "..."
	}
	return summary
}

func (s hiddenInputSet) ReflectiveOutreachEligible() bool {
	return s.has(hiddenInputSemanticRecurrence) && (s.has(hiddenInputUnresolvedMemory) || s.has(hiddenInputTemporalPressure))
}

func (s hiddenInputSet) Metadata() core.FloorMetadata {
	metadata := core.FloorMetadata{
		ProvenanceSummary: s.ProvenanceSummary(),
	}
	for _, input := range s.Inputs {
		metadata.HiddenInputs = append(metadata.HiddenInputs, core.HiddenInput{
			Category: input.Category,
			Summary:  input.Summary,
			Claim:    input.Claim,
		})
	}
	return metadata
}

func encodeFloorMetadata(metadata core.FloorMetadata) string {
	if metadata.Empty() {
		return ""
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (s hiddenInputSet) toTurnAwareness() turn.HiddenInputAwareness {
	return turn.HiddenInputAwareness{
		Active:            s.Active(),
		Categories:        append([]string(nil), s.Categories()...),
		ProvenanceSummary: strings.TrimSpace(s.ProvenanceSummary()),
	}
}

func (r *Runtime) assembleInteractiveHiddenInputs(ctx context.Context, key session.SessionKey, scope sandbox.Scope, now time.Time, userText string, priorFloorMetadata string) hiddenInputSet {
	root := dynamicPromptRoot(scope)
	query := strings.TrimSpace(userText)
	inputs := hiddenInputSet{}
	if query != "" {
		if summary := r.detectSemanticRecurrence(ctx, root, semanticScopeForPrincipal(scope.Principal), query, memstore.SemanticModeInteractive, now); summary != "" {
			inputs.add(hiddenInputSemanticRecurrence, summary)
		}
		if summary := detectOverlappingQuestions(root, query); summary != "" {
			inputs.add(hiddenInputUnresolvedMemory, summary)
		}
	}
	if summary := detectRetainedArtifactContext(priorFloorMetadata); summary != "" {
		inputs.add(hiddenInputRetainedArtifacts, summary)
	}
	if summary := r.pendingMissionAskHiddenInput(scope.Principal, key); summary != "" {
		inputs.add(hiddenInputMissionAsk, summary)
	}
	return inputs
}

func (r *Runtime) pendingMissionAskHiddenInput(actor principal.Principal, key session.SessionKey) string {
	if r == nil || r.store == nil {
		return ""
	}
	owner := missionCommandOwner(actor, actor.TelegramUserID)
	if owner == "" || owner == "system" {
		return ""
	}
	prompt, ok, err := r.store.PendingMissionAskPromptForSession(owner, key)
	if err != nil || !ok {
		return ""
	}
	target := strings.TrimSpace(prompt.MissionID)
	if target == "" {
		target = "possible new mission"
	}
	return "mission Ask Me prompt is awaiting the user's natural answer; prompt_id=" + prompt.ID + "; target=" + target + "; question=" + prompt.QuestionText
}

func detectRetainedArtifactContext(priorFloorMetadata string) string {
	metadata := strings.TrimSpace(priorFloorMetadata)
	if metadata == "" {
		return ""
	}
	var floor core.FloorMetadata
	if err := json.Unmarshal([]byte(metadata), &floor); err != nil {
		return ""
	}
	items := make([]string, 0, 3)
	for _, ref := range floor.Artifacts {
		retention := strings.TrimSpace(ref.Retention)
		if retention != "session_reference" && retention != "child_local" {
			continue
		}
		label := strings.TrimSpace(ref.Summary)
		if label == "" {
			label = strings.TrimSpace(ref.ArtifactID)
		}
		if label == "" {
			continue
		}
		if strings.TrimSpace(ref.MaterializedPath) != "" {
			label += " at " + strings.TrimSpace(ref.MaterializedPath)
		}
		items = append(items, label)
		if len(items) == 3 {
			break
		}
	}
	if len(items) == 0 {
		return ""
	}
	return "retained artifacts from the prior turn remain available: " + strings.Join(items, "; ")
}

func (r *Runtime) assembleHeartbeatHiddenInputs(ctx context.Context, scope sandbox.Scope, now time.Time, activeWindow bool, events []session.ReviewEvent) hiddenInputSet {
	root := dynamicPromptRoot(scope)
	inputs := hiddenInputSet{}

	if activeWindow {
		inputs.add(hiddenInputTemporalPressure, "active work window is open for reflective outreach")
	}

	query := heartbeatEventQuery(events)
	if summary := detectRecurringEventTheme(events); summary != "" {
		inputs.add(hiddenInputSemanticRecurrence, summary)
	} else if summary := r.detectLatentSemanticRecurrence(ctx, root, semanticScopeForPrincipal(scope.Principal), now); summary != "" {
		inputs.add(hiddenInputSemanticRecurrence, summary)
	} else if summary := r.detectSemanticRecurrence(ctx, root, semanticScopeForPrincipal(scope.Principal), query, memstore.SemanticModeHeartbeat, now); summary != "" {
		inputs.add(hiddenInputSemanticRecurrence, summary)
	}

	if summary := detectOpenQuestions(root); summary != "" {
		inputs.add(hiddenInputUnresolvedMemory, summary)
	}

	return inputs
}

func (r *Runtime) detectLatentSemanticRecurrence(ctx context.Context, root string, scope string, now time.Time) string {
	questions := loadMemoryBullets(filepath.Join(root, "memory", "questions.md"))
	noteTexts := r.loadRecentDailyNotes(root, now)
	texts := make([]string, 0, len(questions)+len(noteTexts))
	texts = append(texts, questions...)
	texts = append(texts, noteTexts...)

	if summary := detectRecurringTheme(texts, "latent state keeps converging around"); summary != "" {
		return summary
	}
	if len(questions) == 0 {
		return ""
	}
	query := strings.Join(questions[:minInt(2, len(questions))], "\n")
	return r.detectSemanticRecurrence(ctx, root, scope, query, memstore.SemanticModeHeartbeat, now)
}

func (r *Runtime) detectSemanticRecurrence(ctx context.Context, root string, scope string, query string, mode memstore.SemanticMode, now time.Time) string {
	if r == nil || r.semantic == nil || !r.semantic.Enabled() {
		return ""
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	semanticScope, principalID := splitSemanticScope(scope)
	hits, err := r.semantic.Search(ctx, memstore.SemanticSearchRequest{
		Root:        root,
		Scope:       semanticScope,
		PrincipalID: principalID,
		Query:       query,
		Mode:        mode,
		Limit:       3,
		MaxLen:      1200,
		Now:         now,
	})
	if err != nil || len(hits) == 0 {
		return ""
	}
	top := hits[0]
	return fmt.Sprintf("related prior material in %s is surfacing again", strings.TrimSpace(top.Source))
}

func semanticScopeForPrincipal(p principal.Principal) string {
	if p.Role == principal.RoleApprovedUser && p.TelegramUserID > 0 {
		return fmt.Sprintf("principal:%d", p.TelegramUserID)
	}
	return "shared"
}

func splitSemanticScope(scope string) (string, string) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "shared", ""
	}
	if strings.HasPrefix(scope, "principal:") {
		return "principal", strings.TrimSpace(strings.TrimPrefix(scope, "principal:"))
	}
	return scope, ""
}

func heartbeatEventQuery(events []session.ReviewEvent) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		if summary := strings.TrimSpace(event.Summary); summary != "" {
			parts = append(parts, summary)
		}
	}
	return strings.Join(parts, "\n")
}

func detectRecurringEventTheme(events []session.ReviewEvent) string {
	texts := make([]string, 0, len(events))
	for _, event := range events {
		if summary := strings.TrimSpace(event.Summary); summary != "" {
			texts = append(texts, summary)
		}
	}
	return detectRecurringTheme(texts, "pending review events keep converging around")
}

func detectRecurringTheme(texts []string, prefix string) string {
	if len(texts) < 2 {
		return ""
	}
	counts := make(map[string]int)
	for _, text := range texts {
		seen := make(map[string]struct{})
		for _, token := range hiddenInputTokens(text) {
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			counts[token]++
		}
	}
	type ranked struct {
		term  string
		count int
	}
	rankedTerms := make([]ranked, 0, len(counts))
	for term, count := range counts {
		if count < 2 {
			continue
		}
		rankedTerms = append(rankedTerms, ranked{term: term, count: count})
	}
	sort.Slice(rankedTerms, func(i, j int) bool {
		if rankedTerms[i].count == rankedTerms[j].count {
			return rankedTerms[i].term < rankedTerms[j].term
		}
		return rankedTerms[i].count > rankedTerms[j].count
	})
	if len(rankedTerms) == 0 {
		return ""
	}
	limit := 2
	if len(rankedTerms) < limit {
		limit = len(rankedTerms)
	}
	terms := make([]string, 0, limit)
	for _, entry := range rankedTerms[:limit] {
		terms = append(terms, entry.term)
	}
	return fmt.Sprintf("%s %s", strings.TrimSpace(prefix), strings.Join(terms, ", "))
}

func detectOpenQuestions(root string) string {
	lines := loadMemoryBullets(filepath.Join(root, "memory", "questions.md"))
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("open questions remain in memory/questions.md: %s", lines[0])
}

func detectOverlappingQuestions(root string, query string) string {
	queryTerms := tokenSet(hiddenInputTokens(query))
	if len(queryTerms) == 0 {
		return ""
	}
	for _, line := range loadMemoryBullets(filepath.Join(root, "memory", "questions.md")) {
		lineTerms := tokenSet(hiddenInputTokens(line))
		if len(lineTerms) == 0 {
			continue
		}
		overlap := 0
		for term := range lineTerms {
			if _, ok := queryTerms[term]; ok {
				overlap++
			}
		}
		if overlap >= 2 {
			return fmt.Sprintf("an open question overlaps with this turn: %s", line)
		}
	}
	return ""
}

func loadMemoryBullets(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "- "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		case line == "":
			continue
		}
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func (r *Runtime) loadRecentDailyNotes(root string, now time.Time) []string {
	if r == nil || r.cfg == nil || !r.cfg.Agent.DailyNotes {
		return nil
	}
	notesDir := strings.TrimSpace(r.cfg.Agent.DailyNotesDir)
	if notesDir == "" {
		return nil
	}
	paths := []string{
		filepath.Join(root, filepath.FromSlash(notesDir), now.Format("2006-01-02")+".md"),
		filepath.Join(root, filepath.FromSlash(notesDir), now.AddDate(0, 0, -1).Format("2006-01-02")+".md"),
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if text := strings.TrimSpace(string(raw)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func hiddenInputTokens(text string) []string {
	parts := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 4 {
			continue
		}
		if _, stop := hiddenInputStopwords[part]; stop {
			continue
		}
		out = append(out, part)
	}
	return out
}

func tokenSet(tokens []string) map[string]struct{} {
	out := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if strings.TrimSpace(token) != "" {
			out[token] = struct{}{}
		}
	}
	return out
}

var hiddenInputStopwords = map[string]struct{}{
	"about": {}, "after": {}, "again": {}, "been": {}, "before": {}, "being": {},
	"from": {}, "have": {}, "just": {}, "that": {}, "them": {}, "there": {},
	"they": {}, "this": {}, "what": {}, "when": {}, "where": {}, "which": {},
	"with": {}, "would": {}, "should": {}, "could": {}, "around": {}, "into": {},
	"because": {}, "their": {}, "turn": {}, "reply": {}, "user": {}, "message": {},
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
