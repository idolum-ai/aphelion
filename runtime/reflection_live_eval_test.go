//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/session"
)

type reflectionLiveEvalCase struct {
	ID              string
	Input           *reflectionInput
	RequiredGroups  [][]string
	RejectedPhrases []string
}

type reflectionLiveEvalCaseResult struct {
	ID      string `json:"id"`
	Raw     string `json:"raw,omitempty"`
	Passed  bool   `json:"passed"`
	Failure string `json:"failure,omitempty"`
}

type reflectionLiveEvalReport struct {
	GeneratedAt string                         `json:"generated_at"`
	Model       string                         `json:"model"`
	Passed      bool                           `json:"passed"`
	Results     []reflectionLiveEvalCaseResult `json:"results"`
}

func TestLiveReflectionEvals(t *testing.T) {
	if os.Getenv("APHELION_LIVE_EVAL") != "1" {
		t.Skip("set APHELION_LIVE_EVAL=1 to run live OpenAI reflection evals")
	}

	cfg, configPath, err := loadMissionAskLiveEvalConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" {
		t.Skipf("providers.openai.api_key is not configured in %s", configPath)
	}
	model := firstRuntimeNonEmpty(os.Getenv("APHELION_LIVE_EVAL_MODEL"), cfg.Providers.OpenAI.Model)
	if strings.TrimSpace(model) == "" {
		t.Skipf("providers.openai.model is not configured in %s", configPath)
	}
	subject, err := provider.NewOpenAI(provider.OpenAIOptions{
		APIKey:     cfg.Providers.OpenAI.APIKey,
		BaseURL:    cfg.Providers.OpenAI.BaseURL,
		Model:      model,
		MaxTokens:  cfg.Providers.OpenAI.MaxTokens,
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
		UserAgent:  config.EffectiveUserAgent(cfg, ""),
	})
	if err != nil {
		t.Fatalf("new OpenAI provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report := reflectionLiveEvalReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Model:       model,
		Passed:      true,
	}
	for _, tc := range reflectionLiveEvalCases() {
		resp, err := subject.Complete(ctx, []agent.Message{{Role: "user", Content: renderReflectionRequest(tc.Input)}}, nil)
		result := reflectionLiveEvalCaseResult{ID: tc.ID}
		if err != nil {
			result.Failure = err.Error()
		} else if resp == nil {
			result.Failure = "empty response"
		} else {
			result.Raw = strings.TrimSpace(resp.Content)
			result = evaluateReflectionLiveEvalCase(tc, result)
		}
		if result.Failure != "" {
			report.Passed = false
		} else {
			result.Passed = true
		}
		report.Results = append(report.Results, result)
	}
	writeReflectionLiveEvalReportIfRequested(t, report)
	if !report.Passed {
		t.Fatalf("reflection live eval failed:\n%s", mustReflectionLiveEvalJSON(report))
	}
	t.Logf("reflection live eval passed with %d case(s)", len(report.Results))
}

func reflectionLiveEvalCases() []reflectionLiveEvalCase {
	return []reflectionLiveEvalCase{
		{
			ID: "specific_decisions_not_chatter",
			Input: &reflectionInput{
				Notes: []string{strings.Join([]string{
					"### daily/2026-05-26.md",
					"- The operator said thanks and moved on.",
					"- Decision: Telegram approval prompts in non-default threads must visibly include the thread prefix.",
					"- Decision: Progress detail mode should be remembered per run so Details/Summary toggles stay stable.",
				}, "\n")},
				Events: []session.ReviewEvent{
					{Summary: "Resolved that approval buttons should not hide thread identity."},
				},
			},
			RequiredGroups: [][]string{
				{"thread prefix", "thread identity", "non-default threads"},
				{"progress detail", "details/summary", "detail mode"},
			},
			RejectedPhrases: []string{"said thanks", "moved on", "transient chatter", "general updates"},
		},
		{
			ID: "event_supported_by_semantic_context",
			Input: &reflectionInput{
				Events: []session.ReviewEvent{
					{Summary: "Decision: eval reports are iteration evidence for prompt changes, not runtime authority."},
				},
				Semantic: []memstore.SemanticHit{
					{
						Source:     "memory/knowledge.md",
						Scope:      "shared",
						Kind:       "knowledge",
						Provenance: "approved_import",
						Score:      0.94,
						Excerpt:    "- Aphelion treats eval reports as iteration evidence, not runtime authority.",
					},
				},
			},
			RequiredGroups: [][]string{
				{"eval reports", "iteration evidence"},
				{"runtime authority", "not authority"},
			},
			RejectedPhrases: []string{"the user discussed", "important information", "various topics"},
		},
	}
}

