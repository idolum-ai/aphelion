//go:build linux

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
)

type evalCommandFailure struct {
	count int
}

func (e evalCommandFailure) Error() string {
	return fmt.Sprintf("eval failed with %d hard failure(s)", e.count)
}

func runEvalCommand(args []string) error {
	return runEvalCommandWithDeps(args, os.Stdout)
}

func runEvalCommandWithDeps(args []string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	if len(args) == 0 {
		return &cliUsageError{Text: renderEvalCommandHelp("")}
	}
	switch strings.TrimSpace(args[0]) {
	case "help", "-h", "--help":
		fmt.Fprintln(out, renderEvalCommandHelp(""))
		return nil
	case "list":
		return runEvalListCommand(args[1:], out)
	case "run":
		return runEvalRunCommand(args[1:], out)
	case "compare":
		return runEvalCompareCommand(args[1:], out)
	default:
		return &cliUsageError{Text: renderEvalCommandHelp("Unknown eval command: " + args[0])}
	}
}

func runEvalListCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("eval list", flag.ContinueOnError)
	suiteFlag := fs.String("suite", aphruntime.EvalSuiteCanonical, "eval suite")
	formatFlag := fs.String("format", "human", "output format: human, kv, json")
	jsonFlag := fs.Bool("json", false, "emit JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for eval list", extra)
	}
	format := normalizeEvalOutputFormat(*formatFlag, *jsonFlag)
	scenarios, err := aphruntime.ListEvalScenarios(*suiteFlag)
	if err != nil {
		return err
	}
	switch format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"suite":     strings.TrimSpace(*suiteFlag),
			"scenarios": scenarios,
		})
	case "kv":
		fmt.Fprintf(out, "suite=%s\n", strings.TrimSpace(*suiteFlag))
		fmt.Fprintf(out, "scenario_count=%d\n", len(scenarios))
		for _, sc := range scenarios {
			fmt.Fprintf(out, "scenario=%s domain=%s authority=%s surface=%s\n", sc.ID, sc.Domain, sc.AuthorityClass, sc.TransportSurface)
		}
	default:
		fmt.Fprintf(out, "Aphelion eval suite: %s\n", strings.TrimSpace(*suiteFlag))
		for _, sc := range scenarios {
			fmt.Fprintf(out, "- %s: %s [%s, %s, %s]\n", sc.ID, sc.Name, sc.Domain, sc.AuthorityClass, sc.TransportSurface)
		}
	}
	return nil
}

func runEvalRunCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("eval run", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml for live mode")
	suiteFlag := fs.String("suite", aphruntime.EvalSuiteCanonical, "eval suite")
	modeFlag := fs.String("mode", aphruntime.EvalModeLocal, "eval mode: local or live")
	subjectFlag := fs.String("subject", aphruntime.EvalSubjectEval, "eval subject: eval or governor")
	rolloutsFlag := fs.Int("rollouts", 0, "rollouts per scenario/route")
	routesFlag := fs.String("routes", "configured", "live routes: configured or comma-separated provider:model specs")
	scenarioFlag := fs.String("scenario", "", "comma-separated scenario IDs to run")
	providerRetriesFlag := fs.Int("provider-retries", 0, "retries for transient provider failures")
	progressFlag := fs.Bool("progress", false, "emit route/scenario/sample progress to stderr")
	formatFlag := fs.String("format", "human", "output format: human, kv, json")
	jsonFlag := fs.Bool("json", false, "emit JSON output")
	outFlag := fs.String("out", "", "optional JSON report path")
	seedFlag := fs.Int64("seed", 1, "deterministic fixture seed")
	timeoutFlag := fs.Duration("timeout", 30*time.Minute, "maximum eval runtime")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for eval run", extra)
	}
	mode := strings.ToLower(strings.TrimSpace(*modeFlag))
	routes, err := evalRoutesForCommand(mode, *routesFlag, *configFlag)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()
	report, runErr := aphruntime.RunEvalSuite(ctx, aphruntime.EvalOptions{
		Suite:           *suiteFlag,
		Mode:            mode,
		Subject:         *subjectFlag,
		Rollouts:        *rolloutsFlag,
		Routes:          routes,
		ScenarioIDs:     splitEvalCSV(*scenarioFlag),
		ProviderRetries: *providerRetriesFlag,
		Progress:        evalProgressReporter(*progressFlag),
		Seed:            *seedFlag,
		Now:             time.Now().UTC(),
	})
	if runErr != nil && len(report.Results) == 0 {
		return runErr
	}
	if path := strings.TrimSpace(*outFlag); path != "" {
		if err := writeEvalJSONReport(path, report); err != nil {
			return err
		}
	}
	switch normalizeEvalOutputFormat(*formatFlag, *jsonFlag) {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	case "kv":
		fmt.Fprint(out, renderEvalReportKV(report))
	default:
		fmt.Fprintln(out, renderEvalReportHuman(report))
	}
	if runErr != nil {
		return runErr
	}
	return evalReportFailureError(report)
}

func runEvalCompareCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("eval compare", flag.ContinueOnError)
	beforeFlag := fs.String("before", "", "baseline JSON report path")
	afterFlag := fs.String("after", "", "branch JSON report path")
	formatFlag := fs.String("format", "markdown", "output format: markdown or json")
	jsonFlag := fs.Bool("json", false, "emit JSON output")
	outFlag := fs.String("out", "", "optional comparison report path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for eval compare", extra)
	}
	if strings.TrimSpace(*beforeFlag) == "" || strings.TrimSpace(*afterFlag) == "" {
		return fmt.Errorf("eval compare requires --before and --after")
	}
	before, err := readEvalJSONReport(*beforeFlag)
	if err != nil {
		return err
	}
	after, err := readEvalJSONReport(*afterFlag)
	if err != nil {
		return err
	}
	comparison := aphruntime.CompareEvalReports(before, after)
	format := normalizeEvalCompareFormat(*formatFlag, *jsonFlag)
	rendered := ""
	switch format {
	case "json":
		raw, err := json.MarshalIndent(comparison, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal eval comparison: %w", err)
		}
		rendered = string(raw) + "\n"
	default:
		rendered = aphruntime.RenderEvalComparisonMarkdown(comparison) + "\n"
	}
	if path := strings.TrimSpace(*outFlag); path != "" {
		if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
			return fmt.Errorf("write eval comparison %s: %w", path, err)
		}
	}
	fmt.Fprint(out, rendered)
	return nil
}

func evalRoutesForCommand(mode string, routesSpec string, configPath string) ([]aphruntime.EvalRoute, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != aphruntime.EvalModeLive {
		return nil, nil
	}
	cfgPath, err := config.ResolveConfigPath(configPath)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: 90 * time.Second}
	spec := strings.TrimSpace(routesSpec)
	if spec == "" || strings.EqualFold(spec, "configured") {
		return configuredEvalRoutes(cfg, httpClient)
	}
	var routes []aphruntime.EvalRoute
	for _, raw := range strings.Split(spec, ",") {
		route, err := explicitEvalRoute(cfg, httpClient, raw)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no live eval routes selected")
	}
	return routes, nil
}

func configuredEvalRoutes(cfg *config.Config, httpClient *http.Client) ([]aphruntime.EvalRoute, error) {
	var routes []aphruntime.EvalRoute
	for _, name := range orderedNativeProviderNames(cfg) {
		entries, err := buildNamedProviderEntries(name, cfg, httpClient)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			route, err := evalRouteFromProvider(entry.Name, entry.Provider)
			if err != nil {
				return nil, err
			}
			routes = append(routes, route)
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no configured provider routes are available for live evals")
	}
	return routes, nil
}

