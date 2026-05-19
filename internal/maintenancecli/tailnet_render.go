//go:build linux

package maintenancecli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

func renderTailnetCommandReport(w io.Writer, report tailnetCommandReport, format string) error {
	switch format {
	case commandOutputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case commandOutputKV:
		renderTailnetCommandReportKV(w, report)
		return nil
	default:
		fmt.Fprintln(w, renderTailnetCommandReportHuman(report))
		return nil
	}
}

func renderTailnetCommandReportKV(w io.Writer, report tailnetCommandReport) {
	fmt.Fprintf(w, "action: %s\n", report.Action)
	fmt.Fprintf(w, "status: %s\n", report.Status)
	fmt.Fprintf(w, "enabled: %t\n", report.Enabled)
	fmt.Fprintf(w, "backend: %s\n", firstNonEmpty(report.Backend, "-"))
	fmt.Fprintf(w, "expected_tailnet: %s\n", firstNonEmpty(report.ExpectedTailnet, "-"))
	if report.SurfaceID != "" {
		fmt.Fprintf(w, "surface_id: %s\n", report.SurfaceID)
	}
	if report.BindingID != "" {
		fmt.Fprintf(w, "binding_id: %s\n", report.BindingID)
	}
	if report.Reason != "" {
		fmt.Fprintf(w, "reason: %s\n", report.Reason)
	}
	fmt.Fprintf(w, "surface_count: %d\n", len(report.Surfaces))
	for _, surface := range report.Surfaces {
		fmt.Fprintf(w, "surface: %s status=%s owner=%s:%s kind=%s url=%s\n",
			surface.SurfaceID,
			firstNonEmpty(surface.Status, "-"),
			firstNonEmpty(surface.OwnerKind, "-"),
			firstNonEmpty(surface.OwnerID, "-"),
			firstNonEmpty(surface.SurfaceKind, "-"),
			firstNonEmpty(surface.URL, "-"),
		)
	}
	fmt.Fprintf(w, "grant_binding_count: %d\n", len(report.Bindings))
	for _, binding := range report.Bindings {
		fmt.Fprintf(w, "grant_binding: %s status=%s grant=%s surface=%s target=%s\n",
			binding.BindingID,
			firstNonEmpty(binding.Status, "-"),
			firstNonEmpty(binding.GrantID, "-"),
			firstNonEmpty(binding.SurfaceID, "-"),
			firstNonEmpty(binding.TargetResource, "-"),
		)
	}
}

func renderTailnetCommandReportHuman(report tailnetCommandReport) string {
	details := []string{
		"Backend: " + firstNonEmpty(report.Backend, "-"),
		"Expected tailnet: " + firstNonEmpty(report.ExpectedTailnet, "-"),
		fmt.Sprintf("Registered surfaces: %d", len(report.Surfaces)),
	}
	if report.SurfaceID != "" {
		details = append(details, "Surface: "+report.SurfaceID)
	}
	if report.Reason != "" {
		details = append(details, "Reason: "+report.Reason)
	}
	if report.BindingID != "" {
		details = append(details, "Binding: "+report.BindingID)
	}
	for _, surface := range report.Surfaces {
		details = append(details, fmt.Sprintf("%s: %s %s", surface.SurfaceID, firstNonEmpty(surface.Status, "-"), firstNonEmpty(surface.URL, surface.Name, "-")))
	}
	for _, binding := range report.Bindings {
		details = append(details, fmt.Sprintf("%s: %s grant=%s surface=%s", binding.BindingID, firstNonEmpty(binding.Status, "-"), firstNonEmpty(binding.GrantID, "-"), firstNonEmpty(binding.SurfaceID, "-")))
	}
	evidence := []string{"Source: Tailnet surface and grant-binding registries in the Aphelion session store."}
	for _, status := range sortedTailnetSurfaceStatuses(report.Surfaces) {
		evidence = append(evidence, fmt.Sprintf("%s surfaces: %d", status, countTailnetSurfaces(report.Surfaces, status)))
	}
	for _, status := range sortedTailnetGrantBindingStatuses(report.Bindings) {
		evidence = append(evidence, fmt.Sprintf("%s grant bindings: %d", status, countTailnetGrantBindings(report.Bindings, status)))
	}
	next := "Use Telegram /tailnet controls for inspection, or CLI revoke/rollback commands for explicit local registry changes."
	if report.Status == "empty" {
		next = "Declare or observe a Tailnet surface before expecting private reachability."
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title:    "Tailnet Registry",
		State:    report.Status,
		Why:      "Private network state is evidence, not authority; Aphelion records what it has declared or observed.",
		Next:     next,
		Details:  details,
		Evidence: evidence,
	})
}