func evaluateReflectionLiveEvalCase(tc reflectionLiveEvalCase, result reflectionLiveEvalCaseResult) reflectionLiveEvalCaseResult {
	if extraneous := reflectionLiveEvalExtraneousText(result.Raw); extraneous != "" {
		result.Failure = "extraneous text outside tags: " + strconvQuoteForEval(extraneous)
		return result
	}
	for _, tag := range []string{
		reflectionMemoryTag,
		reflectionMemoryEndTag,
		reflectionKnowledgeTag,
		reflectionKnowledgeEndTag,
		reflectionDecisionsTag,
		reflectionDecisionsEndTag,
		reflectionQuestionsTag,
		reflectionQuestionsEndTag,
		reflectionRhizomeTag,
		reflectionRhizomeEndTag,
	} {
		if !strings.Contains(result.Raw, tag) {
			result.Failure = "missing tag " + strconvQuoteForEval(tag)
			return result
		}
	}
	sections := parseReflectionSections(result.Raw)
	combined := strings.ToLower(strings.Join([]string{
		sections[memstore.StoreMemory],
		sections[memstore.StoreKnowledge],
		sections[memstore.StoreDecisions],
		sections[memstore.StoreQuestions],
		sections[memstore.StoreRhizome],
	}, "\n"))
	for _, group := range tc.RequiredGroups {
		if !reflectionLiveEvalContainsAny(combined, group) {
			result.Failure = "missing required concept group " + strings.Join(group, "|")
			return result
		}
	}
	for _, phrase := range tc.RejectedPhrases {
		if strings.Contains(combined, strings.ToLower(strings.TrimSpace(phrase))) {
			result.Failure = "included rejected phrase " + strconvQuoteForEval(phrase)
			return result
		}
	}
	return result
}

func reflectionLiveEvalContainsAny(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(value))) {
			return true
		}
	}
	return false
}

func reflectionLiveEvalExtraneousText(raw string) string {
	text := strings.TrimSpace(raw)
	text = strings.TrimPrefix(text, "```text")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	cursor := 0
	for _, pair := range [][2]string{
		{reflectionMemoryTag, reflectionMemoryEndTag},
		{reflectionKnowledgeTag, reflectionKnowledgeEndTag},
		{reflectionDecisionsTag, reflectionDecisionsEndTag},
		{reflectionQuestionsTag, reflectionQuestionsEndTag},
		{reflectionRhizomeTag, reflectionRhizomeEndTag},
	} {
		start := strings.Index(text[cursor:], pair[0])
		if start < 0 {
			return ""
		}
		before := strings.TrimSpace(text[cursor : cursor+start])
		if before != "" {
			return before
		}
		afterStart := cursor + start + len(pair[0])
		end := strings.Index(text[afterStart:], pair[1])
		if end < 0 {
			return ""
		}
		cursor = afterStart + end + len(pair[1])
	}
	return strings.TrimSpace(text[cursor:])
}

func writeReflectionLiveEvalReportIfRequested(t *testing.T, report reflectionLiveEvalReport) {
	t.Helper()

	path := strings.TrimSpace(os.Getenv("APHELION_LIVE_EVAL_REPORT"))
	if path == "" {
		return
	}
	ext := filepath.Ext(path)
	if ext == "" {
		path += ".reflection"
	} else {
		path = strings.TrimSuffix(path, ext) + ".reflection" + ext
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal reflection live eval report: %v", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create reflection live eval report dir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write reflection live eval report %s: %v", path, err)
	}
	t.Logf("wrote reflection live eval report: %s", path)
}

func mustReflectionLiveEvalJSON(report reflectionLiveEvalReport) string {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(raw)
}
