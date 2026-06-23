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

func (r *Registry) recordRejectedShellAlternative(ctx context.Context, key session.SessionKey, command string, rawWorkdir string, plan commandeffect.EffectPlan, judgment session.Judgment, cause error) error {
	if cause == nil {
		return nil
	}
	alt := typedAlternativeForRejectedShell(command, rawWorkdir, plan, cause)
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

func typedAlternativeForRejectedShell(command string, rawWorkdir string, plan commandeffect.EffectPlan, cause error) shellRejectionAlternative {
	if plan.MultipleAuthorities {
		return splitEffectPlanAlternative(command, plan, cause)
	}
	if alt, ok := nativeFileAlternative(command, cause); ok {
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

func nativeFileAlternative(command string, cause error) (shellRejectionAlternative, bool) {
	cmd, args, ok := firstShellCommand(command)
	if !ok {
		return shellRejectionAlternative{}, false
	}
	switch cmd {
	case "cat", "nl", "head", "tail", "sed":
		path := lastPathLikeArg(args)
		if path == "" {
			return shellRejectionAlternative{}, false
		}
		input := map[string]any{"path": path}
		if cmd == "head" || cmd == "sed" {
			input["offset"] = 0
			input["limit"] = 120
		} else {
			input["full"] = true
		}
		return shellRejectionAlternative{
			State:              session.NextActionReadyToExecute,
			OperationKind:      "native_file_read",
			OperationTool:      "read_file",
			OperationInputJSON: mustJSON(input),
			NextAction:         "read the file through the scoped native file tool",
			RequiredAuthority:  "file_read",
			RetryPolicy:        "use_structured_tool_not_raw_shell",
			OperatorProjection: fmt.Sprintf("Raw shell was rejected, but this can be inspected with read_file for %s.", path),
			Reason:             shellRejectionReasonFromError(cause),
		}, true
	case "ls", "find":
		path := lastPathLikeArg(args)
		if path == "" {
			path = "."
		}
		return shellRejectionAlternative{
			State:              session.NextActionReadyToExecute,
			OperationKind:      "native_directory_list",
			OperationTool:      "list_dir",
			OperationInputJSON: mustJSON(map[string]any{"path": path, "limit": 100}),
			NextAction:         "list the directory through the scoped native file tool",
			RequiredAuthority:  "file_read",
			RetryPolicy:        "use_structured_tool_not_raw_shell",
			OperatorProjection: fmt.Sprintf("Raw shell was rejected, but this can be inspected with list_dir for %s.", path),
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
		return shellRejectionAlternative{
			State:              session.NextActionReadyToExecute,
			OperationKind:      "native_text_search",
			OperationTool:      "search",
			OperationInputJSON: mustJSON(map[string]any{"query": query, "path": path, "limit": 50}),
			NextAction:         "search through the scoped native search tool",
			RequiredAuthority:  "file_read",
			RetryPolicy:        "use_structured_tool_not_raw_shell",
			OperatorProjection: "Raw shell was rejected, but this can be inspected with the native search tool.",
			Reason:             shellRejectionReasonFromError(cause),
		}, true
	default:
		return shellRejectionAlternative{}, false
	}
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
		operationKind = "confined_git_inspection"
		requiredAuthority = "read_only_inspection"
	case "go", "make", "pytest", "npm", "pnpm", "yarn", "cargo":
		candidate := canonicalShellCommand(cmd, args)
		plan := commandeffect.PlanCommand(candidate)
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
		OperationInputJSON: mustJSON(input),
		NextAction:         "run the canonical confined command instead of the rejected raw shell shape",
		RequiredAuthority:  requiredAuthority,
		Verifier:           verifier,
		RetryPolicy:        "use_canonical_confined_exec",
		OperatorProjection: fmt.Sprintf("Raw shell was rejected, but the bounded alternative is %s.", canonical),
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
		steps = append(steps, map[string]any{
			"ordinal":            len(steps) + 1,
			"command":            session.RedactEvidenceText(commandeffect.NormalizeCommand(segment)).Text,
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
		OperationInputJSON: mustJSON(map[string]any{"reason": shellRejectionReason(plan, cause), "steps": steps}),
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
		OperationInputJSON: mustJSON(map[string]any{"repair_operation_id": op.ID, "required_action": op.RequiredAction, "required_resource": op.RequiredResource, "rejected_shape": op.RejectedShape}),
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
		OperationInputJSON: mustJSON(map[string]any{"reason": shellRejectionReason(plan, cause), "steps": steps}),
		NextAction:         "replace the rejected shell with a typed operation or split plan",
		RequiredAuthority:  "typed_operation_required",
		RetryPolicy:        "do_not_retry_unbounded_raw_shell",
		OperatorProjection: "Raw shell could not be bounded. Replace it with a typed operation or split plan before retrying.",
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
