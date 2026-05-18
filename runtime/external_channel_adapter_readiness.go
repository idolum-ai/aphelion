//go:build linux

package runtime

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const (
	externalChannelReadinessStatusReady    = "ready"
	externalChannelReadinessStatusBlocked  = "blocked"
	externalChannelReadinessStatusResidual = "residual_risk"
	externalChannelReadinessFailureNone    = "none"
	externalChannelReadinessFailureAdapter = "adapter_missing"
	externalChannelReadinessFailureLife    = "lifecycle_unregistered"
	externalChannelReadinessFailureGrant   = "grant_missing_or_stale"
	externalChannelReadinessFailureSandbox = "sandbox_backend_unavailable"
	externalChannelReadinessFailureRuntime = "runtime_material_missing"
)

type externalChannelAdapterReadiness struct {
	AgentID     string
	Adapter     string
	Status      string
	FailureCode string
	NextRepair  string
	Layers      []externalChannelAdapterReadinessLayer
	LastWake    *externalChannelAdapterWakeStatus
	GeneratedAt time.Time
}

type externalChannelAdapterReadinessLayer struct {
	Name     string
	Status   string
	Evidence string
}

type externalChannelAdapterWakeStatus struct {
	Status       string
	Error        string
	FailureCount int
	BackoffUntil time.Time
}

func (r *Runtime) writeDoctorExternalChannelAdapterReadiness(b *strings.Builder, input doctorDiagnosticInput) {
	writeDoctorLine(b, "classification_contract: external-channel adapter readiness is generic parent-owned metadata; adapter-specific probes belong to the child and report upward as review artifacts.")
	if r == nil || r.store == nil {
		writeDoctorLine(b, "external_channel_adapter_readiness: unavailable")
		return
	}
	rows, err := r.externalChannelAdapterReadinessSnapshots(input.Now)
	if err != nil {
		writeDoctorLine(b, "external_channel_adapter_readiness_error="+strconvQuote(err.Error()))
		return
	}
	if len(rows) == 0 {
		writeDoctorLine(b, "external_channel_adapter_readiness: none")
		return
	}
	for _, row := range rows {
		writeDoctorLine(b, fmt.Sprintf("- agent=%s adapter=%s status=%s failure=%s next_repair=%q",
			firstNonEmpty(row.AgentID, "-"),
			firstNonEmpty(row.Adapter, "-"),
			firstNonEmpty(row.Status, externalChannelReadinessStatusBlocked),
			firstNonEmpty(row.FailureCode, externalChannelReadinessFailureNone),
			truncatePreview(row.NextRepair, 220),
		))
		for _, layer := range row.Layers {
			writeDoctorLine(b, fmt.Sprintf("  - layer=%s status=%s evidence=%q",
				firstNonEmpty(layer.Name, "-"),
				firstNonEmpty(layer.Status, "unknown"),
				truncatePreview(layer.Evidence, 260),
			))
		}
		if row.LastWake != nil {
			writeDoctorLine(b, fmt.Sprintf("  - layer=last_wake status=%s failure_count=%d backoff_until=%s error=%q",
				firstNonEmpty(row.LastWake.Status, "unknown"),
				row.LastWake.FailureCount,
				formatDoctorTime(row.LastWake.BackoffUntil),
				truncatePreview(row.LastWake.Error, 260),
			))
		}
	}
}

func (r *Runtime) externalChannelAdapterReadinessSnapshots(now time.Time) ([]externalChannelAdapterReadiness, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return nil, err
	}
	rows := make([]externalChannelAdapterReadiness, 0, len(agents))
	for _, agent := range agents {
		external := agent.ChannelConfig.ExternalConfig()
		if external == nil || strings.TrimSpace(external.Adapter) == "" || strings.EqualFold(strings.TrimSpace(external.Adapter), codexAppServerAdapterName) {
			continue
		}
		rows = append(rows, r.externalChannelReadinessForAgent(agent, now))
	}
	return rows, nil
}

