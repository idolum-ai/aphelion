//go:build linux

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	aphruntime "github.com/idolum-ai/aphelion/runtime"
)

type evalModelBakeoffReport struct {
	GeneratedAt      string                            `json:"generated_at"`
	Role             string                            `json:"role"`
	RoleStatus       string                            `json:"role_status"`
	QualityOracle    string                            `json:"quality_oracle"`
	Mode             string                            `json:"mode"`
	Suites           []string                          `json:"suites"`
	Rollouts         int                               `json:"rollouts"`
	Jobs             int                               `json:"jobs"`
	RouteCount       int                               `json:"route_count"`
	ScenarioCount    int                               `json:"scenario_count"`
	ResultCount      int                               `json:"result_count"`
	Routes           []evalModelBakeoffRouteSummary    `json:"routes"`
	LiveCostEstimate *evalModelBakeoffLiveCostEstimate `json:"live_cost_estimate,omitempty"`
	RoleReadiness    []evalModelBakeoffReadiness       `json:"role_readiness"`
	SuiteReports     []aphruntime.EvalReport           `json:"suite_reports,omitempty"`
	Notes            []string                          `json:"notes,omitempty"`
}

type evalModelBakeoffReadiness struct {
	Role          string `json:"role"`
	Status        string `json:"status"`
	QualityOracle string `json:"quality_oracle"`
	Notes         string `json:"notes,omitempty"`
}

type evalModelBakeoffRouteSummary struct {
	Route                          string  `json:"route"`
	Provider                       string  `json:"provider,omitempty"`
	Model                          string  `json:"model,omitempty"`
	Effort                         string  `json:"effort,omitempty"`
	ResultCount                    int     `json:"result_count"`
	PassCount                      int     `json:"pass_count"`
	PassRate                       float64 `json:"pass_rate"`
	HardFailureCount               int     `json:"hard_failure_count"`
	ProviderFailureCount           int     `json:"provider_failure_count"`
	AmbiguousCount                 int     `json:"ambiguous_count"`
	DurationMillis                 int64   `json:"duration_millis,omitempty"`
	MaxDurationMillis              int64   `json:"max_duration_millis,omitempty"`
	ContextCleanResults            int     `json:"context_clean_results,omitempty"`
	HydrationHitRate               float64 `json:"hydration_hit_rate,omitempty"`
	CrossThreadLeakRate            float64 `json:"cross_thread_leak_rate,omitempty"`
	EvidenceReferenceRetentionRate float64 `json:"evidence_reference_retention_rate,omitempty"`
	CostCleanResults               int     `json:"cost_clean_results,omitempty"`
	EstimatedPromptTokens          int     `json:"estimated_prompt_tokens,omitempty"`
	MaxPromptTokens                int     `json:"max_prompt_tokens,omitempty"`
	CostModelCallCount             int     `json:"cost_model_call_count,omitempty"`
	CacheEligiblePromptCount       int     `json:"cache_eligible_prompt_count,omitempty"`
	StablePrefixStabilityRate      float64 `json:"stable_prefix_stability_rate,omitempty"`
	ProviderUsageCleanResults      int     `json:"provider_usage_clean_results,omitempty"`
	ProviderModelCallCount         int     `json:"provider_model_call_count,omitempty"`
	ProviderInputTokens            int64   `json:"provider_input_tokens,omitempty"`
	ProviderOutputTokens           int64   `json:"provider_output_tokens,omitempty"`
	ProviderTotalTokens            int64   `json:"provider_total_tokens,omitempty"`
	ProviderCacheReadTokens        int64   `json:"provider_cache_read_tokens,omitempty"`
	ProviderCacheWriteTokens       int64   `json:"provider_cache_write_tokens,omitempty"`
	ProviderCacheCreationTokens    int64   `json:"provider_cache_creation_tokens,omitempty"`
}

type evalModelBakeoffLiveCostEstimate struct {
	ProviderCalls        int      `json:"provider_calls"`
	SubjectCalls         int      `json:"subject_calls"`
	AttackerCalls        int      `json:"attacker_calls,omitempty"`
	JudgeCalls           int      `json:"judge_calls,omitempty"`
	Threshold            int      `json:"threshold,omitempty"`
	ConfirmationRequired bool     `json:"confirmation_required,omitempty"`
	Confirmed            bool     `json:"confirmed,omitempty"`
	Notes                []string `json:"notes,omitempty"`
}

func runEvalModelBakeoffCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("eval model-bakeoff", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml for live mode")
	roleFlag := fs.String("role", "governor", "role to bake off; v1 supports governor")
	suitesFlag := fs.String("suites", strings.Join(evalModelBakeoffDefaultSuites(), ","), "comma-separated eval suites")
	suiteFlag := fs.String("suite", "", "single eval suite alias for --suites")
	modeFlag := fs.String("mode", aphruntime.EvalModeLocal, "eval mode: local or live")
	rolloutsFlag := fs.Int("rollouts", 0, "rollouts per scenario/route")
	jobsFlag := fs.Int("jobs", 1, "maximum concurrent route/scenario/rollout eval jobs")
	routesFlag := fs.String("routes", "configured", "live routes: configured or comma-separated provider:model specs; local mode also accepts comma-separated route labels")
	effortsFlag := fs.String("efforts", "", "optional comma-separated governor reasoning efforts: low, medium, high, xhigh; hard aliases high")
	attackerRoutesFlag := fs.String("attacker-routes", "subject", "boundary_attack attacker routes: subject, configured, or comma-separated provider:model specs")
	attackCorpusFlag := fs.String("attack-corpus", "", "boundary_attack JSON attack corpus path to replay")
	maxAttacksPerScenarioFlag := fs.Int("max-attacks-per-scenario", 0, "maximum attack-corpus replay cases per scenario; 0 means all")
	scenarioFlag := fs.String("scenario", "", "comma-separated scenario IDs to run in each selected suite")
	scoringFlag := fs.String("scoring", aphruntime.EvalScoringDeterministic, "scoring mode: deterministic or judge")
	judgeRoutesFlag := fs.String("judge-routes", "configured", "judge routes: configured or comma-separated provider:model specs")
	judgeQuorumFlag := fs.String("judge-quorum", aphruntime.EvalJudgeQuorumPair, "judge quorum: pair or single")
	traceFlag := fs.String("trace", aphruntime.EvalTraceRedacted, "trace mode: redacted or minimal")
	providerRetriesFlag := fs.Int("provider-retries", 0, "retries for transient provider failures")
	confirmLiveCostFlag := fs.Bool("confirm-live-cost", false, "confirm large live bakeoff provider-call estimates")
	liveCostThresholdFlag := fs.Int("live-cost-threshold", 50, "estimated live provider-call threshold requiring --confirm-live-cost; 0 disables the guard")
	progressFlag := fs.Bool("progress", false, "emit route/scenario/sample progress to stderr")
	formatFlag := fs.String("format", "human", "output format: human, markdown, or json")
	jsonFlag := fs.Bool("json", false, "emit JSON output")
	outFlag := fs.String("out", "", "optional report path; JSON for .json paths, otherwise rendered format")
	seedFlag := fs.Int64("seed", 1, "deterministic fixture seed")
	timeoutFlag := fs.Duration("timeout", 30*time.Minute, "maximum bakeoff runtime")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for eval model-bakeoff", extra)
	}
	if *jobsFlag < 1 {
		return fmt.Errorf("eval model-bakeoff requires --jobs >= 1")
	}
	if *maxAttacksPerScenarioFlag < 0 {
		return fmt.Errorf("eval model-bakeoff requires --max-attacks-per-scenario >= 0")
	}
	if *liveCostThresholdFlag < 0 {
		return fmt.Errorf("eval model-bakeoff requires --live-cost-threshold >= 0")
	}
	role, readiness, err := evalModelBakeoffRoleReadiness(*roleFlag)
	if err != nil {
		return err
	}
	if readiness.Status != "runnable" {
		return fmt.Errorf("role %q is %s, not runnable; quality oracle: %s", role, readiness.Status, readiness.QualityOracle)
	}
	suites := splitEvalCSV(firstNonEmptyEvalModelBakeoff(*suiteFlag, *suitesFlag))
	if len(suites) == 0 {
		return fmt.Errorf("eval model-bakeoff requires at least one suite")
	}
	suites = normalizeEvalModelBakeoffSuites(suites)
	mode := strings.ToLower(strings.TrimSpace(*modeFlag))
	attackCorpusPath := strings.TrimSpace(*attackCorpusFlag)
	if attackCorpusPath != "" && !evalModelBakeoffSuitesOnlyBoundaryAttack(suites) {
		return fmt.Errorf("--attack-corpus with eval model-bakeoff requires --suites boundary_attack")
	}
	if attackCorpusPath == "" && *maxAttacksPerScenarioFlag > 0 {
		return fmt.Errorf("--max-attacks-per-scenario requires --attack-corpus")
	}

	routes, err := evalModelBakeoffRoutesForCommand(mode, *routesFlag, *configFlag)
	if err != nil {
		return err
	}
	efforts, err := evalModelBakeoffEffortsForCommand(*effortsFlag)
	if err != nil {
		return err
	}
	routes = evalModelBakeoffExpandRoutesByEffort(mode, routes, efforts)
	var attackCorpus *aphruntime.EvalAttackCorpus
	if attackCorpusPath != "" {
		attackCorpus, err = aphruntime.LoadEvalAttackCorpus(attackCorpusPath)
		if err != nil {
			return err
		}
	}
	var attackerRoutes []aphruntime.EvalRoute
	if attackCorpus == nil && evalModelBakeoffIncludesBoundaryAttack(suites) {
		attackerRoutes, err = evalAttackerRoutesForCommand(mode, *attackerRoutesFlag, *configFlag)
		if err != nil {
			return err
		}
	}
	judgeRoutes, err := evalJudgeRoutesForCommand(mode, *scoringFlag, *judgeRoutesFlag, *configFlag)
	if err != nil {
		return err
	}

	liveCostEstimate, err := estimateEvalModelBakeoffLiveCost(evalModelBakeoffLiveCostInputs{
		Mode:                  mode,
		Suites:                suites,
		ScenarioIDs:           splitEvalCSV(*scenarioFlag),
		Rollouts:              *rolloutsFlag,
		Routes:                routes,
		AttackerRoutes:        attackerRoutes,
		AttackCorpus:          attackCorpus,
		MaxAttacksPerScenario: *maxAttacksPerScenarioFlag,
		Scoring:               *scoringFlag,
		JudgeRoutes:           judgeRoutes,
		ProviderRetries:       *providerRetriesFlag,
		Threshold:             *liveCostThresholdFlag,
		Confirmed:             *confirmLiveCostFlag,
	})
	if err != nil {
		return err
	}
	if liveCostEstimate != nil && liveCostEstimate.ConfirmationRequired && !liveCostEstimate.Confirmed {
		return fmt.Errorf("live model bakeoff estimates %d provider calls (subject=%d attacker=%d judge=%d), above --live-cost-threshold=%d; rerun with --confirm-live-cost to continue",
			liveCostEstimate.ProviderCalls,
			liveCostEstimate.SubjectCalls,
			liveCostEstimate.AttackerCalls,
			liveCostEstimate.JudgeCalls,
			liveCostEstimate.Threshold,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()

	var reports []aphruntime.EvalReport
	var firstRunErr error
	for _, suite := range suites {
		report, runErr := aphruntime.RunEvalSuite(ctx, aphruntime.EvalOptions{
			Suite:                 suite,
			Mode:                  mode,
			Subject:               aphruntime.EvalSubjectGovernor,
			Rollouts:              *rolloutsFlag,
			Routes:                routes,
			AttackerRoutes:        evalModelBakeoffAttackerRoutesForSuite(suite, attackerRoutes),
			AttackCorpus:          evalModelBakeoffAttackCorpusForSuite(suite, attackCorpus),
			MaxAttacksPerScenario: evalModelBakeoffMaxAttacksForSuite(suite, *maxAttacksPerScenarioFlag),
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
		if runErr != nil && firstRunErr == nil {
			firstRunErr = runErr
		}
		reports = append(reports, report)
	}

	report := buildEvalModelBakeoffReport(role, readiness, mode, suites, *rolloutsFlag, *jobsFlag, reports, liveCostEstimate)
	format := normalizeEvalModelBakeoffFormat(*formatFlag, *jsonFlag)
	rendered, err := renderEvalModelBakeoffReport(report, format)
	if err != nil {
		return err
	}
	if path := strings.TrimSpace(*outFlag); path != "" {
		if err := writeEvalModelBakeoffReport(path, report, format, rendered); err != nil {
			return err
		}
	}
	fmt.Fprint(out, rendered)
	if firstRunErr != nil {
		return firstRunErr
	}
	return nil
}

func evalModelBakeoffDefaultSuites() []string {
	return []string{aphruntime.EvalSuiteCanonical, aphruntime.EvalSuiteTrajectory, aphruntime.EvalSuiteBoundaryAttack}
}

func evalModelBakeoffRoleReadiness(role string) (string, evalModelBakeoffReadiness, error) {
	role = normalizeEvalModelBakeoffRole(role)
	for _, readiness := range evalModelBakeoffRoleReadinesses() {
		if readiness.Role == role {
			return role, readiness, nil
		}
	}
	return "", evalModelBakeoffReadiness{}, fmt.Errorf("unknown model bakeoff role %q; supported roles: %s", role, strings.Join(evalModelBakeoffRoleNames(), ", "))
}

func evalModelBakeoffRoleReadinesses() []evalModelBakeoffReadiness {
	return []evalModelBakeoffReadiness{
		{Role: "governor", Status: "runnable", QualityOracle: "canonical, trajectory, boundary_attack, context-fidelity, cost-fidelity", Notes: "mechanical authority/evidence/continuation oracles exist"},
		{Role: "persona", Status: "scaffolded", QualityOracle: "model phenomenology scaffold; calibrated face judges still required", Notes: "do not promote route choices from prose quality until human-rated anchors exist"},
		{Role: "doctor", Status: "scaffolded", QualityOracle: "status/doctor structure tests; live summarization quality oracle still required", Notes: "diagnostic authority remains typed, but readable summary quality is generative"},
		{Role: "child_default", Status: "scaffolded", QualityOracle: "durable child policy tests; per-child task fixtures still required", Notes: "child model choice depends on child envelope and adapter risk"},
		{Role: "structured", Status: "scaffolded", QualityOracle: "exact-output or ranking fixtures needed per call site", Notes: "working-objective, re-entry ranking, and classifiers should graduate one by one"},
	}
}

func evalModelBakeoffRoleNames() []string {
	readinesses := evalModelBakeoffRoleReadinesses()
	out := make([]string, 0, len(readinesses))
	for _, readiness := range readinesses {
		out = append(out, readiness.Role)
	}
	sort.Strings(out)
	return out
}

func normalizeEvalModelBakeoffRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "governor", "system", "main":
		return "governor"
	case "face", "idolum", "persona":
		return "persona"
	case "doctor", "diagnostic", "diagnostics":
		return "doctor"
	case "child", "children", "durable_child", "child_default":
		return "child_default"
	case "structured", "ranker", "classifier", "small":
		return "structured"
	default:
		return strings.ToLower(strings.TrimSpace(role))
	}
}

func normalizeEvalModelBakeoffSuites(suites []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(suites))
	for _, suite := range suites {
		switch strings.ToLower(strings.TrimSpace(suite)) {
		case aphruntime.EvalSuiteCanonical:
			suite = aphruntime.EvalSuiteCanonical
		case aphruntime.EvalSuiteTrajectory:
			suite = aphruntime.EvalSuiteTrajectory
		case aphruntime.EvalSuiteBoundaryAttack:
			suite = aphruntime.EvalSuiteBoundaryAttack
		case aphruntime.EvalSuiteChallenge:
			suite = aphruntime.EvalSuiteChallenge
		default:
			suite = strings.ToLower(strings.TrimSpace(suite))
		}
		if suite == "" || seen[suite] {
			continue
		}
		seen[suite] = true
		out = append(out, suite)
	}
	return out
}