func tailnetSurfaceReports(records []session.TailnetSurfaceRecord) []tailnetSurfaceReport {
	out := make([]tailnetSurfaceReport, 0, len(records))
	for _, record := range records {
		record = session.NormalizeTailnetSurfaceRecord(record)
		out = append(out, tailnetSurfaceReport{
			SurfaceID:   record.SurfaceID,
			OwnerKind:   record.OwnerKind,
			OwnerID:     record.OwnerID,
			SurfaceKind: record.SurfaceKind,
			Name:        record.Name,
			Hostname:    record.Hostname,
			TailnetName: record.TailnetName,
			ListenAddr:  record.ListenAddr,
			URL:         record.URL,
			Tags:        record.Tags,
			Status:      record.Status,
			LastError:   record.LastError,
		})
	}
	return out
}

func tailnetGrantBindingReports(records []session.TailnetGrantBinding) []tailnetGrantBindingReport {
	out := make([]tailnetGrantBindingReport, 0, len(records))
	for _, record := range records {
		record = session.NormalizeTailnetGrantBinding(record)
		out = append(out, tailnetGrantBindingReport{
			BindingID:          record.BindingID,
			GrantID:            record.GrantID,
			SurfaceID:          record.SurfaceID,
			GrantedTo:          record.GrantedTo,
			CapabilityKind:     record.CapabilityKind,
			TargetResource:     record.TargetResource,
			DesiredPolicyJSON:  record.DesiredPolicyJSON,
			AppliedPolicyHash:  record.AppliedPolicyHash,
			ObservedPolicyHash: record.ObservedPolicyHash,
			Status:             record.Status,
			DriftReason:        record.DriftReason,
		})
	}
	return out
}

func tailnetRegistryStatus(enabled bool, surfaces []session.TailnetSurfaceRecord) string {
	if !enabled {
		return "disabled"
	}
	if len(surfaces) == 0 {
		return "empty"
	}
	for _, surface := range surfaces {
		switch surface.Status {
		case session.TailnetSurfaceStatusDegraded:
			return "degraded"
		case session.TailnetSurfaceStatusActive:
			return "active"
		}
	}
	return "declared"
}

func tailnetGrantBindingRegistryStatus(bindings []tailnetGrantBindingReport) string {
	if len(bindings) == 0 {
		return "empty"
	}
	revoked := 0
	for _, binding := range bindings {
		switch strings.TrimSpace(binding.Status) {
		case session.TailnetGrantBindingStatusDrifted, session.TailnetGrantBindingStatusFailed:
			return "needs_attention"
		case session.TailnetGrantBindingStatusProposed:
			return "pending"
		case session.TailnetGrantBindingStatusRevoked:
			revoked++
		}
	}
	if revoked == len(bindings) {
		return "revoked"
	}
	return "ready"
}

func sortedTailnetSurfaceStatuses(surfaces []tailnetSurfaceReport) []string {
	seen := map[string]bool{}
	for _, surface := range surfaces {
		status := strings.TrimSpace(surface.Status)
		if status == "" {
			continue
		}
		seen[status] = true
	}
	values := make([]string, 0, len(seen))
	for status := range seen {
		values = append(values, status)
	}
	sort.Strings(values)
	return values
}

func sortedTailnetGrantBindingStatuses(bindings []tailnetGrantBindingReport) []string {
	seen := make(map[string]bool, len(bindings))
	values := make([]string, 0)
	for _, binding := range bindings {
		status := strings.TrimSpace(binding.Status)
		if status == "" || seen[status] {
			continue
		}
		seen[status] = true
		values = append(values, status)
	}
	sort.Strings(values)
	return values
}

func countTailnetSurfaces(surfaces []tailnetSurfaceReport, status string) int {
	count := 0
	for _, surface := range surfaces {
		if strings.TrimSpace(surface.Status) == status {
			count++
		}
	}
	return count
}

func countTailnetGrantBindings(bindings []tailnetGrantBindingReport, status string) int {
	count := 0
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Status) == status {
			count++
		}
	}
	return count
}
