//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/session"
)

type shellRejectionAlternative struct {
	State              session.NextActionState
	OperationKind      string
	OperationTool      string
	OperationInputJSON string
	NextAction         string
	RequiredAuthority  string
	ResourceBlocker    string
	Verifier           string
	RetryPolicy        string
	OperatorProjection string
	Reason             string
}

func (r *Registry) recordRejectedShellAlternative(ctx context.Context, key session.SessionKey, command string, rawWorkdir string, workingRoot string, plan commandeffect.EffectPlan, judgment session.Judgment, cause error) error {
	if cause == nil {
		return nil
	}
	alt := typedAlternativeForRejectedShell(command, rawWorkdir, workingRoot, plan, cause)
	if alt.State == session.NextActionReadyToExecute {
		if err := validateRecoveryHandoffToolInput(alt.State, alt.OperationTool, alt.OperationInputJSON); err != nil {
			alt = invalidReadyShellAlternative(command, plan, cause, err)
		}
	}
	outErr := rejectedShellAlternativeError(cause, alt)
	if r == nil || r.store == nil || !toolSessionKeyHasIdentity(key) {
		return outErr
	}
	now := time.Now().UTC()
	turnRunID := int64(0)
	if ref, ok := ToolInvocationRefFromContext(ctx); ok {
		turnRunID = ref.TurnRunID
	}
	commandHash := session.EffectAttemptCommandHash(command)
	causalRefs := []string{
		session.JudgmentUseRef("command_hash", commandHash),
		session.JudgmentUseRef("shell_rejection", shellRejectionReason(plan, cause)),
	}
	if strings.TrimSpace(judgment.ID) != "" {
		causalRefs = append(causalRefs, session.JudgmentRef(judgment.ID))
	}
	if _, err := r.store.RecordNextAction(session.NextActionInput{
		Key:                key,
		TurnRunID:          turnRunID,
		Owner:              "tool.exec",
		State:              alt.State,
		SubjectKind:        "shell_rejection",
		SubjectRef:         commandHash,
		CausalRefs:         causalRefs,
		NextAction:         alt.NextAction,
		RequiredAuthority:  alt.RequiredAuthority,
		ResourceBlocker:    alt.ResourceBlocker,
		Verifier:           alt.Verifier,
		RetryPolicy:        alt.RetryPolicy,
		OperationKind:      alt.OperationKind,
		OperationTool:      alt.OperationTool,
		OperationInputJSON: alt.OperationInputJSON,
		OperatorProjection: alt.OperatorProjection,
		CreatedAt:          now,
	}); err != nil {
		return fmt.Errorf("%w (and failed to record typed shell alternative: %v)", outErr, err)
	}
	return outErr
}

func typedAlternativeForRejectedShell(command string, rawWorkdir string, workingRoot string, plan commandeffect.EffectPlan, cause error) shellRejectionAlternative {
	if plan.Dynamic {
		return genericRejectedShellAlternative(command, plan, cause)
	}
	if plan.MultipleAuthorities {
		return splitEffectPlanAlternative(command, plan, cause)
	}
	if alt, ok := systemLogReadAlternative(command, cause); ok {
		return alt
	}
	if alt, ok := nativeFileAlternative(command, rawWorkdir, workingRoot, cause); ok {
		return alt
	}
	if alt, ok := canonicalExecAlternative(command, rawWorkdir, cause); ok {
		return alt
	}
	if alt, ok := repairOperationAlternative(command, plan, cause); ok {
		return alt
	}
	return genericRejectedShellAlternative(command, plan, cause)
}

func rejectedShellAlternativeError(cause error, alt shellRejectionAlternative) error {
	if alt.OperatorProjection == "" {
		return cause
	}
	return fmt.Errorf("%w; typed alternative: %s", cause, alt.OperatorProjection)
}