func evalModelBakeoffRoutesForCommand(mode string, routesSpec string, configPath string) ([]aphruntime.EvalRoute, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != aphruntime.EvalModeLocal {
		return evalRoutesForCommand(mode, routesSpec, configPath)
	}
	spec := strings.TrimSpace(routesSpec)
	if spec == "" || strings.EqualFold(spec, "configured") {
		return nil, nil
	}
	var routes []aphruntime.EvalRoute
	for _, raw := range strings.Split(spec, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name := raw
		provider := "local"
		model := raw
		if idx := strings.Index(raw, ":"); idx >= 0 {
			provider = strings.TrimSpace(raw[:idx])
			model = strings.TrimSpace(raw[idx+1:])
			if provider == "" {
				provider = "local"
			}
			name = provider + ":" + model
		} else {
			name = "local:" + raw
		}
		routes = append(routes, aphruntime.EvalRoute{Name: name, Provider: provider, Model: model})
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no local eval routes selected")
	}
	return routes, nil
}

func evalModelBakeoffEffortsForCommand(spec string) ([]string, error) {
	values := splitEvalCSV(spec)
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		effort, err := evalModelBakeoffNormalizeEffort(raw)
		if err != nil {
			return nil, err
		}
		if seen[effort] {
			continue
		}
		seen[effort] = true
		out = append(out, effort)
	}
	return out, nil
}

