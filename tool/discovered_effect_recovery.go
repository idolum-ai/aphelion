//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/effectauth"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func discoveredEffectMissingContinuationLeaseError(key session.SessionKey, p principal.Principal, in execInput, workdir string, plan commandeffect.EffectPlan, decision effectauth.Decision, cause error) (error, bool) {
	if !toolSessionKeyHasIdentity(key) || !decision.Active || decision.Allowed {
		return nil, false
	}
	if !discoveredEffectDecisionRecoverable(decision) || plan.Dynamic || plan.MultipleAuthorities {
		return nil, false
	}
	if _, boundaryOK := commandeffect.BoundaryForPlan(plan); boundaryOK {
		return nil, false
	}
	effect := commandeffect.RepresentativeEffect(plan)
	if effect.Kind != commandeffect.KindExternal || !effect.SideEffects || strings.TrimSpace(effect.Action) == "" {
		return nil, false
	}
	command := strings.TrimSpace(in.Command)
	if command == "" {
		return nil, false
	}
	inputPayload := map[string]any{"command": command}
	if trimmed := strings.TrimSpace(workdir); trimmed != "" {
		inputPayload["workdir"] = filepath.Clean(trimmed)
	}
	if in.TimeoutSec > 0 {
		inputPayload["timeout_sec"] = in.TimeoutSec
	}
	inputJSON, err := json.Marshal(inputPayload)
	if err != nil {
		return nil, false
	}
	commandHash := session.EffectAttemptCommandHash(command)
	constraints := map[string]string{
		"contract_kind":      session.ContinuationRecoveryContractKindDiscoveredEffect,
		"effect_kind":        string(effect.Kind),
		"effect_action":      strings.TrimSpace(effect.Action),
		"command":            command,
		"command_hash":       commandHash,
		"normalized_command": commandeffect.NormalizeCommand(command),
	}
	if provider := strings.TrimSpace(effect.Provider); provider != "" {
		constraints["effect_provider"] = provider
	}
	if subcmd := strings.TrimSpace(effect.GitSubcommand); subcmd != "" {
		constraints["git_subcommand"] = subcmd
	}
	if trimmed := strings.TrimSpace(workdir); trimmed != "" {
		constraints["workdir"] = filepath.Clean(trimmed)
	}
	requirement := normalizeMissingContinuationLeaseRequirement(missingContinuationLeaseRequirement{
		SubjectKind:    session.ContinuationRecoverySubjectKindDiscoveredEffect,
		Resource:       "command:" + commandHash,
		Principal:      toolAuthorityCanonicalPrincipal(p),
		LeaseClass:     session.ContinuationLeaseClassDataAccess,
		AllowedActions: discoveredEffectAllowedActions(command, effect),
		Constraints:    constraints,
		Tool:           "exec",
		ToolAction:     session.ContinuationRecoveryRetryExecExactCommand,
		RetryOperation: session.ContinuationRetryOperation{Contract: session.ContinuationRecoveryRetryVersion, OperationKind: session.ContinuationRecoveryRetryExecExactCommand, Tool: "exec", InputJSON: string(inputJSON), SubjectKind: session.ContinuationRecoverySubjectKindDiscoveredEffect},
		NextAction:     "approve the discovered exact external command before retrying it once",
		OperatorProjection: fmt.Sprintf("The command %q was blocked by the active continuation envelope as %s/%s. Approve the stored discovered-effect contract to retry exactly this command once.",
			command,
			strings.TrimSpace(string(effect.Kind)),
			strings.TrimSpace(effect.Action),
		),
	})
	if missingContinuationLeaseSubjectToken(requirement) == "" || requirement.Principal == "" {
		return nil, false
	}
	return missingContinuationLeaseError{requirement: requirement, cause: cause}, true
}

func discoveredEffectDecisionRecoverable(decision effectauth.Decision) bool {
	switch strings.TrimSpace(decision.Reason) {
	case "external_effect_not_allowed_by_contract",
		"action_not_allowed_by_continuation_envelope",
		"invalid_authority_contract":
		return true
	default:
		return false
	}
}

func discoveredEffectAllowedActions(command string, effect commandeffect.Effect) []string {
	actions := []string{strings.TrimSpace(effect.Action)}
	if exact := discoveredEffectExactActionLabel(command, effect); exact != "" {
		actions = append(actions, exact)
	}
	switch strings.TrimSpace(effect.Action) {
	case "fetch":
		actions = append(actions, "report_fetch_evidence")
	default:
		actions = append(actions, "report_evidence")
	}
	return normalizeUniqueStrings(actions)
}

func discoveredEffectExactActionLabel(command string, effect commandeffect.Effect) string {
	if strings.TrimSpace(effect.Provider) != "git" || strings.TrimSpace(effect.GitSubcommand) == "" {
		return ""
	}
	fields := strings.Fields(commandeffect.NormalizeCommand(command))
	if len(fields) < 2 || fields[0] != "git" || fields[1] != strings.TrimSpace(effect.GitSubcommand) {
		return ""
	}
	parts := []string{"git", strings.TrimSpace(effect.GitSubcommand)}
	for _, field := range fields[2:] {
		token := discoveredEffectActionToken(field)
		if token == "" {
			continue
		}
		parts = append(parts, token)
		if len(parts) >= 6 {
			break
		}
	}
	return strings.Join(parts, "_")
}

func discoveredEffectActionToken(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "-")
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(value) {
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
