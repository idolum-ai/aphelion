//go:build linux

package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

func checksumBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func checksumText(raw string) string {
	return checksumBytes([]byte(raw))
}

func chunkText(source string, kind string, raw string) []semanticChunkDraft {
	paragraphs := splitMarkdownParagraphs(raw)
	if len(paragraphs) == 0 {
		return nil
	}

	var chunks []string
	switch kind {
	case "knowledge", "decision", "question", "daily_note":
		chunks = paragraphs
	case "rhizome":
		chunks = broaderChunks(paragraphs, 2)
	default:
		chunks = broaderChunks(paragraphs, 2)
	}

	out := make([]semanticChunkDraft, 0, len(chunks))
	for i, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		out = append(out, semanticChunkDraft{
			ordinal: i,
			text:    chunk,
		})
	}
	return out
}

func classifySemanticSource(source string, kind string) string {
	switch kind {
	case "daily_note":
		return "daily_note"
	case "memory", "knowledge", "decision", "question", "rhizome", "dream":
		return "curated"
	default:
		if strings.Contains(filepath.ToSlash(source), "archive/") {
			return "archive"
		}
		return "curated"
	}
}

func detectImportedSemanticKind(sourcePath string, source string) string {
	kind := detectSemanticKind(sourcePath)
	if kind != "memory" {
		return kind
	}
	cleanPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(sourcePath)))
	switch {
	case cleanPath == "memory.md":
		return "memory"
	case strings.Contains(cleanPath, "/memory/knowledge.md"), strings.HasSuffix(cleanPath, "knowledge.md"):
		return "knowledge"
	case strings.Contains(cleanPath, "/memory/decisions.md"), strings.HasSuffix(cleanPath, "decisions.md"):
		return "decision"
	case strings.Contains(cleanPath, "/memory/questions.md"), strings.HasSuffix(cleanPath, "questions.md"):
		return "question"
	case strings.Contains(cleanPath, "/memory/rhizome.md"), strings.HasSuffix(cleanPath, "rhizome.md"):
		return "rhizome"
	case strings.Contains(cleanPath, "/memory/dreams.md"), strings.HasSuffix(cleanPath, "dreams.md"):
		return "dream"
	case strings.Contains(cleanPath, "/daily/"):
		return "daily_note"
	case strings.TrimSpace(source) != "" && strings.ToLower(strings.TrimSpace(source)) != "memory":
		return "imported_archive"
	default:
		return "imported_archive"
	}
}

func joinChunkTexts(chunks []semanticChunkDraft) string {
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		text := strings.TrimSpace(chunk.text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
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

func similarityScore(query string, queryVec map[string]float64, totalDocs float64, df map[string]int, chunk semanticChunk, mode SemanticMode, now time.Time) float64 {
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
	if chunk.kind == "daily_note" && withinDailyWindow(mode, now, chunk.source, chunk.mtime) {
		score += 0.01
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncate(raw string, max int) string {
	raw = strings.TrimSpace(raw)
	if max <= 0 || len(raw) <= max {
		return raw
	}
	if max <= 1 {
		return raw[:max]
	}
	return raw[:max-1] + "…"
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