func evalModelBakeoffNormalizeEffort(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "hard" {
		value = "high"
	}
	switch value {
	case "low", "medium", "high", "xhigh":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported eval model-bakeoff effort %q; use low, medium, high, or xhigh", raw)
	}
}

func evalModelBakeoffExpandRoutesByEffort(mode string, routes []aphruntime.EvalRoute, efforts []string) []aphruntime.EvalRoute {
	if len(efforts) == 0 {
		return routes
	}
	if len(routes) == 0 && strings.EqualFold(strings.TrimSpace(mode), aphruntime.EvalModeLocal) {
		routes = []aphruntime.EvalRoute{{Name: "local:scripted", Provider: "local", Model: "scripted"}}
	}
	out := make([]aphruntime.EvalRoute, 0, len(routes)*len(efforts))
	for _, route := range routes {
		baseName := strings.TrimSpace(route.Name)
		for _, effort := range efforts {
			expanded := route
			expanded.Effort = effort
			if baseName != "" {
				expanded.Name = baseName + "@" + effort
			}
			out = append(out, expanded)
		}
	}
	return out
}

func evalModelBakeoffIncludesBoundaryAttack(suites []string) bool {
	for _, suite := range suites {
		if strings.EqualFold(suite, aphruntime.EvalSuiteBoundaryAttack) || strings.EqualFold(suite, aphruntime.EvalSuiteChallenge) {
			return true
		}
	}
	return false
}