func systemLogReadAlternative(command string, cause error) (shellRejectionAlternative, bool) {
	segments := shellishCommandSegments(command)
	if len(segments) == 0 {
		return shellRejectionAlternative{}, false
	}
	firstCmd, firstArgs, ok := firstShellCommand(segments[0])
	if !ok || firstCmd != "journalctl" {
		return shellRejectionAlternative{}, false
	}
	input := map[string]any{"limit": defaultSystemLogLimit}
	system := false
	unit := ""
	for i := 0; i < len(firstArgs); i++ {
		arg := strings.TrimSpace(strings.Trim(firstArgs[i].Text, `"'`))
		switch arg {
		case "--system":
			system = true
		case "--user":
			system = false
		case "-u", "--unit":
			if i+1 < len(firstArgs) {
				i++
				unit = strings.TrimSpace(strings.Trim(firstArgs[i].Text, `"'`))
			}
		case "--since":
			if i+1 < len(firstArgs) {
				i++
				input["since"] = strings.TrimSpace(strings.Trim(firstArgs[i].Text, `"'`))
			}
		case "--until":
			if i+1 < len(firstArgs) {
				i++
				input["until"] = strings.TrimSpace(strings.Trim(firstArgs[i].Text, `"'`))
			}
		case "--priority", "-p":
			if i+1 < len(firstArgs) {
				i++
				input["priority"] = strings.TrimSpace(strings.Trim(firstArgs[i].Text, `"'`))
			}
		default:
			if strings.HasPrefix(arg, "--unit=") {
				unit = strings.TrimSpace(strings.TrimPrefix(arg, "--unit="))
			} else if strings.HasPrefix(arg, "--since=") {
				input["since"] = strings.TrimSpace(strings.TrimPrefix(arg, "--since="))
			} else if strings.HasPrefix(arg, "--until=") {
				input["until"] = strings.TrimSpace(strings.TrimPrefix(arg, "--until="))
			} else if strings.HasPrefix(arg, "--priority=") {
				input["priority"] = strings.TrimSpace(strings.TrimPrefix(arg, "--priority="))
			}
		}
	}
	if unit == "" {
		return shellRejectionAlternative{}, false
	}
	input["unit"] = unit
	if system {
		input["system"] = true
	}
	state := session.NextActionReadyToExecute
	retry := "use_structured_system_log_read"
	next := "read the service logs through the bounded system_log_read tool"
	projection := fmt.Sprintf("Raw shell was rejected. Recommended next action: inspect %s with system_log_read.", shellAlternativeDisplay(unit))
	if include, exact := journalctlPipelineIncludeTerms(segments[1:]); len(include) > 0 {
		input["include"] = include
		if !exact {
			state = session.NextActionWaitingForOperator
			retry = "operator_confirm_lossy_log_filter"
			next = "rewrite the shell log filter as an exact system_log_read request"
			projection = "Raw shell was rejected. system_log_read can replace journalctl, but the shell filter was not exactly representable; confirm the literal include terms before execution."
			input["lossy_reason"] = "grep/pipe regex semantics are not fully represented by system_log_read literal filters"
		}
	}
	if limit := journalctlPipelineTailLimit(segments[1:]); limit > 0 {
		input["limit"] = limit
	}
	return shellRejectionAlternative{
		State:              state,
		OperationKind:      "system_log_read",
		OperationTool:      "system_log_read",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(input)),
		NextAction:         next,
		RequiredAuthority:  "system_log_read",
		RetryPolicy:        retry,
		OperatorProjection: projection,
		Reason:             shellRejectionReasonFromError(cause),
	}, true
}

func journalctlPipelineIncludeTerms(segments []string) ([]string, bool) {
	for _, segment := range segments {
		cmd, args, ok := firstShellCommand(segment)
		if !ok {
			continue
		}
		switch cmd {
		case "grep", "egrep", "fgrep", "rg":
			values := nonOptionArgs(args)
			if len(values) == 0 {
				return nil, true
			}
			pattern := values[0]
			terms := splitSimpleLogPattern(pattern)
			if len(terms) == 0 {
				return nil, false
			}
			return terms, simpleLogPattern(pattern)
		}
	}
	return nil, true
}

