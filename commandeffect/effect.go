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

func Classify(command string) Effect {
	compact := NormalizeCommand(command)
	if compact == "" {
		return Effect{Kind: KindUnknown, Reason: "empty command", SideEffects: true}
	}
	if AppServerStatusCommandAllowed(compact) {
		return Effect{Kind: KindReadOnlyInspection, Reason: "status inspection"}
	}
	lower := strings.ToLower(compact)
	if commandHasHighImpactStorageMarker(lower) {
		return Effect{Kind: KindHighImpactStorage, Reason: "high-impact storage command", SideEffects: true}
	}
	segments := commandSegments(command)
	if len(segments) == 0 {
		return Effect{Kind: KindUnknown, Reason: "unclassified command", SideEffects: true}
	}
	var out Effect
	if commandHasRedirection(lower) {
		out = Effect{Kind: KindBuildArtifact, Reason: "shell redirection", SideEffects: true}
	} else {
		out = Effect{Kind: KindReadOnlyInspection, Reason: "read-only inspection"}
	}
	for _, segment := range segments {
		effect := classifySegment(segment)
		if effectDominates(effect, out) {
			out = effect
		}
	}
	return out
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
	effect := Classify(command)
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
				SideEffects:   effect.SideEffects,
			}}, true
		}
	case KindRemoteHost:
		return Boundary{Kind: BoundaryRemoteHostOperation, Effect: Effect{
			Kind:        effect.Kind,
			Reason:      ReasonRemoteHostOperation,
			Command:     effect.Command,
			SideEffects: effect.SideEffects,
		}}, true
	case KindService:
		return Boundary{Kind: BoundaryServiceProcessChange, Effect: Effect{
			Kind:        effect.Kind,
			Reason:      ReasonServiceProcessChange,
			Command:     effect.Command,
			SideEffects: effect.SideEffects,
		}}, true
	}
	return Boundary{}, false
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
	cmd := normalizeCommandToken(tokens[idx].Text)
	args := tokens[idx+1:]
	if cmd == "sh" || cmd == "bash" {
		if script := shellCommandStringArg(args); script != "" {
			return Classify(script)
		}
	}
	lowerSegment := strings.ToLower(strings.Join(tokenTexts(tokens[idx:]), " "))
	switch cmd {
	case "set", "printf", "echo", "true", "false", "test":
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " shell builtin", Command: cmd}
	case "git":
		return classifyGitCommand(args)
	case "rg", "grep", "egrep", "fgrep", "cat", "nl", "head", "tail", "less", "more", "wc", "pwd", "ls", "find":
		if cmd == "find" && (tokensContain(args, "-delete") || tokensContain(args, "-exec") || tokensContain(args, "-ok")) {
			return Effect{Kind: KindWorkspaceMutation, Reason: "find mutation", Command: cmd, SideEffects: true}
		}
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " inspection", Command: cmd}
	case "sed":
		if tokensContain(args, "-i") || tokensContainPrefix(args, "-i") {
			return Effect{Kind: KindWorkspaceMutation, Reason: "sed in-place edit", Command: cmd, SideEffects: true}
		}
		return Effect{Kind: KindReadOnlyInspection, Reason: "sed inspection", Command: cmd}
	case "go":
		return classifyGoCommand(args)
	case "npm", "pnpm", "yarn", "make", "pytest", "python", "python3", "uv", "pip", "pip3", "cargo", "playwright", "npx":
		return classifyBuildTestPackageCommand(cmd, args)
	case "curl", "wget":
		return Effect{Kind: KindExternal, Reason: cmd + " external contact", Command: cmd, SideEffects: true}
	case "ssh", "scp", "rsync":
		return Effect{Kind: KindRemoteHost, Reason: ReasonRemoteHostOperation, Command: cmd, SideEffects: true}
	case "rm", "mv", "cp", "mkdir", "touch", "chmod", "chown", "ln", "tee", "apply_patch":
		return Effect{Kind: KindWorkspaceMutation, Reason: cmd + " filesystem mutation", Command: cmd, SideEffects: true}
	case "systemctl":
		if systemctlArgsLookReadOnly(args) {
			return Effect{Kind: KindReadOnlyInspection, Reason: "systemctl inspection", Command: cmd}
		}
		return Effect{Kind: KindService, Reason: ReasonServiceProcessChange, Command: cmd, SideEffects: true}
	case "service", "launchctl", "kill", "pkill", "docker", "docker-compose", "kubectl":
		return Effect{Kind: KindService, Reason: ReasonServiceProcessChange, Command: cmd, SideEffects: true}
	case "ps", "df", "du", "uname", "hostname", "uptime", "date", "whoami", "id", "env", "printenv":
		return Effect{Kind: KindReadOnlyInspection, Reason: cmd + " status inspection", Command: cmd}
	case "sqlite3", "psql", "mysql":
		if sqlLooksMutating(lowerSegment) {
			return Effect{Kind: KindDatabase, Reason: "database mutation", Command: cmd, SideEffects: true}
		}
		return Effect{Kind: KindUnknown, Reason: cmd + " database command", Command: cmd, SideEffects: true}
	case "gh", "aws", "gcloud", "az", "op":
		if commandContainsAny(lowerSegment, " login", " logout", " auth ", " configure", " token", " secret") {
			return Effect{Kind: KindCredential, Reason: cmd + " credential/config effect", Command: cmd, SideEffects: true}
		}
		return Effect{Kind: KindExternalAccount, Reason: ReasonExternalAccount, Command: cmd, SideEffects: true}
	default:
		if sqlLooksMutating(lowerSegment) {
			return Effect{Kind: KindDatabase, Reason: "database mutation", Command: cmd, SideEffects: true}
		}
		return Effect{Kind: KindUnknown, Reason: cmd + " unclassified", Command: cmd, SideEffects: true}
	}
}