func evalModelBakeoffSuitesOnlyBoundaryAttack(suites []string) bool {
	return len(suites) == 1 && strings.EqualFold(suites[0], aphruntime.EvalSuiteBoundaryAttack)
}

func evalModelBakeoffAttackerRoutesForSuite(suite string, routes []aphruntime.EvalRoute) []aphruntime.EvalRoute {
	if !strings.EqualFold(suite, aphruntime.EvalSuiteBoundaryAttack) {
		return nil
	}
	return routes
}

func evalModelBakeoffAttackCorpusForSuite(suite string, corpus *aphruntime.EvalAttackCorpus) *aphruntime.EvalAttackCorpus {
	if !strings.EqualFold(suite, aphruntime.EvalSuiteBoundaryAttack) {
		return nil
	}
	return corpus
}

func evalModelBakeoffMaxAttacksForSuite(suite string, n int) int {
	if !strings.EqualFold(suite, aphruntime.EvalSuiteBoundaryAttack) {
		return 0
	}
	return n
}

type evalModelBakeoffLiveCostInputs struct {
	Mode                  string
	Suites                []string
	ScenarioIDs           []string
	Rollouts              int
	Routes                []aphruntime.EvalRoute
	AttackerRoutes        []aphruntime.EvalRoute
	AttackCorpus          *aphruntime.EvalAttackCorpus
	MaxAttacksPerScenario int
	Scoring               string
	JudgeRoutes           []aphruntime.EvalRoute
	ProviderRetries       int
	Threshold             int
	Confirmed             bool
}

func estimateEvalModelBakeoffLiveCost(inputs evalModelBakeoffLiveCostInputs) (*evalModelBakeoffLiveCostEstimate, error) {
	if !strings.EqualFold(strings.TrimSpace(inputs.Mode), aphruntime.EvalModeLive) {
		return nil, nil
	}
	rollouts := inputs.Rollouts
	if rollouts <= 0 {
		rollouts = 5
	}
	estimate := &evalModelBakeoffLiveCostEstimate{
		Threshold: inputs.Threshold,
		Confirmed: inputs.Confirmed,
		Notes: []string{
			"provider_calls estimates live model calls before transient retry attempts",
			"subject, attacker, and judge calls are separated because provider_usage reports only subject-route usage",
		},
	}
	if inputs.ProviderRetries > 0 {
		estimate.Notes = append(estimate.Notes, fmt.Sprintf("provider retries can add up to %d additional attempt(s) per failed provider call", inputs.ProviderRetries))
	}
	resultRuns := 0
	for _, suite := range inputs.Suites {
		scenarioIDs, err := evalModelBakeoffSelectedScenarioIDs(suite, inputs.ScenarioIDs)
		if err != nil {
			return nil, err
		}
		if len(scenarioIDs) == 0 {
			continue
		}
		if strings.EqualFold(suite, aphruntime.EvalSuiteBoundaryAttack) {
			subject, attacker, results := evalModelBakeoffBoundaryAttackCallEstimate(scenarioIDs, rollouts, len(inputs.Routes), inputs.AttackerRoutes, inputs.AttackCorpus, inputs.MaxAttacksPerScenario)
			estimate.SubjectCalls += subject
			estimate.AttackerCalls += attacker
			resultRuns += results
			continue
		}
		if strings.EqualFold(suite, aphruntime.EvalSuiteChallenge) {
			subject, attacker, results := evalModelBakeoffChallengeCallEstimate(scenarioIDs, rollouts, len(inputs.Routes), inputs.AttackerRoutes)
			estimate.SubjectCalls += subject
			estimate.AttackerCalls += attacker
			resultRuns += results
			continue
		}
		results := len(inputs.Routes) * len(scenarioIDs) * rollouts
		estimate.SubjectCalls += results
		resultRuns += results
	}
	if strings.EqualFold(strings.TrimSpace(inputs.Scoring), aphruntime.EvalScoringJudge) && len(inputs.JudgeRoutes) > 0 {
		estimate.JudgeCalls = resultRuns * len(inputs.JudgeRoutes)
	}
	estimate.ProviderCalls = estimate.SubjectCalls + estimate.AttackerCalls + estimate.JudgeCalls
	estimate.ConfirmationRequired = inputs.Threshold > 0 && estimate.ProviderCalls > inputs.Threshold
	return estimate, nil
}