func journalctlPipelineTailLimit(segments []string) int {
	for _, segment := range segments {
		cmd, args, ok := firstShellCommand(segment)
		if !ok || cmd != "tail" {
			continue
		}
		for i := 0; i < len(args); i++ {
			arg := strings.TrimSpace(strings.Trim(args[i].Text, `"'`))
			if strings.HasPrefix(arg, "-") && len(arg) > 1 {
				if n, err := strconv.Atoi(strings.TrimPrefix(arg, "-")); err == nil && n > 0 {
					return n
				}
			}
			if (arg == "-n" || arg == "--lines") && i+1 < len(args) {
				next := strings.TrimSpace(strings.Trim(args[i+1].Text, `"'`))
				if n, err := strconv.Atoi(strings.TrimPrefix(next, "+")); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	return 0
}

func splitSimpleLogPattern(pattern string) []string {
	var out []string
	for _, part := range strings.Split(pattern, "|") {
		part = strings.TrimSpace(strings.Trim(part, `"'`))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func simpleLogPattern(pattern string) bool {
	for _, part := range splitSimpleLogPattern(pattern) {
		if strings.ContainsAny(part, "[]()+*?{}\\") {
			return false
		}
	}
	return true
}

func nativeFileAlternative(command string, rawWorkdir string, workingRoot string, cause error) (shellRejectionAlternative, bool) {
	cmd, args, ok := firstShellCommand(command)
	if !ok {
		return shellRejectionAlternative{}, false
	}
	switch cmd {
	case "cat":
		paths := nonOptionArgs(args)
		if len(paths) == 0 {
			return shellRejectionAlternative{}, false
		}
		if pathsContainStdin(paths) {
			return nativeStdinRewriteRequired(cmd, cause), true
		}
		if hasShellOptions(args) || len(paths) != 1 {
			return lossyNativeReadSuggestion(cmd, paths, rawWorkdir, workingRoot, cause), true
		}
		path, ok := nativeAlternativePath(paths[0], rawWorkdir, workingRoot)
		if !ok {
			return nativeWorkdirRewriteRequired(cmd, cause), true
		}
		input := map[string]any{"path": path, "full": true}
		return shellRejectionAlternative{
			State:              session.NextActionReadyToExecute,
			OperationKind:      "native_file_read",
			OperationTool:      "read_file",
			OperationInputJSON: mustJSON(recommendedShellAlternativeInput(input)),
			NextAction:         "read the file through the scoped native file tool",
			RequiredAuthority:  "file_read",
			RetryPolicy:        "use_structured_tool_not_raw_shell",
			OperatorProjection: fmt.Sprintf("Raw shell was rejected. Recommended next action: inspect with read_file for %s.", shellAlternativeDisplay(path)),
			Reason:             shellRejectionReasonFromError(cause),
		}, true
	case "nl", "head", "tail", "sed":
		paths := nonOptionArgs(args)
		if len(paths) == 0 {
			return shellRejectionAlternative{}, false
		}
		if pathsContainStdin(paths) {
			return nativeStdinRewriteRequired(cmd, cause), true
		}
		return lossyNativeReadSuggestion(cmd, paths, rawWorkdir, workingRoot, cause), true
	case "ls":
		paths := nonOptionArgs(args)
		if hasShellOptions(args) || len(paths) > 1 {
			return lossyNativeListSuggestion(cmd, paths, rawWorkdir, workingRoot, cause), true
		}
		path := "."
		if len(paths) == 1 {
			path = paths[0]
		}
		path, ok := nativeAlternativePath(path, rawWorkdir, workingRoot)
		if !ok {
			return nativeWorkdirRewriteRequired(cmd, cause), true
		}
		return shellRejectionAlternative{
			State:              session.NextActionReadyToExecute,
			OperationKind:      "native_directory_list",
			OperationTool:      "list_dir",
			OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"path": path, "limit": 100})),
			NextAction:         "list the directory through the scoped native file tool",
			RequiredAuthority:  "file_read",
			RetryPolicy:        "use_structured_tool_not_raw_shell",
			OperatorProjection: fmt.Sprintf("Raw shell was rejected. Recommended next action: inspect with list_dir for %s.", shellAlternativeDisplay(path)),
			Reason:             shellRejectionReasonFromError(cause),
		}, true
	case "find":
		path := findPathArg(args)
		if path == "" {
			path = "."
		}
		path, ok := nativeAlternativePath(path, rawWorkdir, workingRoot)
		if !ok {
			return nativeWorkdirRewriteRequired(cmd, cause), true
		}
		return shellRejectionAlternative{
			State:              session.NextActionWaitingForOperator,
			OperationKind:      "native_directory_list",
			OperationTool:      "list_dir",
			OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"path": path, "limit": 100, "lossy_reason": "find predicates are not represented by list_dir"})),
			NextAction:         "replace the find command with a typed search/list plan before execution",
			RequiredAuthority:  "file_read",
			RetryPolicy:        "operator_confirm_lossy_native_suggestion",
			OperatorProjection: fmt.Sprintf("Raw shell was rejected. list_dir for %s is only a lossy inspection suggestion; rewrite find predicates as typed operations before execution.", shellAlternativeDisplay(path)),
			Reason:             shellRejectionReasonFromError(cause),
		}, true
	case "rg", "grep", "egrep", "fgrep":
		query, path := searchArgs(args)
		if query == "" {
			return shellRejectionAlternative{}, false
		}
		if path == "" {
			path = "."
		}
		path, ok := nativeAlternativePath(path, rawWorkdir, workingRoot)
		if !ok {
			return nativeWorkdirRewriteRequired(cmd, cause), true
		}
		return shellRejectionAlternative{
			State:              session.NextActionWaitingForOperator,
			OperationKind:      "native_text_search",
			OperationTool:      "search",
			OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"query": query, "path": path, "limit": 50, "lossy_reason": "shell search options and regex semantics are not fully represented"})),
			NextAction:         "rewrite the shell search as a typed search operation before execution",
			RequiredAuthority:  "file_read",
			RetryPolicy:        "operator_confirm_lossy_native_suggestion",
			OperatorProjection: "Raw shell was rejected. Native search is only a lossy inspection suggestion; rewrite shell search options as a typed operation before execution.",
			Reason:             shellRejectionReasonFromError(cause),
		}, true
	default:
		return shellRejectionAlternative{}, false
	}
}

