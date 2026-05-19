//go:build linux

package standalonecli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/provider"
)

func TestLiveAgencySpectrumEvals(t *testing.T) {
	if os.Getenv("APHELION_LIVE_EVAL") != "1" {
		t.Skip("set APHELION_LIVE_EVAL=1 to run live OpenAI agency spectrum evals")
	}

	cfg, configPath, err := loadConfigForCommand(os.Getenv("APHELION_CONFIG"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" {
		t.Skipf("providers.openai.api_key is not configured in %s", configPath)
	}
	model := firstAgencyEvalNonEmpty(os.Getenv("APHELION_LIVE_EVAL_MODEL"), cfg.Providers.OpenAI.Model)
	if strings.TrimSpace(model) == "" {
		t.Skipf("providers.openai.model is not configured in %s", configPath)
	}
	judgeModel := firstAgencyEvalNonEmpty(os.Getenv("APHELION_LIVE_EVAL_JUDGE_MODEL"), model)

	httpClient := &http.Client{Timeout: 90 * time.Second}
	subject, err := provider.NewOpenAI(provider.OpenAIOptions{
		APIKey:     cfg.Providers.OpenAI.APIKey,
		BaseURL:    cfg.Providers.OpenAI.BaseURL,
		Model:      model,
		MaxTokens:  cfg.Providers.OpenAI.MaxTokens,
		HTTPClient: httpClient,
		UserAgent:  config.EffectiveUserAgent(cfg, ""),
	})
	if err != nil {
		t.Fatalf("new OpenAI subject provider: %v", err)
	}
	judge := subject
	if judgeModel != model {
		judge, err = provider.NewOpenAI(provider.OpenAIOptions{
			APIKey:     cfg.Providers.OpenAI.APIKey,
			BaseURL:    cfg.Providers.OpenAI.BaseURL,
			Model:      judgeModel,
			MaxTokens:  cfg.Providers.OpenAI.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
		if err != nil {
			t.Fatalf("new OpenAI judge provider: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	report, err := runAgencyEval(ctx, subject, judge, agencyEvalRunOptions{
		Profile:    agencyEvalProfileFull,
		Variant:    agencyEvalVariantCompare,
		Model:      model,
		JudgeModel: judgeModel,
		Now:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("live agency eval: %v", err)
	}
	t.Logf("agency eval summary: current_avg=%.2f baseline_avg=%.2f hard_failures=%d improved=%d regressed=%d",
		agencyEvalVariantAverage(report.Results, agencyEvalVariantCurrent),
		agencyEvalVariantAverage(report.Results, agencyEvalVariantBaseline),
		report.Summary.HardFailureCount,
		report.Summary.CompareImproved,
		report.Summary.CompareRegressed,
	)
	if failures := agencyEvalVariantHardFailures(report.Results, agencyEvalVariantCurrent); failures > 0 {
		t.Fatalf("current prompt produced %d hard failure(s):\n%s", failures, mustAgencyEvalJSON(report))
	}
	currentAvg := agencyEvalVariantAverage(report.Results, agencyEvalVariantCurrent)
	baselineAvg := agencyEvalVariantAverage(report.Results, agencyEvalVariantBaseline)
	if currentAvg < 3.5 {
		t.Fatalf("current prompt target average %.2f below release floor 3.50:\n%s", currentAvg, mustAgencyEvalJSON(report))
	}
	if currentAvg+0.50 < baselineAvg {
		t.Fatalf("current prompt target average %.2f materially below baseline %.2f:\n%s", currentAvg, baselineAvg, mustAgencyEvalJSON(report))
	}
}

func agencyEvalVariantAverage(results []agencyEvalCaseResult, variant string) float64 {
	total := 0.0
	count := 0
	for _, result := range results {
		if result.Variant != variant {
			continue
		}
		total += result.TargetAverage
		count++
	}
	if count == 0 {
		return 0
	}
	return roundAgencyEvalFloat(total / float64(count))
}

func agencyEvalVariantHardFailures(results []agencyEvalCaseResult, variant string) int {
	count := 0
	for _, result := range results {
		if result.Variant == variant && agencyEvalHardFailureCount(result.HardFailures) > 0 {
			count++
		}
	}
	return count
}

func mustAgencyEvalJSON(report agencyEvalReport) string {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(raw)
}
