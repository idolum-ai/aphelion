package commandeffect

import (
	"path/filepath"
	"strings"
)

type Kind string

const (
	KindReadOnlyInspection Kind = "read_only_inspection"
	KindValidation         Kind = "validation_execution"
	KindBuildArtifact      Kind = "build_or_generated_artifact"
	KindWorkspaceMutation  Kind = "workspace_mutation"
	KindRepoHistory        Kind = "repo_or_history_mutation"
	KindExternal           Kind = "network_or_external_contact"
	KindExternalAccount    Kind = "external_account_command"
	KindRemoteHost         Kind = "remote_host_operation"
	KindService            Kind = "process_or_service_change"
	KindCapability         Kind = "capability_acquisition"
	KindCredential         Kind = "credential_or_config_effect"
	KindDatabase           Kind = "database_or_state_mutation"
	KindHighImpactStorage  Kind = "high_impact_storage"
	KindUnknown            Kind = "unknown_or_unclassified"
)

const (
	ReasonGitCommit            = "git commit"
	ReasonGitPush              = "git push"
	ReasonExternalAccount      = "external account command"
	ReasonRemoteHostOperation  = "remote host operation"
	ReasonServiceProcessChange = "service/process change"
)

type Effect struct {
	Kind          Kind
	Reason        string
	Command       string
	GitSubcommand string
	Action        string
	Provider      string
	Target        string
	Subject       string
	SideEffects   bool
}

func (e Effect) ReadOnlyAllowed() bool {
	return e.Kind == KindReadOnlyInspection && !e.SideEffects
}

type BoundaryKind string

const (
	BoundaryGitCommit            BoundaryKind = "git_commit"
	BoundaryGitPush              BoundaryKind = "git_push"
	BoundaryExternalAccount      BoundaryKind = "external_account_command"
	BoundaryRemoteHostOperation  BoundaryKind = "remote_host_operation"
	BoundaryServiceProcessChange BoundaryKind = "service_process_change"
)

type Boundary struct {
	Kind   BoundaryKind
	Effect Effect
}

type EffectPlan struct {
	Command             string
	Effects             []Effect
	Dynamic             bool
	DynamicReason       string
	MultipleAuthorities bool
}

func Classify(command string) Effect {
	plan := PlanCommand(command)
	return RepresentativeEffect(plan)
}

func PlanCommand(command string) EffectPlan {
	compact := NormalizeCommand(command)
	if compact == "" {
		return EffectPlan{
			Command: compact,
			Effects: []Effect{{Kind: KindUnknown, Reason: "empty command", SideEffects: true}},
		}
	}
	if AppServerStatusCommandAllowed(compact) {
		return EffectPlan{
			Command: compact,
			Effects: []Effect{{Kind: KindReadOnlyInspection, Reason: "status inspection"}},
		}
	}
	if reason := executableResolutionDynamicReason(command); reason != "" {
		return EffectPlan{
			Command:       compact,
			Effects:       []Effect{{Kind: KindUnknown, Reason: reason, SideEffects: true}},
			Dynamic:       true,
			DynamicReason: reason,
		}
	}
	if reason := dynamicShellExecutionReason(command); reason != "" {
		return EffectPlan{
			Command:       compact,
			Effects:       []Effect{{Kind: KindUnknown, Reason: reason, SideEffects: true}},
			Dynamic:       true,
			DynamicReason: reason,
		}
	}
	lower := strings.ToLower(compact)
	if commandHasHighImpactStorageMarker(lower) {
		return EffectPlan{
			Command: compact,
			Effects: []Effect{{Kind: KindHighImpactStorage, Reason: "high-impact storage command", SideEffects: true}},
		}
	}
	segments := commandSegments(command)
	if len(segments) == 0 {
		return EffectPlan{
			Command: compact,
			Effects: []Effect{{Kind: KindUnknown, Reason: "unclassified command", SideEffects: true}},
		}
	}
	var effects []Effect
	if commandHasRedirection(lower) {
		effects = append(effects, Effect{Kind: KindBuildArtifact, Reason: "shell redirection", SideEffects: true})
	}
	for _, segment := range segments {
		effect := classifySegment(segment)
		effects = appendPlanEffect(effects, effect)
		for _, extra := range additionalSegmentEffects(segment, effect) {
			effects = appendPlanEffect(effects, extra)
		}
	}
	if len(effects) == 0 {
		effects = append(effects, Effect{Kind: KindReadOnlyInspection, Reason: "read-only inspection"})
	}
	if reason := dynamicEffectReason(effects); reason != "" {
		return EffectPlan{
			Command:       compact,
			Effects:       effects,
			Dynamic:       true,
			DynamicReason: reason,
		}
	}
	return EffectPlan{
		Command:             compact,
		Effects:             effects,
		MultipleAuthorities: planHasMultipleAuthorityEffects(effects),
	}
}

func dynamicEffectReason(effects []Effect) string {
	for _, effect := range effects {
		reason := strings.TrimSpace(effect.Reason)
		if strings.Contains(reason, "dynamic shell execution") {
			return reason
		}
		if strings.Contains(reason, "transport override") {
			return reason
		}
	}
	return ""
}

