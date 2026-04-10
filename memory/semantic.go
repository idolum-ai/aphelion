//go:build linux

package memory

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type SemanticMode string

const (
	SemanticModeInteractive SemanticMode = "interactive"
	SemanticModeHeartbeat   SemanticMode = "heartbeat"
)

type SemanticOptions struct {
	Enabled             bool
	Sources             []string
	IncludeDailyNotes   bool
	IncludeQuestions    bool
	IncludeRhizome      bool
	InteractiveTopK     int
	HeartbeatTopK       int
	InteractiveMaxChars int
	HeartbeatMaxChars   int
	DailyNotesDir       string
}

type SemanticSearchRequest struct {
	Root   string
	Scope  string
	Query  string
	Mode   SemanticMode
	Limit  int
	MaxLen int
	Now    time.Time
}

type SemanticHit struct {
	Source  string
	Scope   string
	Kind    string
	Score   float64
	Excerpt string
}

type SemanticEngine struct {
	opts SemanticOptions
}

func NewSemanticEngine(opts SemanticOptions) *SemanticEngine {
	return &SemanticEngine{opts: opts}
}

func (e *SemanticEngine) Enabled() bool {
	return e != nil && e.opts.Enabled
}

func (e *SemanticEngine) Search(_ context.Context, req SemanticSearchRequest) ([]SemanticHit, error) {
	if e == nil || !e.opts.Enabled {
		return nil, fmt.Errorf("semantic retrieval is not enabled")
	}
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("semantic search root is required")
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, fmt.Errorf("semantic search query is required")
	}
	mode := req.Mode
	if mode == "" {
		mode = SemanticModeInteractive
	}

	corpus, err := e.buildCorpus(root, req.Scope, mode, req.Now)
	if err != nil {
		return nil, err
	}
	if len(corpus) == 0 {
		return nil, nil
	}

	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	df := make(map[string]int)
	for _, chunk := range corpus {
		seen := make(map[string]struct{}, len(chunk.terms))
		for _, term := range chunk.terms {
			if _, ok := seen[term]; ok {
				continue
			}
			seen[term] = struct{}{}
			df[term]++
		}
	}

	queryVec := make(map[string]float64)
	for _, term := range queryTerms {
		queryVec[term]++
	}
	totalDocs := float64(len(corpus))
	for term, weight := range queryVec {
		queryVec[term] = weight * idf(totalDocs, float64(df[term]))
	}

	scored := make([]SemanticHit, 0, len(corpus))
	for _, chunk := range corpus {
		score := similarityScore(query, queryVec, totalDocs, df, chunk)
		if score <= 0 {
			continue
		}
		scored = append(scored, SemanticHit{
			Source:  chunk.source,
			Scope:   chunk.scope,
			Kind:    chunk.kind,
			Score:   score,
			Excerpt: chunk.text,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			if scored[i].Source == scored[j].Source {
				return scored[i].Excerpt < scored[j].Excerpt
			}
			return scored[i].Source < scored[j].Source
		}
		return scored[i].Score > scored[j].Score
	})

	limit := req.Limit
	if limit <= 0 {
		switch mode {
		case SemanticModeHeartbeat:
			limit = e.opts.HeartbeatTopK
		default:
			limit = e.opts.InteractiveTopK
		}
	}
	maxChars := req.MaxLen
	if maxChars <= 0 {
		switch mode {
		case SemanticModeHeartbeat:
			maxChars = e.opts.HeartbeatMaxChars
		default:
			maxChars = e.opts.InteractiveMaxChars
		}
	}

	out := make([]SemanticHit, 0, min(limit, len(scored)))
	chars := 0
	for _, hit := range scored {
		if len(out) >= limit {
			break
		}
		nextCost := len(hit.Excerpt) + len(hit.Source) + len(hit.Kind) + 48
		if len(out) > 0 && chars+nextCost > maxChars {
			break
		}
		out = append(out, hit)
		chars += nextCost
	}
	return out, nil
}

type semanticChunk struct {
	source string
	scope  string
	kind   string
	text   string
	terms  []string
}

func (e *SemanticEngine) buildCorpus(root string, scope string, mode SemanticMode, now time.Time) ([]semanticChunk, error) {
	sources := append([]string(nil), e.opts.Sources...)
	if e.opts.IncludeQuestions {
		sources = append(sources, "memory/questions.md")
	}
	if e.opts.IncludeRhizome {
		sources = append(sources, "memory/rhizome.md")
	}
	sources = uniqueStrings(sources)

	out := make([]semanticChunk, 0, len(sources)*4)
	for _, rel := range sources {
		path := filepath.Join(root, filepath.FromSlash(rel))
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read semantic source %s: %w", path, err)
		}
		chunks := chunkText(filepath.ToSlash(rel), detectSemanticKind(rel), string(raw), mode)
		for _, text := range chunks {
			terms := tokenize(text)
			if len(terms) == 0 {
				continue
			}
			out = append(out, semanticChunk{
				source: filepath.ToSlash(rel),
				scope:  scope,
				kind:   detectSemanticKind(rel),
				text:   text,
				terms:  terms,
			})
		}
	}

	if e.opts.IncludeDailyNotes {
		for _, rel := range semanticDailyNotePaths(e.opts.DailyNotesDir, mode, now) {
			path := filepath.Join(root, filepath.FromSlash(rel))
			raw, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("read semantic daily note %s: %w", path, err)
			}
			for _, text := range chunkText(rel, "daily_note", string(raw), mode) {
				terms := tokenize(text)
				if len(terms) == 0 {
					continue
				}
				out = append(out, semanticChunk{
					source: rel,
					scope:  scope,
					kind:   "daily_note",
					text:   text,
					terms:  terms,
				})
			}
		}
	}

	return out, nil
}

