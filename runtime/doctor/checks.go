//go:build linux

package doctor

import (
	"fmt"
	"strconv"
	"strings"
)

func (r *Runtime) writeDoctorIssueStatusChecks(b *strings.Builder, input DiagnosticInput) {
	WriteLine(b, "classification_contract: before reporting an issue as current, compare historical failure evidence with current runtime, prompt, memory, and source-state evidence.")
	WriteLine(b, "classification_scope: operational_tension")
	WriteLine(b, "allowed_statuses: active, likely_fixed, historical_resolved, residual_risk, unknown")
	WriteLine(b, "reporting_rule: if evidence is old and the current-state check passes, report it as historical/resolved or residual risk, not as an active failure.")

	identityStatus, identityEvidence := doctorPromptIdentityStatus(input.PromptContext)
	writeDoctorIssueCheck(b, "prompt_identity_canonical", identityStatus, identityEvidence)

	workingRoot := strings.TrimSpace(input.Scope.WorkingRoot)
	retrySourceOK := doctorSourceContainsAll(workingRoot, "agent/turn.go", []string{"completeWithRetry", "isRetryableProviderError", "maxProviderRetries"})
	transportRetries := 0
	if r != nil && r.cfg != nil {
		transportRetries = r.cfg.Governor.Codex.TransportRetries
	}
	switch {
	case retrySourceOK && transportRetries > 0:
		writeDoctorIssueCheck(b, "codex_timeout_retries", "likely_fixed", fmt.Sprintf("codex_transport_retries=%d and provider retry loop is present in agent/turn.go", transportRetries))
	case retrySourceOK:
		writeDoctorIssueCheck(b, "codex_timeout_retries", "residual_risk", fmt.Sprintf("agent provider retry loop is present, but codex_transport_retries=%d", transportRetries))
	default:
		writeDoctorIssueCheck(b, "codex_timeout_retries", "unknown", "could not confirm retry-loop source evidence from working_root")
	}

	skillsConfigured := r != nil && r.cfg != nil && doctorPathListContains(r.cfg.Agent.DynamicFiles, "SKILLS.md")
	skillsLoaded := doctorPromptContextHasFile(input.PromptContext, "SKILLS.md")
	if skillsConfigured && skillsLoaded {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "likely_fixed", "SKILLS.md is configured as a dynamic file and is present in loaded prompt context")
	} else if skillsConfigured {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "active", "SKILLS.md is configured but was not present in loaded prompt context")
	} else if skillsLoaded {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "residual_risk", "SKILLS.md loaded, but it is not explicitly listed in configured dynamic files")
	} else {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "active", "SKILLS.md is not configured or loaded as dynamic context")
	}

	memoryConfigured := r != nil && r.cfg != nil &&
		doctorPathListContains(r.cfg.Agent.DynamicFiles, "memory/knowledge.md") &&
		doctorPathListContains(r.cfg.Agent.DynamicFiles, "memory/decisions.md")
	memoryLoaded := doctorPromptContextHasFile(input.PromptContext, "memory/knowledge.md") ||
		doctorPromptContextHasFile(input.PromptContext, "memory/decisions.md")
	recoverySourceOK := doctorSourceContainsAll(workingRoot, "main.go", []string{"StartStartupRecovery"}) &&
		doctorSourceContainsAll(workingRoot, "runtime/recovery.go", []string{"StartStartupRecovery", "PendingRecoveryTurnRuns"})
	switch {
	case memoryConfigured && memoryLoaded && recoverySourceOK:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "likely_fixed", "structured memory files load dynamically and startup recovery source evidence is present")
	case memoryConfigured && memoryLoaded:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "residual_risk", "structured memory files load dynamically, but startup recovery source evidence was not confirmed")
	case memoryConfigured:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "active", "structured memory files are configured but were not present in loaded prompt context")
	default:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "active", "structured memory files are not fully configured as dynamic context")
	}

	operationalAlertSourceOK := doctorSourceContainsAll(workingRoot, "runtime/operational_alerts.go", []string{"reportOperationalIssueAsync", "sendOperationalNoticeToAdmin", "system_warning"}) &&
		doctorSourceContainsAll(workingRoot, "runtime/turn_coordinator_common.go", []string{"reportOperationalIssueAsync", "provider"})
	adminConfigured := r != nil && r.cfg != nil && len(uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)) > 0
	if operationalAlertSourceOK && adminConfigured {
		writeDoctorIssueCheck(b, "runtime_failures_surface_to_telegram", "likely_fixed", "operational alert delivery source is present and at least one admin Telegram ID is configured")
	} else if operationalAlertSourceOK {
		writeDoctorIssueCheck(b, "runtime_failures_surface_to_telegram", "residual_risk", "operational alert delivery source is present, but no admin Telegram ID is configured")
	} else {
		writeDoctorIssueCheck(b, "runtime_failures_surface_to_telegram", "unknown", "could not confirm operational alert delivery source evidence from working_root")
	}

	workspaceEscapeGateOK := doctorSourceContainsAll(workingRoot, "tool/exec.go", []string{"workspace_escape", "ConfirmExec", "ProposalStatusApproved"})
	if workspaceEscapeGateOK {
		writeDoctorIssueCheck(b, "admin_workspace_escape_requires_approval", "likely_fixed", "exec workspace escape path is gated by capability/proposal source evidence")
	} else {
		writeDoctorIssueCheck(b, "admin_workspace_escape_requires_approval", "unknown", "could not confirm workspace escape approval gate source evidence from working_root")
	}

	childBotRunnerSourceOK := doctorSourceContainsAll(workingRoot, "main_telegram_child_bot.go", []string{"runTelegramChildBotCommandWithDeps", "validateTelegramChildBotTokenMetadata", "telegramChildBotHealthStatus", "runTelegramChildBotGetMeSmoke", "runTelegramChildBotDryStart", "telegramChildBotNoSendOutbound"})
	childBotRunbookOK := doctorSourceContainsAll(workingRoot, "docs/architecture/telegram-child-bot-runbook.md", []string{"generic but narrow", "telegram-child-bot"})
	switch {
	case childBotRunnerSourceOK && childBotRunbookOK:
		writeDoctorIssueCheck(b, "telegram_child_bot_runner", "likely_fixed", "generic child bot runner source includes metadata-only health/status, no-send dry-start, and getMe smoke gates")
	case childBotRunnerSourceOK:
		writeDoctorIssueCheck(b, "telegram_child_bot_runner", "residual_risk", "generic child bot runner source is present, but runbook/status projection evidence was not confirmed")
	default:
		writeDoctorIssueCheck(b, "telegram_child_bot_runner", "unknown", "could not confirm generic child bot runner source evidence from working_root")
	}
}

