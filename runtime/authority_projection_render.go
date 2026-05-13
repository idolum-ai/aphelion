//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (p authorityProjection) snapshot() core.AuthorityStatusSnapshot {
	status := "healthy"
	if len(p.Findings) > 0 || p.TruncatedCapabilitySet {
		status = "needs_attention"
	}
	out := core.AuthorityStatusSnapshot{
		GeneratedAt:            p.GeneratedAt,
		Status:                 status,
		ContinuationRecords:    p.ContinuationRecords,
		OperationRecords:       p.OperationRecords,
		PendingDecisions:       p.PendingDecisions,
		AutoApprovalLeases:     p.AutoApprovalLeases,
		CapabilityGrants:       p.CapabilityGrants,
		ActiveProposals:        p.ActiveProposals,
		ActiveLeases:           p.ActiveLeases,
		ActivePlanLeases:       p.ActivePlanLeases,
		FindingCount:           len(p.Findings),
		Findings:               make([]core.AuthorityFindingSnapshot, 0, len(p.Findings)),
		TruncatedCapabilitySet: p.TruncatedCapabilitySet,
	}
	for _, finding := range p.Findings {
		switch strings.ToLower(strings.TrimSpace(finding.Severity)) {
		case "error":
			out.ErrorCount++
		case "warning":
			out.WarningCount++
		}
		out.Findings = append(out.Findings, core.AuthorityFindingSnapshot{
			FindingID:       finding.FindingID,
			Code:            finding.Code,
			Severity:        finding.Severity,
			SourceKind:      finding.SourceKind,
			SourceID:        finding.SourceID,
			SessionID:       finding.SessionID,
			ChatID:          finding.ChatID,
			Detail:          finding.Detail,
			SuggestedRepair: finding.SuggestedRepair,
			ApplyAction:     finding.ApplyAction,
			ApplyScope:      finding.ApplyScope,
			Applicable:      finding.Applicable,
		})
	}
	return out
}

func (r *Runtime) writeDoctorAuthorityProjection(b *strings.Builder, now time.Time) {
	projection, err := r.authorityProjection(now)
	if err != nil {
		writeDoctorKV(b, "authority_projection_error", err.Error())
		return
	}
	status := "healthy"
	if len(projection.Findings) > 0 || projection.TruncatedCapabilitySet {
		status = "needs_attention"
	}
	writeDoctorKV(b, "authority_projection_status", status)
	writeDoctorKV(b, "authority_projection_generated_at", projection.GeneratedAt.Format(time.RFC3339))
	writeDoctorKV(b, "authority_continuation_records", fmt.Sprintf("%d", projection.ContinuationRecords))
	writeDoctorKV(b, "authority_operation_records", fmt.Sprintf("%d", projection.OperationRecords))
	writeDoctorKV(b, "authority_pending_decisions", fmt.Sprintf("%d", projection.PendingDecisions))
	writeDoctorKV(b, "authority_autoapproval_active_leases", fmt.Sprintf("%d", projection.AutoApprovalLeases))
	writeDoctorKV(b, "authority_capability_active_grants", fmt.Sprintf("%d", projection.CapabilityGrants))
	writeDoctorKV(b, "authority_active_proposals", fmt.Sprintf("%d", projection.ActiveProposals))
	writeDoctorKV(b, "authority_active_leases", fmt.Sprintf("%d", projection.ActiveLeases))
	writeDoctorKV(b, "authority_active_plan_leases", fmt.Sprintf("%d", projection.ActivePlanLeases))
	writeDoctorKV(b, "authority_finding_count", fmt.Sprintf("%d", len(projection.Findings)))
	if projection.TruncatedCapabilitySet {
		writeDoctorKV(b, "authority_capability_projection_truncated", "true")
	}
	writeDoctorLine(b, "authority_findings:")
	if len(projection.Findings) == 0 {
		writeDoctorLine(b, "- none")
		return
	}
	for _, finding := range projection.Findings {
		parts := []string{
			"finding_id=" + quoteDoctorToken(finding.FindingID),
			"code=" + quoteDoctorToken(finding.Code),
			"severity=" + quoteDoctorToken(finding.Severity),
			"source=" + quoteDoctorToken(firstNonEmpty(finding.SourceKind, "unknown")+":"+firstNonEmpty(finding.SourceID, "unknown")),
		}
		if finding.SessionID != "" {
			parts = append(parts, "session_id="+quoteDoctorToken(finding.SessionID))
		}
		if finding.ChatID != 0 {
			parts = append(parts, fmt.Sprintf("chat_id=%d", finding.ChatID))
		}
		if finding.Detail != "" {
			parts = append(parts, "detail="+quoteDoctorToken(finding.Detail))
		}
		if finding.SuggestedRepair != "" {
			parts = append(parts, "suggested_repair="+quoteDoctorToken(finding.SuggestedRepair))
		}
		if finding.ApplyAction != "" {
			parts = append(parts, "apply_action="+quoteDoctorToken(finding.ApplyAction))
		}
		if finding.ApplyScope != "" {
			parts = append(parts, "apply_scope="+quoteDoctorToken(finding.ApplyScope))
		}
		if finding.Applicable {
			parts = append(parts, "applicable=true")
		}
		writeDoctorLine(b, "- "+strings.Join(parts, " "))
	}
}