func explicitEvalRoute(cfg *config.Config, httpClient *http.Client, raw string) (aphruntime.EvalRoute, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return aphruntime.EvalRoute{}, fmt.Errorf("empty eval route")
	}
	name := raw
	model := ""
	if idx := strings.Index(raw, ":"); idx >= 0 {
		name = strings.TrimSpace(raw[:idx])
		model = strings.TrimSpace(raw[idx+1:])
	}
	cfgCopy := *cfg
	if model != "" {
		switch strings.ToLower(name) {
		case "openai":
			cfgCopy.Providers.OpenAI.Model = model
			cfgCopy.Providers.OpenAI.FallbackModels = nil
		case "anthropic":
			cfgCopy.Providers.Anthropic.Model = model
		case "openrouter":
			cfgCopy.Providers.OpenRouter.Model = model
		case "gemini":
			cfgCopy.Providers.Gemini.Model = model
		case "ollama":
			cfgCopy.Providers.Ollama.Model = model
		default:
			return aphruntime.EvalRoute{}, fmt.Errorf("unsupported eval route provider %q", name)
		}
	}
	p, err := buildNamedProvider(name, &cfgCopy, httpClient)
	if err != nil {
		return aphruntime.EvalRoute{}, err
	}
	routeName := strings.ToLower(strings.TrimSpace(name))
	if model != "" {
		routeName += ":" + model
	}
	return evalRouteFromProvider(routeName, p)
}

func evalRouteFromProvider(name string, p agent.Provider) (aphruntime.EvalRoute, error) {
	if p == nil {
		return aphruntime.EvalRoute{}, fmt.Errorf("provider route %s is nil", name)
	}
	subject, ok := p.(agent.ProviderWithOptions)
	if !ok {
		subject = providerWithOptionsAdapter{Provider: p}
	}
	providerName, model := splitEvalRouteName(name)
	return aphruntime.EvalRoute{
		Name:     strings.TrimSpace(name),
		Provider: providerName,
		Model:    model,
		Subject:  subject,
	}, nil
}

type providerWithOptionsAdapter struct {
	agent.Provider
}

func (p providerWithOptionsAdapter) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, _ agent.CompleteOptions) (*agent.Response, error) {
	return p.Provider.Complete(ctx, messages, tools)
}

func splitEvalRouteName(name string) (string, string) {
	name = strings.TrimSpace(name)
	if idx := strings.Index(name, ":"); idx >= 0 {
		return strings.TrimSpace(name[:idx]), strings.TrimSpace(name[idx+1:])
	}
	return name, ""
}

func writeEvalJSONReport(path string, report aphruntime.EvalReport) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal eval report: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write eval report %s: %w", path, err)
	}
	return nil
}

func readEvalJSONReport(path string) (aphruntime.EvalReport, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return aphruntime.EvalReport{}, fmt.Errorf("read eval report %s: %w", path, err)
	}
	var report aphruntime.EvalReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return aphruntime.EvalReport{}, fmt.Errorf("decode eval report %s: %w", path, err)
	}
	return report, nil
}

func normalizeEvalOutputFormat(format string, jsonAlias bool) string {
	if jsonAlias {
		return "json"
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		return "json"
	case "kv":
		return "kv"
	default:
		return "human"
	}
}

func normalizeEvalCompareFormat(format string, jsonAlias bool) string {
	if jsonAlias {
		return "json"
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		return "json"
	default:
		return "markdown"
	}
}

func splitEvalCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func evalProgressReporter(enabled bool) func(aphruntime.EvalProgress) {
	if !enabled {
		return nil
	}
	return func(progress aphruntime.EvalProgress) {
		switch progress.Event {
		case "retry":
			fmt.Fprintf(os.Stderr, "eval retry route=%s scenario=%s sample=%d attempt=%d err=%s\n", progress.Route, progress.ScenarioID, progress.SampleIndex, progress.Attempt, progress.Error)
		case "result":
			status := "done"
			if progress.Error != "" {
				status = "error"
			}
			fmt.Fprintf(os.Stderr, "eval %s route=%s scenario=%s sample=%d/%d\n", status, progress.Route, progress.ScenarioID, progress.SampleIndex+1, progress.Rollouts)
		default:
			fmt.Fprintf(os.Stderr, "eval start route=%s scenario=%s sample=%d/%d subject=%s\n", progress.Route, progress.ScenarioID, progress.SampleIndex+1, progress.Rollouts, progress.SubjectMode)
		}
	}
}