func appendPlanEffect(effects []Effect, effect Effect) []Effect {
	if effect.Kind == KindReadOnlyInspection && !effect.SideEffects {
		if len(effects) == 0 {
			return append(effects, effect)
		}
		if effect.Action != "" || effect.Target != "" || effect.Subject != "" {
			for i, existing := range effects {
				if existing.Kind == KindReadOnlyInspection && !existing.SideEffects &&
					existing.Action == "" && existing.Target == "" && existing.Subject == "" {
					effects[i] = effect
					return effects
				}
				if existing.Kind == effect.Kind && existing.Action == effect.Action &&
					existing.Target == effect.Target && existing.Subject == effect.Subject &&
					existing.Command == effect.Command {
					return effects
				}
			}
			return append(effects, effect)
		}
		return effects
	}
	if effect.SideEffects {
		return append(effects, effect)
	}
	for i, existing := range effects {
		if existing.Kind == effect.Kind && existing.Reason == effect.Reason && existing.Command == effect.Command && existing.GitSubcommand == effect.GitSubcommand {
			if effectDominates(effect, existing) {
				effects[i] = effect
			}
			return effects
		}
	}
	return append(effects, effect)
}

func planHasMultipleAuthorityEffects(effects []Effect) bool {
	count := 0
	for _, effect := range effects {
		if !effect.SideEffects || !effectCountsAsIndependentAuthority(effect) {
			continue
		}
		count++
		if count > 1 {
			return true
		}
	}
	return false
}

func effectCountsAsIndependentAuthority(effect Effect) bool {
	switch effect.Kind {
	case KindReadOnlyInspection:
		return false
	default:
		return effect.SideEffects
	}
}

func effectDominates(candidate Effect, current Effect) bool {
	return effectRank(candidate.Kind) > effectRank(current.Kind)
}

func effectRank(kind Kind) int {
	switch kind {
	case KindReadOnlyInspection:
		return 0
	case KindValidation:
		return 20
	case KindBuildArtifact:
		return 30
	case KindWorkspaceMutation:
		return 40
	case KindUnknown:
		return 45
	case KindRepoHistory:
		return 50
	case KindExternal:
		return 60
	case KindExternalAccount, KindCredential, KindCapability:
		return 70
	case KindRemoteHost:
		return 80
	case KindService:
		return 90
	case KindDatabase:
		return 100
	case KindHighImpactStorage:
		return 110
	default:
		return 10
	}
}

func BoundaryForCommand(command string) (Boundary, bool) {
	plan := PlanCommand(command)
	return BoundaryForPlan(plan)
}

func BoundaryForPlan(plan EffectPlan) (Boundary, bool) {
	if plan.Dynamic || plan.MultipleAuthorities {
		return Boundary{}, false
	}
	effect := RepresentativeEffect(plan)
	switch effect.Kind {
	case KindRepoHistory:
		switch effect.Reason {
		case ReasonGitCommit:
			return Boundary{Kind: BoundaryGitCommit, Effect: effect}, true
		case ReasonGitPush:
			return Boundary{Kind: BoundaryGitPush, Effect: effect}, true
		}
	case KindExternalAccount, KindCredential:
		if isExternalAccountCommand(effect.Command) {
			return Boundary{Kind: BoundaryExternalAccount, Effect: Effect{
				Kind:          effect.Kind,
				Reason:        ReasonExternalAccount,
				Command:       effect.Command,
				GitSubcommand: effect.GitSubcommand,
				Action:        effect.Action,
				Provider:      effect.Provider,
				Target:        effect.Target,
				Subject:       effect.Subject,
				SideEffects:   effect.SideEffects,
			}}, true
		}
	case KindRemoteHost:
		return Boundary{Kind: BoundaryRemoteHostOperation, Effect: Effect{
			Kind:        effect.Kind,
			Reason:      ReasonRemoteHostOperation,
			Command:     effect.Command,
			Action:      effect.Action,
			Provider:    effect.Provider,
			Target:      effect.Target,
			Subject:     effect.Subject,
			SideEffects: effect.SideEffects,
		}}, true
	case KindService:
		return Boundary{Kind: BoundaryServiceProcessChange, Effect: Effect{
			Kind:        effect.Kind,
			Reason:      ReasonServiceProcessChange,
			Command:     effect.Command,
			Action:      effect.Action,
			Provider:    effect.Provider,
			Target:      effect.Target,
			Subject:     effect.Subject,
			SideEffects: effect.SideEffects,
		}}, true
	}
	return Boundary{}, false
}

func RepresentativeEffect(plan EffectPlan) Effect {
	if plan.Dynamic {
		return Effect{Kind: KindUnknown, Reason: plan.DynamicReason, SideEffects: true}
	}
	if plan.MultipleAuthorities {
		return Effect{Kind: KindUnknown, Reason: "multiple authority effects require effect plan", SideEffects: true}
	}
	out := Effect{Kind: KindReadOnlyInspection, Reason: "read-only inspection"}
	for _, effect := range plan.Effects {
		if effectDominates(effect, out) {
			out = effect
		}
	}
	return out
}

func NormalizeCommand(command string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
}

