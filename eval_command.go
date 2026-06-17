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
	"path/filepath"
	"sort"
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
	case "model-bakeoff":
		return runEvalModelBakeoffCommand(args[1:], out)
	case "attack-corpus":
		return runEvalAttackCorpusCommand(args[1:], out)
	case "compare":
		return runEvalCompareCommand(args[1:], out)
	case "gate":
		return runEvalGateCommand(args[1:], out)
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
	jobsFlag := fs.Int("jobs", 1, "maximum concurrent route/scenario/rollout eval jobs")
	routesFlag := fs.String("routes", "configured", "live routes: configured or comma-separated provider:model specs")
	attackerRoutesFlag := fs.String("attacker-routes", "subject", "boundary_attack attacker routes: subject, configured, or comma-separated provider:model specs")
	attackCorpusFlag := fs.String("attack-corpus", "", "boundary_attack JSON attack corpus path to replay instead of generating attacker turns")
	maxAttacksPerScenarioFlag := fs.Int("max-attacks-per-scenario", 0, "maximum attack-corpus replay cases per scenario; 0 means all")
	scenarioFlag := fs.String("scenario", "", "comma-separated scenario IDs to run")
	scoringFlag := fs.String("scoring", aphruntime.EvalScoringDeterministic, "scoring mode: deterministic or judge")
	judgeRoutesFlag := fs.String("judge-routes", "configured", "judge routes: configured or comma-separated provider:model specs")
	judgeQuorumFlag := fs.String("judge-quorum", aphruntime.EvalJudgeQuorumPair, "judge quorum: pair or single")
	traceFlag := fs.String("trace", aphruntime.EvalTraceRedacted, "trace mode: redacted or minimal")
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
	if *jobsFlag < 1 {
		return fmt.Errorf("eval run requires --jobs >= 1")
	}
	if *maxAttacksPerScenarioFlag < 0 {
		return fmt.Errorf("eval run requires --max-attacks-per-scenario >= 0")
	}
	mode := strings.ToLower(strings.TrimSpace(*modeFlag))
	attackCorpusPath := strings.TrimSpace(*attackCorpusFlag)
	if attackCorpusPath != "" && !strings.EqualFold(strings.TrimSpace(*suiteFlag), aphruntime.EvalSuiteBoundaryAttack) {
		return fmt.Errorf("--attack-corpus is only supported with --suite boundary_attack")
	}
	if attackCorpusPath == "" && *maxAttacksPerScenarioFlag > 0 {
		return fmt.Errorf("--max-attacks-per-scenario requires --attack-corpus")
	}
	if !evalCommandSuiteUsesBoundaryAttack(*suiteFlag) && evalAttackerRoutesFlagRequestsExplicit(*attackerRoutesFlag) {
		return fmt.Errorf("--attacker-routes is only supported with --suite boundary_attack or challenge")
	}
	routes, err := evalRoutesForCommand(mode, *routesFlag, *configFlag)
	if err != nil {
		return err
	}
	var attackCorpus *aphruntime.EvalAttackCorpus
	if attackCorpusPath != "" {
		attackCorpus, err = aphruntime.LoadEvalAttackCorpus(attackCorpusPath)
		if err != nil {
			return err
		}
	}
	var attackerRoutes []aphruntime.EvalRoute
	if attackCorpus == nil {
		attackerRoutes, err = evalAttackerRoutesForCommand(mode, *attackerRoutesFlag, *configFlag)
		if err != nil {
			return err
		}
		if !evalCommandSuiteUsesBoundaryAttack(*suiteFlag) && len(attackerRoutes) > 0 {
			return fmt.Errorf("--attacker-routes is only supported with --suite boundary_attack or challenge")
		}
	}
	judgeRoutes, err := evalJudgeRoutesForCommand(mode, *scoringFlag, *judgeRoutesFlag, *configFlag)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()
	report, runErr := aphruntime.RunEvalSuite(ctx, aphruntime.EvalOptions{
		Suite:                 *suiteFlag,
		Mode:                  mode,
		Subject:               *subjectFlag,
		Rollouts:              *rolloutsFlag,
		Routes:                routes,
		AttackerRoutes:        attackerRoutes,
		AttackCorpus:          attackCorpus,
		MaxAttacksPerScenario: *maxAttacksPerScenarioFlag,
		ScenarioIDs:           splitEvalCSV(*scenarioFlag),
		Scoring:               *scoringFlag,
		JudgeRoutes:           judgeRoutes,
		JudgeQuorum:           *judgeQuorumFlag,
		TraceMode:             *traceFlag,
		ProviderRetries:       *providerRetriesFlag,
		Jobs:                  *jobsFlag,
		Progress:              evalProgressReporter(*progressFlag),
		Seed:                  *seedFlag,
		Now:                   time.Now().UTC(),
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

func evalAttackerRoutesFlagRequestsExplicit(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	for _, raw := range strings.Split(spec, ",") {
		if value := strings.TrimSpace(raw); value != "" && !strings.EqualFold(value, "subject") {
			return true
		}
	}
	return false
}

func evalCommandSuiteUsesBoundaryAttack(suite string) bool {
	switch strings.ToLower(strings.TrimSpace(suite)) {
	case aphruntime.EvalSuiteBoundaryAttack, aphruntime.EvalSuiteChallenge:
		return true
	default:
		return false
	}
}

func runEvalAttackCorpusCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return &cliUsageError{Text: renderEvalCommandHelp("eval attack-corpus requires a subcommand")}
	}
	switch strings.TrimSpace(args[0]) {
	case "generate":
		return runEvalAttackCorpusGenerateCommand(args[1:], out)
	case "help", "-h", "--help":
		fmt.Fprintln(out, renderEvalCommandHelp(""))
		return nil
	default:
		return &cliUsageError{Text: renderEvalCommandHelp("Unknown eval attack-corpus command: " + args[0])}
	}
}

func runEvalAttackCorpusGenerateCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("eval attack-corpus generate", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml for live mode")
	suiteFlag := fs.String("suite", aphruntime.EvalSuiteBoundaryAttack, "eval suite; only boundary_attack is supported")
	modeFlag := fs.String("mode", aphruntime.EvalModeLocal, "generation mode: local or live")
	profileFlag := fs.String("profile", "boundary", "attack profile: boundary or redteam")
	attackerRoutesFlag := fs.String("attacker-routes", "configured", "live attacker routes: configured or comma-separated provider:model specs")
	scenarioFlag := fs.String("scenario", "", "comma-separated scenario IDs to generate")
	perScenarioFlag := fs.Int("per-scenario", 3, "selected attack cases per scenario after dedupe/ranking")
	jobsFlag := fs.Int("jobs", 1, "maximum concurrent attacker-generation jobs")
	providerRetriesFlag := fs.Int("provider-retries", 0, "retries for transient attacker provider failures")
	progressFlag := fs.Bool("progress", false, "emit corpus-generation progress to stderr")
	formatFlag := fs.String("format", "human", "output format: human, kv, json")
	jsonFlag := fs.Bool("json", false, "emit JSON output")
	outFlag := fs.String("out", "", "JSON attack corpus path; defaults to ~/.aphelion/workspace/evals")
	seedFlag := fs.Int64("seed", 1, "deterministic generation seed")
	timeoutFlag := fs.Duration("timeout", 30*time.Minute, "maximum corpus generation runtime")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for eval attack-corpus generate", extra)
	}
	if *perScenarioFlag < 1 {
		return fmt.Errorf("eval attack-corpus generate requires --per-scenario >= 1")
	}
	if *jobsFlag < 1 {
		return fmt.Errorf("eval attack-corpus generate requires --jobs >= 1")
	}
	mode := strings.ToLower(strings.TrimSpace(*modeFlag))
	if !strings.EqualFold(strings.TrimSpace(*suiteFlag), aphruntime.EvalSuiteBoundaryAttack) {
		return fmt.Errorf("attack corpus generation is only supported with --suite boundary_attack")
	}
	var attackerRoutes []aphruntime.EvalRoute
	var err error
	if mode == aphruntime.EvalModeLive {
		attackerRoutes, err = evalAttackerRoutesForCommand(mode, *attackerRoutesFlag, *configFlag)
		if err != nil {
			return err
		}
		if len(attackerRoutes) == 0 {
			return fmt.Errorf("live attack corpus generation requires explicit attacker routes; use --attacker-routes configured or provider:model")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()
	corpus, err := aphruntime.GenerateEvalAttackCorpus(ctx, aphruntime.EvalAttackCorpusOptions{
		Suite:           *suiteFlag,
		Mode:            mode,
		Profile:         *profileFlag,
		AttackerRoutes:  attackerRoutes,
		ScenarioIDs:     splitEvalCSV(*scenarioFlag),
		PerScenario:     *perScenarioFlag,
		Jobs:            *jobsFlag,
		ProviderRetries: *providerRetriesFlag,
		Progress:        evalProgressReporter(*progressFlag),
		Seed:            *seedFlag,
		Now:             time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	path := strings.TrimSpace(*outFlag)
	if path == "" {
		path = defaultEvalAttackCorpusPath(corpus.Suite, time.Now().UTC())
	}
	if err := writeEvalAttackCorpusJSON(path, corpus); err != nil {
		return err
	}
	switch normalizeEvalOutputFormat(*formatFlag, *jsonFlag) {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(corpus)
	case "kv":
		fmt.Fprintf(out, "path=%s\n", path)
		fmt.Fprintf(out, "suite=%s\n", corpus.Suite)
		fmt.Fprintf(out, "profile=%s\n", corpus.Profile)
		fmt.Fprintf(out, "generator_version=%s\n", corpus.GeneratorVersion)
		fmt.Fprintf(out, "scenario_revision=%s\n", corpus.ScenarioRevision)
		fmt.Fprintf(out, "scenario_count=%d\n", corpus.ScenarioCount)
		fmt.Fprintf(out, "attack_count=%d\n", corpus.AttackCount)
		fmt.Fprintf(out, "duplicate_count=%d\n", corpus.DuplicateCount)
		fmt.Fprintf(out, "rejected_count=%d\n", corpus.RejectedCount)
		fmt.Fprintf(out, "provider_failure_count=%d\n", corpus.ProviderFailureCount)
		for _, kind := range sortedEvalCountKeys(corpus.SelectedSourceKindCounts) {
			fmt.Fprintf(out, "selected_source_kind.%s=%d\n", kind, corpus.SelectedSourceKindCounts[kind])
		}
	default:
		fmt.Fprintf(out, "Generated %s attack corpus\n", corpus.Suite)
		fmt.Fprintf(out, "- Path: %s\n", path)
		fmt.Fprintf(out, "- Profile: %s\n", corpus.Profile)
		fmt.Fprintf(out, "- Scenarios: %d\n", corpus.ScenarioCount)
		fmt.Fprintf(out, "- Attacks: %d\n", corpus.AttackCount)
		if len(corpus.SelectedSourceKindCounts) > 0 {
			fmt.Fprintf(out, "- Selected source kinds: %s\n", formatEvalCountMap(corpus.SelectedSourceKindCounts))
		}
		fmt.Fprintf(out, "- Duplicates: %d\n", corpus.DuplicateCount)
		fmt.Fprintf(out, "- Rejected/provider failures: %d/%d\n", corpus.RejectedCount, corpus.ProviderFailureCount)
	}
	return nil
}

func sortedEvalCountKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatEvalCountMap(counts map[string]int) string {
	var parts []string
	for _, key := range sortedEvalCountKeys(counts) {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
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

func runEvalGateCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("eval gate", flag.ContinueOnError)
	beforeFlag := fs.String("before", "", "comma-separated baseline JSON report paths")
	afterFlag := fs.String("after", "", "comma-separated branch JSON report paths")
	formatFlag := fs.String("format", "markdown", "output format: markdown or json")
	jsonFlag := fs.Bool("json", false, "emit JSON output")
	outFlag := fs.String("out", "", "optional gate report path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for eval gate", extra)
	}
	beforePaths := splitEvalCSV(*beforeFlag)
	afterPaths := splitEvalCSV(*afterFlag)
	if len(beforePaths) == 0 || len(afterPaths) == 0 {
		return fmt.Errorf("eval gate requires --before and --after")
	}
	before, err := readEvalJSONReports(beforePaths)
	if err != nil {
		return err
	}
	after, err := readEvalJSONReports(afterPaths)
	if err != nil {
		return err
	}
	gate, err := aphruntime.GateEvalReports(before, after)
	if err != nil {
		return err
	}
	format := normalizeEvalCompareFormat(*formatFlag, *jsonFlag)
	rendered := ""
	switch format {
	case "json":
		raw, err := json.MarshalIndent(gate, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal eval gate: %w", err)
		}
		rendered = string(raw) + "\n"
	default:
		rendered = aphruntime.RenderEvalGateMarkdown(gate) + "\n"
	}
	if path := strings.TrimSpace(*outFlag); path != "" {
		if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
			return fmt.Errorf("write eval gate %s: %w", path, err)
		}
	}
	fmt.Fprint(out, rendered)
	if !gate.Passed {
		return fmt.Errorf("eval gate failed")
	}
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

func evalJudgeRoutesForCommand(mode string, scoring string, routesSpec string, configPath string) ([]aphruntime.EvalRoute, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	scoring = strings.ToLower(strings.TrimSpace(scoring))
	if scoring == "" || scoring == aphruntime.EvalScoringDeterministic {
		return nil, nil
	}
	if scoring != aphruntime.EvalScoringJudge {
		return nil, fmt.Errorf("unsupported eval scoring %q; use deterministic or judge", scoring)
	}
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
		return configuredEvalJudgeRoutes(cfg, httpClient)
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
		return nil, fmt.Errorf("no live eval judge routes selected")
	}
	return routes, nil
}

func evalAttackerRoutesForCommand(mode string, routesSpec string, configPath string) ([]aphruntime.EvalRoute, error) {
	spec := strings.TrimSpace(routesSpec)
	if spec == "" || strings.EqualFold(spec, "subject") {
		return nil, nil
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != aphruntime.EvalModeLive {
		return nil, fmt.Errorf("explicit eval attacker routes require live mode; use --attacker-routes subject for local mode")
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
	if strings.EqualFold(spec, "configured") {
		return configuredEvalRoutes(cfg, httpClient)
	}
	var routes []aphruntime.EvalRoute
	for _, raw := range strings.Split(spec, ",") {
		if strings.EqualFold(strings.TrimSpace(raw), "subject") {
			routes = append(routes, aphruntime.EvalRoute{Name: "subject", Provider: "subject", Model: "same-as-subject"})
			continue
		}
		route, err := explicitEvalRoute(cfg, httpClient, raw)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no live attacker routes selected")
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

func configuredEvalJudgeRoutes(cfg *config.Config, httpClient *http.Client) ([]aphruntime.EvalRoute, error) {
	var routes []aphruntime.EvalRoute
	for _, name := range []string{"openai", "anthropic"} {
		if !isConfiguredProvider(name, cfg) {
			continue
		}
		route, err := configuredSingleModelEvalRoute(name, cfg, httpClient)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no configured OpenAI or Anthropic provider routes are available for judge evals")
	}
	return routes, nil
}

func configuredSingleModelEvalRoute(name string, cfg *config.Config, httpClient *http.Client) (aphruntime.EvalRoute, error) {
	p, err := buildNamedProvider(name, cfg, httpClient)
	if err != nil {
		return aphruntime.EvalRoute{}, err
	}
	model := configuredProviderModel(name, cfg)
	routeName := strings.ToLower(strings.TrimSpace(name))
	if model != "" {
		routeName += ":" + model
	}
	return evalRouteFromProvider(routeName, p)
}

func configuredProviderModel(name string, cfg *config.Config) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "openai":
		return strings.TrimSpace(cfg.Providers.OpenAI.Model)
	case "anthropic":
		return strings.TrimSpace(cfg.Providers.Anthropic.Model)
	case "openrouter":
		return strings.TrimSpace(cfg.Providers.OpenRouter.Model)
	case "gemini":
		return strings.TrimSpace(cfg.Providers.Gemini.Model)
	case "ollama":
		return strings.TrimSpace(cfg.Providers.Ollama.Model)
	default:
		return ""
	}
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

func writeEvalAttackCorpusJSON(path string, corpus aphruntime.EvalAttackCorpus) error {
	raw, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal eval attack corpus: %w", err)
	}
	dir := filepath.Dir(strings.TrimSpace(path))
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create eval attack corpus dir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write eval attack corpus %s: %w", path, err)
	}
	return nil
}

func defaultEvalAttackCorpusPath(suite string, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = "."
	}
	name := fmt.Sprintf("%s-attack-corpus-%s.json", sanitizeEvalArtifactName(suite), now.UTC().Format("20060102T150405Z"))
	return filepath.Join(home, ".aphelion", "workspace", "evals", name)
}

func sanitizeEvalArtifactName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "eval"
	}
	return out
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

func readEvalJSONReports(paths []string) ([]aphruntime.EvalReport, error) {
	reports := make([]aphruntime.EvalReport, 0, len(paths))
	for _, path := range paths {
		report, err := readEvalJSONReport(path)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, nil
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
			fmt.Fprintf(os.Stderr, "eval %s route=%s scenario=%s sample=%d/%d%s\n", status, progress.Route, progress.ScenarioID, progress.SampleIndex+1, progress.Rollouts, evalProgressJobSuffix(progress))
		default:
			fmt.Fprintf(os.Stderr, "eval start route=%s scenario=%s sample=%d/%d subject=%s%s\n", progress.Route, progress.ScenarioID, progress.SampleIndex+1, progress.Rollouts, progress.SubjectMode, evalProgressJobSuffix(progress))
		}
	}
}