func (r *Runtime) externalChannelReadinessForAgent(agent core.DurableAgent, now time.Time) externalChannelAdapterReadiness {
	agent = normalizeDoctorReadinessAgent(agent)
	adapterName := externalChannelAdapter(agent)
	row := externalChannelAdapterReadiness{
		AgentID:     strings.TrimSpace(agent.AgentID),
		Adapter:     adapterName,
		Status:      externalChannelReadinessStatusReady,
		FailureCode: externalChannelReadinessFailureNone,
		NextRepair:  "none",
		GeneratedAt: now.UTC(),
	}
	setFailure := func(code string, next string) {
		if row.FailureCode == externalChannelReadinessFailureNone || row.FailureCode == "" {
			row.FailureCode = strings.TrimSpace(code)
			row.NextRepair = strings.TrimSpace(next)
			row.Status = externalChannelReadinessStatusBlocked
		}
	}
	addLayer := func(name string, status string, evidence string) {
		row.Layers = append(row.Layers, externalChannelAdapterReadinessLayer{Name: name, Status: status, Evidence: evidence})
	}

	external := agent.ChannelConfig.ExternalConfig()
	if external == nil || adapterName == "" {
		addLayer("policy_channel_adapter", externalChannelReadinessStatusBlocked, "durable child does not declare channel_config.external.adapter")
		setFailure(externalChannelReadinessFailureAdapter, "configure the durable child external_channel adapter before scheduling polls")
		return row
	}
	addLayer("policy_channel_adapter", externalChannelReadinessStatusReady, "external_channel adapter="+adapterName+" configured without implying adapter-local access")

	registered, registeredOK, registeredErr := r.store.RegisteredTool(adapterName)
	install, installOK, installErr := r.store.ToolInstallRecord(adapterName)
	probe, probeOK, probeErr := r.store.ToolProbeRecord(adapterName)
	audit, auditOK, auditErr := r.store.ToolAuditRecord(adapterName)
	if registeredErr != nil || installErr != nil || probeErr != nil || auditErr != nil {
		addLayer("tool_lifecycle", externalChannelReadinessStatusBlocked, firstNonEmpty(errorText(registeredErr), errorText(installErr), errorText(probeErr), errorText(auditErr)))
		setFailure(externalChannelReadinessFailureLife, "repair tool lifecycle records for "+adapterName+" before polling")
	} else if !registeredOK || !registered.Registered || !installOK || !probeOK || !auditOK {
		addLayer("tool_lifecycle", externalChannelReadinessStatusBlocked, adapterName+" lacks complete registered/install/audit/probe lifecycle records")
		setFailure(externalChannelReadinessFailureLife, "register, install, audit, and probe "+adapterName+" as a first-class external tool")
	} else if install.Status != session.ToolInstallStatusVerified || probe.Status != session.ToolProbeStatusPassed || audit.Status != session.ToolAuditStatusPassed {
		addLayer("tool_lifecycle", externalChannelReadinessStatusBlocked, fmt.Sprintf("install=%s audit=%s probe=%s", install.Status, audit.Status, probe.Status))
		setFailure(externalChannelReadinessFailureLife, "rerun or repair "+adapterName+" install/audit/probe lifecycle")
	} else {
		addLayer("tool_lifecycle", externalChannelReadinessStatusReady, fmt.Sprintf("registered=true install=%s audit=%s probe=%s", install.Status, audit.Status, probe.Status))
	}

	principalID := core.DurableAgentPrincipal(agent.AgentID)
	grants, grantsErr := r.store.CapabilityGrants(200, "", "", principalID)
	if grantsErr != nil {
		addLayer("grant_materialization", externalChannelReadinessStatusBlocked, grantsErr.Error())
		setFailure(externalChannelReadinessFailureGrant, "repair capability grant lookup before polling")
		return rowWithLastWake(r, row, agent)
	}
	toolGrant, toolMaterial, toolMaterialOK, toolEvidence := selectExternalChannelToolGrant(grants, principalID, adapterName)
	if strings.TrimSpace(toolGrant.GrantID) == "" || !toolMaterialOK {
		addLayer("grant_tool_runtime", externalChannelReadinessStatusBlocked, firstNonEmpty(toolEvidence, "missing active "+adapterName+" tool grant with child_runtime material"))
		setFailure(externalChannelReadinessFailureGrant, "create or repair an active "+adapterName+" tool grant with child_runtime material")
	} else {
		addLayer("grant_tool_runtime", externalChannelReadinessStatusReady, toolEvidence)
	}

	workspaceRoot, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if workspaceRoot == "" || memoryRoot == "" {
		if r.store != nil {
			workspaceRoot, memoryRoot = durableagent.DefaultLocalRoots(r.store.DBPath(), agent.AgentID)
		}
	}
	durableProfile := sandbox.Profile{}
	if r != nil && r.cfg != nil {
		if profiles, err := SandboxProfilesFromConfig(r.cfg.Sandbox); err == nil {
			durableProfile = profiles.DurableAgent
		}
	}
	scope, scopeErr := sandbox.DurableAgentScopeWithProfile(agent.AgentID, doctorReadinessGlobalRoot(r), workspaceRoot, memoryRoot, durableProfile, agent.NetworkPolicy)
	if scopeErr != nil {
		addLayer("sandbox", externalChannelReadinessStatusBlocked, scopeErr.Error())
		setFailure(externalChannelReadinessFailureSandbox, "repair durable child local roots before sandbox readiness can be checked")
	} else {
		stage := sandbox.NewRunner().Stage(scope)
		if stage == sandbox.StageUnavailable {
			addLayer("sandbox", externalChannelReadinessStatusBlocked, "isolated durable-agent sandbox backend is unavailable")
			setFailure(externalChannelReadinessFailureSandbox, "install or enable the configured isolated sandbox backend before child wakes")
		} else {
			addLayer("sandbox", externalChannelReadinessStatusReady, "isolated durable-agent sandbox backend="+string(stage))
		}
	}

	if toolMaterialOK {
		if missing := firstMissingChildRuntimeMaterial(toolMaterial); missing != "" {
			addLayer("runtime_material", externalChannelReadinessStatusBlocked, "runtime material missing: "+missing)
			setFailure(externalChannelReadinessFailureRuntime, "provide or correct the named child_runtime material without printing secret values")
		} else {
			addLayer("runtime_material", externalChannelReadinessStatusReady, "child_runtime material sources exist")
		}
	}

	return rowWithLastWake(r, row, agent)
}