func lossyNativeReadSuggestion(cmd string, paths []string, rawWorkdir string, workingRoot string, cause error) shellRejectionAlternative {
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		next, ok := nativeAlternativePath(path, rawWorkdir, workingRoot)
		if !ok {
			return nativeWorkdirRewriteRequired(cmd, cause)
		}
		resolved = append(resolved, next)
	}
	return shellRejectionAlternative{
		State:              session.NextActionWaitingForOperator,
		OperationKind:      "native_file_read",
		OperationTool:      "read_file",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"paths": resolved, "lossy_reason": cmd + " semantics are not exactly represented by one read_file call"})),
		NextAction:         "rewrite the shell file read as exact typed file operation(s) before execution",
		RequiredAuthority:  "file_read",
		RetryPolicy:        "operator_confirm_lossy_native_suggestion",
		OperatorProjection: fmt.Sprintf("Raw shell was rejected. read_file is only a lossy inspection suggestion for %s; rewrite the command as exact typed file operation(s) before execution.", cmd),
		Reason:             shellRejectionReasonFromError(cause),
	}
}

func lossyNativeListSuggestion(cmd string, paths []string, rawWorkdir string, workingRoot string, cause error) shellRejectionAlternative {
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		next, ok := nativeAlternativePath(path, rawWorkdir, workingRoot)
		if !ok {
			return nativeWorkdirRewriteRequired(cmd, cause)
		}
		resolved = append(resolved, next)
	}
	if len(resolved) == 0 {
		next, ok := nativeAlternativePath(".", rawWorkdir, workingRoot)
		if !ok {
			return nativeWorkdirRewriteRequired(cmd, cause)
		}
		resolved = append(resolved, next)
	}
	return shellRejectionAlternative{
		State:              session.NextActionWaitingForOperator,
		OperationKind:      "native_directory_list",
		OperationTool:      "list_dir",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"paths": resolved, "lossy_reason": cmd + " options are not exactly represented by one list_dir call"})),
		NextAction:         "rewrite the shell directory listing as exact typed operation(s) before execution",
		RequiredAuthority:  "file_read",
		RetryPolicy:        "operator_confirm_lossy_native_suggestion",
		OperatorProjection: fmt.Sprintf("Raw shell was rejected. list_dir is only a lossy inspection suggestion for %s; rewrite the command as exact typed operation(s) before execution.", cmd),
		Reason:             shellRejectionReasonFromError(cause),
	}
}