func classifyGitCommand(args []shellToken) Effect {
	subcmd := firstGitSubcommand(args)
	switch subcmd {
	case "status", "diff", "log", "show", "grep", "rev-parse", "cat-file", "branch", "describe", "ls-files", "show-ref", "remote":
		return Effect{Kind: KindReadOnlyInspection, Reason: "git " + subcmd + " inspection", Command: "git", GitSubcommand: subcmd}
	case "fetch", "pull", "clone", "ls-remote", "submodule":
		return Effect{Kind: KindExternal, Reason: "git " + subcmd, Command: "git", GitSubcommand: subcmd, SideEffects: true}
	case "commit":
		return Effect{Kind: KindRepoHistory, Reason: ReasonGitCommit, Command: "git", GitSubcommand: subcmd, SideEffects: true}
	case "push":
		return Effect{Kind: KindRepoHistory, Reason: ReasonGitPush, Command: "git", GitSubcommand: subcmd, SideEffects: true}
	case "add", "checkout", "cherry-pick", "clean", "merge", "mv", "rebase", "reset", "restore", "revert", "rm", "switch", "stash", "tag", "worktree":
		return Effect{Kind: KindRepoHistory, Reason: "git " + subcmd, Command: "git", GitSubcommand: subcmd, SideEffects: true}
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
	case "version", "env", "list":
		return Effect{Kind: KindReadOnlyInspection, Reason: "go " + subcmd + " inspection", Command: "go"}
	default:
		return Effect{Kind: KindUnknown, Reason: "go " + subcmd + " unclassified", Command: "go", SideEffects: true}
	}
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
			b.WriteRune(r)
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
		if token == "-c" && i+1 < len(tokens) {
			return strings.TrimSpace(tokens[i+1].Text)
		}
	}
	return ""
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

func commandHasRedirection(command string) bool {
	return strings.Contains(command, " >") ||
		strings.Contains(command, "> ") ||
		strings.Contains(command, ">>") ||
		strings.Contains(command, " 2>") ||
		strings.Contains(command, "1>")
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
