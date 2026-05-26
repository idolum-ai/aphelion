//go:build linux

package standalonecli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/provider"
)

type liveAgencyEvalProviders struct {
	Subject    agent.ProviderWithOptions
	Judge      agent.ProviderWithOptions
	Model      string
	JudgeModel string
}

func loadLiveAgencyEvalProviders(t *testing.T) liveAgencyEvalProviders {
	t.Helper()

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
	judge := agent.ProviderWithOptions(subject)
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
	return liveAgencyEvalProviders{
		Subject:    subject,
		Judge:      judge,
		Model:      model,
		JudgeModel: judgeModel,
	}
}

func writeLiveAgencyEvalReportIfRequested(t *testing.T, suite string, report agencyEvalReport) {
	t.Helper()

	path := strings.TrimSpace(os.Getenv("APHELION_LIVE_EVAL_REPORT"))
	if path == "" {
		return
	}
	path = liveEvalReportPath(path, suite)
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal live eval report: %v", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create live eval report dir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write live eval report %s: %v", path, err)
	}
	t.Logf("wrote live eval report: %s", path)
}

func liveEvalReportPath(path string, suite string) string {
	suite = strings.TrimSpace(suite)
	if suite == "" {
		return path
	}
	ext := filepath.Ext(path)
	if ext == "" {
		return path + "." + suite
	}
	return strings.TrimSuffix(path, ext) + "." + suite + ext
}
