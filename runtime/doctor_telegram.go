//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) telegramDoctorReport(ctx context.Context, key session.SessionKey, exec pipeline.TurnExecutionContract, systemPrompt string, systemBlocks []agent.SystemBlock, report string, progress *toolProgressReporter) (string, core.TokenUsage) {
	report = strings.TrimSpace(redactDoctorText(report))
	if report == "" {
		return doctorReportFallbackText, core.TokenUsage{}
	}
	if doctorCharCount(report) <= doctorTelegramMaxChars {
		return report, core.TokenUsage{}
	}
	surfaceDoctorProgress(ctx, progress, "Condensing the doctor report for one Telegram message")
	limitText := strconv.Itoa(doctorTelegramMaxChars)
	input := []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "system", Content: doctorTelegramSummarySystemNote()},
		{Role: "user", Content: strings.Join([]string{
			doctorSummaryMarker,
			"telegram_hard_limit_chars=" + strconv.Itoa(doctorTelegramHardLimit),
			"service_single_message_limit_chars=" + limitText,
			"full_report_chars=" + strconv.Itoa(doctorCharCount(report)),
			"",
			"Full report to condense:",
			report,
		}, "\n")},
	}
	r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptStarted, "provider", "started", map[string]any{
		"backend":              strings.TrimSpace(exec.Backend),
		"provider":             strings.TrimSpace(exec.ProviderName),
		"model":                strings.TrimSpace(exec.ModelName),
		"provider_path":        strings.Join(exec.ProviderPath, ","),
		"run_kind":             string(session.TurnRunKindDoctor),
		"doctor_summary_stage": "telegram_condense",
		"target_chars":         doctorTelegramMaxChars,
	}, time.Now().UTC())
	turnResult, _, err := agent.RunTurn(ctx, exec.Provider, nil, &agent.Budget{
		Max:     2,
		Caution: 0.7,
		Warning: 0.9,
	}, r.reasoningOptionsForRun(session.TurnRunKindDoctor), input)
	if err != nil {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":              strings.TrimSpace(exec.Backend),
			"provider":             strings.TrimSpace(exec.ProviderName),
			"model":                strings.TrimSpace(exec.ModelName),
			"error":                trimError(err.Error()),
			"run_kind":             string(session.TurnRunKindDoctor),
			"doctor_summary_stage": "telegram_condense",
		}, time.Now().UTC())
		r.reportOperationalIssueAsync("doctor_summary", err)
		return doctorFitTelegramReport(report, doctorTelegramMaxChars), core.TokenUsage{}
	}
	if turnResult == nil {
		err := fmt.Errorf("doctor telegram summary returned no turn result")
		r.reportOperationalIssueAsync("doctor_summary", err)
		return doctorFitTelegramReport(report, doctorTelegramMaxChars), core.TokenUsage{}
	}
	if strings.TrimSpace(turnResult.ProviderFailure) != "" {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":              strings.TrimSpace(exec.Backend),
			"provider":             strings.TrimSpace(exec.ProviderName),
			"model":                strings.TrimSpace(exec.ModelName),
			"error":                trimError(turnResult.ProviderFailure),
			"run_kind":             string(session.TurnRunKindDoctor),
			"doctor_summary_stage": "telegram_condense",
		}, time.Now().UTC())
		r.reportOperationalIssueAsync("doctor_summary", fmt.Errorf("%s", strings.TrimSpace(turnResult.ProviderFailure)))
		return doctorFitTelegramReport(report, doctorTelegramMaxChars), turnResult.TokenUsage
	}
	r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptSucceeded, "provider", "succeeded", map[string]any{
		"backend":              strings.TrimSpace(exec.Backend),
		"provider":             strings.TrimSpace(exec.ProviderName),
		"model":                strings.TrimSpace(exec.ModelName),
		"run_kind":             string(session.TurnRunKindDoctor),
		"doctor_summary_stage": "telegram_condense",
		"target_chars":         doctorTelegramMaxChars,
	}, time.Now().UTC())

	summary := strings.TrimSpace(redactDoctorText(turnResult.Text))
	if summary == "" {
		summary = doctorFitTelegramReport(report, doctorTelegramMaxChars)
	}
	return doctorFitTelegramReport(summary, doctorTelegramMaxChars), turnResult.TokenUsage
}