func evalModelBakeoffSelectedScenarioIDs(suite string, selected []string) ([]string, error) {
	infos, err := aphruntime.ListEvalScenarios(suite)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		out := make([]string, 0, len(infos))
		for _, info := range infos {
			out = append(out, info.ID)
		}
		return out, nil
	}
	wanted := make(map[string]bool, len(selected))
	for _, raw := range selected {
		id := strings.TrimSpace(raw)
		if id != "" {
			wanted[id] = true
		}
	}
	if len(wanted) == 0 {
		out := make([]string, 0, len(infos))
		for _, info := range infos {
			out = append(out, info.ID)
		}
		return out, nil
	}
	var out []string
	for _, info := range infos {
		if wanted[info.ID] {
			out = append(out, info.ID)
			delete(wanted, info.ID)
		}
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for id := range wanted {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("unknown eval scenario(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func evalModelBakeoffBoundaryAttackCallEstimate(scenarioIDs []string, rollouts int, routeCount int, attackerRoutes []aphruntime.EvalRoute, corpus *aphruntime.EvalAttackCorpus, maxAttacksPerScenario int) (int, int, int) {
	if routeCount <= 0 || len(scenarioIDs) == 0 {
		return 0, 0, 0
	}
	if corpus != nil {
		cases, turns := evalModelBakeoffAttackCorpusCounts(corpus, scenarioIDs, maxAttacksPerScenario)
		return routeCount * turns, 0, routeCount * cases
	}
	attackerCount := len(attackerRoutes)
	if attackerCount <= 0 {
		attackerCount = 1
	}
	const builtInBoundaryAttackTurnEstimate = 2
	results := routeCount * attackerCount * len(scenarioIDs) * rollouts
	calls := results * builtInBoundaryAttackTurnEstimate
	return calls, calls, results
}

func evalModelBakeoffChallengeCallEstimate(scenarioIDs []string, rollouts int, routeCount int, attackerRoutes []aphruntime.EvalRoute) (int, int, int) {
	if routeCount <= 0 || len(scenarioIDs) == 0 {
		return 0, 0, 0
	}
	var boundaryIDs []string
	nonBoundary := 0
	for _, id := range scenarioIDs {
		if strings.HasPrefix(strings.TrimSpace(id), "boundary_") {
			boundaryIDs = append(boundaryIDs, id)
		} else {
			nonBoundary++
		}
	}
	subject, attacker, boundaryResults := evalModelBakeoffBoundaryAttackCallEstimate(boundaryIDs, rollouts, routeCount, attackerRoutes, nil, 0)
	const nonBoundaryChallengeTurnEstimate = 2
	nonBoundaryResults := routeCount * nonBoundary * rollouts
	subject += nonBoundaryResults * nonBoundaryChallengeTurnEstimate
	return subject, attacker, boundaryResults + nonBoundaryResults
}

func evalModelBakeoffAttackCorpusCounts(corpus *aphruntime.EvalAttackCorpus, scenarioIDs []string, maxAttacksPerScenario int) (int, int) {
	if corpus == nil {
		return 0, 0
	}
	wanted := map[string]bool{}
	for _, id := range scenarioIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = true
		}
	}
	turnCountsByScenario := map[string][]int{}
	for _, attack := range corpus.Attacks {
		id := strings.TrimSpace(attack.ScenarioID)
		if id == "" || !wanted[id] {
			continue
		}
		turns := len(attack.Turns)
		if turns <= 0 {
			turns = 1
		}
		turnCountsByScenario[id] = append(turnCountsByScenario[id], turns)
	}
	total := 0
	cases := 0
	for _, id := range scenarioIDs {
		id = strings.TrimSpace(id)
		turnCounts := turnCountsByScenario[id]
		if maxAttacksPerScenario > 0 && len(turnCounts) > maxAttacksPerScenario {
			sort.Sort(sort.Reverse(sort.IntSlice(turnCounts)))
			turnCounts = turnCounts[:maxAttacksPerScenario]
		}
		cases += len(turnCounts)
		for _, turns := range turnCounts {
			total += turns
		}
	}
	return cases, total
}

