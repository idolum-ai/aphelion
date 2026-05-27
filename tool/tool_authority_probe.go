//go:build linux

package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) toolAuthorityProbeRun(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority probe_run requires tool_name")
	}
	manifest, ok := r.externalManifestByName(toolName)
	if !ok {
		return "", fmt.Errorf("tool_authority probe_run requires an external tool manifest-backed tool_name")
	}
	manifest = NormalizeExternalToolManifest(manifest)
	if len(manifest.Probe.Command) == 0 {
		return "", fmt.Errorf("external tool %q does not declare a probe command", manifest.Name)
	}
	record, exists, err := r.store.ToolInstallRecord(manifest.Name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("external tool %q requires an install record before probe_run", manifest.Name)
	}
	prevProbe, _, err := r.store.ToolProbeRecord(manifest.Name)
	if err != nil {
		return "", err
	}
	fingerprint, fingerprintErr := externalToolFingerprints(manifest, scope.WorkingRoot, record.InstallRef)
	probeOutput := ""
	if fingerprintErr != nil {
		err = fingerprintErr
	} else {
		probeOutput, err = r.runExternalManifestProbe(ctx, manifest, scope)
	}
	now := time.Now().UTC()
	probeRefs := r.manifestCommandArtifactRefs(manifest, manifest.Probe.Command, "probe command")
	record.ProbeOutput = probeOutput
	record.LastProbedAt = now
	record.ArtifactRefs = probeRefs
	if err != nil {
		record.ProbeStatus = session.ToolProbeStatusFailed
		record.Rationale = "probe_run failed against the declared probe command"
		driftSource := session.ToolDriftSourceProbeFailure
		if fingerprintErr != nil {
			driftSource = session.ToolDriftSourceFingerprintError
		}
		if isExternalPolicyViolation(err) {
			driftSource = session.ToolDriftSourcePolicyViolation
			record.Rationale = "probe_run failed due to policy_violation"
		}
		consecutiveFailures := prevProbe.ConsecutiveFailures + 1
		probeRecord := session.ToolProbeRecord{ToolName: manifest.Name, Status: session.ToolProbeStatusFailed, ProbeOutput: probeOutput, Rationale: record.Rationale, ArtifactRefs: probeRefs, ProbedAt: now, ConsecutiveFailures: consecutiveFailures, LastFailureAt: now, DriftSource: driftSource, StaleReason: err.Error()}
		setProbeRecordCurrentAnchors(&probeRecord, fingerprint)
		if _, saveProbeErr := r.store.UpsertToolProbeRecord(probeRecord); saveProbeErr != nil {
			return "", saveProbeErr
		}
		if record.Status == session.ToolInstallStatusVerified {
			if consecutiveFailures >= 3 {
				record.Status = session.ToolInstallStatusFailed
			} else {
				record.Status = session.ToolInstallStatusStale
			}
			record.StaleReason = string(driftSource) + ": " + err.Error()
			record.DriftSource = driftSource
		} else if record.Status == session.ToolInstallStatusStale && consecutiveFailures >= 3 {
			record.Status = session.ToolInstallStatusFailed
		} else {
			record.Status = session.ToolInstallStatusFailed
		}
		record.AttestedAt = time.Time{}
		record.UpdatedAt = now
		stored, saveErr := r.store.UpsertToolInstallRecord(record)
		if saveErr != nil {
			return "", saveErr
		}
		if eventErr := r.appendToolAuthorityEvent(key, core.ExecutionEventToolInstallUpdated, string(stored.Status), map[string]any{
			"tool_name":     stored.ToolName,
			"status":        string(stored.Status),
			"probe_status":  string(stored.ProbeStatus),
			"install_ref":   stored.InstallRef,
			"actor_role":    strings.TrimSpace(string(actor.Role)),
			"actor_user_id": actor.TelegramUserID,
		}); eventErr != nil {
			warnDroppedEvidenceWrite("tool_authority.probe_run.failure_event", eventErr)
		}
		return "", err
	}
	record.ProbeStatus = session.ToolProbeStatusPassed
	record.Rationale = "probe_run passed against the declared probe command"
	probeRecord := session.ToolProbeRecord{ToolName: manifest.Name, Status: session.ToolProbeStatusPassed, ProbeOutput: probeOutput, Rationale: "probe_run passed against the declared probe command", ArtifactRefs: probeRefs, ProbedAt: now, ConsecutiveFailures: 0}
	setProbeRecordBaselineAnchors(&probeRecord, fingerprint)
	if _, err := r.store.UpsertToolProbeRecord(probeRecord); err != nil {
		return "", err
	}
	record.UpdatedAt = now
	stored, err := r.store.UpsertToolInstallRecord(record)
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(key, core.ExecutionEventToolInstallUpdated, string(stored.Status), map[string]any{
		"tool_name":     stored.ToolName,
		"status":        string(stored.Status),
		"probe_status":  string(stored.ProbeStatus),
		"install_ref":   stored.InstallRef,
		"actor_role":    strings.TrimSpace(string(actor.Role)),
		"actor_user_id": actor.TelegramUserID,
	}); err != nil {
		return "", err
	}
	return renderToolInstallRecord("[TOOL_INSTALL]", stored), nil
}