func (r *Runtime) writeDoctorDesignPrincipleHealth(b *strings.Builder, input DiagnosticInput) {
	workingRoot := strings.TrimSpace(input.Scope.WorkingRoot)
	WriteLine(b, "classification_contract: design-principle health is advisory evidence, not runtime authority.")
	WriteLine(b, "classification_scope: principle_debt")

	principlesOK := doctorSourceContainsAll(workingRoot, "docs/architecture/design-principles.md", []string{
		"Text is presentation, not authority",
		"Compile contracts; interpret ambiguity",
		"Short paths to truth",
	})
	if principlesOK {
		writeDoctorIssueCheck(b, "design_principles_doc", "likely_fixed", "design-principles.md names text/presentation boundaries, compiled contracts, interpreted ambiguity, and short debug paths")
	} else {
		writeDoctorIssueCheck(b, "design_principles_doc", "active", "could not confirm required design-principle text from docs/architecture/design-principles.md")
	}

	debtOK := doctorSourceContainsAll(workingRoot, "docs/architecture/principle-debt.md", []string{
		"## Active Debt",
		"None.",
		"## Machine-Checked Paths",
	})
	if debtOK {
		writeDoctorIssueCheck(b, "principle_debt_ledger", "likely_fixed", "principle-debt.md reports no active debt and keeps machine-checked paths")
	} else {
		writeDoctorIssueCheck(b, "principle_debt_ledger", "active", "could not confirm principle-debt.md with no active debt and machine-checked paths")
	}

	retiredStringDebtMatches := doctorSourceMatches(workingRoot, []string{"runtime", "session", "core"}, []string{
		"lexical_" + "safety_scanner",
		"status_" + "line_fallback",
		"detect" + "ExecutionClaims",
		"text" + "Requests" + "PendingAudioTranscription",
	}, false, 1)
	retiredStringDebtAbsent := len(retiredStringDebtMatches) == 0
	interpretationLaneOK := doctorSourceContainsAll(workingRoot, "runtime/interpretation_claims.go", []string{
		"INTERPRETATION_CLAIMS",
		"interpretCurrentTurnClaims",
		"InterpretationClaim",
	})
	if retiredStringDebtAbsent && interpretationLaneOK {
		writeDoctorIssueCheck(b, "string_authority_retired", "likely_fixed", "retired string-authority helpers are absent and the typed interpretation lane is present")
	} else {
		writeDoctorIssueCheck(b, "string_authority_retired", "active", "could not confirm retired string-authority helpers are absent and typed interpretation lane is present")
	}

	debugBreadcrumbsOK := doctorSourceContainsAll(workingRoot, "core/interpretation.go", []string{
		"DebugBreadcrumb",
		"TraceID",
		"InspectCommand",
		"NextRepairAction",
	}) && doctorSourceContainsAll(workingRoot, "runtime/status_lifecycle.go", []string{
		"attachPendingItemDebugBreadcrumbs",
		"pendingItemDebugBreadcrumb",
	}) && doctorSourceContainsAll(workingRoot, "face/status_render.go", []string{
		"next_repair_action",
		"inspect_command",
	})
	if debugBreadcrumbsOK {
		writeDoctorIssueCheck(b, "short_debug_path_contract", "likely_fixed", "standard debug breadcrumb schema is present and status pending items render inspect and repair fields")
	} else {
		writeDoctorIssueCheck(b, "short_debug_path_contract", "active", "standard debug breadcrumb schema or status projection wiring was not confirmed")
	}
	WriteKV(b, "design_principle_next", "keep typed interpretation and debug breadcrumb gates green during feature work")
	_ = r
}