func nativeWorkdirRewriteRequired(cmd string, cause error) shellRejectionAlternative {
	return shellRejectionAlternative{
		State:              session.NextActionWaitingForOperator,
		OperationKind:      "typed_operation_required",
		OperationTool:      "update_operation",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"reason": "workdir_not_representable_for_native_alternative", "command": cmd})),
		NextAction:         "rewrite the rejected shell with an explicit typed operation and scoped path",
		RequiredAuthority:  "typed_operation_required",
		RetryPolicy:        "do_not_execute_native_guess",
		OperatorProjection: "Raw shell was rejected, and its workdir/path semantics could not be represented safely. Rewrite it as an explicit typed operation before execution.",
		Reason:             shellRejectionReasonFromError(cause),
	}
}

func nativeStdinRewriteRequired(cmd string, cause error) shellRejectionAlternative {
	return shellRejectionAlternative{
		State:              session.NextActionWaitingForOperator,
		OperationKind:      "typed_operation_required",
		OperationTool:      "update_operation",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"reason": "stdin_semantics_not_native_file_path", "command": cmd})),
		NextAction:         "rewrite stdin-dependent shell semantics as an explicit typed operation",
		RequiredAuthority:  "typed_operation_required",
		RetryPolicy:        "do_not_execute_native_guess",
		OperatorProjection: "Raw shell used stdin semantics that read_file cannot represent. Rewrite it as an explicit typed operation before execution.",
		Reason:             shellRejectionReasonFromError(cause),
	}
}

func pathsContainStdin(paths []string) bool {
	for _, path := range paths {
		if strings.TrimSpace(path) == "-" {
			return true
		}
	}
	return false
}

func nativeAlternativePath(path string, rawWorkdir string, workingRoot string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), true
	}
	if strings.TrimSpace(rawWorkdir) == "" {
		return path, true
	}
	if strings.TrimSpace(workingRoot) == "" {
		return "", false
	}
	workdir, escaped, err := resolveWorkdirForExec(workingRoot, rawWorkdir)
	if err != nil || escaped {
		return "", false
	}
	return filepath.Clean(filepath.Join(workdir, path)), true
}

func canonicalExecAlternative(command string, rawWorkdir string, cause error) (shellRejectionAlternative, bool) {
	cmd, args, ok := firstShellCommand(command)
	if !ok {
		return shellRejectionAlternative{}, false
	}
	canonical := ""
	operationKind := ""
	requiredAuthority := ""
	verifier := ""
	switch cmd {
	case "git":
		if !readOnlyInspectionCommand(cmd, args) {
			return shellRejectionAlternative{}, false
		}
		canonical = canonicalShellCommand(cmd, args)
		if err := validateExecEffectPlanDispatchable(commandeffect.PlanCommand(canonical)); err != nil {
			return shellRejectionAlternative{}, false
		}
		operationKind = "confined_git_inspection"
		requiredAuthority = "read_only_inspection"
	case "go", "make", "pytest", "npm", "pnpm", "yarn", "cargo":
		candidate := canonicalShellCommand(cmd, args)
		plan := commandeffect.PlanCommand(candidate)
		if err := validateExecEffectPlanDispatchable(plan); err != nil {
			return shellRejectionAlternative{}, false
		}
		effect := commandeffect.RepresentativeEffect(plan)
		if effect.Kind != commandeffect.KindValidation {
			return shellRejectionAlternative{}, false
		}
		canonical = candidate
		operationKind = "confined_verification_exec"
		requiredAuthority = "validation_execution"
		verifier = "confined_validation_command"
	default:
		return shellRejectionAlternative{}, false
	}
	if canonical == "" {
		return shellRejectionAlternative{}, false
	}
	input := map[string]any{"command": canonical}
	if strings.TrimSpace(rawWorkdir) != "" {
		input["workdir"] = strings.TrimSpace(rawWorkdir)
	}
	return shellRejectionAlternative{
		State:              session.NextActionReadyToExecute,
		OperationKind:      operationKind,
		OperationTool:      "exec",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(input)),
		NextAction:         "run the canonical confined command instead of the rejected raw shell shape",
		RequiredAuthority:  requiredAuthority,
		Verifier:           verifier,
		RetryPolicy:        "use_canonical_confined_exec",
		OperatorProjection: fmt.Sprintf("Raw shell was rejected. Recommended bounded alternative: %s.", shellAlternativeDisplay(canonical)),
		Reason:             shellRejectionReasonFromError(cause),
	}, true
}

