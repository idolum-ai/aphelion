//go:build linux

package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) toolAuthorityAuditRun(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority audit_run requires tool_name")
	}
	manifest, ok := r.externalManifestByName(toolName)
	if !ok {
		return "", fmt.Errorf("tool_authority audit_run requires an external tool manifest-backed tool_name")
	}
	installRecord, exists, err := r.store.ToolInstallRecord(manifest.Name)
	if err != nil {
		return "", err
	} else if !exists {
		return "", fmt.Errorf("external tool %q requires an install record before audit_run", manifest.Name)
	}
	prevAudit, prevAuditExists, err := r.store.ToolAuditRecord(manifest.Name)
	if err != nil {
		return "", err
	}
	output, fingerprint, err := r.runExternalManifestAudit(ctx, manifest, scope, installRecord.InstallRef)
	now := time.Now().UTC()
	record := session.ToolAuditRecord{ToolName: manifest.Name, AuditOutput: output, UpdatedAt: now, AuditedAt: now, ArtifactRefs: auditOutputArtifactRefs(output)}
	setAuditRecordCurrentAnchors(&record, fingerprint)
	if prevAuditExists {
		record.CreatedAt = prevAudit.CreatedAt
	}
	if err != nil {
		record.Status = session.ToolAuditStatusFailed
		record.Rationale = "audit_run could not resolve the declared execution entry"
		record.DriftSource = session.ToolDriftSourceAuditFailure
		if isExternalPolicyViolation(err) {
			record.Rationale = "audit_run failed due to policy_violation"
			record.DriftSource = session.ToolDriftSourcePolicyViolation
		}
		record.ConsecutiveFailures = prevAudit.ConsecutiveFailures + 1
		record.LastFailureAt = now
		stored, saveErr := r.store.UpsertToolAuditRecord(record)
		if saveErr != nil {
			return "", saveErr
		}
		if installRecord.Status == session.ToolInstallStatusVerified {
			installRecord.Status = session.ToolInstallStatusStale
			installRecord.AttestedAt = time.Time{}
			installRecord.StaleReason = string(record.DriftSource) + ": " + err.Error()
			installRecord.DriftSource = record.DriftSource
			installRecord.UpdatedAt = now
			if _, updateErr := r.store.UpsertToolInstallRecord(installRecord); updateErr != nil {
				return "", updateErr
			}
		}
		if eventErr := r.appendToolAuthorityEvent(key, core.ExecutionEventToolAuditUpdated, string(stored.Status), map[string]any{"tool_name": stored.ToolName, "status": string(stored.Status), "actor_role": strings.TrimSpace(string(actor.Role)), "actor_user_id": actor.TelegramUserID}); eventErr != nil {
			warnDroppedEvidenceWrite("tool_authority.audit_run.failure_event", eventErr)
		}
		return "", err
	}
	record.Status = session.ToolAuditStatusPassed
	if manifest.Execution.Mode == "container" {
		record.Rationale = "audit_run resolved the declared container image and health check"
	} else {
		record.Rationale = "audit_run resolved the declared execution entry"
	}
	setAuditRecordBaselineAnchors(&record, fingerprint)
	record.ConsecutiveFailures = 0
	record.LastFailureAt = time.Time{}
	stored, err := r.store.UpsertToolAuditRecord(record)
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(key, core.ExecutionEventToolAuditUpdated, string(stored.Status), map[string]any{"tool_name": stored.ToolName, "status": string(stored.Status), "actor_role": strings.TrimSpace(string(actor.Role)), "actor_user_id": actor.TelegramUserID}); err != nil {
		return "", err
	}
	return renderToolAuditRecord("[TOOL_AUDIT]", stored), nil
}

func (r *Registry) toolAuthorityAuditShow(in toolAuthorityInput, scope sandbox.Scope) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority audit_show requires tool_name")
	}
	if manifest, ok := r.externalManifestByName(toolName); ok {
		if _, _, err := r.refreshExternalToolDrift(manifest, scope); err != nil {
			return "", err
		}
	}
	record, ok, err := r.store.ToolAuditRecord(toolName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool audit record %q not found", toolName)
	}
	return renderToolAuditRecord("[TOOL_AUDIT]", record), nil
}

func (r *Registry) toolAuthorityAuditList(in toolAuthorityInput) (string, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	records, err := r.store.ToolAuditRecords("", limit)
	if err != nil {
		return "", err
	}
	return renderToolAuditRecordList(records), nil
}