func classifySegment(segment string) Effect {
	tokens := shellTokens(segment)
	if len(tokens) == 0 {
		return Effect{Kind: KindReadOnlyInspection, Reason: "empty segment"}
	}
	idx := commandTokenIndex(tokens)
	if idx < 0 || idx >= len(tokens) {
		return Effect{Kind: KindUnknown, Reason: "unclassified wrapper", SideEffects: true}
	}
	rawCmd := trimShellToken(tokens[idx].Text)
	if strings.Contains(rawCmd, "/") {
		return Effect{Kind: KindUnknown, Reason: "path-qualified executable", Command: rawCmd, SideEffects: true}
	}
	cmd := normalizeCommandToken(tokens[idx].Text)
	args := tokens[idx+1:]
	if cmd == "sh" || cmd == "bash" {
		if script := shellCommandStringArg(args); script != "" {
			return Classify(script)
		}
	}
	lowerSegment := strings.ToLower(strings.Join(tokenTexts(tokens[idx:]), " "))
	switch cmd {
	case "eval", "source":
		return Effect{Kind: KindUnknown, Reason: cmd + " dynamic shell execution", Command: cmd, SideEffects: true}
	case "set", "printf", "echo", "true", "false":
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " shell builtin", Command: cmd}
	case "test":
		if target := testFileMetadataTarget(args); target != "" {
			return fileMetadataReadEffect(cmd, target)
		}
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " shell builtin", Command: cmd}
	case "[":
		if target := testFileMetadataTarget(args); target != "" {
			return fileMetadataReadEffect(cmd, target)
		}
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " shell builtin", Command: cmd}
	case "git":
		return classifyGitCommand(args)
	case "rg", "grep", "egrep", "fgrep", "cat", "nl", "head", "tail", "less", "more", "wc", "pwd", "find":
		if cmd == "find" && findArgsMutateOrExecute(args) {
			return Effect{Kind: KindWorkspaceMutation, Reason: "find mutation", Command: cmd, SideEffects: true}
		}
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " inspection", Command: cmd}
	case "ls":
		if target := lsFileMetadataTarget(args); target != "" {
			return fileMetadataReadEffect(cmd, target)
		}
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " inspection", Command: cmd}
	case "stat":
		if target := statFileMetadataTarget(args); target != "" {
			return fileMetadataReadEffect(cmd, target)
		}
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " metadata inspection", Command: cmd}
	case "sed":
		if tokensContain(args, "-i") || tokensContainPrefix(args, "-i") {
			return Effect{Kind: KindWorkspaceMutation, Reason: "sed in-place edit", Command: cmd, SideEffects: true}
		}
		if sedArgsExecuteCommands(args) {
			return Effect{Kind: KindUnknown, Reason: "sed command execution", Command: cmd, SideEffects: true}
		}
		return Effect{Kind: KindReadOnlyInspection, Reason: "sed inspection", Command: cmd}
	case "go":
		return classifyGoCommand(args)
	case "npm", "pnpm", "yarn", "make", "pytest", "python", "python3", "uv", "pip", "pip3", "cargo", "playwright", "npx":
		return classifyBuildTestPackageCommand(cmd, args)
	case "curl", "wget":
		return Effect{Kind: KindExternal, Reason: cmd + " external contact", Command: cmd, Action: "external_contact", Provider: cmd, SideEffects: true}
	case "ssh", "scp", "rsync":
		return Effect{Kind: KindRemoteHost, Reason: ReasonRemoteHostOperation, Command: cmd, Action: cmd, Provider: cmd, SideEffects: true}
	case "rm", "mv", "cp", "mkdir", "touch", "chmod", "chown", "ln", "tee", "apply_patch":
		return Effect{Kind: KindWorkspaceMutation, Reason: cmd + " filesystem mutation", Command: cmd, SideEffects: true}
	case "systemctl":
		if systemctlArgsLookReadOnly(args) {
			return Effect{Kind: KindReadOnlyInspection, Reason: "systemctl inspection", Command: cmd}
		}
		return Effect{Kind: KindService, Reason: ReasonServiceProcessChange, Command: cmd, Action: systemctlAction(args), Provider: cmd, SideEffects: true}
	case "service", "launchctl", "kill", "pkill", "docker", "docker-compose", "kubectl":
		return Effect{Kind: KindService, Reason: ReasonServiceProcessChange, Command: cmd, Action: cmd, Provider: cmd, SideEffects: true}
	case "xargs":
		return Effect{Kind: KindUnknown, Reason: "xargs dynamic shell execution", Command: cmd, SideEffects: true}
	case "ps", "df", "du", "uname", "hostname", "uptime", "date", "whoami", "id", "env", "printenv", "sleep":
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " status inspection", Command: cmd}
	case "sqlite3", "psql", "mysql":
		if sqlLooksMutating(lowerSegment) {
			return Effect{Kind: KindDatabase, Reason: "database mutation", Command: cmd, SideEffects: true}
		}
		return Effect{Kind: KindUnknown, Reason: cmd + " database command", Command: cmd, SideEffects: true}
	case "gh", "aws", "gcloud", "az", "op":
		if commandContainsAny(lowerSegment, " login", " logout", " auth ", " configure", " token", " secret") {
			return Effect{Kind: KindCredential, Reason: cmd + " credential/config effect", Command: cmd, Action: "credential_config", Provider: cmd, SideEffects: true}
		}
		return Effect{Kind: KindExternalAccount, Reason: ReasonExternalAccount, Command: cmd, Action: externalAccountAction(cmd, args), Provider: cmd, SideEffects: true}
	default:
		if sqlLooksMutating(lowerSegment) {
			return Effect{Kind: KindDatabase, Reason: "database mutation", Command: cmd, SideEffects: true}
		}
		return Effect{Kind: KindUnknown, Reason: cmd + " unclassified", Command: cmd, SideEffects: true}
	}
}

func fileMetadataReadEffect(cmd string, target string) Effect {
	return Effect{
		Kind:    KindReadOnlyInspection,
		Reason:  cmd + " file metadata read",
		Command: cmd,
		Action:  "file_metadata_read",
		Target:  strings.TrimSpace(target),
	}
}

