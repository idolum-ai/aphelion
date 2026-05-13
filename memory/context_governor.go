//go:build linux

package memory

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	adaptiveRecallCharsPerToken = 4

	RecallPurposeInteractive = "interactive"
	RecallPurposeHeartbeat   = "heartbeat"
	RecallPurposeRecovery    = "recovery"
	RecallPurposeDoctor      = "doctor"
)

type RecallMode string

const (
	RecallModeLean   RecallMode = "lean"
	RecallModeNormal RecallMode = "normal"
	RecallModeDeep   RecallMode = "deep"
	RecallModeDoctor RecallMode = "doctor"
)

type AdaptiveRecallRequest struct {
	Query            string
	Purpose          string
	ContextWindow    int
	MaxContextRatio  float64
	BaselineTopK     int
	BaselineMaxChars int
}

type AdaptiveRecallPlan struct {
	Mode        RecallMode
	Score       float64
	TopK        int
	MaxChars    int
	TokenBudget int
	Reasons     []string
}

func PlanAdaptiveRecall(req AdaptiveRecallRequest) AdaptiveRecallPlan {
	baselineTopK := req.BaselineTopK
	if baselineTopK <= 0 {
		baselineTopK = 5
	}
	baselineMaxChars := req.BaselineMaxChars
	if baselineMaxChars <= 0 {
		baselineMaxChars = 4000
	}

	purpose := normalizeRecallPurpose(req.Purpose)
	score, reasons := scoreAdaptiveRecall(req.Query, purpose)
	mode := recallModeForScore(score)
	if purpose == RecallPurposeDoctor {
		mode = RecallModeDoctor
	} else if purpose == RecallPurposeRecovery && mode == RecallModeNormal {
		mode = RecallModeDeep
	}

	poolTokens := adaptiveRecallPoolTokens(req.ContextWindow, req.MaxContextRatio)
	tokenBudget := adaptiveRecallTokenBudget(poolTokens, mode)
	maxChars := minInt(adaptiveRecallTargetChars(baselineMaxChars, mode), tokenBudget*adaptiveRecallCharsPerToken)
	if maxChars <= 0 {
		maxChars = minInt(400, maxInt(1, poolTokens*adaptiveRecallCharsPerToken))
	}

	return AdaptiveRecallPlan{
		Mode:        mode,
		Score:       math.Round(score*100) / 100,
		TopK:        adaptiveRecallTopK(baselineTopK, mode),
		MaxChars:    maxChars,
		TokenBudget: tokenBudget,
		Reasons:     reasons,
	}
}

func normalizeRecallPurpose(purpose string) string {
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case RecallPurposeHeartbeat:
		return RecallPurposeHeartbeat
	case RecallPurposeRecovery:
		return RecallPurposeRecovery
	case RecallPurposeDoctor:
		return RecallPurposeDoctor
	default:
		return RecallPurposeInteractive
	}
}

func recallModeForScore(score float64) RecallMode {
	switch {
	case score >= 0.65:
		return RecallModeDeep
	case score >= 0.25:
		return RecallModeNormal
	default:
		return RecallModeLean
	}
}

func adaptiveRecallPoolTokens(contextWindow int, maxContextRatio float64) int {
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	ratio := maxContextRatio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.90
	}
	usable := int(float64(contextWindow) * ratio)
	if usable <= 0 {
		usable = contextWindow
	}
	reserve := adaptiveRecallReserveTokens(contextWindow)
	if reserve > usable/2 {
		reserve = usable / 2
	}
	pool := usable - reserve
	if pool < 128 {
		pool = maxInt(1, usable)
	}
	return pool
}

func adaptiveRecallReserveTokens(contextWindow int) int {
	switch {
	case contextWindow >= 200000:
		return 30000
	case contextWindow >= 100000:
		return 18000
	case contextWindow >= 32000:
		return 8000
	default:
		return maxInt(256, contextWindow/4)
	}
}

func adaptiveRecallTokenBudget(poolTokens int, mode RecallMode) int {
	if poolTokens <= 0 {
		poolTokens = 512
	}
	var fraction float64
	var floor int
	switch mode {
	case RecallModeDoctor:
		fraction = 0.08
		floor = 4000
	case RecallModeDeep:
		fraction = 0.06
		floor = 1600
	case RecallModeNormal:
		fraction = 0.02
		floor = 800
	default:
		fraction = 0.005
		floor = 200
	}
	budget := int(float64(poolTokens) * fraction)
	if budget < floor {
		budget = floor
	}
	if budget > poolTokens {
		budget = poolTokens
	}
	return maxInt(1, budget)
}