func splitEffectPlanAlternative(command string, plan commandeffect.EffectPlan, cause error) shellRejectionAlternative {
	segments := shellishCommandSegments(command)
	steps := make([]map[string]any, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		segmentPlan := commandeffect.PlanCommand(segment)
		effect := commandeffect.RepresentativeEffect(segmentPlan)
		normalizedSegment := commandeffect.NormalizeCommand(segment)
		steps = append(steps, map[string]any{
			"ordinal":            len(steps) + 1,
			"command_hash":       session.EffectAttemptCommandHash(normalizedSegment),
			"command_preview":    session.RedactEvidenceText(normalizedSegment).Text,
			"effect_kind":        string(effect.Kind),
			"effect_reason":      strings.TrimSpace(effect.Reason),
			"required_authority": requiredAuthorityForEffect(effect),
		})
	}
	if len(steps) == 0 {
		for _, effect := range plan.Effects {
			steps = append(steps, map[string]any{
				"ordinal":            len(steps) + 1,
				"effect_kind":        string(effect.Kind),
				"effect_reason":      strings.TrimSpace(effect.Reason),
				"required_authority": requiredAuthorityForEffect(effect),
			})
		}
	}
	return shellRejectionAlternative{
		State:              session.NextActionBlockedNeedsAuthority,
		OperationKind:      "split_effect_plan",
		OperationTool:      "update_operation",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"reason": shellRejectionReason(plan, cause), "steps": steps})),
		NextAction:         "split the compound shell into separate typed effect steps before execution",
		RequiredAuthority:  "split_effect_plan",
		RetryPolicy:        "do_not_retry_raw_compound_shell",
		OperatorProjection: "Raw shell mixed multiple authority-bearing effects. Split it into separate typed operations and approve each bounded step.",
		Reason:             shellRejectionReasonFromError(cause),
	}
}

func repairOperationAlternative(command string, plan commandeffect.EffectPlan, cause error) (shellRejectionAlternative, bool) {
	shape := rejectedRepairShape(command, plan, cause)
	op, ok := TypedRepairOperationForRejectedShape(shape)
	if !ok {
		return shellRejectionAlternative{}, false
	}
	return shellRejectionAlternative{
		State:              session.NextActionWaitingForOperator,
		OperationKind:      "typed_repair_operation",
		OperationTool:      "update_operation",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"repair_operation_id": op.ID, "required_action": op.RequiredAction, "required_resource": op.RequiredResource, "rejected_shape": op.RejectedShape})),
		NextAction:         op.Summary,
		RequiredAuthority:  op.RequiredAction,
		ResourceBlocker:    op.RequiredResource,
		RetryPolicy:        "replace_raw_shell_with_typed_repair_operation",
		OperatorProjection: op.Summary,
		Reason:             shellRejectionReasonFromError(cause),
	}, true
}

func genericRejectedShellAlternative(command string, plan commandeffect.EffectPlan, cause error) shellRejectionAlternative {
	steps := []map[string]any{{
		"ordinal":            1,
		"command_hash":       session.EffectAttemptCommandHash(command),
		"effect_kind":        string(commandeffect.RepresentativeEffect(plan).Kind),
		"effect_reason":      strings.TrimSpace(commandeffect.RepresentativeEffect(plan).Reason),
		"required_authority": "typed_operation_required",
	}}
	return shellRejectionAlternative{
		State:              session.NextActionWaitingForOperator,
		OperationKind:      "typed_operation_required",
		OperationTool:      "update_operation",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"reason": shellRejectionReason(plan, cause), "steps": steps})),
		NextAction:         "replace the rejected shell with a typed operation or split plan",
		RequiredAuthority:  "typed_operation_required",
		RetryPolicy:        "do_not_retry_unbounded_raw_shell",
		OperatorProjection: "Raw shell could not be bounded. Replace it with a typed operation or split plan before retrying.",
		Reason:             shellRejectionReasonFromError(cause),
	}
}