func quoteDoctorToken(value string) string {
	return fmt.Sprintf("%q", strings.TrimSpace(value))
}

func (p *authorityProjection) addFinding(finding authorityProjectionFinding) {
	if p == nil {
		return
	}
	finding.FindingID = strings.TrimSpace(finding.FindingID)
	finding.Code = strings.TrimSpace(finding.Code)
	finding.Severity = strings.TrimSpace(finding.Severity)
	finding.SourceKind = strings.TrimSpace(finding.SourceKind)
	finding.SourceID = strings.TrimSpace(finding.SourceID)
	finding.SessionID = strings.TrimSpace(finding.SessionID)
	finding.Detail = strings.TrimSpace(finding.Detail)
	finding.SuggestedRepair = strings.TrimSpace(finding.SuggestedRepair)
	finding.ApplyAction = strings.TrimSpace(finding.ApplyAction)
	finding.ApplyScope = strings.TrimSpace(finding.ApplyScope)
	if !finding.Applicable {
		finding.ApplyAction = ""
		finding.ApplyScope = ""
	}
	if finding.Code == "" || finding.Severity == "" {
		return
	}
	if finding.FindingID == "" {
		finding.FindingID = authorityFindingID(finding)
	}
	p.Findings = append(p.Findings, finding)
}

func authorityFindingID(finding authorityProjectionFinding) string {
	fields := []string{
		strings.TrimSpace(finding.Code),
		strings.TrimSpace(finding.SourceKind),
		strings.TrimSpace(finding.SourceID),
		strings.TrimSpace(finding.SessionID),
		strconv.FormatInt(finding.ChatID, 10),
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x1f")))
	return "af_" + hex.EncodeToString(sum[:])[:16]
}

func (p *authorityProjection) sortFindings() {
	if p == nil {
		return
	}
	sort.SliceStable(p.Findings, func(i, j int) bool {
		left, right := p.Findings[i], p.Findings[j]
		if authoritySeverityRank(left.Severity) != authoritySeverityRank(right.Severity) {
			return authoritySeverityRank(left.Severity) < authoritySeverityRank(right.Severity)
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.SourceKind != right.SourceKind {
			return left.SourceKind < right.SourceKind
		}
		if left.SourceID != right.SourceID {
			return left.SourceID < right.SourceID
		}
		if left.SessionID != right.SessionID {
			return left.SessionID < right.SessionID
		}
		return left.FindingID < right.FindingID
	})
}

func authoritySeverityRank(severity string) int {
	switch strings.TrimSpace(strings.ToLower(severity)) {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}