func adaptiveRecallTargetChars(baselineMaxChars int, mode RecallMode) int {
	switch mode {
	case RecallModeDoctor:
		return maxInt(baselineMaxChars*5, 24000)
	case RecallModeDeep:
		return maxInt(baselineMaxChars*3, 12000)
	case RecallModeNormal:
		return baselineMaxChars
	default:
		return minPositive(maxInt(400, baselineMaxChars/4), 1200)
	}
}

func adaptiveRecallTopK(baselineTopK int, mode RecallMode) int {
	switch mode {
	case RecallModeDoctor:
		return minInt(maxInt(baselineTopK*3, 12), 24)
	case RecallModeDeep:
		return minInt(maxInt(baselineTopK*2, 8), 16)
	case RecallModeNormal:
		return minInt(maxInt(baselineTopK, 3), 10)
	default:
		return 1
	}
}

func scoreAdaptiveRecall(query string, purpose string) (float64, []string) {
	text := strings.TrimSpace(strings.ToLower(query))
	if text == "" {
		return 0, nil
	}
	terms := recallTerms(text)
	unique := uniqueTermCount(terms)

	score := 0.02
	reasons := make([]string, 0, 6)
	if len(text) >= 120 {
		score += 0.18
		reasons = append(reasons, "long_input")
	}
	if len(text) >= 600 {
		score += 0.12
		reasons = append(reasons, "large_input")
	}
	if len(terms) >= 12 {
		score += 0.16
		reasons = append(reasons, "multi_concept")
	}
	if len(terms) >= 40 {
		score += 0.12
		reasons = append(reasons, "many_concepts")
	}
	if len(terms) >= 8 {
		density := float64(unique) / float64(len(terms))
		if density >= 0.60 {
			score += 0.10
			reasons = append(reasons, "high_density")
		}
	}

	if hits := countSignalHits(text, recallContinuitySignals); hits > 0 {
		score += minFloat(0.20, float64(hits)*0.07)
		reasons = append(reasons, "continuity")
	}
	if hits := countSignalHits(text, recallTechnicalSignals); hits > 0 {
		score += minFloat(0.28, float64(hits)*0.06)
		reasons = append(reasons, "technical")
	}
	if hits := countSignalHits(text, recallActionSignals); hits > 0 {
		score += minFloat(0.22, float64(hits)*0.06)
		reasons = append(reasons, "work_request")
	}
	if purpose == RecallPurposeHeartbeat {
		score += 0.08
		reasons = append(reasons, "heartbeat")
	}
	if purpose == RecallPurposeRecovery {
		score += 0.18
		reasons = append(reasons, "recovery")
	}
	if purpose == RecallPurposeDoctor || strings.Contains(text, "/health diagnose") || strings.Contains(text, "deep diagnosis") {
		score = maxFloat(score, 0.95)
		reasons = append(reasons, "doctor")
	}

	if score > 1 {
		score = 1
	}
	sort.Strings(reasons)
	return score, uniqueStrings(reasons)
}

var recallContinuitySignals = []string{
	"again", "continue", "earlier", "forgot", "history", "last ", "last-", "previous", "recent", "session", "shutdown", "restart", "yesterday",
}

var recallTechnicalSignals = []string{
	"agent", "architecture", "code", "commit", "config", "context", "database", "error", "failure", "index", "log", "memory", "prompt", "retry", "semantic", "test", "timeout",
}

var recallActionSignals = []string{
	"debug", "diagnose", "fix", "implement", "investigate", "plan", "recommend", "review", "tdd", "verify",
}

func recallTerms(text string) []string {
	terms := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(strings.ToLower(term))
		if len(term) < 2 {
			continue
		}
		out = append(out, term)
	}
	return out
}

func uniqueTermCount(terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		seen[term] = struct{}{}
	}
	return len(seen)
}

func countSignalHits(text string, signals []string) int {
	hits := 0
	for _, signal := range signals {
		if strings.Contains(text, signal) {
			hits++
		}
	}
	return hits
}

func minPositive(value int, cap int) int {
	if value <= 0 {
		return cap
	}
	if cap > 0 && value > cap {
		return cap
	}
	return value
}

func maxFloat(a float64, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a float64, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