func buildEvalModelBakeoffReport(role string, readiness evalModelBakeoffReadiness, mode string, suites []string, rollouts int, jobs int, reports []aphruntime.EvalReport, liveCostEstimate *evalModelBakeoffLiveCostEstimate) evalModelBakeoffReport {
	report := evalModelBakeoffReport{
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		Role:             role,
		RoleStatus:       readiness.Status,
		QualityOracle:    readiness.QualityOracle,
		Mode:             mode,
		Suites:           append([]string(nil), suites...),
		Rollouts:         rollouts,
		Jobs:             jobs,
		LiveCostEstimate: liveCostEstimate,
		RoleReadiness:    evalModelBakeoffRoleReadinesses(),
		SuiteReports:     reports,
		Notes: []string{
			"model bakeoff reports evidence only; it does not mutate runtime model-slot configuration",
			"provider_usage excludes attacker and judge overhead; use suite_reports for judge/provider failure details",
			"cost_fidelity is deterministic prompt-shape evidence, not provider billing truth",
		},
	}
	routeStats := map[string]*evalModelBakeoffRouteAccumulator{}
	for _, suiteReport := range reports {
		report.ScenarioCount += suiteReport.ScenarioCount
		report.ResultCount += suiteReport.ResultCount
		for _, result := range suiteReport.Results {
			acc := routeStats[result.Route]
			if acc == nil {
				acc = &evalModelBakeoffRouteAccumulator{summary: evalModelBakeoffRouteSummary{
					Route:    result.Route,
					Provider: result.Provider,
					Model:    result.Model,
					Effort:   result.Effort,
				}}
				routeStats[result.Route] = acc
			}
			acc.add(result)
		}
	}
	keys := make([]string, 0, len(routeStats))
	for key := range routeStats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		report.Routes = append(report.Routes, routeStats[key].finish())
	}
	report.RouteCount = len(report.Routes)
	return report
}

type evalModelBakeoffRouteAccumulator struct {
	summary            evalModelBakeoffRouteSummary
	contextHits        int
	contextLeaks       int
	contextRetention   int
	contextRetained    int
	costStablePrefixes int
}

func (a *evalModelBakeoffRouteAccumulator) add(result aphruntime.EvalScenarioResult) {
	a.summary.ResultCount++
	if result.Pass {
		a.summary.PassCount++
	}
	a.summary.HardFailureCount += len(result.HardFailures)
	if result.ProviderFailure {
		a.summary.ProviderFailureCount++
	}
	if result.Ambiguous {
		a.summary.AmbiguousCount++
	}
	a.summary.DurationMillis += result.DurationMillis
	if result.DurationMillis > a.summary.MaxDurationMillis {
		a.summary.MaxDurationMillis = result.DurationMillis
	}
	if metrics := result.ContextFidelity; metrics != nil && metrics.Clean {
		a.summary.ContextCleanResults++
		if metrics.HydrationHit {
			a.contextHits++
		}
		if metrics.HydrationLeak || metrics.ReplyLeak {
			a.contextLeaks++
		}
		if len(metrics.RetentionEvidenceIDs) > 0 && metrics.ExpectedReferenceTurns > 0 {
			a.contextRetention++
			if metrics.EvidenceReferenceRetained {
				a.contextRetained++
			}
		}
	}
	if metrics := result.CostFidelity; metrics != nil && metrics.Clean {
		a.summary.CostCleanResults++
		a.summary.EstimatedPromptTokens += metrics.EstimatedPromptTokens
		if metrics.MaxPromptTokens > a.summary.MaxPromptTokens {
			a.summary.MaxPromptTokens = metrics.MaxPromptTokens
		}
		a.summary.CostModelCallCount += metrics.ModelCallCount
		a.summary.CacheEligiblePromptCount += metrics.CacheEligiblePromptCount
		if metrics.StablePrefixStable {
			a.costStablePrefixes++
		}
	}
	if metrics := result.ProviderUsage; metrics != nil && metrics.Clean {
		a.summary.ProviderUsageCleanResults++
		a.summary.ProviderModelCallCount += metrics.ModelCallCount
		a.summary.ProviderInputTokens += metrics.InputTokens
		a.summary.ProviderOutputTokens += metrics.OutputTokens
		a.summary.ProviderTotalTokens += metrics.TotalTokens
		a.summary.ProviderCacheReadTokens += metrics.CacheReadTokens
		a.summary.ProviderCacheWriteTokens += metrics.CacheWriteTokens
		a.summary.ProviderCacheCreationTokens += metrics.CacheCreationTokens
	}
}

func (a *evalModelBakeoffRouteAccumulator) finish() evalModelBakeoffRouteSummary {
	a.summary.PassRate = evalModelBakeoffRate(a.summary.PassCount, a.summary.ResultCount)
	a.summary.HydrationHitRate = evalModelBakeoffRate(a.contextHits, a.summary.ContextCleanResults)
	a.summary.CrossThreadLeakRate = evalModelBakeoffRate(a.contextLeaks, a.summary.ContextCleanResults)
	a.summary.EvidenceReferenceRetentionRate = evalModelBakeoffRate(a.contextRetained, a.contextRetention)
	a.summary.StablePrefixStabilityRate = evalModelBakeoffRate(a.costStablePrefixes, a.summary.CostCleanResults)
	return a.summary
}