func invalidReadyShellAlternative(command string, plan commandeffect.EffectPlan, cause error, validationErr error) shellRejectionAlternative {
	steps := []map[string]any{{
		"ordinal":              1,
		"command_hash":         session.EffectAttemptCommandHash(command),
		"effect_kind":          string(commandeffect.RepresentativeEffect(plan).Kind),
		"effect_reason":        strings.TrimSpace(commandeffect.RepresentativeEffect(plan).Reason),
		"required_authority":   "typed_operation_required",
		"validation_failure":   strings.TrimSpace(validationErr.Error()),
		"original_reject_kind": shellRejectionReason(plan, cause),
	}}
	return shellRejectionAlternative{
		State:              session.NextActionWaitingForOperator,
		OperationKind:      "typed_operation_required",
		OperationTool:      "update_operation",
		OperationInputJSON: mustJSON(recommendedShellAlternativeInput(map[string]any{"reason": "ready_alternative_failed_contract_validation", "steps": steps})),
		NextAction:         "rewrite the rejected shell as a typed operation before execution",
		RequiredAuthority:  "typed_operation_required",
		RetryPolicy:        "do_not_execute_invalid_recovery_handoff",
		OperatorProjection: "Raw shell was rejected, and the candidate ready action failed recovery-handoff validation. Rewrite it as an exact typed operation before execution.",
		Reason:             shellRejectionReasonFromError(cause),
	}
}

func rejectedRepairShape(command string, plan commandeffect.EffectPlan, cause error) string {
	reason := strings.ToLower(strings.Join([]string{shellRejectionReason(plan, cause), command}, " "))
	switch {
	case strings.Contains(reason, "path-qualified executable"):
		return "path-qualified executable"
	case strings.Contains(reason, "python") || strings.Contains(reason, "ruby") || strings.Contains(reason, "perl") || strings.Contains(reason, "interpreter"):
		return "interpreter repair"
	case plan.MultipleAuthorities:
		return "multi-effect repair"
	default:
		return ""
	}
}

func shellRejectionReason(plan commandeffect.EffectPlan, cause error) string {
	if plan.Dynamic {
		return "dynamic_shell"
	}
	if plan.MultipleAuthorities {
		return "multiple_authorities"
	}
	for _, effect := range plan.Effects {
		if effect.Kind == commandeffect.KindUnknown {
			return normalizeShellAlternativeToken(effect.Reason)
		}
	}
	return shellRejectionReasonFromError(cause)
}

func shellRejectionReasonFromError(cause error) string {
	if cause == nil {
		return "shell_rejected"
	}
	reason := normalizeShellAlternativeToken(cause.Error())
	if reason == "" {
		return "shell_rejected"
	}
	return reason
}

func requiredAuthorityForEffect(effect commandeffect.Effect) string {
	if strings.TrimSpace(effect.Action) != "" {
		return normalizeShellAlternativeToken(effect.Action)
	}
	switch effect.Kind {
	case commandeffect.KindReadOnlyInspection:
		return "read_only_inspection"
	case commandeffect.KindValidation:
		return "validation_execution"
	case commandeffect.KindBuildArtifact:
		return "build_artifact"
	case commandeffect.KindWorkspaceMutation:
		return "workspace_write"
	case commandeffect.KindRepoHistory:
		switch effect.Reason {
		case commandeffect.ReasonGitCommit:
			return "git_commit"
		case commandeffect.ReasonGitPush:
			return "git_push"
		default:
			return "repo_history_mutation"
		}
	case commandeffect.KindExternal:
		return "external_network_contact"
	case commandeffect.KindExternalAccount:
		return "external_account_action"
	case commandeffect.KindRemoteHost:
		return "remote_host_operation"
	case commandeffect.KindService:
		return "service_process_change"
	case commandeffect.KindCapability:
		return "capability_acquisition"
	case commandeffect.KindCredential:
		return "credential_or_config_effect"
	case commandeffect.KindDatabase:
		return "database_mutation"
	case commandeffect.KindHighImpactStorage:
		return "high_impact_storage"
	case commandeffect.KindAdminUnboundedExec:
		return "admin_unbounded_exact_exec"
	default:
		return "typed_operation_required"
	}
}

