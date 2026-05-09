//go:build linux

package runtime

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const (
	gogCLIAdapterName              = "gog_cli"
	gogCLIRequiredSecretEnvName    = "GOG_KEYRING_PASSWORD"
	gogCLIChildConfigRelativeRoot  = ".config/gogcli"
	gogCLIChildSandboxExecutable   = "/usr/local/bin/gog_cli"
	gogCLIReadinessStatusReady     = "ready"
	gogCLIReadinessStatusBlocked   = "blocked"
	gogCLIReadinessStatusResidual  = "residual_risk"
	gogCLIReadinessFailureNone     = "none"
	gogCLIReadinessFailureBinary   = "binary_missing"
	gogCLIReadinessFailureWrapper  = "wrapper_mismatch"
	gogCLIReadinessFailureLife     = "lifecycle_unregistered"
	gogCLIReadinessFailureGrant    = "grant_missing_or_stale"
	gogCLIReadinessFailureSandbox  = "sandbox_backend_unavailable"
	gogCLIReadinessFailurePath     = "sandbox_path_unbound"
	gogCLIReadinessFailureConfig   = "config_root_missing"
	gogCLIReadinessFailureCreds    = "credentials_metadata_missing"
	gogCLIReadinessFailureKeyring  = "keyring_material_missing"
	gogCLIReadinessFailureSecret   = "secret_env_unavailable"
	gogCLIReadinessFailureLeak     = "probe_secret_leak"
	gogCLIReadinessFailureMailbox  = "mailbox_probe_attempted"
	gogCLIReadinessFailureLivePoll = "live_poll_blocked"
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
	writeDoctorLine(b, "classification_contract: external-channel adapter readiness is metadata-only; no mailbox query, OAuth flow, token/passphrase value, or live account call is performed.")
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
		writeDoctorLine(b, fmt.Sprintf("- agent=%s adapter=%s status=%s failure=%s executable=%s next_repair=%q",
			firstNonEmpty(row.AgentID, "-"),
			firstNonEmpty(row.Adapter, "-"),
			firstNonEmpty(row.Status, gogCLIReadinessStatusBlocked),
			firstNonEmpty(row.FailureCode, gogCLIReadinessFailureNone),
			gogCLIChildSandboxExecutable,
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
		if external == nil || !strings.EqualFold(strings.TrimSpace(external.Adapter), gogCLIAdapterName) {
			continue
		}
		rows = append(rows, r.gogCLIReadinessForAgent(agent, now))
	}
	return rows, nil
}

func (r *Runtime) gogCLIReadinessForAgent(agent core.DurableAgent, now time.Time) externalChannelAdapterReadiness {
	agent = normalizeDoctorReadinessAgent(agent)
	row := externalChannelAdapterReadiness{
		AgentID:     strings.TrimSpace(agent.AgentID),
		Adapter:     gogCLIAdapterName,
		Status:      gogCLIReadinessStatusReady,
		FailureCode: gogCLIReadinessFailureNone,
		NextRepair:  "none",
		GeneratedAt: now.UTC(),
	}
	setFailure := func(code string, next string) {
		if row.FailureCode == gogCLIReadinessFailureNone || row.FailureCode == "" {
			row.FailureCode = strings.TrimSpace(code)
			row.NextRepair = strings.TrimSpace(next)
			row.Status = gogCLIReadinessStatusBlocked
		}
	}
	addLayer := func(name string, status string, evidence string) {
		row.Layers = append(row.Layers, externalChannelAdapterReadinessLayer{Name: name, Status: status, Evidence: evidence})
	}

	external := agent.ChannelConfig.ExternalConfig()
	if external == nil || !strings.EqualFold(strings.TrimSpace(external.Adapter), gogCLIAdapterName) {
		addLayer("policy_channel_adapter", gogCLIReadinessStatusBlocked, "durable child does not declare external_channel adapter gog_cli")
		setFailure(gogCLIReadinessFailureGrant, "configure the durable child external_channel adapter before materializing runtime")
		return row
	}
	addLayer("policy_channel_adapter", gogCLIReadinessStatusReady, "external_channel adapter=gog_cli query configured without implying mailbox access")

	registered, registeredOK, registeredErr := r.store.RegisteredTool(gogCLIAdapterName)
	install, installOK, installErr := r.store.ToolInstallRecord(gogCLIAdapterName)
	probe, probeOK, probeErr := r.store.ToolProbeRecord(gogCLIAdapterName)
	audit, auditOK, auditErr := r.store.ToolAuditRecord(gogCLIAdapterName)
	if registeredErr != nil || installErr != nil || probeErr != nil || auditErr != nil {
		addLayer("tool_lifecycle", gogCLIReadinessStatusBlocked, firstNonEmpty(errorText(registeredErr), errorText(installErr), errorText(probeErr), errorText(auditErr)))
		setFailure(gogCLIReadinessFailureLife, "repair tool lifecycle records for gog_cli before polling")
	} else if !registeredOK || !registered.Registered || !installOK || !probeOK || !auditOK {
		addLayer("tool_lifecycle", gogCLIReadinessStatusBlocked, "gog_cli lacks complete registered/install/audit/probe lifecycle records")
		setFailure(gogCLIReadinessFailureLife, "register, install, audit, and probe gog_cli as a first-class external tool")
	} else if install.Status != session.ToolInstallStatusVerified || probe.Status != session.ToolProbeStatusPassed || audit.Status != session.ToolAuditStatusPassed {
		addLayer("tool_lifecycle", gogCLIReadinessStatusBlocked, fmt.Sprintf("install=%s audit=%s probe=%s", install.Status, audit.Status, probe.Status))
		setFailure(gogCLIReadinessFailureLife, "rerun or repair gog_cli install/audit/probe lifecycle")
	} else {
		addLayer("tool_lifecycle", gogCLIReadinessStatusReady, fmt.Sprintf("registered=true install=%s audit=%s probe=%s", install.Status, audit.Status, probe.Status))
	}

	principalID := core.DurableAgentPrincipal(agent.AgentID)
	grants, grantsErr := r.store.CapabilityGrants(200, "", "", principalID)
	if grantsErr != nil {
		addLayer("grant_materialization", gogCLIReadinessStatusBlocked, grantsErr.Error())
		setFailure(gogCLIReadinessFailureGrant, "repair capability grant lookup before polling")
		return rowWithLastWake(r, row, agent)
	}
	toolGrant, toolMaterial, toolMaterialOK, toolEvidence := selectGogCLIToolGrant(grants, principalID)
	accountGrant, accountMaterial, accountMaterialOK, accountEvidence := selectGogCLIAccountGrant(grants, principalID)
	if strings.TrimSpace(toolGrant.GrantID) == "" || !toolMaterialOK {
		addLayer("grant_tool_runtime", gogCLIReadinessStatusBlocked, firstNonEmpty(toolEvidence, "missing active gog_cli tool grant with child_runtime material"))
		setFailure(gogCLIReadinessFailureGrant, "create or repair active gog_cli tool grant with child_runtime executable/bind/env_from_parent")
	} else {
		addLayer("grant_tool_runtime", gogCLIReadinessStatusReady, toolEvidence)
	}
	if strings.TrimSpace(accountGrant.GrantID) == "" || !accountMaterialOK {
		addLayer("grant_account_material", gogCLIReadinessStatusBlocked, firstNonEmpty(accountEvidence, "missing active gog_cli external-account grant with config material"))
		setFailure(gogCLIReadinessFailureGrant, "create or repair active gog_cli external-account grant with read-only config material")
	} else {
		addLayer("grant_account_material", gogCLIReadinessStatusReady, accountEvidence)
	}

	workspaceRoot, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if workspaceRoot == "" || memoryRoot == "" {
		if r.store != nil {
			workspaceRoot, memoryRoot = durableagent.DefaultLocalRoots(r.store.DBPath(), agent.AgentID)
		}
	}
	scope, scopeErr := sandbox.DurableAgentScope(agent.AgentID, doctorReadinessGlobalRoot(r), workspaceRoot, memoryRoot, agent.NetworkPolicy)
	if scopeErr != nil {
		addLayer("sandbox", gogCLIReadinessStatusBlocked, scopeErr.Error())
		setFailure(gogCLIReadinessFailureSandbox, "repair durable child local roots before sandbox readiness can be checked")
	} else {
		stage := sandbox.NewRunner().Stage(scope)
		if stage == sandbox.StageUnavailable {
			addLayer("sandbox", gogCLIReadinessStatusBlocked, "isolated durable-agent sandbox backend is unavailable")
			setFailure(gogCLIReadinessFailureSandbox, "install or enable the configured isolated sandbox backend before child wakes")
		} else {
			addLayer("sandbox", gogCLIReadinessStatusReady, "isolated durable-agent sandbox backend="+string(stage))
		}
	}

	if toolMaterialOK {
		if missing := firstMissingChildRuntimeMaterial(toolMaterial); missing != "" {
			addLayer("sandbox_material", gogCLIReadinessStatusBlocked, "runtime material missing: "+missing)
			setFailure(classifyGogCLIMaterialFailure(missing), "provide or correct the named child_runtime material without printing secret values")
		} else if !gogCLIToolMaterialBindsExecutable(toolMaterial) {
			addLayer("sandbox_material", gogCLIReadinessStatusBlocked, "child_runtime does not bind or declare gog_cli into /usr/local/bin")
			setFailure(gogCLIReadinessFailurePath, "bind gog_cli runtime material into the child sandbox PATH at /usr/local/bin/gog_cli")
		} else {
			addLayer("sandbox_material", gogCLIReadinessStatusReady, "child_runtime material sources exist and required env names are inherited by name")
		}
	}
	if accountMaterialOK {
		if missing := firstMissingChildRuntimeMaterial(accountMaterial); missing != "" {
			addLayer("account_material", gogCLIReadinessStatusBlocked, "runtime material missing: "+missing)
			setFailure(classifyGogCLIMaterialFailure(missing), "provide or correct the read-only account config material without reading token contents")
		} else {
			addLayer("account_material", gogCLIReadinessStatusReady, "external-account child_runtime material sources exist")
		}
	}

	runtimeBin := filepath.Join(filepath.Dir(strings.TrimSpace(workspaceRoot)), "runtime-bin")
	wrapperPath := filepath.Join(runtimeBin, "gog_cli")
	if wrapperEvidence, ok := gogCLIWrapperEvidence(wrapperPath); !ok {
		addLayer("wrapper", gogCLIReadinessStatusBlocked, wrapperEvidence)
		setFailure(gogCLIReadinessFailureWrapper, "materialize a deterministic gog_cli wrapper that execs a pinned gog binary and sets child-local XDG_CONFIG_HOME")
	} else {
		addLayer("wrapper", gogCLIReadinessStatusReady, wrapperEvidence)
	}

	configRoot := filepath.Join(workspaceRoot, filepath.FromSlash(gogCLIChildConfigRelativeRoot))
	configEvidence, configFailure := gogCLIConfigMetadataEvidence(configRoot)
	if configFailure != gogCLIReadinessFailureNone {
		addLayer("child_config_metadata", gogCLIReadinessStatusBlocked, configEvidence)
		setFailure(configFailure, "materialize read-only gogcli config/credential/keyring metadata into the child config root")
	} else {
		addLayer("child_config_metadata", gogCLIReadinessStatusReady, configEvidence)
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
	if runtimeState == nil || !strings.EqualFold(strings.TrimSpace(runtimeState.Adapter), gogCLIAdapterName) {
		return row
	}
	row.LastWake = &externalChannelAdapterWakeStatus{
		Status:       strings.TrimSpace(runtimeState.LastStatus),
		Error:        classifyAndRedactGogCLIWakeError(runtimeState.LastError),
		FailureCount: runtimeState.FailureCount,
		BackoffUntil: runtimeState.BackoffUntil,
	}
	if strings.EqualFold(strings.TrimSpace(runtimeState.LastStatus), "wake_blocked") && row.Status == gogCLIReadinessStatusReady {
		row.Status = gogCLIReadinessStatusResidual
		row.FailureCode = classifyGogCLIWakeFailure(runtimeState.LastError)
		row.NextRepair = "metadata readiness passes, but last wake was blocked; require a separately approved readiness/live check before mailbox polling"
	}
	return row
}

func selectGogCLIToolGrant(grants []session.CapabilityGrant, principalID string) (session.CapabilityGrant, core.ChildRuntimeContract, bool, string) {
	for _, grant := range grants {
		grant = session.NormalizeCapabilityGrant(grant)
		if strings.TrimSpace(grant.GrantedTo) != principalID || grant.Kind != session.CapabilityKindTool || !strings.EqualFold(strings.TrimSpace(grant.TargetResource), gogCLIAdapterName) {
			continue
		}
		if grant.Status != session.CapabilityGrantStatusActive || !grant.RevokedAt.IsZero() || (!grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(time.Now().UTC())) || strings.TrimSpace(grant.StaleReason) != "" {
			return grant, core.ChildRuntimeContract{}, false, fmt.Sprintf("grant=%s status=%s stale=%s", grant.GrantID, grant.Status, strings.TrimSpace(grant.StaleReason))
		}
		material, ok, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints)
		if err != nil {
			return grant, core.ChildRuntimeContract{}, false, "invalid child_runtime contract: " + err.Error()
		}
		if !ok {
			return grant, core.ChildRuntimeContract{}, false, "active tool grant has no child_runtime material"
		}
		if !containsReadinessString(grant.AllowedActions, "invoke") && !containsReadinessString(grant.AllowedActions, "connection_test") {
			return grant, material, false, "active tool grant does not allow invoke or connection_test"
		}
		if !containsReadinessString(material.EnvFromParent, gogCLIRequiredSecretEnvName) {
			return grant, material, false, "child_runtime env_from_parent does not include " + gogCLIRequiredSecretEnvName
		}
		return grant, material, true, fmt.Sprintf("grant=%s child_runtime=present env_from_parent=%s", grant.GrantID, gogCLIRequiredSecretEnvName)
	}
	return session.CapabilityGrant{}, core.ChildRuntimeContract{}, false, ""
}

func selectGogCLIAccountGrant(grants []session.CapabilityGrant, principalID string) (session.CapabilityGrant, core.ChildRuntimeContract, bool, string) {
	for _, grant := range grants {
		grant = session.NormalizeCapabilityGrant(grant)
		if strings.TrimSpace(grant.GrantedTo) != principalID || grant.Kind != session.CapabilityKindExternalAccount || !strings.HasPrefix(strings.TrimSpace(grant.TargetResource), gogCLIAdapterName+":") {
			continue
		}
		if grant.Status != session.CapabilityGrantStatusActive || !grant.RevokedAt.IsZero() || (!grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(time.Now().UTC())) || strings.TrimSpace(grant.StaleReason) != "" {
			return grant, core.ChildRuntimeContract{}, false, fmt.Sprintf("grant=%s status=%s stale=%s", grant.GrantID, grant.Status, strings.TrimSpace(grant.StaleReason))
		}
		material, ok, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints)
		if err != nil {
			return grant, core.ChildRuntimeContract{}, false, "invalid child_runtime contract: " + err.Error()
		}
		if !ok || (len(material.ReadonlyPaths) == 0 && len(material.ReadonlyBinds) == 0 && len(material.SecretBinds) == 0) {
			return grant, material, false, "active external-account grant has no read-only config material"
		}
		return grant, material, true, fmt.Sprintf("grant=%s child_runtime=config_material_present", grant.GrantID)
	}
	return session.CapabilityGrant{}, core.ChildRuntimeContract{}, false, ""
}

