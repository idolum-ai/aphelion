//go:build linux

package tool

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func renderToolAuthorityHelp() string {
	return strings.Join([]string{
		"[TOOL_AUTHORITY]",
		"actions:",
		"- register | registered_show | registered_list",
		"- install_set | install_show | install_list | install_execute | rollback | uninstall",
		"- audit_run | audit_show | audit_list | probe_run | probe_show | probe_list",
		"- access_check",
	}, "\n")
}

func renderRegisteredTool(header string, record session.RegisteredTool) string {
	record = session.NormalizeRegisteredTool(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "registered: %t\n", record.Registered)
	if record.ImplementationRef != "" {
		fmt.Fprintf(&b, "implementation_ref: %s\n", record.ImplementationRef)
	}
	return b.String()
}

func renderRegisteredToolList(records []session.RegisteredTool) string {
	var b strings.Builder
	b.WriteString("[REGISTERED_TOOLS]\n")
	b.WriteString(fmt.Sprintf("count: %d\n", len(records)))
	if len(records) == 0 {
		b.WriteString("- (none)\n")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeRegisteredTool(record)
		fmt.Fprintf(
			&b,
			"- tool_name=%s registered=%t implementation_ref=%s\n",
			record.ToolName,
			record.Registered,
			firstNonEmpty(record.ImplementationRef, "-"),
		)
	}
	return b.String()
}

func renderRecordTraceability(b *strings.Builder, rationale string, refs []session.RecordReference) {
	rationale = strings.TrimSpace(rationale)
	if rationale != "" {
		fmt.Fprintf(b, "rationale: %s\n", rationale)
	}
	for _, ref := range session.NormalizeRecordReferences(refs) {
		fmt.Fprintf(b, "artifact_ref: %s %s", ref.Kind, ref.Ref)
		if strings.TrimSpace(ref.Label) != "" {
			fmt.Fprintf(b, " label=%s", ref.Label)
		}
		b.WriteString("\n")
	}
}