func (r *Registry) runExternalManifestCommand(ctx context.Context, manifest ExternalToolManifest, command []string, scope sandbox.Scope) (string, error) {
	return r.runExternalManifestLifecycleCommand(ctx, manifest, command, scope, "install execution")
}

func (r *Registry) runExternalManifestLifecycleCommand(ctx context.Context, manifest ExternalToolManifest, command []string, scope sandbox.Scope, label string) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if len(command) == 0 {
		return "", fmt.Errorf("external tool %q does not declare a command", manifest.Name)
	}
	label = firstNonEmpty(strings.TrimSpace(label), "command execution")
	if err := validateExternalProcessPolicy(manifest); err != nil {
		return "", err
	}
	workdir, err := resolveWorkdir(scope.WorkingRoot, manifest.Execution.Workdir)
	if err != nil {
		return "", err
	}
	timeout := defaultTimeout(15 * time.Second)
	if manifest.Execution.TimeoutSeconds > 0 {
		timeout = time.Duration(manifest.Execution.TimeoutSeconds) * time.Second
	}
	if manifest.Constraints.MaxRuntimeSeconds > 0 {
		constraintTimeout := time.Duration(manifest.Constraints.MaxRuntimeSeconds) * time.Second
		if timeout <= 0 || constraintTimeout < timeout {
			timeout = constraintTimeout
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, runErr := r.runCommand(runCtx, scope, shellQuoteCommand(command), workdir)
	output := renderOutput(stdout, stderr, r.maxOutputBytes)
	if runErr != nil {
		return output, fmt.Errorf("external tool %q %s failed: %s", manifest.Name, label, output)
	}
	return output, nil
}

func shellQuoteCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func (r *Registry) runExternalManifestProbe(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if len(manifest.Probe.Command) == 0 {
		return "", fmt.Errorf("external tool %q does not declare a probe command", manifest.Name)
	}
	output, runErr := r.runExternalManifestCommand(ctx, manifest, manifest.Probe.Command, scope)
	if runErr != nil {
		return output, fmt.Errorf("external tool %q probe execution failed: %s", manifest.Name, output)
	}
	if expected := strings.TrimSpace(manifest.Probe.ExpectedOutputContains); expected != "" {
		if !strings.Contains(output, expected) {
			return output, fmt.Errorf("external tool %q probe output did not contain expected text %q", manifest.Name, expected)
		}
	}
	return output, nil
}

func (r *Registry) toolAuthorityProbeShow(in toolAuthorityInput) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority probe_show requires tool_name")
	}
	record, ok, err := r.store.ToolProbeRecord(toolName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool probe record %q not found", toolName)
	}
	return renderToolProbeRecord("[TOOL_PROBE]", record), nil
}

func (r *Registry) toolAuthorityProbeList(in toolAuthorityInput) (string, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	status := session.NormalizeToolProbeStatus(session.ToolProbeStatus(in.ProbeStatus))
	if strings.TrimSpace(in.ProbeStatus) != "" && status == "" {
		return "", fmt.Errorf("tool_authority probe_list probe_status must be passed or failed")
	}
	records, err := r.store.ToolProbeRecords(status, limit)
	if err != nil {
		return "", err
	}
	return renderToolProbeRecordList(records), nil
}

func (r *Registry) toolAuthorityAccessCheck(in toolAuthorityInput) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	principalID := strings.TrimSpace(in.Principal)
	if toolName == "" || principalID == "" {
		return "", fmt.Errorf("tool_authority access_check requires tool_name and principal")
	}
	registered, registeredOK, err := r.store.RegisteredTool(toolName)
	if err != nil {
		return "", err
	}
	grant, grantOK, err := r.store.ActiveCapabilityGrant(session.CapabilityKindTool, toolName, principalID, "invoke")
	if err != nil {
		return "", err
	}
	allowed := registeredOK && registered.Registered && grantOK
	var b strings.Builder
	b.WriteString("[TOOL_ACCESS]\n")
	fmt.Fprintf(&b, "tool_name: %s\n", toolName)
	fmt.Fprintf(&b, "principal: %s\n", principalID)
	fmt.Fprintf(&b, "registered: %t\n", registeredOK && registered.Registered)
	fmt.Fprintf(&b, "capability_grant_active: %t\n", grantOK)
	if grantOK {
		fmt.Fprintf(&b, "capability_grant_id: %s\n", grant.GrantID)
	}
	fmt.Fprintf(&b, "allowed: %t\n", allowed)
	return b.String(), nil
}