func renderEvalReportHuman(report aphruntime.EvalReport) string {
	var b strings.Builder
	status := "pass"
	if report.Failed {
		status = "fail"
	}
	fmt.Fprintf(&b, "Aphelion eval %s: %s\n", report.Suite, status)
	fmt.Fprintf(&b, "mode=%s subject=%s routes=%d scenarios=%d rollouts=%d results=%d hard_failures=%d provider_failures=%d hard_failure_rate=%.2f%%\n", report.Mode, report.SubjectMode, report.RouteCount, report.ScenarioCount, report.Rollouts, report.ResultCount, report.HardFailureCount, report.ProviderFailureCount, report.HardFailureRate*100)
	for _, result := range report.Results {
		mark := "PASS"
		if !result.Pass {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "- %s %s route=%s score=%d", mark, result.ScenarioID, result.Route, result.Score)
		if len(result.HardFailures) > 0 {
			classes := make([]string, 0, len(result.HardFailures))
			for _, finding := range result.HardFailures {
				classes = append(classes, finding.Class)
			}
			fmt.Fprintf(&b, " hard=%s", strings.Join(classes, ","))
		}
		if result.ProviderFailure {
			fmt.Fprintf(&b, " provider_failure=true")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderEvalReportKV(report aphruntime.EvalReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "suite=%s\n", report.Suite)
	fmt.Fprintf(&b, "mode=%s\n", report.Mode)
	fmt.Fprintf(&b, "subject_mode=%s\n", report.SubjectMode)
	fmt.Fprintf(&b, "scenario_revision=%s\n", report.ScenarioRevision)
	fmt.Fprintf(&b, "failed=%t\n", report.Failed)
	fmt.Fprintf(&b, "hard_failure_count=%d\n", report.HardFailureCount)
	fmt.Fprintf(&b, "provider_failure_count=%d\n", report.ProviderFailureCount)
	fmt.Fprintf(&b, "hard_failure_rate=%.6f\n", report.HardFailureRate)
	fmt.Fprintf(&b, "result_count=%d\n", report.ResultCount)
	for i, result := range report.Results {
		prefix := "result." + strconv.Itoa(i) + "."
		fmt.Fprintf(&b, "%sscenario_id=%s\n", prefix, result.ScenarioID)
		fmt.Fprintf(&b, "%sroute=%s\n", prefix, result.Route)
		fmt.Fprintf(&b, "%spass=%t\n", prefix, result.Pass)
		fmt.Fprintf(&b, "%sscore=%d\n", prefix, result.Score)
		fmt.Fprintf(&b, "%sprovider_failure=%t\n", prefix, result.ProviderFailure)
	}
	return b.String()
}

func evalReportFailureError(report aphruntime.EvalReport) error {
	if report.Failed {
		return evalCommandFailure{count: report.HardFailureCount}
	}
	return nil
}

func renderEvalCommandHelp(note string) string {
	lines := []string{"Aphelion eval", "Usage:", "  aphelion eval list [--suite canonical] [--format human|kv|json]", "  aphelion eval run [--suite canonical] [--mode local|live] [--subject eval|governor] [--rollouts N] [--routes configured|provider:model,...] [--scenario id[,id]] [--progress] [--format human|kv|json] [--out report.json]", "  aphelion eval compare --before baseline.json --after branch.json [--format markdown|json] [--out impact.md]", ""}
	if note = strings.TrimSpace(note); note != "" {
		lines = append([]string{note, ""}, lines...)
	}
	lines = append(lines,
		"Local mode uses deterministic scripted providers and simulated external effects.",
		"Live mode uses configured provider routes but still simulates GitHub, deploy, Tailscale, child, and private-content effects.",
	)
	return strings.Join(lines, "\n")
}