func writeDoctorIssueCheck(b *strings.Builder, issue string, status string, evidence string) {
	WriteLine(b, fmt.Sprintf("- issue=%s status=%s evidence=%q",
		strings.TrimSpace(issue),
		strings.TrimSpace(status),
		truncatePreview(evidence, 600),
	))
}

func (r *Runtime) writeDoctorExternalToolInvocationReadiness(b *strings.Builder, input DiagnosticInput) {
	if r == nil || r.store == nil {
		WriteLine(b, "external_tool_invocation_readiness: unavailable")
		return
	}
	if r.toolLifecycleStatusSnapshot == nil || r.capabilityStatusSnapshot == nil || r.externalToolInvocationReadinessStatusSnapshot == nil {
		WriteLine(b, "external_tool_invocation_readiness: unavailable")
		return
	}
	tools, err := r.toolLifecycleStatusSnapshot(20)
	if err != nil {
		WriteLine(b, "external_tool_invocation_readiness_error="+strconv.Quote("tool lifecycle: "+err.Error()))
		return
	}
	_, grants, err := r.capabilityStatusSnapshot(20)
	if err != nil {
		WriteLine(b, "external_tool_invocation_readiness_error="+strconv.Quote("capability grants: "+err.Error()))
		return
	}
	rows := r.externalToolInvocationReadinessStatusSnapshot(tools, grants)
	if len(rows) == 0 {
		WriteLine(b, "external_tool_invocation_readiness: none")
		return
	}
	for _, row := range rows {
		status := "blocked"
		if row.Ready || strings.EqualFold(strings.TrimSpace(row.Status), "ready") {
			status = "ready"
		}
		selector := strings.TrimSpace(row.SelectorName)
		if selector == "" {
			selector = "-"
		}
		WriteLine(b, fmt.Sprintf("- tool=%s child=%s action=%s selector=%s status=%s why=%q next_repair=%q",
			firstNonEmpty(strings.TrimSpace(row.ToolName), "-"),
			firstNonEmpty(strings.TrimSpace(row.ChildPrincipal), "-"),
			firstNonEmpty(strings.TrimSpace(row.Action), "-"),
			selector,
			status,
			truncatePreview(strings.TrimSpace(row.Why), 180),
			truncatePreview(strings.TrimSpace(row.NextRepairAction), 160),
		))
	}
	_ = input
}