func rowWithLastWake(r *Runtime, row externalChannelAdapterReadiness, agent core.DurableAgent) externalChannelAdapterReadiness {
	if r == nil || r.store == nil {
		return row
	}
	state, err := r.store.DurableAgentState(strings.TrimSpace(agent.AgentID))
	if err != nil {
		if !strings.Contains(err.Error(), sql.ErrNoRows.Error()) {
			row.Layers = append(row.Layers, externalChannelAdapterReadinessLayer{Name: "last_wake", Status: "unknown", Evidence: err.Error()})
		}
		return row
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		row.Layers = append(row.Layers, externalChannelAdapterReadinessLayer{Name: "last_wake", Status: "unknown", Evidence: err.Error()})
		return row
	}
	runtimeState := continuity.ExternalChannel
	if runtimeState == nil || !strings.EqualFold(strings.TrimSpace(runtimeState.Adapter), strings.TrimSpace(row.Adapter)) {
		return row
	}
	row.LastWake = &externalChannelAdapterWakeStatus{
		Status:       strings.TrimSpace(runtimeState.LastStatus),
		Error:        redactDoctorText(runtimeState.LastError),
		FailureCount: runtimeState.FailureCount,
		BackoffUntil: runtimeState.BackoffUntil,
	}
	if externalChannelLastWakeNeedsAttention(runtimeState.LastStatus) && row.Status == externalChannelReadinessStatusReady {
		row.Status = externalChannelReadinessStatusResidual
		row.FailureCode = "last_" + strings.TrimSpace(runtimeState.LastStatus)
		row.NextRepair = "generic readiness passes, but the last child wake needs attention; inspect the child review artifact before the next live poll"
	}
	return row
}

func externalChannelLastWakeNeedsAttention(status string) bool {
	switch strings.TrimSpace(status) {
	case "wake_blocked", "wake_failed":
		return true
	default:
		return false
	}
}

func selectExternalChannelToolGrant(grants []session.CapabilityGrant, principalID string, toolName string) (session.CapabilityGrant, core.ChildRuntimeContract, bool, string) {
	toolName = strings.TrimSpace(toolName)
	var firstBlocked session.CapabilityGrant
	firstEvidence := ""
	rememberBlocked := func(grant session.CapabilityGrant, evidence string) {
		if strings.TrimSpace(firstEvidence) != "" {
			return
		}
		firstBlocked = grant
		firstEvidence = strings.TrimSpace(evidence)
	}
	for _, grant := range grants {
		grant = session.NormalizeCapabilityGrant(grant)
		if strings.TrimSpace(grant.GrantedTo) != principalID || grant.Kind != session.CapabilityKindTool || !strings.EqualFold(strings.TrimSpace(grant.TargetResource), toolName) {
			continue
		}
		if grant.Status != session.CapabilityGrantStatusActive || !grant.RevokedAt.IsZero() || (!grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(time.Now().UTC())) || strings.TrimSpace(grant.StaleReason) != "" {
			rememberBlocked(grant, fmt.Sprintf("grant=%s status=%s stale=%s", grant.GrantID, grant.Status, strings.TrimSpace(grant.StaleReason)))
			continue
		}
		material, ok, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints)
		if err != nil {
			rememberBlocked(grant, "invalid child_runtime contract: "+err.Error())
			continue
		}
		if !ok {
			rememberBlocked(grant, "active tool grant has no child_runtime material")
			continue
		}
		if !containsReadinessString(grant.AllowedActions, "invoke") {
			rememberBlocked(grant, "active tool grant does not allow invoke")
			continue
		}
		return grant, material, true, fmt.Sprintf("grant=%s child_runtime=present", grant.GrantID)
	}
	return firstBlocked, core.ChildRuntimeContract{}, false, firstEvidence
}

func normalizeDoctorReadinessAgent(agent core.DurableAgent) core.DurableAgent {
	agent.AgentID = strings.TrimSpace(agent.AgentID)
	agent.ChannelKind = strings.TrimSpace(agent.ChannelKind)
	agent.NetworkPolicy = strings.TrimSpace(agent.NetworkPolicy)
	agent.ChannelConfig = core.NormalizeDurableAgentChannelConfig(agent.ChannelConfig)
	return agent
}

func doctorReadinessGlobalRoot(r *Runtime) string {
	if r != nil && r.cfg != nil {
		return strings.TrimSpace(r.cfg.Agent.PromptRoot)
	}
	return "/"
}

func formatDoctorTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func strconvQuote(value string) string {
	return fmt.Sprintf("%q", strings.TrimSpace(value))
}