func renderToolProbeRecord(header string, record session.ToolProbeRecord) string {
	record = session.NormalizeToolProbeRecord(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(string(record.Status), "-"))
	fmt.Fprintf(&b, "probe_output: %s\n", firstNonEmpty(record.ProbeOutput, "-"))
	fmt.Fprintf(&b, "consecutive_failures: %d\n", record.ConsecutiveFailures)
	if fp := strings.TrimSpace(record.BaselineFingerprint); fp != "" {
		fmt.Fprintf(&b, "baseline_fingerprint: %s\n", fp)
	}
	if fp := strings.TrimSpace(record.CurrentFingerprint); fp != "" {
		fmt.Fprintf(&b, "current_fingerprint: %s\n", fp)
	}
	if hash := strings.TrimSpace(record.BaselineManifestHash); hash != "" {
		fmt.Fprintf(&b, "baseline_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentManifestHash); hash != "" {
		fmt.Fprintf(&b, "current_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.BaselineWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "baseline_workspace_fingerprint: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "current_workspace_fingerprint: %s\n", hash)
	}
	if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
		fmt.Fprintf(&b, "drift_source: %s\n", source)
	}
	if reason := strings.TrimSpace(record.StaleReason); reason != "" {
		fmt.Fprintf(&b, "stale_reason: %s\n", reason)
	}
	renderRecordTraceability(&b, record.Rationale, record.ArtifactRefs)
	if !record.ProbedAt.IsZero() {
		fmt.Fprintf(&b, "probed_at: %s\n", record.ProbedAt.UTC().Format(time.RFC3339))
	}
	if !record.LastFailureAt.IsZero() {
		fmt.Fprintf(&b, "last_failure_at: %s\n", record.LastFailureAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", record.UpdatedAt.UTC().Format(time.RFC3339))
	return strings.TrimRight(b.String(), "\n")
}

func renderToolProbeRecordList(records []session.ToolProbeRecord) string {
	var b strings.Builder
	b.WriteString("[TOOL_PROBES]")
	if len(records) == 0 {
		b.WriteString("\n- (none)")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeToolProbeRecord(record)
		b.WriteString("\n- ")
		b.WriteString(record.ToolName)
		b.WriteString(" status=")
		b.WriteString(firstNonEmpty(string(record.Status), "-"))
		if why := strings.TrimSpace(record.Rationale); why != "" {
			b.WriteString(" why=")
			b.WriteString(why)
		}
		if refs := len(session.NormalizeRecordReferences(record.ArtifactRefs)); refs > 0 {
			b.WriteString(" refs=")
			b.WriteString(strconv.Itoa(refs))
		}
		if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
			b.WriteString(" drift_source=")
			b.WriteString(source)
		}
	}
	return b.String()
}

func renderToolAuditRecord(header string, record session.ToolAuditRecord) string {
	record = session.NormalizeToolAuditRecord(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(string(record.Status), "-"))
	fmt.Fprintf(&b, "audit_output: %s\n", firstNonEmpty(record.AuditOutput, "-"))
	fmt.Fprintf(&b, "consecutive_failures: %d\n", record.ConsecutiveFailures)
	if fp := strings.TrimSpace(record.BaselineFingerprint); fp != "" {
		fmt.Fprintf(&b, "baseline_fingerprint: %s\n", fp)
	}
	if fp := strings.TrimSpace(record.CurrentFingerprint); fp != "" {
		fmt.Fprintf(&b, "current_fingerprint: %s\n", fp)
	}
	if hash := strings.TrimSpace(record.BaselineManifestHash); hash != "" {
		fmt.Fprintf(&b, "baseline_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentManifestHash); hash != "" {
		fmt.Fprintf(&b, "current_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.BaselineWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "baseline_workspace_fingerprint: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "current_workspace_fingerprint: %s\n", hash)
	}
	if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
		fmt.Fprintf(&b, "drift_source: %s\n", source)
	}
	if reason := strings.TrimSpace(record.StaleReason); reason != "" {
		fmt.Fprintf(&b, "stale_reason: %s\n", reason)
	}
	renderRecordTraceability(&b, record.Rationale, record.ArtifactRefs)
	if !record.AuditedAt.IsZero() {
		fmt.Fprintf(&b, "audited_at: %s\n", record.AuditedAt.UTC().Format(time.RFC3339))
	}
	if !record.LastFailureAt.IsZero() {
		fmt.Fprintf(&b, "last_failure_at: %s\n", record.LastFailureAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", record.UpdatedAt.UTC().Format(time.RFC3339))
	return strings.TrimRight(b.String(), "\n")
}

func renderToolAuditRecordList(records []session.ToolAuditRecord) string {
	var b strings.Builder
	b.WriteString("[TOOL_AUDITS]")
	if len(records) == 0 {
		b.WriteString("\n- (none)")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeToolAuditRecord(record)
		b.WriteString("\n- ")
		b.WriteString(record.ToolName)
		b.WriteString(" status=")
		b.WriteString(firstNonEmpty(string(record.Status), "-"))
		if why := strings.TrimSpace(record.Rationale); why != "" {
			b.WriteString(" why=")
			b.WriteString(why)
		}
		if reason := strings.TrimSpace(record.StaleReason); reason != "" {
			b.WriteString(" stale_reason=")
			b.WriteString(reason)
		}
		if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
			b.WriteString(" drift_source=")
			b.WriteString(source)
		}
		if refs := len(session.NormalizeRecordReferences(record.ArtifactRefs)); refs > 0 {
			b.WriteString(" refs=")
			b.WriteString(strconv.Itoa(refs))
		}
	}
	return b.String()
}

func renderToolInstallRecord(header string, record session.ToolInstallRecord) string {
	record = session.NormalizeToolInstallRecord(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(string(record.Status), "-"))
	fmt.Fprintf(&b, "installer: %s\n", firstNonEmpty(record.Installer, "-"))
	fmt.Fprintf(&b, "install_ref: %s\n", firstNonEmpty(record.InstallRef, "-"))
	fmt.Fprintf(&b, "probe_status: %s\n", firstNonEmpty(string(record.ProbeStatus), "-"))
	fmt.Fprintf(&b, "probe_output: %s\n", firstNonEmpty(record.ProbeOutput, "-"))
	fmt.Fprintf(&b, "consecutive_failures: %d\n", record.ConsecutiveFailures)
	if fp := strings.TrimSpace(record.BaselineFingerprint); fp != "" {
		fmt.Fprintf(&b, "baseline_fingerprint: %s\n", fp)
	}
	if fp := strings.TrimSpace(record.CurrentFingerprint); fp != "" {
		fmt.Fprintf(&b, "current_fingerprint: %s\n", fp)
	}
	if hash := strings.TrimSpace(record.BaselineManifestHash); hash != "" {
		fmt.Fprintf(&b, "baseline_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentManifestHash); hash != "" {
		fmt.Fprintf(&b, "current_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.BaselineWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "baseline_workspace_fingerprint: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "current_workspace_fingerprint: %s\n", hash)
	}
	if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
		fmt.Fprintf(&b, "drift_source: %s\n", source)
	}
	if reason := strings.TrimSpace(record.StaleReason); reason != "" {
		fmt.Fprintf(&b, "stale_reason: %s\n", reason)
	}
	renderRecordTraceability(&b, record.Rationale, record.ArtifactRefs)
	if !record.InstalledAt.IsZero() {
		fmt.Fprintf(&b, "installed_at: %s\n", record.InstalledAt.UTC().Format(time.RFC3339))
	}
	if !record.LastProbedAt.IsZero() {
		fmt.Fprintf(&b, "last_probed_at: %s\n", record.LastProbedAt.UTC().Format(time.RFC3339))
	}
	if !record.AttestedAt.IsZero() {
		fmt.Fprintf(&b, "attested_at: %s\n", record.AttestedAt.UTC().Format(time.RFC3339))
	}
	if !record.LastFailureAt.IsZero() {
		fmt.Fprintf(&b, "last_failure_at: %s\n", record.LastFailureAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", record.UpdatedAt.UTC().Format(time.RFC3339))
	return strings.TrimRight(b.String(), "\n")
}

func renderToolInstallRecordList(records []session.ToolInstallRecord) string {
	var b strings.Builder
	b.WriteString("[TOOL_INSTALLS]")
	if len(records) == 0 {
		b.WriteString("\n- (none)")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeToolInstallRecord(record)
		b.WriteString("\n- ")
		b.WriteString(record.ToolName)
		b.WriteString(" status=")
		b.WriteString(firstNonEmpty(string(record.Status), "-"))
		if strings.TrimSpace(record.InstallRef) != "" {
			b.WriteString(" install_ref=")
			b.WriteString(record.InstallRef)
		}
		if strings.TrimSpace(string(record.ProbeStatus)) != "" {
			b.WriteString(" probe_status=")
			b.WriteString(string(record.ProbeStatus))
		}
		if reason := strings.TrimSpace(record.StaleReason); reason != "" {
			b.WriteString(" stale_reason=")
			b.WriteString(reason)
		}
		if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
			b.WriteString(" drift_source=")
			b.WriteString(source)
		}
		if why := strings.TrimSpace(record.Rationale); why != "" {
			b.WriteString(" why=")
			b.WriteString(why)
		}
		if refs := len(session.NormalizeRecordReferences(record.ArtifactRefs)); refs > 0 {
			b.WriteString(" refs=")
			b.WriteString(strconv.Itoa(refs))
		}
	}
	return b.String()
}