func lsFileMetadataTarget(args []shellToken) string {
	var targets []string
	endOptions := false
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i].Text)
		if token == "" {
			continue
		}
		if endOptions {
			targets = append(targets, trimShellToken(token))
			continue
		}
		if token == "--" {
			endOptions = true
			continue
		}
		if lsOptionConsumesValue(token) {
			if !shortOptionHasInlineOperand(token, 'I') && !longOptionHasInlineOperand(token) && i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(token, "-") && token != "-" {
			continue
		}
		targets = append(targets, trimShellToken(token))
	}
	if len(targets) == 0 {
		return ""
	}
	return strings.Join(targets, " ")
}

func statFileMetadataTarget(args []shellToken) string {
	var targets []string
	endOptions := false
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i].Text)
		if token == "" {
			continue
		}
		if endOptions {
			targets = append(targets, trimShellToken(token))
			continue
		}
		if token == "--" {
			endOptions = true
			continue
		}
		if statOptionConsumesValue(token) {
			if !shortOptionHasInlineOperand(token, 'c') && !longOptionHasInlineOperand(token) && i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(token, "-") && token != "-" {
			continue
		}
		targets = append(targets, trimShellToken(token))
	}
	if len(targets) == 0 {
		return ""
	}
	return strings.Join(targets, " ")
}

func lsOptionConsumesValue(token string) bool {
	token = strings.TrimSpace(token)
	if token == "-I" || token == "--ignore" || strings.HasPrefix(token, "--ignore=") ||
		token == "--hide" || strings.HasPrefix(token, "--hide=") ||
		token == "--quoting-style" || strings.HasPrefix(token, "--quoting-style=") ||
		token == "--sort" || strings.HasPrefix(token, "--sort=") ||
		token == "--time" || strings.HasPrefix(token, "--time=") ||
		token == "--time-style" || strings.HasPrefix(token, "--time-style=") ||
		token == "--block-size" || strings.HasPrefix(token, "--block-size=") {
		return true
	}
	if strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--") {
		return strings.Contains(token[1:], "I")
	}
	return false
}

func statOptionConsumesValue(token string) bool {
	token = strings.TrimSpace(token)
	if token == "-c" ||
		token == "--format" ||
		strings.HasPrefix(token, "--format=") ||
		token == "--printf" ||
		strings.HasPrefix(token, "--printf=") {
		return true
	}
	if strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--") {
		return strings.Contains(token[1:], "c")
	}
	return false
}

func shortOptionHasInlineOperand(token string, option byte) bool {
	if !strings.HasPrefix(token, "-") || strings.HasPrefix(token, "--") {
		return false
	}
	body := token[1:]
	idx := strings.IndexByte(body, option)
	return idx >= 0 && idx < len(body)-1
}

func longOptionHasInlineOperand(token string) bool {
	return strings.HasPrefix(token, "--") && strings.Contains(token, "=")
}

func testFileMetadataTarget(args []shellToken) string {
	idx := 0
	for idx < len(args) && strings.TrimSpace(args[idx].Text) == "!" {
		idx++
	}
	if idx+1 >= len(args) {
		return ""
	}
	op := strings.TrimSpace(args[idx].Text)
	switch op {
	case "-a", "-b", "-c", "-d", "-e", "-f", "-g", "-h", "-k", "-L", "-O", "-p", "-r", "-S", "-s", "-u", "-w", "-x":
		return trimShellToken(args[idx+1].Text)
	default:
		return ""
	}
}

func classifyGitCommand(args []shellToken) Effect {
	if gitArgsOverrideTransport(args) {
		return Effect{Kind: KindUnknown, Reason: "git transport override", Command: "git", SideEffects: true}
	}
	subcmd := firstGitSubcommand(args)
	switch subcmd {
	case "status", "diff", "log", "show", "grep", "rev-parse", "cat-file", "describe", "ls-files", "show-ref":
		return Effect{Kind: KindReadOnlyInspection, Reason: "git " + subcmd + " inspection", Command: "git", GitSubcommand: subcmd}
	case "branch":
		if gitBranchArgsLookReadOnly(args) {
			return Effect{Kind: KindReadOnlyInspection, Reason: "git branch inspection", Command: "git", GitSubcommand: subcmd}
		}
		return Effect{Kind: KindRepoHistory, Reason: "git branch mutation", Command: "git", GitSubcommand: subcmd, Action: "git_branch", Provider: "git", SideEffects: true}
	case "remote":
		if gitRemoteArgsLookReadOnly(args) {
			return Effect{Kind: KindReadOnlyInspection, Reason: "git remote inspection", Command: "git", GitSubcommand: subcmd}
		}
		return Effect{Kind: KindRepoHistory, Reason: "git remote mutation", Command: "git", GitSubcommand: subcmd, Action: "git_remote", Provider: "git", SideEffects: true}
	case "fetch", "pull", "clone", "ls-remote", "submodule":
		return Effect{Kind: KindExternal, Reason: "git " + subcmd, Command: "git", GitSubcommand: subcmd, Action: subcmd, Provider: "git", SideEffects: true}
	case "commit":
		return Effect{Kind: KindRepoHistory, Reason: ReasonGitCommit, Command: "git", GitSubcommand: subcmd, Action: "git_commit", Provider: "git", SideEffects: true}
	case "push":
		return Effect{Kind: KindRepoHistory, Reason: ReasonGitPush, Command: "git", GitSubcommand: subcmd, Action: "git_push", Provider: "git", SideEffects: true}
	case "add", "checkout", "cherry-pick", "clean", "merge", "mv", "rebase", "reset", "restore", "revert", "rm", "switch", "stash", "tag", "worktree":
		return Effect{Kind: KindRepoHistory, Reason: "git " + subcmd, Command: "git", GitSubcommand: subcmd, Action: "git_" + subcmd, Provider: "git", SideEffects: true}
	case "":
		return Effect{Kind: KindUnknown, Reason: "git command without subcommand", Command: "git", SideEffects: true}
	default:
		return Effect{Kind: KindUnknown, Reason: "git " + subcmd + " unclassified", Command: "git", GitSubcommand: subcmd, SideEffects: true}
	}
}