func semanticDailyNotePaths(notesDir string, mode SemanticMode, now time.Time) []string {
	dir := strings.TrimSpace(notesDir)
	if dir == "" {
		dir = "memory/daily"
	}
	days := 2
	if mode == SemanticModeHeartbeat {
		days = 7
	}
	out := make([]string, 0, days)
	for i := 0; i < days; i++ {
		out = append(out, filepath.ToSlash(filepath.Join(dir, now.AddDate(0, 0, -i).Format("2006-01-02")+".md")))
	}
	return out
}

func detectSemanticKind(source string) string {
	switch strings.ToLower(filepath.ToSlash(strings.TrimSpace(source))) {
	case "memory.md":
		return "memory"
	case "memory/knowledge.md":
		return "knowledge"
	case "memory/decisions.md":
		return "decision"
	case "memory/questions.md":
		return "question"
	case "memory/rhizome.md":
		return "rhizome"
	default:
		if strings.Contains(source, "daily/") || strings.Contains(source, "daily\\") {
			return "daily_note"
		}
		return "memory"
	}
}

func chunkText(source string, kind string, raw string, mode SemanticMode) []string {
	paragraphs := splitMarkdownParagraphs(raw)
	if len(paragraphs) == 0 {
		return nil
	}

	if mode == SemanticModeHeartbeat {
		return broaderChunks(paragraphs, 3)
	}

	switch kind {
	case "knowledge", "decision", "question", "rhizome", "daily_note":
		return paragraphs
	default:
		return broaderChunks(paragraphs, 2)
	}
}

func broaderChunks(parts []string, width int) []string {
	if len(parts) <= width {
		return []string{strings.Join(parts, "\n\n")}
	}
	out := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i += width {
		end := i + width
		if end > len(parts) {
			end = len(parts)
		}
		out = append(out, strings.Join(parts[i:end], "\n\n"))
	}
	return out
}

func splitMarkdownParagraphs(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	chunks := strings.Split(raw, "\n\n")
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		out = append(out, chunk)
	}
	return out
}

func similarityScore(query string, queryVec map[string]float64, totalDocs float64, df map[string]int, chunk semanticChunk) float64 {
	docVec := make(map[string]float64)
	for _, term := range chunk.terms {
		docVec[term]++
	}
	for term, weight := range docVec {
		docVec[term] = weight * idf(totalDocs, float64(df[term]))
	}

	score := cosineSimilarity(queryVec, docVec)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	lowerText := strings.ToLower(chunk.text)
	if lowerQuery != "" && strings.Contains(lowerText, lowerQuery) {
		score += 0.35
	}
	matchedTerms := 0
	seen := make(map[string]struct{}, len(chunk.terms))
	for _, term := range chunk.terms {
		seen[term] = struct{}{}
	}
	for term := range queryVec {
		if _, ok := seen[term]; ok {
			matchedTerms++
		}
	}
	score += float64(matchedTerms) * 0.03
	switch chunk.kind {
	case "knowledge", "decision":
		score += 0.05
	case "question", "rhizome":
		score -= 0.02
	}
	return score
}

func idf(totalDocs float64, df float64) float64 {
	return math.Log(1 + totalDocs/(1+df))
}

func cosineSimilarity(a map[string]float64, b map[string]float64) float64 {
	var dot float64
	var aNorm float64
	var bNorm float64
	for k, av := range a {
		aNorm += av * av
		if bv, ok := b[k]; ok {
			dot += av * bv
		}
	}
	for _, bv := range b {
		bNorm += bv * bv
	}
	if aNorm == 0 || bNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(aNorm) * math.Sqrt(bNorm))
}

func tokenize(raw string) []string {
	raw = strings.ToLower(raw)
	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	fields := strings.Fields(b.String())
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) <= 1 {
			continue
		}
		if _, skip := semanticStopwords[field]; skip {
			continue
		}
		out = append(out, field)
	}
	return out
}

var semanticStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "that": {}, "with": {}, "this": {}, "from": {},
	"into": {}, "what": {}, "when": {}, "where": {}, "which": {}, "while": {}, "have": {},
	"has": {}, "had": {}, "was": {}, "were": {}, "will": {}, "would": {}, "should": {},
	"could": {}, "about": {}, "there": {}, "their": {}, "them": {}, "they": {}, "then": {},
	"than": {}, "your": {}, "ours": {}, "our": {}, "you": {}, "not": {}, "but": {},
	"are": {}, "is": {}, "be": {}, "to": {}, "of": {}, "in": {}, "on": {}, "it": {},
	"as": {}, "or": {}, "at": {}, "by": {}, "an": {}, "if": {}, "we": {}, "do": {},
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