func evalProgressJobSuffix(progress aphruntime.EvalProgress) string {
	if progress.JobCount <= 0 {
		return ""
	}
	return fmt.Sprintf(" job=%d/%d", progress.JobIndex+1, progress.JobCount)
}

func renderEvalReportHuman(report aphruntime.EvalReport) string {
	var b strings.Builder
	status := "pass"
	if report.Failed {
		status = "fail"
	}
	fmt.Fprintf(&b, "Aphelion eval %s: %s\n", report.Suite, status)
	fmt.Fprintf(&b, "mode=%s subject=%s scoring=%s routes=%d attacker_routes=%d judge_routes=%d scenarios=%d rollouts=%d jobs=%d results=%d hard_failures=%d provider_failures=%d ambiguous=%d hard_failure_rate=%.2f%%\n", report.Mode, report.SubjectMode, report.ScoringMode, report.RouteCount, report.AttackerRouteCount, report.JudgeRouteCount, report.ScenarioCount, report.Rollouts, report.Jobs, report.ResultCount, report.HardFailureCount, report.ProviderFailureCount, report.AmbiguousCount, report.HardFailureRate*100)
	if len(report.AttackCorpusCaseCounts) > 0 {
		fmt.Fprintf(&b, "attack_corpus_cases=%s\n", formatEvalCountMap(report.AttackCorpusCaseCounts))
	}
	for _, result := range report.Results {
		mark := "PASS"
		if !result.Pass {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "- %s %s route=%s score=%d", mark, result.ScenarioID, result.Route, result.Score)
		if result.AttackerRoute != "" {
			fmt.Fprintf(&b, " attacker=%s", result.AttackerRoute)
		}
		if result.BountyClass != "" {
			fmt.Fprintf(&b, " bounty=%s", result.BountyClass)
		}
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
		if result.JudgeFailure {
			fmt.Fprintf(&b, " judge_provider_failure=true")
		}
		if result.Ambiguous {
			fmt.Fprintf(&b, " ambiguous=true")
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
	fmt.Fprintf(&b, "scoring_mode=%s\n", report.ScoringMode)
	fmt.Fprintf(&b, "jobs=%d\n", report.Jobs)
	fmt.Fprintf(&b, "attacker_route_count=%d\n", report.AttackerRouteCount)
	fmt.Fprintf(&b, "failed=%t\n", report.Failed)
	fmt.Fprintf(&b, "hard_failure_count=%d\n", report.HardFailureCount)
	fmt.Fprintf(&b, "provider_failure_count=%d\n", report.ProviderFailureCount)
	fmt.Fprintf(&b, "ambiguous_count=%d\n", report.AmbiguousCount)
	fmt.Fprintf(&b, "hard_failure_rate=%.6f\n", report.HardFailureRate)
	fmt.Fprintf(&b, "result_count=%d\n", report.ResultCount)
	for _, scenarioID := range sortedEvalCountKeys(report.AttackCorpusCaseCounts) {
		fmt.Fprintf(&b, "attack_corpus_case_count.%s=%d\n", scenarioID, report.AttackCorpusCaseCounts[scenarioID])
	}
	for i, result := range report.Results {
		prefix := "result." + strconv.Itoa(i) + "."
		fmt.Fprintf(&b, "%sscenario_id=%s\n", prefix, result.ScenarioID)
		fmt.Fprintf(&b, "%sroute=%s\n", prefix, result.Route)
		fmt.Fprintf(&b, "%sattacker_route=%s\n", prefix, result.AttackerRoute)
		fmt.Fprintf(&b, "%sbounty_class=%s\n", prefix, result.BountyClass)
		fmt.Fprintf(&b, "%spass=%t\n", prefix, result.Pass)
		fmt.Fprintf(&b, "%sscore=%d\n", prefix, result.Score)
		fmt.Fprintf(&b, "%sprovider_failure=%t\n", prefix, result.ProviderFailure)
		fmt.Fprintf(&b, "%sambiguous=%t\n", prefix, result.Ambiguous)
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
	lines := []string{"Aphelion eval", "Usage:", "  aphelion eval list [--suite canonical|trajectory|boundary_attack|challenge] [--format human|kv|json]", "  aphelion eval run [--suite canonical|trajectory|boundary_attack|challenge] [--mode local|live] [--subject eval|governor] [--rollouts N] [--jobs N] [--routes configured|provider:model,...] [--attacker-routes subject|configured|provider:model,...] [--attack-corpus corpus.json] [--max-attacks-per-scenario N] [--scenario id[,id]] [--scoring deterministic|judge] [--judge-routes configured|provider:model,...] [--judge-quorum pair|single] [--trace redacted|minimal] [--progress] [--format human|kv|json] [--out report.json]", "  aphelion eval model-bakeoff [--role governor] [--suites canonical,trajectory,boundary_attack,challenge] [--mode local|live] [--routes configured|provider:model,...] [--efforts low,medium,high] [--rollouts N] [--jobs N] [--confirm-live-cost] [--format human|markdown|json] [--out report.json]", "  aphelion eval attack-corpus generate [--mode local|live] [--profile boundary|redteam] [--attacker-routes configured|provider:model,...] [--scenario id[,id]] [--per-scenario N] [--jobs N] [--progress] [--out corpus.json]", "  aphelion eval compare --before baseline.json --after branch.json [--format markdown|json] [--out impact.md]", "  aphelion eval gate --before base1.json,base2.json --after branch1.json,branch2.json [--format markdown|json] [--out gate.md]", ""}
	if note = strings.TrimSpace(note); note != "" {
		lines = append([]string{note, ""}, lines...)
	}
	lines = append(lines,
		"Local mode uses deterministic scripted providers and simulated external effects.",
		"Live mode uses configured provider routes but still simulates GitHub, deploy, Tailscale, child, and private-content effects.",
		"model-bakeoff reports evidence for per-role model selection; v1 is runnable for governor and changes no model-slot config.",
		"model-bakeoff --efforts expands subject routes by governor reasoning effort; attacker and judge routes keep their own reasoning policy.",
		"boundary_attack adds transcript-driven attacker turns; --attacker-routes subject reuses the subject route without multiplying jobs.",
		"attack-corpus generate spends attacker tokens once; eval run --attack-corpus replays fixed attacks without attacker-provider calls.",
		"--jobs bounds the worker pool across route/scenario/rollout eval jobs; it does not parallelize within one eval job.",
		"Use --jobs > 1 only with concurrency-safe provider routes/clients and stable credentials.",
	)
	return strings.Join(lines, "\n")
}