func gogCLIToolMaterialBindsExecutable(material core.ChildRuntimeContract) bool {
	material = core.NormalizeChildRuntimeContract(material)
	if strings.TrimSpace(material.Executable) != "" {
		return true
	}
	for _, bind := range material.ReadonlyBinds {
		if strings.TrimSpace(bind.Target) == "/usr/local/bin" || strings.TrimSpace(bind.Target) == gogCLIChildSandboxExecutable {
			return true
		}
	}
	return false
}

func gogCLIWrapperEvidence(wrapperPath string) (string, bool) {
	info, err := os.Stat(wrapperPath)
	if err != nil {
		return "gog_cli wrapper missing at child runtime-bin", false
	}
	if info.IsDir() {
		return "gog_cli wrapper path is a directory", false
	}
	sibling := filepath.Join(filepath.Dir(wrapperPath), "gog")
	if siblingInfo, err := os.Stat(sibling); err != nil || siblingInfo.IsDir() {
		return "pinned gog sibling binary missing beside wrapper", false
	}
	raw, err := os.ReadFile(wrapperPath)
	if err != nil {
		return "gog_cli wrapper metadata readable=false", false
	}
	text := string(raw)
	if !strings.Contains(text, "XDG_CONFIG_HOME") || !strings.Contains(text, "exec") || !strings.Contains(text, "gog") {
		return "gog_cli wrapper does not declare XDG_CONFIG_HOME and exec pinned gog", false
	}
	return fmt.Sprintf("wrapper_present=true mode=%#o pinned_gog_present=true", info.Mode().Perm()), true
}