func classifyGoCommand(args []shellToken) Effect {
	if len(args) == 0 {
		return Effect{Kind: KindUnknown, Reason: "go command without subcommand", Command: "go", SideEffects: true}
	}
	subcmd := strings.ToLower(strings.Trim(args[0].Text, `"'`))
	switch subcmd {
	case "test", "vet":
		return Effect{Kind: KindValidation, Reason: "go " + subcmd, Command: "go", SideEffects: true}
	case "build", "generate", "run":
		return Effect{Kind: KindBuildArtifact, Reason: "go " + subcmd, Command: "go", SideEffects: true}
	case "install", "get", "mod", "work":
		return Effect{Kind: KindCapability, Reason: "go " + subcmd, Command: "go", SideEffects: true}
	case "env":
		if tokensContain(args[1:], "-w") || tokensContain(args[1:], "-u") {
			return Effect{Kind: KindCredential, Reason: "go env mutation", Command: "go", SideEffects: true}
		}
		return Effect{Kind: KindReadOnlyInspection, Reason: "go env inspection", Command: "go"}
	case "version", "list":
		return Effect{Kind: KindReadOnlyInspection, Reason: "go " + subcmd + " inspection", Command: "go"}
	default:
		return Effect{Kind: KindUnknown, Reason: "go " + subcmd + " unclassified", Command: "go", SideEffects: true}
	}
}

func additionalSegmentEffects(segment string, base Effect) []Effect {
	tokens := shellTokens(segment)
	if len(tokens) == 0 {
		return nil
	}
	idx := commandTokenIndex(tokens)
	if idx < 0 || idx >= len(tokens) {
		return nil
	}
	cmd := normalizeCommandToken(tokens[idx].Text)
	args := tokens[idx+1:]
	switch cmd {
	case "curl":
		if tokensContain(args, "-o") || tokensContainPrefix(args, "--output") || tokensContainPrefix(args, "-o") {
			return []Effect{{Kind: KindBuildArtifact, Reason: "curl output file", Command: cmd, Action: "write_output_file", Provider: cmd, SideEffects: true}}
		}
	case "wget":
		if tokensContain(args, "-O") || tokensContainPrefix(args, "--output-document") || tokensContainPrefix(args, "-O") {
			return []Effect{{Kind: KindBuildArtifact, Reason: "wget output file", Command: cmd, Action: "write_output_file", Provider: cmd, SideEffects: true}}
		}
	case "git":
		subcmd := firstGitSubcommand(args)
		switch subcmd {
		case "pull", "clone":
			return []Effect{{Kind: KindRepoHistory, Reason: "git " + subcmd + " workspace/repository mutation", Command: cmd, GitSubcommand: subcmd, Action: "repo_worktree_update", Provider: "git", SideEffects: true}}
		}
	case "scp", "rsync":
		return []Effect{{Kind: KindWorkspaceMutation, Reason: cmd + " local file mutation", Command: cmd, Action: "remote_copy_local_write", Provider: cmd, SideEffects: true}}
	}
	_ = base
	return nil
}

func classifyBuildTestPackageCommand(cmd string, args []shellToken) Effect {
	joined := strings.ToLower(strings.Join(tokenTexts(args), " "))
	subcmd := ""
	if len(args) > 0 {
		subcmd = strings.ToLower(strings.Trim(args[0].Text, `"'`))
	}
	switch cmd {
	case "pytest":
		return Effect{Kind: KindValidation, Reason: "pytest", Command: cmd, SideEffects: true}
	case "python", "python3":
		if strings.Contains(joined, "-m pip install") || strings.Contains(joined, "-m playwright install") {
			return Effect{Kind: KindCapability, Reason: cmd + " package install", Command: cmd, SideEffects: true}
		}
		if strings.Contains(joined, "pytest") || strings.Contains(joined, "unittest") {
			return Effect{Kind: KindValidation, Reason: cmd + " test runner", Command: cmd, SideEffects: true}
		}
	case "npm", "pnpm", "yarn":
		if subcmd == "test" || (subcmd == "run" && strings.Contains(joined, "test")) {
			return Effect{Kind: KindValidation, Reason: cmd + " test", Command: cmd, SideEffects: true}
		}
		if subcmd == "build" || (subcmd == "run" && strings.Contains(joined, "build")) {
			return Effect{Kind: KindBuildArtifact, Reason: cmd + " build", Command: cmd, SideEffects: true}
		}
		if subcmd == "install" || subcmd == "add" || subcmd == "ci" {
			return Effect{Kind: KindCapability, Reason: cmd + " install", Command: cmd, SideEffects: true}
		}
	case "make":
		if subcmd == "architecture" || subcmd == "design-principles" || subcmd == "taste" ||
			strings.Contains(joined, "test") || subcmd == "test" {
			return Effect{Kind: KindValidation, Reason: "make validation", Command: cmd, SideEffects: true}
		}
		if strings.Contains(joined, "build") || subcmd == "build" {
			return Effect{Kind: KindBuildArtifact, Reason: "make build", Command: cmd, SideEffects: true}
		}
		if strings.Contains(joined, "install") || strings.Contains(joined, "update") || strings.Contains(joined, "restart") {
			return Effect{Kind: KindCapability, Reason: "make capability/service target", Command: cmd, SideEffects: true}
		}
	case "uv", "pip", "pip3", "cargo", "playwright", "npx":
		if strings.Contains(joined, "install") || subcmd == "install" || subcmd == "add" {
			return Effect{Kind: KindCapability, Reason: cmd + " install", Command: cmd, SideEffects: true}
		}
		if strings.Contains(joined, "test") || subcmd == "test" {
			return Effect{Kind: KindValidation, Reason: cmd + " test", Command: cmd, SideEffects: true}
		}
		if strings.Contains(joined, "build") || subcmd == "build" {
			return Effect{Kind: KindBuildArtifact, Reason: cmd + " build", Command: cmd, SideEffects: true}
		}
	}
	return Effect{Kind: KindUnknown, Reason: cmd + " unclassified", Command: cmd, SideEffects: true}
}

