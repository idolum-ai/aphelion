//go:build linux

// Package stoplabels projects typed continuation authority facts into compact
// operator-facing stop labels. It is presentation code: it must not grant,
// revoke, or otherwise decide authority.
package stoplabels

import (
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

// Context is the typed authority shape needed to project forbidden action tokens
// into human stop labels without treating prose as authority.
type Context struct {
	LeaseClass     session.ContinuationLeaseClass
	RiskClass      string
	AllowedActions []string
	BoundedEffect  string
}

// Options controls surface-specific presentation without duplicating taxonomy.
type Options struct {
	Defaults []string
	Limit    int
}

// ContextFromContinuationState extracts projection context from the ledger-backed
// continuation state. Authority still lives in the state; this is only a view.
func ContextFromContinuationState(state session.ContinuationState) Context {
	state = session.NormalizeContinuationState(state)
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	allowed := make([]string, 0, len(proposal.AllowedActions)+len(lease.AllowedActions))
	allowed = append(allowed, proposal.AllowedActions...)
	allowed = append(allowed, lease.AllowedActions...)
	return Context{
		LeaseClass:     lease.LeaseClass,
		RiskClass:      proposal.RiskClass,
		AllowedActions: allowed,
		BoundedEffect:  proposal.BoundedEffect,
	}
}

// LabelsForContinuationState projects the provided forbidden action tokens using
// context from a continuation state.
func LabelsForContinuationState(state session.ContinuationState, forbiddenValues []string, opts Options) []string {
	return Labels(ContextFromContinuationState(state), forbiddenValues, opts)
}

// Labels projects forbidden action tokens into compact human labels.
func Labels(ctx Context, forbiddenValues []string, opts Options) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(forbiddenValues))
	for _, value := range forbiddenValues {
		label := labelForValue(ctx, value)
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	if len(out) == 0 {
		out = defaultLabels(ctx, opts.Defaults)
	}
	out = prioritize(out)
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out
}

func defaultLabels(ctx Context, defaults []string) []string {
	out := make([]string, 0, len(defaults))
	deployRestartAllowed := allowsDeployRestart(ctx)
	for _, value := range defaults {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if deployRestartAllowed && value == "deploy/restart" {
			out = append(out, "release outside approved scope")
			continue
		}
		out = append(out, value)
	}
	if len(out) == 0 && deployRestartAllowed {
		return []string{"release outside approved scope"}
	}
	return out
}

func labelForValue(ctx Context, value string) string {
	if allowsDeployRestart(ctx) {
		if label := releaseGuardrailLabel(value); label != "" {
			return label
		}
	}
	return broadLabel(value)
}

func allowsDeployRestart(ctx Context) bool {
	if session.NormalizeContinuationLeaseClass(ctx.LeaseClass) == session.ContinuationLeaseClassDeployRestart {
		return true
	}
	return session.InferContinuationLeaseClass(ctx.RiskClass, ctx.AllowedActions, ctx.BoundedEffect) == session.ContinuationLeaseClassDeployRestart
}

func releaseGuardrailLabel(value string) string {
	normalized := normalizeToken(value)
	switch normalized {
	case "":
		return ""
	case "deploy", "restart", "restart_service", "service_restart", "deploy_restart", "restart_deploy", "deploy_or_restart", "restart_or_deploy", "deploy_or_enable_systemd", "deploy_or_enable_service", "deploy_service_restart", "restart_or_service_restart":
		return "release outside approved scope"
	case "deploy_without_handoff":
		return "release without handoff"
	case "restart_without_recovery_artifact":
		return "restart without recovery artifact"
	case "skip_build_or_tests_before_restart":
		return "skip build/tests before restart"
	case "skip_post_deploy_verification":
		return "skip post-release verification"
	case "unbounded_restart_loop":
		return "unbounded restart loop"
	case "deploy_restart_without_explicit_approval", "deploy_or_restart_without_explicit_approval", "deploy_or_restart_without_parking":
		return "release outside approved scope"
	}
	if strings.Contains(normalized, "deploy") || strings.Contains(normalized, "restart") {
		return strings.ReplaceAll(normalized, "_", " ")
	}
	return ""
}

func broadLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	switch {
	case value == "":
		return ""
	case strings.Contains(value, "credential") || strings.Contains(value, "token"):
		return "credentials/tokens"
	case strings.Contains(value, "mailbox"):
		return "mailbox access or mutation"
	case strings.Contains(value, "deploy") || strings.Contains(value, "restart"):
		return "deploy/restart"
	case strings.Contains(value, "archive") || strings.Contains(value, "delete") || strings.Contains(value, "mutate source"):
		return "archive/delete"
	case strings.Contains(value, "send") || strings.Contains(value, "contact"):
		return "external send/contact"
	case strings.Contains(value, "hard interrupt"):
		return "hard gates"
	case strings.Contains(value, "lane") || strings.Contains(value, "outside") || strings.Contains(value, "scope") || strings.Contains(value, "budget"):
		return "anything outside scope"
	case strings.Contains(value, "policy") || strings.Contains(value, "grant") || strings.Contains(value, "permission"):
		return "policy or permission changes"
	case strings.Contains(value, "external"):
		return "external account/effect"
	case strings.Contains(value, "purchase") || strings.Contains(value, "spend"):
		return "spend"
	case strings.Contains(value, "public"):
		return "public contact/posting"
	case strings.Contains(value, "autonomous"):
		return "unapproved autonomous work"
	default:
		return value
	}
}

func prioritize(stops []string) []string {
	if len(stops) == 0 {
		return nil
	}
	priority := []string{
		"anything outside scope",
		"hard gates",
		"deploy/restart",
		"release outside approved scope",
		"credentials/tokens",
		"external send/contact",
		"archive/delete",
		"policy or permission changes",
		"mailbox access or mutation",
		"external account/effect",
		"spend",
		"public contact/posting",
		"unapproved autonomous work",
		"release without handoff",
		"restart without recovery artifact",
		"skip build/tests before restart",
		"skip post-release verification",
		"unbounded restart loop",
	}
	seen := make(map[string]struct{}, len(stops))
	for _, stop := range stops {
		if stop = strings.TrimSpace(stop); stop != "" {
			seen[stop] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	add := func(stop string) {
		if _, ok := seen[stop]; !ok {
			return
		}
		out = append(out, stop)
		delete(seen, stop)
	}
	for _, stop := range priority {
		add(stop)
	}
	for _, stop := range stops {
		add(strings.TrimSpace(stop))
	}
	return out
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", ".", "_")
	value = replacer.Replace(value)
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return strings.Trim(value, "_")
}