func (r *Registry) runExternalManifestAudit(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope, installRef string) (string, externalToolFingerprintSet, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if manifest.Execution.Mode == "container" {
		return r.runExternalContainerManifestAudit(ctx, manifest, scope, installRef)
	}
	if err := validateExternalProcessPolicy(manifest); err != nil {
		return "", externalToolFingerprintSet{}, err
	}
	workdir, err := resolveWorkdir(scope.WorkingRoot, manifest.Execution.Workdir)
	if err != nil {
		return "", externalToolFingerprintSet{}, err
	}
	entry := strings.TrimSpace(manifest.Execution.Entry)
	if entry == "" {
		return "", externalToolFingerprintSet{}, fmt.Errorf("external tool %q execution entry is empty", manifest.Name)
	}
	firstToken := strings.Fields(entry)
	if len(firstToken) == 0 {
		return "", externalToolFingerprintSet{}, fmt.Errorf("external tool %q execution entry is empty", manifest.Name)
	}
	target := firstToken[0]
	output := ""
	if strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "/") {
		resolved := target
		if !strings.HasPrefix(target, "/") {
			resolved = filepath.Join(workdir, target)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit failed: entry path does not exist", manifest.Name)
			}
			return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit stat failed: %w", manifest.Name, err)
		}
		if info.IsDir() {
			return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit failed: entry path is a directory", manifest.Name)
		}
		if info.Mode().Perm()&0o111 == 0 {
			return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit failed: entry path is not executable", manifest.Name)
		}
		if err := r.auditExternalLocalEntryLoadability(ctx, manifest, scope, resolved, workdir); err != nil {
			return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, err
		}
		output = fmt.Sprintf("entry_path: %s", resolved)
	} else {
		if _, err := exec.LookPath(target); err != nil {
			return fmt.Sprintf("entry_command: %s", target), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit failed: command %q is not on PATH", manifest.Name, target)
		}
		output = fmt.Sprintf("entry_command: %s", target)
	}
	if len(manifest.Audit.Command) > 0 {
		auditOutput, err := r.runExternalManifestCommand(ctx, manifest, manifest.Audit.Command, scope)
		if err != nil {
			return output, externalToolFingerprintSet{}, err
		}
		if expected := strings.TrimSpace(manifest.Audit.ExpectedOutputContains); expected != "" && !strings.Contains(auditOutput, expected) {
			return output, externalToolFingerprintSet{}, fmt.Errorf("external tool %q audit output did not contain expected text %q", manifest.Name, expected)
		}
		if strings.TrimSpace(auditOutput) != "" {
			output = output + "\naudit_output: " + strings.TrimSpace(auditOutput)
		}
	}
	fingerprint, err := externalToolFingerprints(manifest, scope.WorkingRoot, installRef)
	if err != nil {
		return output, externalToolFingerprintSet{}, err
	}
	return output, fingerprint, nil
}

func (r *Registry) runExternalContainerManifestAudit(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope, installRef string) (string, externalToolFingerprintSet, error) {
	image := strings.TrimSpace(firstNonEmpty(manifest.Container.Image, manifest.Execution.Entry))
	if image == "" {
		return "container_image: -", externalToolFingerprintSet{}, fmt.Errorf("external tool %q container audit failed: container image is required", manifest.Name)
	}
	if strings.TrimSpace(manifest.Container.Digest) == "" && strings.TrimSpace(manifest.Container.BuildRef) == "" {
		return "container_image: " + image, externalToolFingerprintSet{}, fmt.Errorf("external tool %q container audit failed: digest or build_ref is required", manifest.Name)
	}
	output := "container_image: " + image
	if strings.TrimSpace(manifest.Container.Digest) != "" {
		output += "\ncontainer_digest: " + strings.TrimSpace(manifest.Container.Digest)
	}
	if strings.TrimSpace(manifest.Container.BuildRef) != "" {
		output += "\ncontainer_build_ref: " + strings.TrimSpace(manifest.Container.BuildRef)
	}
	if len(manifest.Container.Healthcheck.Command) > 0 {
		healthOutput, err := r.runExternalManifestCommand(ctx, manifest, manifest.Container.Healthcheck.Command, scope)
		if err != nil {
			return output, externalToolFingerprintSet{}, fmt.Errorf("external tool %q container health check failed: %w", manifest.Name, err)
		}
		if expected := strings.TrimSpace(manifest.Container.Healthcheck.ExpectedOutputContains); expected != "" && !strings.Contains(healthOutput, expected) {
			return output, externalToolFingerprintSet{}, fmt.Errorf("external tool %q container health check output did not contain expected text %q", manifest.Name, expected)
		}
		if strings.TrimSpace(healthOutput) != "" {
			output += "\nhealthcheck_output: " + strings.TrimSpace(healthOutput)
		}
	}
	fingerprint, err := externalToolFingerprints(manifest, scope.WorkingRoot, installRef)
	if err != nil {
		return output, externalToolFingerprintSet{}, err
	}
	return output, fingerprint, nil
}