func externalAccountAction(cmd string, args []shellToken) string {
	switch cmd {
	case "gh":
		values := tokenTexts(args)
		for i := 0; i < len(values); i++ {
			token := strings.ToLower(trimShellToken(values[i]))
			if token != "pr" || i+1 >= len(values) {
				continue
			}
			switch strings.ToLower(trimShellToken(values[i+1])) {
			case "create", "new":
				return "github_pr_create"
			case "merge":
				return "github_pr_merge"
			case "edit":
				return "github_pr_update"
			}
		}
	case "aws":
		values := tokenTexts(args)
		if len(values) >= 2 {
			return "aws_" + normalizeCommandToken(values[0]) + "_" + normalizeCommandToken(values[1])
		}
	}
	return cmd + "_external_account_action"
}

func AppServerStatusCommandAllowed(command string) bool {
	compact := NormalizeCommand(command)
	allowedExact := map[string]struct{}{
		"hostname":                    {},
		"sw_vers":                     {},
		"uname -m":                    {},
		"uptime":                      {},
		"df -h /":                     {},
		"df -g /":                     {},
		"ps -A -o comm= -r | head -5": {},
		"ps -A -o comm= -m | head -5": {},
	}
	_, ok := allowedExact[compact]
	return ok
}

type shellToken struct {
	Text   string
	Quoted bool
}