func evalModelBakeoffRate(n int, d int) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func normalizeEvalModelBakeoffFormat(format string, jsonAlias bool) string {
	if jsonAlias {
		return "json"
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		return "json"
	case "markdown", "md":
		return "markdown"
	default:
		return "human"
	}
}

func renderEvalModelBakeoffReport(report evalModelBakeoffReport, format string) (string, error) {
	switch format {
	case "json":
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal eval model bakeoff report: %w", err)
		}
		return string(raw) + "\n", nil
	case "markdown":
		return renderEvalModelBakeoffMarkdown(report), nil
	default:
		return renderEvalModelBakeoffHuman(report), nil
	}
}

func renderEvalModelBakeoffHuman(report evalModelBakeoffReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Aphelion model bakeoff: %s (%s)\n", report.Role, report.RoleStatus)
	fmt.Fprintf(&b, "mode=%s suites=%s routes=%d results=%d oracle=%s\n", report.Mode, strings.Join(report.Suites, ","), report.RouteCount, report.ResultCount, report.QualityOracle)
	if estimate := report.LiveCostEstimate; estimate != nil {
		fmt.Fprintf(&b, "live_cost_estimate provider_calls=%d subject=%d attacker=%d judge=%d threshold=%d confirmed=%t\n",
			estimate.ProviderCalls,
			estimate.SubjectCalls,
			estimate.AttackerCalls,
			estimate.JudgeCalls,
			estimate.Threshold,
			estimate.Confirmed,
		)
	}
	for _, route := range report.Routes {
		fmt.Fprintf(&b, "- %s effort=%s pass=%.2f%% results=%d hard=%d provider_failures=%d ambiguous=%d est_prompt=%d provider_tokens=%d cache_read=%d duration_ms=%d\n",
			route.Route,
			firstNonEmptyEvalModelBakeoff(route.Effort, "low"),
			route.PassRate*100,
			route.ResultCount,
			route.HardFailureCount,
			route.ProviderFailureCount,
			route.AmbiguousCount,
			route.EstimatedPromptTokens,
			route.ProviderTotalTokens,
			route.ProviderCacheReadTokens,
			route.DurationMillis,
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderEvalModelBakeoffMarkdown(report evalModelBakeoffReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Aphelion Model Bakeoff: %s\n\n", report.Role)
	fmt.Fprintf(&b, "- Status: `%s`\n", report.RoleStatus)
	fmt.Fprintf(&b, "- Mode: `%s`\n", report.Mode)
	fmt.Fprintf(&b, "- Suites: `%s`\n", strings.Join(report.Suites, "`, `"))
	fmt.Fprintf(&b, "- Quality oracle: %s\n\n", report.QualityOracle)
	if estimate := report.LiveCostEstimate; estimate != nil {
		fmt.Fprintf(&b, "- Live cost estimate: `%d` provider calls (`%d` subject, `%d` attacker, `%d` judge), threshold `%d`, confirmed `%t`\n\n",
			estimate.ProviderCalls,
			estimate.SubjectCalls,
			estimate.AttackerCalls,
			estimate.JudgeCalls,
			estimate.Threshold,
			estimate.Confirmed,
		)
	}
	b.WriteString("## Route Frontier\n\n")
	b.WriteString("| Route | Effort | Pass | Results | Hard | Provider failures | Ambiguous | Est prompt | Provider tokens | Cache read | Duration ms |\n")
	b.WriteString("| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, route := range report.Routes {
		fmt.Fprintf(&b, "| `%s` | `%s` | %.2f%% | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			route.Route,
			firstNonEmptyEvalModelBakeoff(route.Effort, "low"),
			route.PassRate*100,
			route.ResultCount,
			route.HardFailureCount,
			route.ProviderFailureCount,
			route.AmbiguousCount,
			route.EstimatedPromptTokens,
			route.ProviderTotalTokens,
			route.ProviderCacheReadTokens,
			route.DurationMillis,
		)
	}
	b.WriteString("\n## Role Readiness\n\n")
	b.WriteString("| Role | Status | Quality oracle |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, readiness := range report.RoleReadiness {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", readiness.Role, readiness.Status, readiness.QualityOracle)
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeEvalModelBakeoffReport(path string, report evalModelBakeoffReport, format string, rendered string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal eval model bakeoff report: %w", err)
		}
		rendered = string(raw) + "\n"
	} else if rendered == "" {
		var err error
		rendered, err = renderEvalModelBakeoffReport(report, format)
		if err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("write eval model bakeoff report %s: %w", path, err)
	}
	return nil
}

func firstNonEmptyEvalModelBakeoff(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