func gogCLIConfigMetadataEvidence(configRoot string) (string, string) {
	info, err := os.Stat(configRoot)
	if err != nil {
		return "child gogcli config root missing", gogCLIReadinessFailureConfig
	}
	if !info.IsDir() {
		return "child gogcli config root is not a directory", gogCLIReadinessFailureConfig
	}
	configPath := filepath.Join(configRoot, "config.json")
	credentialsPath := filepath.Join(configRoot, "credentials.json")
	if info, err := os.Stat(configPath); err != nil || info.IsDir() {
		return "config.json metadata missing", gogCLIReadinessFailureConfig
	}
	if info, err := os.Stat(credentialsPath); err != nil || info.IsDir() {
		return "credentials.json metadata missing", gogCLIReadinessFailureCreds
	}
	keyringRoot := filepath.Join(configRoot, "keyring")
	entries, err := os.ReadDir(keyringRoot)
	if err != nil {
		return "keyring metadata missing", gogCLIReadinessFailureKeyring
	}
	files := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			files++
		}
	}
	if files == 0 {
		return "keyring metadata has zero files", gogCLIReadinessFailureKeyring
	}
	return fmt.Sprintf("config_root_present=true config_json=true credentials_json=true keyring_file_count=%d", files), gogCLIReadinessFailureNone
}