func commandSegments(command string) []string {
	var segments []string
	var b strings.Builder
	var quote rune
	escaped := false
	skipNext := false
	flush := func() {
		if segment := strings.TrimSpace(b.String()); segment != "" {
			segments = append(segments, segment)
		}
		b.Reset()
	}
	for i, r := range command {
		if skipNext {
			skipNext = false
			continue
		}
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			b.WriteRune(r)
			escaped = true
			continue
		}
		if quote != 0 {
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			b.WriteRune(r)
		case '\n', '\r', ';', '|':
			flush()
			if r == '|' && i+1 < len(command) && command[i+1] == '|' {
				skipNext = true
				continue
			}
		case '&':
			if i+1 < len(command) && command[i+1] == '&' {
				flush()
				skipNext = true
				continue
			}
			if i+1 < len(command) && command[i+1] == '>' || i > 0 && command[i-1] == '>' {
				b.WriteRune(r)
				continue
			}
			flush()
			continue
		case '(', ')':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return segments
}

func shellTokens(command string) []shellToken {
	var tokens []shellToken
	var b strings.Builder
	var quote rune
	escaped := false
	quoted := false
	flush := func() {
		if b.Len() == 0 {
			quoted = false
			return
		}
		tokens = append(tokens, shellToken{Text: b.String(), Quoted: quoted})
		b.Reset()
		quoted = false
	}
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			quoted = true
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func commandTokenIndex(tokens []shellToken) int {
	for i := 0; i < len(tokens); i++ {
		token := strings.TrimSpace(tokens[i].Text)
		if token == "" {
			continue
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "-") && strings.Index(token, "=") > 0 {
			continue
		}
		switch normalizeCommandToken(token) {
		case "sudo":
			i = skipSudoArgs(tokens, i)
			continue
		case "env":
			i = skipEnvArgs(tokens, i)
			continue
		case "time":
			i = skipOptionArgs(tokens, i)
			continue
		case "timeout":
			i = skipTimeoutArgs(tokens, i)
			continue
		case "command", "nohup":
			continue
		default:
			return i
		}
	}
	return -1
}

func skipSudoArgs(tokens []shellToken, idx int) int {
	for idx+1 < len(tokens) {
		next := strings.TrimSpace(tokens[idx+1].Text)
		if next == "" {
			idx++
			continue
		}
		if !strings.HasPrefix(next, "-") || next == "--" {
			break
		}
		idx++
		if shellOptionConsumesValue(next, "-C", "-D", "-g", "-h", "-p", "-T", "-t", "-U", "-u") && idx+1 < len(tokens) {
			idx++
		}
	}
	return idx
}

func skipEnvArgs(tokens []shellToken, idx int) int {
	for idx+1 < len(tokens) {
		next := strings.TrimSpace(tokens[idx+1].Text)
		if next == "" {
			idx++
			continue
		}
		if strings.Contains(next, "=") && !strings.HasPrefix(next, "-") && strings.Index(next, "=") > 0 {
			idx++
			continue
		}
		if !strings.HasPrefix(next, "-") || next == "--" {
			break
		}
		idx++
		if shellOptionConsumesValue(next, "-S", "-u") && idx+1 < len(tokens) {
			idx++
		}
	}
	return idx
}

func skipOptionArgs(tokens []shellToken, idx int) int {
	for idx+1 < len(tokens) {
		next := strings.TrimSpace(tokens[idx+1].Text)
		if next == "" {
			idx++
			continue
		}
		if !strings.HasPrefix(next, "-") || next == "--" {
			break
		}
		idx++
	}
	return idx
}

func skipTimeoutArgs(tokens []shellToken, idx int) int {
	idx = skipOptionArgs(tokens, idx)
	if idx+1 < len(tokens) {
		next := strings.TrimSpace(tokens[idx+1].Text)
		if next != "" && !strings.HasPrefix(next, "-") {
			idx++
		}
	}
	return idx
}

func shellOptionConsumesValue(token string, options ...string) bool {
	for _, option := range options {
		if token == option {
			return true
		}
	}
	return false
}

func shellCommandStringArg(tokens []shellToken) string {
	for i := 0; i < len(tokens); i++ {
		token := strings.TrimSpace(tokens[i].Text)
		if token == "" {
			continue
		}
		if shellOptionEnablesCommandString(token) && i+1 < len(tokens) {
			return strings.TrimSpace(tokens[i+1].Text)
		}
	}
	return ""
}

func shellOptionEnablesCommandString(token string) bool {
	if token == "-c" {
		return true
	}
	if strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--") {
		return strings.Contains(token[1:], "c")
	}
	return false
}

func executableResolutionDynamicReason(command string) string {
	segments := commandSegments(command)
	if len(segments) == 0 {
		segments = []string{command}
	}
	for _, segment := range segments {
		tokens := shellTokens(segment)
		limit := commandTokenIndex(tokens)
		if limit < 0 {
			limit = len(tokens)
		}
		for i, token := range tokens {
			if i >= limit {
				break
			}
			value := strings.TrimSpace(token.Text)
			if value == "" {
				continue
			}
			switch normalizeCommandToken(value) {
			case "env", "sudo", "command", "nohup", "nice", "stdbuf", "exec", "time", "timeout":
				continue
			}
			idx := strings.Index(value, "=")
			if idx <= 0 || strings.HasPrefix(value, "-") {
				continue
			}
			key := strings.ToUpper(strings.TrimSpace(value[:idx]))
			switch key {
			case "PATH", "BASH_ENV", "ENV", "SHELLOPTS", "GIT_EXEC_PATH", "GIT_SSH", "GIT_SSH_COMMAND", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "PYTHONPATH", "NODE_OPTIONS":
				return "environment-sensitive executable resolution"
			default:
				continue
			}
		}
	}
	return ""
}

func gitArgsOverrideTransport(args []shellToken) bool {
	values := tokenTexts(args)
	for i := 0; i < len(values); i++ {
		token := strings.ToLower(trimShellToken(values[i]))
		switch {
		case token == "-c" && i+1 < len(values):
			i++
			if gitConfigKeyExecutesPrograms(strings.ToLower(trimShellToken(values[i]))) {
				return true
			}
		case strings.HasPrefix(token, "-ccore.sshcommand="),
			strings.HasPrefix(token, "--config=core.sshcommand="),
			strings.HasPrefix(token, "-cdiff.external="),
			strings.HasPrefix(token, "--config=diff.external="),
			strings.HasPrefix(token, "-ccore.fsmonitor="),
			strings.HasPrefix(token, "--config=core.fsmonitor="),
			strings.HasPrefix(token, "-ccore.hookspath="),
			strings.HasPrefix(token, "--config=core.hookspath="):
			return true
		}
	}
	return false
}

func gitConfigKeyExecutesPrograms(config string) bool {
	config = strings.TrimSpace(strings.ToLower(config))
	for _, prefix := range []string{
		"core.sshcommand=",
		"diff.external=",
		"core.fsmonitor=",
		"core.hookspath=",
		"core.pager=",
		"alias.",
	} {
		if strings.HasPrefix(config, prefix) {
			return true
		}
	}
	return false
}

func dynamicShellExecutionReason(command string) string {
	var quote rune
	escaped := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if rune(ch) == quote {
				quote = 0
			}
			if quote == '\'' {
				continue
			}
		} else if ch == '\'' || ch == '"' {
			quote = rune(ch)
			continue
		}
		if quote == '\'' {
			continue
		}
		if ch == '`' {
			return "command substitution"
		}
		if ch == '$' && i+1 < len(command) && command[i+1] == '(' {
			return "command substitution"
		}
		if ch == '<' && i+1 < len(command) && command[i+1] == '(' {
			return "process substitution"
		}
	}
	return ""
}

func gitBranchArgsLookReadOnly(args []shellToken) bool {
	for _, arg := range args[1:] {
		token := strings.ToLower(trimShellToken(arg.Text))
		switch token {
		case "-d", "-D", "-m", "-M", "-c", "-C", "--delete", "--move", "--copy", "--set-upstream-to", "--unset-upstream":
			return false
		}
		if strings.HasPrefix(token, "--delete=") || strings.HasPrefix(token, "--move=") || strings.HasPrefix(token, "--copy=") || strings.HasPrefix(token, "--set-upstream-to=") {
			return false
		}
	}
	return true
}

func findArgsMutateOrExecute(args []shellToken) bool {
	for _, arg := range args {
		token := strings.ToLower(trimShellToken(arg.Text))
		switch {
		case token == "-delete" || token == "-exec" || token == "-execdir" || token == "-ok" || token == "-okdir":
			return true
		case token == "-fprint" || token == "-fprintf" || token == "-fls":
			return true
		}
	}
	return false
}

