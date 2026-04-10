//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	memstore "github.com/idolum-ai/aphelion/memory"
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
	Notes  []string
	Events []session.ReviewEvent
}

func (r *Runtime) reflectCuratedMemory(
	ctx context.Context,
	scopeRoot string,
	systemPrompt string,
	since time.Time,
	now time.Time,
	events []session.ReviewEvent,
) (string, error) {
	input, err := r.loadReflectionInput(scopeRoot, since, now, events)
	if err != nil {
		return "", err
	}
	if len(input.Notes) == 0 && len(input.Events) == 0 {
		return "", nil
	}

	messages := []agent.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: renderReflectionRequest(input)},
	}
	result, _, err := agent.RunTurn(ctx, r.provider, nil, &agent.Budget{
		Max:     2,
		Caution: 0.8,
		Warning: 0.9,
	}, messages)
	if err != nil {
		return "", err
	}

	sections := parseReflectionSections(result.Text)
	if len(sections) == 0 {
		return "", nil
	}

	updatedStores := make([]string, 0, len(sections))
	for _, store := range []string{
		memstore.StoreMemory,
		memstore.StoreKnowledge,
		memstore.StoreDecisions,
		memstore.StoreQuestions,
		memstore.StoreRhizome,
	} {
		content := strings.TrimSpace(sections[store])
		if content == "" {
			continue
		}
		if _, err := memstore.ApplyWrite(memstore.WriteRequest{
			Root:    scopeRoot,
			Store:   store,
			Action:  "add",
			Content: content,
		}); err != nil {
			return "", fmt.Errorf("write reflected %s memory: %w", store, err)
		}
		updatedStores = append(updatedStores, store)
	}

	if len(updatedStores) == 0 {
		return "", nil
	}
	return "Reflected curated memory updates for: " + strings.Join(updatedStores, ", "), nil
}

func (r *Runtime) loadReflectionInput(scopeRoot string, since time.Time, now time.Time, events []session.ReviewEvent) (*reflectionInput, error) {
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
	return out, nil
}

func renderReflectionRequest(input *reflectionInput) string {
	var b strings.Builder
	b.WriteString(heartbeatReflectionMarker)
	b.WriteString("\nDistill the material below into compact curated memory updates.\n")
	b.WriteString("Output only the tagged sections below. Omit prose outside the tags.\n")
	b.WriteString("Use concise durable items only. Prefer provenance tags in knowledge when appropriate.\n\n")
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