func (r *Registry) auditExternalLocalEntryLoadability(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope, entryPath string, workdir string) error {
	interpreter, kind, err := discoverExternalEntryInterpreter(entryPath)
	if err != nil {
		return fmt.Errorf("external tool %q import audit failed: %w", manifest.Name, err)
	}
	switch kind {
	case "shell":
		return r.runExternalAuditCheck(ctx, manifest, scope, workdir, []string{firstNonEmpty(interpreter, "bash"), "-n", entryPath})
	case "python":
		return r.runExternalAuditCheck(ctx, manifest, scope, workdir, []string{firstNonEmpty(interpreter, "python3"), "-m", "py_compile", entryPath})
	default:
		return nil
	}
}

func (r *Registry) runExternalAuditCheck(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope, workdir string, command []string) error {
	timeout := 10 * time.Second
	if manifest.Constraints.MaxRuntimeSeconds > 0 && time.Duration(manifest.Constraints.MaxRuntimeSeconds)*time.Second < timeout {
		timeout = time.Duration(manifest.Constraints.MaxRuntimeSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, runErr := r.runCommand(runCtx, scope, shellQuoteCommand(command), workdir)
	if runErr != nil {
		return fmt.Errorf("external tool %q import audit loadability check failed: %s", manifest.Name, renderOutput(stdout, stderr, r.maxOutputBytes))
	}
	return nil
}

func discoverExternalEntryInterpreter(path string) (string, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	firstLine := ""
	if idx := strings.IndexByte(string(raw), '\n'); idx >= 0 {
		firstLine = string(raw[:idx])
	} else {
		firstLine = string(raw)
	}
	interpreter := ""
	if strings.HasPrefix(firstLine, "#!") {
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(firstLine, "#!")))
		if len(fields) > 0 {
			interpreter = resolveShebangInterpreter(fields)
		}
	}
	ext := strings.ToLower(filepath.Ext(path))
	name := strings.ToLower(filepath.Base(interpreter))
	if interpreter != "" {
		if strings.Contains(interpreter, "/") {
			if _, err := os.Stat(interpreter); err != nil {
				return "", "", fmt.Errorf("interpreter %q is not available: %w", interpreter, err)
			}
		} else if _, err := exec.LookPath(interpreter); err != nil {
			return "", "", fmt.Errorf("interpreter %q is not on PATH", interpreter)
		}
	}
	switch {
	case strings.Contains(name, "bash"), strings.Contains(name, "sh"), ext == ".sh":
		if interpreter == "" {
			interpreter = "bash"
		}
		if _, err := exec.LookPath(interpreter); err != nil && !strings.Contains(interpreter, "/") {
			return "", "", fmt.Errorf("interpreter %q is not on PATH", interpreter)
		}
		return interpreter, "shell", nil
	case strings.Contains(name, "python"), ext == ".py":
		if interpreter == "" {
			interpreter = "python3"
		}
		if _, err := exec.LookPath(interpreter); err != nil && !strings.Contains(interpreter, "/") {
			return "", "", fmt.Errorf("interpreter %q is not on PATH", interpreter)
		}
		return interpreter, "python", nil
	default:
		return interpreter, "", nil
	}
}

func resolveShebangInterpreter(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	first := strings.TrimSpace(fields[0])
	if strings.HasSuffix(first, "/env") || first == "env" {
		for _, field := range fields[1:] {
			field = strings.TrimSpace(field)
			if field == "" || strings.HasPrefix(field, "-") {
				continue
			}
			return field
		}
		return ""
	}
	return first
}