func classifyGogCLIMaterialFailure(missing string) string {
	missing = strings.ToLower(strings.TrimSpace(missing))
	switch {
	case strings.Contains(missing, "env_from_parent"):
		return gogCLIReadinessFailureSecret
	case strings.Contains(missing, "readonly_bind") || strings.Contains(missing, "executable"):
		return gogCLIReadinessFailurePath
	case strings.Contains(missing, "credential"):
		return gogCLIReadinessFailureCreds
	case strings.Contains(missing, "keyring") || strings.Contains(missing, "token"):
		return gogCLIReadinessFailureKeyring
	default:
		return gogCLIReadinessFailureGrant
	}
}

func classifyGogCLIWakeFailure(errText string) string {
	errText = strings.ToLower(strings.TrimSpace(errText))
	switch {
	case errText == "":
		return gogCLIReadinessFailureNone
	case strings.Contains(errText, "mail") || strings.Contains(errText, "inbox"):
		return gogCLIReadinessFailureLivePoll
	case strings.Contains(errText, "tty") || strings.Contains(errText, "passphrase") || strings.Contains(errText, "keyring"):
		return gogCLIReadinessFailureKeyring
	case strings.Contains(errText, "oauth") || strings.Contains(errText, "credential"):
		return gogCLIReadinessFailureCreds
	case strings.Contains(errText, "sandbox"):
		return gogCLIReadinessFailureSandbox
	case strings.Contains(errText, "binary") || strings.Contains(errText, "path") || strings.Contains(errText, "not found"):
		return gogCLIReadinessFailureBinary
	default:
		return gogCLIReadinessFailureLivePoll
	}
}

func classifyAndRedactGogCLIWakeError(errText string) string {
	errText = strings.TrimSpace(errText)
	if errText == "" {
		return ""
	}
	return redactDoctorText(errText)
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