func doctorTelegramSummarySystemNote() string {
	return strings.Join([]string{
		"Role: You are compressing a /health diagnose report for Telegram.",
		"## Goal",
		"Produce the shortest useful operator-facing health summary from the provided report.",
		"## Success Criteria",
		"- The operator can identify the most important current issue and the next sensible action.",
		"- Evidence is preserved only when it justifies priority, status, or risk.",
		"- Read-only status is clear: do not claim to have changed files, memory, services, branches, or commits.",
		"## Constraints",
		"- Stay under the provided service_single_message_limit_chars, which is below Telegram's 4096-character ceiling.",
		"- Pick the most important thing to fix first. If there is only one thing the operator should do next, make that obvious.",
		"- Prefer at most three findings. Include only evidence needed to justify the priority.",
		"- Preserve resolved/current status labels when relevant: active, likely_fixed, historical_resolved, residual_risk, unknown.",
		"## Output",
		"- Return one operator-facing message only.",
		"## Stop Rules",
		"- Do not include exhaustive logs, full inventories, or every recommendation.",
	}, "\n")
}

func doctorFloorMetadata(fullReport string, telegramReport string, maintainer *doctorMaintainerDelegate, maintainerArtifact string) string {
	fullChars := doctorCharCount(fullReport)
	telegramChars := doctorCharCount(telegramReport)
	parts := make([]string, 0, 5)
	if fullChars > 0 || telegramChars > 0 {
		parts = append(parts, fmt.Sprintf("doctor_full_report_chars=%d doctor_telegram_report_chars=%d doctor_telegram_limit_chars=%d", fullChars, telegramChars, doctorTelegramMaxChars))
	}
	if maintainer != nil {
		parts = append(parts, "doctor_delegate_agent_id="+strings.TrimSpace(maintainer.Agent.AgentID))
	}
	if strings.TrimSpace(maintainerArtifact) != "" {
		parts = append(parts, "doctor_delegate_artifact="+strings.TrimSpace(maintainerArtifact))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func doctorFitTelegramReport(text string, limit int) string {
	text = strings.TrimSpace(redactDoctorText(text))
	if text == "" {
		return doctorReportFallbackText
	}
	if limit <= 0 {
		limit = doctorTelegramMaxChars
	}
	if doctorCharCount(text) <= limit {
		return text
	}
	suffix := "\n\n[trimmed to fit one Telegram message]"
	suffixChars := doctorCharCount(suffix)
	if suffixChars >= limit {
		return string([]rune(text)[:limit])
	}
	headLimit := limit - suffixChars
	runes := []rune(text)
	cut := headLimit
	searchFloor := headLimit - 400
	if searchFloor < 0 {
		searchFloor = 0
	}
	for i := headLimit; i >= searchFloor; i-- {
		if runes[i] == '\n' {
			cut = i
			break
		}
		if i < headLimit && (runes[i] == '.' || runes[i] == ';') {
			cut = i + 1
			break
		}
	}
	return strings.TrimSpace(string(runes[:cut])) + suffix
}

func doctorCharCount(text string) int {
	return utf8.RuneCountInString(text)
}

func doctorReadOnlySystemNote() string {
	return strings.Join([]string{
		"You are running /health diagnose.",
		"This is a read-only diagnostic pass. Do not claim to have edited files, run commands, restarted services, changed memory, or committed code.",
		"Use the diagnostic packet and the loaded prompt/memory context to produce an operator-facing report.",
		"For every issue you report, classify it as active, likely_fixed, historical_resolved, residual_risk, or unknown by comparing old failure evidence with current-state checks.",
		"Do not present an old failure as active when the current-state checks indicate it is likely fixed; instead call out remaining verification gaps.",
		"Include concrete code recommendations when the evidence points to code changes, but frame them as recommendations only.",
		"Required sections: State of Things, Recent Failures or Risks, Memory and Prompt Health, Runtime and Session Health, Recommendations, Code Recommendations, Confidence and Unknowns.",
	}, "\n")
}

func doctorMaintainerSystemNote(maintainer *doctorMaintainerDelegate) string {
	if maintainer == nil {
		return ""
	}
	return strings.Join([]string{
		"This /health diagnose run is delegated to the aphelion-maintainer durable child in read-only mode.",
		"Durable agent: " + strings.TrimSpace(maintainer.Agent.AgentID),
		"Use the maintainer archetype and profile as the operating boundary for diagnosis and recommendations.",
		"Do not mutate the local Aphelion clone. If recommending implementation, specify the approved path: isolated /tmp clone, tests there, GitHub PR via a separately approved GitHub App PEM.",
		"Do not claim active grants, repository edits, service restarts, commits, pushes, or PRs unless the diagnostic packet contains concrete evidence that they happened.",
	}, "\n")
}