func sedArgsExecuteCommands(args []shellToken) bool {
	for _, arg := range args {
		text := strings.TrimSpace(arg.Text)
		if text == "" {
			continue
		}
		option := strings.ToLower(trimShellToken(text))
		if option == "-f" || strings.HasPrefix(option, "-f") || option == "--file" || strings.HasPrefix(option, "--file=") {
			return true
		}
		if strings.HasPrefix(text, "-") {
			continue
		}
		lower := strings.ToLower(text)
		for _, line := range strings.Split(lower, "\n") {
			line = strings.TrimSpace(line)
			if sedLineExecutesCommand(line) {
				return true
			}
		}
	}
	return false
}

func sedLineExecutesCommand(line string) bool {
	if line == "e" || strings.HasPrefix(line, "e ") {
		return true
	}
	if len(line) > 1 && line[0] >= '0' && line[0] <= '9' {
		rest := strings.TrimLeft(line, "0123456789")
		if rest == "e" || strings.HasPrefix(rest, "e ") {
			return true
		}
	}
	if strings.Contains(line, ";e ") || strings.Contains(line, "; e ") || strings.HasSuffix(line, ";e") || strings.HasSuffix(line, "; e") {
		return true
	}
	if strings.Contains(line, "/e") || strings.Contains(line, "/eg") || strings.Contains(line, "/ge") {
		return true
	}
	return false
}

func gitRemoteArgsLookReadOnly(args []shellToken) bool {
	sub := ""
	for _, arg := range args[1:] {
		token := strings.ToLower(trimShellToken(arg.Text))
		if token == "" || strings.HasPrefix(token, "-") {
			continue
		}
		sub = token
		break
	}
	switch sub {
	case "", "show", "get-url", "show-origin":
		return true
	default:
		return false
	}
}

func firstGitSubcommand(args []shellToken) string {
	values := tokenTexts(args)
	for i := 0; i < len(values); i++ {
		token := trimShellToken(values[i])
		if token == "" || token == "--" {
			continue
		}
		if gitGlobalOptionConsumesValue(token) {
			i++
			continue
		}
		if gitGlobalOptionHasInlineValue(token) || strings.HasPrefix(token, "-") {
			continue
		}
		return normalizeCommandToken(token)
	}
	return ""
}

func gitGlobalOptionConsumesValue(token string) bool {
	switch token {
	case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path":
		return true
	default:
		return false
	}
}

func gitGlobalOptionHasInlineValue(token string) bool {
	return strings.HasPrefix(token, "-C") && len(token) > len("-C") ||
		strings.HasPrefix(token, "-c") && len(token) > len("-c") ||
		strings.HasPrefix(token, "--git-dir=") ||
		strings.HasPrefix(token, "--work-tree=") ||
		strings.HasPrefix(token, "--namespace=") ||
		strings.HasPrefix(token, "--exec-path=")
}

func tokensContain(tokens []shellToken, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, token := range tokens {
		if strings.ToLower(strings.Trim(token.Text, `"'`)) == want {
			return true
		}
	}
	return false
}

func tokensContainPrefix(tokens []shellToken, prefix string) bool {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	for _, token := range tokens {
		if strings.HasPrefix(strings.ToLower(strings.Trim(token.Text, `"'`)), prefix) {
			return true
		}
	}
	return false
}

func tokenTexts(tokens []shellToken) []string {
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, token.Text)
	}
	return out
}

func normalizeCommandToken(token string) string {
	token = trimShellToken(token)
	if token == "" {
		return ""
	}
	base := filepath.Base(token)
	return strings.ToLower(strings.TrimSpace(base))
}

func trimShellToken(token string) string {
	return strings.Trim(strings.TrimSpace(token), `"'`)
}

func systemctlArgsLookReadOnly(args []shellToken) bool {
	for _, arg := range args {
		token := strings.ToLower(strings.TrimSpace(arg.Text))
		if token == "" || strings.HasPrefix(token, "-") {
			continue
		}
		switch token {
		case "status", "show", "list-units", "list-unit-files", "is-active", "is-enabled", "cat":
			return true
		default:
			return false
		}
	}
	return false
}

func systemctlAction(args []shellToken) string {
	for _, arg := range args {
		token := strings.ToLower(strings.TrimSpace(arg.Text))
		if token == "" || strings.HasPrefix(token, "-") {
			continue
		}
		return "systemctl_" + normalizeCommandToken(token)
	}
	return "systemctl"
}

func commandHasRedirection(command string) bool {
	var quote rune
	escaped := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if rune(ch) == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = rune(ch)
			continue
		}
		if ch == '>' {
			return true
		}
	}
	return false
}

func commandHasHighImpactStorageMarker(command string) bool {
	return strings.Contains(command, "rm -rf /") ||
		strings.Contains(command, " rm -rf /") ||
		strings.Contains(command, "mkfs") ||
		strings.Contains(command, " dd ") ||
		strings.HasPrefix(command, "dd ")
}

func sqlLooksMutating(command string) bool {
	return strings.Contains(command, "drop table") ||
		strings.Contains(command, "drop database") ||
		strings.Contains(command, "truncate table") ||
		strings.Contains(command, "delete from") ||
		strings.Contains(command, "insert into") ||
		strings.Contains(command, "update ") ||
		strings.Contains(command, "alter table") ||
		strings.Contains(command, "create table") ||
		strings.Contains(command, "migrate")
}

func commandContainsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func isExternalAccountCommand(command string) bool {
	switch command {
	case "gh", "aws", "gcloud", "az", "op":
		return true
	default:
		return false
	}
}
