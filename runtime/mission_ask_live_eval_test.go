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

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/session"
)

type missionAskLiveEvalCase struct {
	ID          string
	Observation missionAskObservation
	WantActions []string
	WantAsk     bool
}

type missionAskLiveEvalCaseResult struct {
	ID         string                     `json:"id"`
	Action     string                     `json:"action"`
	MissionID  string                     `json:"mission_id,omitempty"`
	Confidence string                     `json:"confidence,omitempty"`
	Question   string                     `json:"question,omitempty"`
	Reason     string                     `json:"reason,omitempty"`
	Raw        string                     `json:"raw,omitempty"`
	Passed     bool                       `json:"passed"`
	Failure    string                     `json:"failure,omitempty"`
	Expected   missionAskLiveEvalExpected `json:"expected"`
}

type missionAskLiveEvalExpected struct {
	Actions []string `json:"actions"`
	Ask     bool     `json:"ask"`
}

type missionAskLiveEvalReport struct {
	GeneratedAt string                         `json:"generated_at"`
	Model       string                         `json:"model"`
	Passed      bool                           `json:"passed"`
	Results     []missionAskLiveEvalCaseResult `json:"results"`
}

func TestLiveMissionAskClassifierEvals(t *testing.T) {
	if os.Getenv("APHELION_LIVE_EVAL") != "1" {
		t.Skip("set APHELION_LIVE_EVAL=1 to run live OpenAI Mission Question classifier evals")
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
	report := missionAskLiveEvalReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Model:       model,
		Passed:      true,
	}
	for _, tc := range missionAskLiveEvalCases() {
		resp, err := subject.Complete(ctx, missionAskClassifierMessages(tc.Observation), nil)
		result := missionAskLiveEvalCaseResult{
			ID: tc.ID,
			Expected: missionAskLiveEvalExpected{
				Actions: append([]string(nil), tc.WantActions...),
				Ask:     tc.WantAsk,
			},
		}
		if err != nil {
			result.Failure = err.Error()
		} else if resp == nil {
			result.Failure = "empty response"
		} else {
			result.Raw = strings.TrimSpace(resp.Content)
			result = evaluateMissionAskLiveEvalCase(tc, result)
		}
		if result.Failure != "" {
			report.Passed = false
		} else {
			result.Passed = true
		}
		report.Results = append(report.Results, result)
	}
	writeMissionAskLiveEvalReportIfRequested(t, report)
	if !report.Passed {
		t.Fatalf("mission ask live eval failed:\n%s", mustMissionAskLiveEvalJSON(report))
	}
	t.Logf("mission ask live eval passed with %d case(s)", len(report.Results))
}

func missionAskLiveEvalCases() []missionAskLiveEvalCase {
	return []missionAskLiveEvalCase{
		{
			ID: "same_objective_readme",
			Observation: missionAskObservation{
				Query:       "Please review the README docs again and keep the installation story tight.",
				MissionID:   "mission-readme",
				MissionName: "README cleanup",
				Question:    renderMissionAskQuestion("README cleanup", true),
				Confidence:  session.MissionAskConfidenceLow,
			},
			WantActions: []string{"same_objective"},
			WantAsk:     true,
		},
		{
			ID: "new_objective_provider_watch",
			Observation: missionAskObservation{
				Query:      "Every Friday, gather provider errors from the live service and summarize what we should tune next.",
				Question:   renderMissionAskQuestion("", false),
				Confidence: session.MissionAskConfidenceLow,
			},
			WantActions: []string{"new_objective"},
			WantAsk:     true,
		},
		{
			ID: "mundane_ack_ignore",
			Observation: missionAskObservation{
				Query:      "thanks, that makes sense. I will try it now.",
				Question:   renderMissionAskQuestion("", false),
				Confidence: session.MissionAskConfidenceLow,
			},
			WantActions: []string{"ignore", "unclear"},
			WantAsk:     false,
		},
		{
			ID: "wrong_candidate_becomes_new_objective",
			Observation: missionAskObservation{
				Query:       "Let's plan the Tailscale child install guide next.",
				MissionID:   "mission-readme",
				MissionName: "README cleanup",
				Question:    renderMissionAskQuestion("README cleanup", true),
				Confidence:  session.MissionAskConfidenceLow,
			},
			WantActions: []string{"new_objective"},
			WantAsk:     true,
		},
	}
}

func evaluateMissionAskLiveEvalCase(tc missionAskLiveEvalCase, result missionAskLiveEvalCaseResult) missionAskLiveEvalCaseResult {
	var out missionAskClassifierOutput
	if err := json.Unmarshal([]byte(extractMissionAskJSON(result.Raw)), &out); err != nil {
		result.Failure = "invalid JSON: " + err.Error()
		return result
	}
	action := strings.ToLower(strings.TrimSpace(out.Action))
	result.Action = action
	result.MissionID = strings.TrimSpace(out.MissionID)
	result.Confidence = strings.ToLower(strings.TrimSpace(out.Confidence))
	result.Question = strings.TrimSpace(out.Question)
	result.Reason = strings.TrimSpace(out.Reason)
	if !containsMissionAskLiveEvalString(tc.WantActions, action) {
		result.Failure = "unexpected action " + strconvQuoteForEval(action)
		return result
	}
	if action == "same_objective" && result.MissionID != "" && result.MissionID != tc.Observation.MissionID {
		result.Failure = "same_objective returned non-candidate mission_id " + strconvQuoteForEval(result.MissionID)
		return result
	}
	asks := action == "same_objective" || action == "new_objective"
	if asks != tc.WantAsk {
		result.Failure = "ask decision mismatch"
		return result
	}
	if asks && result.Question == "" {
		result.Failure = "ask action omitted question"
		return result
	}
	if result.Confidence != "" && result.Confidence != string(session.MissionAskConfidenceLow) && result.Confidence != string(session.MissionAskConfidenceHigh) {
		result.Failure = "invalid confidence " + strconvQuoteForEval(result.Confidence)
		return result
	}
	return result
}

func loadMissionAskLiveEvalConfig() (*config.Config, string, error) {
	path, err := config.ResolveConfigPath(os.Getenv("APHELION_CONFIG"))
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, path, err
	}
	return cfg, path, nil
}

func containsMissionAskLiveEvalString(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func writeMissionAskLiveEvalReportIfRequested(t *testing.T, report missionAskLiveEvalReport) {
	t.Helper()

	path := strings.TrimSpace(os.Getenv("APHELION_LIVE_EVAL_REPORT"))
	if path == "" {
		return
	}
	ext := filepath.Ext(path)
	if ext == "" {
		path += ".mission-ask"
	} else {
		path = strings.TrimSuffix(path, ext) + ".mission-ask" + ext
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal mission ask live eval report: %v", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create mission ask live eval report dir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write mission ask live eval report %s: %v", path, err)
	}
	t.Logf("wrote mission ask live eval report: %s", path)
}

func mustMissionAskLiveEvalJSON(report missionAskLiveEvalReport) string {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(raw)
}

func strconvQuoteForEval(text string) string {
	raw, _ := json.Marshal(text)
	return string(raw)
}