func firstShellCommand(command string) (string, []shellToken, bool) {
	for _, segment := range shellishCommandSegments(command) {
		tokens := shellishTokens(segment)
		idx := shellishCommandTokenIndex(tokens)
		if idx < 0 || idx >= len(tokens) {
			continue
		}
		cmd := normalizeShellishCommandToken(tokens[idx].Text)
		if cmd == "bash" || cmd == "sh" {
			if script := shellCommandStringArg(tokens[idx+1:]); script != "" {
				return firstShellCommand(script)
			}
		}
		return cmd, tokens[idx+1:], true
	}
	return "", nil, false
}

func canonicalShellCommand(cmd string, args []shellToken) string {
	parts := []string{cmd}
	for _, token := range args {
		text := strings.TrimSpace(token.Text)
		if text == "" {
			continue
		}
		parts = append(parts, shellAlternativeQuote(text))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func shellAlternativeQuote(text string) string {
	if text == "" {
		return ""
	}
	if strings.ContainsAny(text, " \t\n\r;&|()<>$`\"'") {
		return strconv.Quote(text)
	}
	return text
}

func lastPathLikeArg(args []shellToken) string {
	values := nonOptionArgs(args)
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func searchArgs(args []shellToken) (string, string) {
	values := nonOptionArgs(args)
	if len(values) == 0 {
		return "", ""
	}
	query := values[0]
	path := ""
	if len(values) > 1 {
		path = values[len(values)-1]
	}
	return query, path
}

func findPathArg(args []shellToken) string {
	for _, arg := range args {
		text := strings.TrimSpace(strings.Trim(arg.Text, `"'`))
		if text == "" || text == "--" {
			continue
		}
		if strings.HasPrefix(text, "-") || text == "(" || text == ")" || text == "!" || text == "," {
			return ""
		}
		return text
	}
	return ""
}

func hasShellOptions(args []shellToken) bool {
	endOptions := false
	for _, arg := range args {
		text := strings.TrimSpace(arg.Text)
		if text == "" {
			continue
		}
		if !endOptions && text == "--" {
			endOptions = true
			continue
		}
		if !endOptions && strings.HasPrefix(text, "-") && text != "-" {
			return true
		}
	}
	return false
}

func nonOptionArgs(args []shellToken) []string {
	var values []string
	endOptions := false
	for i := 0; i < len(args); i++ {
		text := strings.TrimSpace(args[i].Text)
		if text == "" {
			continue
		}
		if !endOptions && text == "--" {
			endOptions = true
			continue
		}
		if !endOptions && strings.HasPrefix(text, "-") && text != "-" {
			if shellAlternativeOptionConsumesNext(text) && i+1 < len(args) {
				i++
			}
			continue
		}
		values = append(values, strings.TrimSpace(strings.Trim(text, `"'`)))
	}
	return values
}

func shellAlternativeOptionConsumesNext(option string) bool {
	option = strings.TrimSpace(option)
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-C", "-A", "-B", "-m", "-g", "-I", "-O", "-o", "-c", "--max-count", "--context", "--after-context", "--before-context", "--glob", "--include", "--exclude", "--format", "--printf":
		return true
	default:
		return false
	}
}

func normalizeShellAlternativeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func recommendedShellAlternativeInput(input map[string]any) map[string]any {
	return shellAlternativePayload(input)
}

func shellAlternativeDisplay(value string) string {
	return session.RedactEvidenceText(strings.TrimSpace(value)).Text
}

func cleanAlternativePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(path)
}
