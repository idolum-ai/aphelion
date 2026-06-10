//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
)

const (
	reflectionSemanticWeight   = 0.10
	reflectionUnresolvedWeight = 0.15
	nocturneSignalWeight       = 0.05
)

func (r *Runtime) recordReflectionInteriorSignals(source string, sections map[string]string, evidence []session.RecordReference, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	inputs := reflectionInteriorSignalInputs(source, sections, evidence, now)
	if len(inputs) == 0 {
		return nil
	}
	_, err := r.store.RecordInteriorSignalObservations(heartbeatSignalKey(), inputs, now)
	return err
}

func reflectionInteriorSignalInputs(source string, sections map[string]string, baseEvidence []session.RecordReference, now time.Time) []session.InteriorSignalObservationInput {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "heartbeat_reflection"
	}
	stores := []string{
		memstore.StoreQuestions,
		memstore.StoreDecisions,
		memstore.StoreKnowledge,
		memstore.StoreMemory,
		memstore.StoreRhizome,
	}
	out := make([]session.InteriorSignalObservationInput, 0, len(stores))
	for _, store := range stores {
		content := strings.TrimSpace(sections[store])
		if content == "" {
			continue
		}
		line := firstInteriorSignalLine(content)
		if line == "" {
			continue
		}
		category := hiddenInputSemanticRecurrence
		weight := reflectionSemanticWeight
		prefix := "reflection kept durable context"
		if store == memstore.StoreQuestions {
			category = hiddenInputUnresolvedMemory
			weight = reflectionUnresolvedWeight
			prefix = "reflection kept an unresolved thread"
		}
		evidence := append([]session.RecordReference(nil), baseEvidence...)
		evidence = append(evidence, session.RecordReference{Kind: "memory_store", Ref: store, Label: "heartbeat reflection"})
		summary := compactSignalText(prefix+": "+line, 180)
		out = append(out, session.InteriorSignalObservationInput{
			Category:          category,
			SubjectKey:        runtimeInteriorSignalSubject(source + ":" + store),
			Summary:           summary,
			Source:            source,
			Evidence:          evidence,
			SourceFingerprint: shortRuntimeHash(source, store, content),
			Weight:            weight,
			Confidence:        0.6,
			ObservedAt:        now,
		})
	}
	return out
}

func reflectionProposalEvidence(ids []string) []session.RecordReference {
	refs := make([]session.RecordReference, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		refs = append(refs, session.RecordReference{Kind: "memory_proposal", Ref: id, Label: "heartbeat reflection proposal"})
	}
	return refs
}

func firstInteriorSignalLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if line != "" {
			return line
		}
	}
	return ""
}

func runtimeInteriorSignalSubject(text string) string {
	tokens := hiddenInputTokens(text)
	if len(tokens) == 0 {
		tokens = []string{"signal"}
	}
	limit := minInt(len(tokens), 4)
	hash := strings.TrimPrefix(shortRuntimeHash(text), "sha256:")
	return strings.Join(tokens[:limit], "-") + "-" + hash[:8]
}

type nocturneInteriorSignalOutput struct {
	Record     bool    `json:"record"`
	Category   string  `json:"category"`
	SubjectKey string  `json:"subject_key"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
}

func (r *Runtime) recordNocturneInteriorSignal(ctx context.Context, date string, artifactPath string, text string, now time.Time) error {
	if r == nil || r.store == nil || r.provider == nil {
		return nil
	}
	out, ok, err := r.classifyNocturneInteriorSignal(ctx, date, text)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	evidence := []session.RecordReference{{
		Kind:  "nocturne_artifact",
		Ref:   r.nocturneArtifactEvidenceRef(artifactPath),
		Label: "Nocturne " + strings.TrimSpace(date),
	}}
	unlock := r.lockSession(heartbeatSignalKey())
	defer unlock()
	_, err = r.store.RecordInteriorSignalObservations(heartbeatSignalKey(), []session.InteriorSignalObservationInput{{
		Category:          out.Category,
		SubjectKey:        out.SubjectKey,
		Summary:           out.Summary,
		Source:            "nocturne",
		Evidence:          evidence,
		SourceFingerprint: shortRuntimeHash("nocturne", date, artifactPath, out.Category, out.SubjectKey, out.Summary),
		Weight:            nocturneSignalWeight,
		Confidence:        out.Confidence,
		ObservedAt:        now,
	}}, now)
	return err
}

func (r *Runtime) classifyNocturneInteriorSignal(ctx context.Context, date string, text string) (nocturneInteriorSignalOutput, bool, error) {
	resp, err := r.provider.Complete(ctx, []agent.Message{
		{
			Role: "system",
			Content: strings.Join([]string{
				"You are Aphelion's Nocturne interior signal classifier.",
				"Read the private Nocturne artifact and decide whether it contains one stable, reusable interior signal.",
				"Do not infer facts from mood alone. Abstain unless the artifact names a recurring theme, unresolved thread, or durable concern.",
				"Return only JSON: {\"record\":true|false,\"category\":\"semantic_recurrence|unresolved_memory_state\",\"subject_key\":\"stable-kebab-case\",\"summary\":\"one grounded sentence\",\"confidence\":0.0}",
			}, "\n"),
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("date=%s\nartifact:\n%s", strings.TrimSpace(date), strings.TrimSpace(text)),
		},
	}, nil)
	if err != nil {
		return nocturneInteriorSignalOutput{}, false, fmt.Errorf("classify nocturne interior signal: %w", err)
	}
	if resp == nil {
		return nocturneInteriorSignalOutput{}, false, nil
	}
	return parseNocturneInteriorSignalOutput(resp.Content)
}

func parseNocturneInteriorSignalOutput(raw string) (nocturneInteriorSignalOutput, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nocturneInteriorSignalOutput{}, false, nil
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return nocturneInteriorSignalOutput{}, false, nil
	}
	var out nocturneInteriorSignalOutput
	if err := json.Unmarshal([]byte(raw[start:end+1]), &out); err != nil {
		return nocturneInteriorSignalOutput{}, false, nil
	}
	if !out.Record {
		return nocturneInteriorSignalOutput{}, false, nil
	}
	out.Category = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(out.Category)), " ", "_")
	switch out.Category {
	case hiddenInputSemanticRecurrence, hiddenInputUnresolvedMemory:
	default:
		return nocturneInteriorSignalOutput{}, false, nil
	}
	out.Summary = compactSignalText(out.Summary, 180)
	if out.Summary == "" {
		return nocturneInteriorSignalOutput{}, false, nil
	}
	out.SubjectKey = strings.TrimSpace(out.SubjectKey)
	if out.SubjectKey == "" {
		out.SubjectKey = runtimeInteriorSignalSubject(out.Summary)
	}
	if out.Confidence <= 0 {
		out.Confidence = 0.45
	}
	if out.Confidence > 1 {
		out.Confidence = 1
	}
	return out, true, nil
}

func (r *Runtime) nocturneArtifactEvidenceRef(path string) string {
	path = strings.TrimSpace(path)
	root := ""
	if r != nil && r.cfg != nil {
		root = strings.TrimSpace(r.cfg.Agent.SharedMemoryRoot)
	}
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}

func heartbeatSignalKey() session.SessionKey {
	return session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}
}
