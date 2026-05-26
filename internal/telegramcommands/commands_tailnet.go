//go:build linux

package telegramcommands

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

const tailnetCallbackPrefix = "tailnet:"
const tailnetCallbackRefresh = "refresh"
const tailnetCallbackSurfaces = "surfaces"
const tailnetCallbackGrants = "grants"
const tailnetCommandSurfaces = "surfaces"
const tailnetCommandGrants = "grants"
const tailnetCommandRevoke = "revoke"
const tailnetPageSize = 5
const tailnetSurfaceCallbackPrefix = "tailnet_surface:"
const tailnetGrantCallbackPrefix = "tailnet_grant:"
const tailnetRevokeTokenCallbackPrefix = "tailnet_revoke_v2:"
const tailnetRevokeCallbackAsk = "ask"
const tailnetRevokeCallbackConfirm = "confirm"
const tailnetRevokeCallbackCancel = "cancel"

func renderTailnetCommand(snapshot core.TailnetStatusSnapshot) (string, [][]telegram.InlineButton) {
	return renderTailnetCommandWithMode(snapshot, false)
}

func renderTailnetCommandWithMode(snapshot core.TailnetStatusSnapshot, detailed bool) (string, [][]telegram.InlineButton) {
	status := firstTailnetNonEmpty(snapshot.Status, "unknown")
	state := status
	if len(snapshot.Issues) > 0 {
		state += fmt.Sprintf("; %d issue(s)", len(snapshot.Issues))
	}
	details := make([]string, 0, 12)
	if summary := strings.TrimSpace(snapshot.Summary); summary != "" {
		details = append(details, truncateOperatorLine(summary, 220))
	}
	if node := firstTailnetNonEmpty(snapshot.DNSName, snapshot.HostName); node != "" {
		details = append(details, "Node: "+node)
	}
	privateStatusURL := ""
	if snapshot.Parent != nil {
		parent := snapshot.Parent
		details = append(details, "Parent tsnet: enabled "+operatorBoolLabel(parent.Enabled)+", running "+operatorBoolLabel(parent.Running))
		if magic := strings.TrimSpace(parent.MagicDNSURL); magic != "" {
			details = append(details, "Private status URL: "+magic)
			privateStatusURL = strings.TrimRight(magic, "/") + "/status"
		}
		if host := strings.TrimSpace(parent.Hostname); host != "" {
			details = append(details, "Parent hostname: "+host)
		}
		if listen := strings.TrimSpace(parent.ListenAddr); listen != "" {
			details = append(details, "Parent listen: "+listen)
		}
		if errText := strings.TrimSpace(parent.LastError); errText != "" {
			details = append(details, "Parent error: "+truncateOperatorLine(errText, 220))
		}
	}
	details = append(details,
		"Enabled: "+operatorBoolLabel(snapshot.Enabled),
		"Backend: "+firstTailnetNonEmpty(snapshot.Backend, "-"),
	)
	if tailnet := strings.TrimSpace(snapshot.TailnetName); tailnet != "" {
		details = append(details, "Tailnet: "+tailnet)
	}
	if len(snapshot.TailscaleIPs) > 0 {
		details = append(details, "IPs: "+strings.Join(snapshot.TailscaleIPs, ", "))
	}
	if len(snapshot.Tags) > 0 {
		details = append(details, "Tags: "+strings.Join(snapshot.Tags, ", "))
	}
	if snapshot.NetcheckAvailable && strings.TrimSpace(snapshot.NetcheckSummary) != "" {
		details = append(details, "Netcheck: "+truncateOperatorLine(snapshot.NetcheckSummary, 180))
	}
	if len(snapshot.Surfaces) > 0 {
		details = append(details, fmt.Sprintf("Surfaces: %d registered", len(snapshot.Surfaces)))
	}
	if len(snapshot.GrantBindings) > 0 {
		details = append(details, fmt.Sprintf("Grant bindings: %d registered", len(snapshot.GrantBindings)))
	}
	next := "Refresh status or open the surfaces view."
	if snapshot.Enabled && privateStatusURL != "" {
		next = "Open the private status URL or refresh after a Tailnet change."
	}
	evidence := make([]string, 0, 6)
	if len(snapshot.Issues) == 0 {
		evidence = append(evidence, "Issues: none")
	} else {
		limit := len(snapshot.Issues)
		if limit > 6 {
			limit = 6
		}
		for i := 0; i < limit; i++ {
			issue := snapshot.Issues[i]
			evidence = append(evidence, fmt.Sprintf("%s/%s: %s", firstTailnetNonEmpty(issue.Severity, "unknown"), firstTailnetNonEmpty(issue.Code, "issue"), truncateOperatorLine(issue.Summary, 220)))
		}
		if len(snapshot.Issues) > limit {
			evidence = append(evidence, fmt.Sprintf("%d more issue(s) omitted", len(snapshot.Issues)-limit))
		}
	}
	row := []telegram.InlineButton{{Text: "Refresh", CallbackData: encodeTailnetCallbackData(tailnetCallbackRefresh)}}
	row = append(row, telegram.InlineButton{Text: "Surfaces", CallbackData: encodeTailnetCallbackData(tailnetCallbackSurfaces)})
	row = append(row, telegram.InlineButton{Text: "Grants", CallbackData: encodeTailnetCallbackData(tailnetCallbackGrants)})
	if privateStatusURL != "" {
		row = append(row, telegram.InlineButton{Text: "Open Status", URL: privateStatusURL})
	}
	rows := [][]telegram.InlineButton{row}
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:    "Tailnet",
		State:    state,
		Why:      "Tailnet surfaces are private machine mirrors; operator control stays in Telegram and CLI.",
		Next:     next,
		Details:  details,
		Evidence: evidence,
	}, detailed), rows
}

func renderTailnetSurfacesCommand(surfaces []core.TailnetSurfaceStatus) (string, [][]telegram.InlineButton) {
	return renderTailnetSurfacesCommandPage(surfaces, 1)
}

func renderTailnetSurfacesCommandPage(surfaces []core.TailnetSurfaceStatus, page int) (string, [][]telegram.InlineButton) {
	visible, info := telegramPageItems(surfaces, page, tailnetPageSize)
	state := fmt.Sprintf("%d registered surface(s)", len(surfaces))
	if info.PageCount > 1 {
		state = fmt.Sprintf("%d registered surface(s); page %d of %d", len(surfaces), info.Page, info.PageCount)
	}
	next := "Inspect a surface before revoking local registry trust."
	details := make([]string, 0, len(visible)*3)
	if len(surfaces) == 0 {
		details = append(details, "No registered surfaces.")
	} else {
		for i, surface := range visible {
			label := firstTailnetNonEmpty(surface.Name, surface.SurfaceID, "surface")
			owner := strings.Trim(strings.TrimSpace(surface.OwnerKind)+"/"+strings.TrimSpace(surface.OwnerID), "/")
			line := fmt.Sprintf("%d. %s %s", info.Start+i+1, firstTailnetNonEmpty(surface.Status, "unknown"), label)
			if kind := strings.TrimSpace(surface.SurfaceKind); kind != "" {
				line += " (" + kind + ")"
			}
			if owner != "" {
				line += "; owner " + owner
			}
			details = append(details, truncateOperatorLine(line, 220))
			if url := strings.TrimSpace(surface.URL); url != "" {
				details = append(details, "URL: "+truncateOperatorLine(url, 220))
			}
			if host := firstTailnetNonEmpty(surface.Hostname, surface.TailnetName); host != "" {
				details = append(details, "Host: "+truncateOperatorLine(host, 160))
			}
			if errText := strings.TrimSpace(surface.LastError); errText != "" {
				details = append(details, "Error: "+truncateOperatorLine(errText, 180))
			}
		}
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Status", CallbackData: encodeTailnetCallbackData(tailnetCallbackRefresh)},
		{Text: "Refresh", CallbackData: encodeTailnetCallbackData(tailnetCallbackSurfaces)},
		{Text: "Grants", CallbackData: encodeTailnetCallbackData(tailnetCallbackGrants)},
	}}
	rows = append(rows, tailnetSurfaceDetailRows(visible, info.Start)...)
	rows = append(rows, telegramPageNavigationRows(info, telegramPageSurfaceTailnet, telegramPageViewSurfaces)...)
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:   "Tailnet Surfaces",
		State:   state,
		Why:     "Registered surfaces are private mirrors of approved child or parent grants.",
		Next:    next,
		Details: details,
	}, 12, 0), rows
}

func renderTailnetGrantBindingsCommand(bindings []core.TailnetGrantBindingStatus) (string, [][]telegram.InlineButton) {
	return renderTailnetGrantBindingsCommandPage(bindings, 1)
}

func renderTailnetGrantBindingsCommandPage(bindings []core.TailnetGrantBindingStatus, page int) (string, [][]telegram.InlineButton) {
	visible, info := telegramPageItems(bindings, page, tailnetPageSize)
	state := fmt.Sprintf("%d grant binding(s)", len(bindings))
	if info.PageCount > 1 {
		state = fmt.Sprintf("%d grant binding(s); page %d of %d", len(bindings), info.Page, info.PageCount)
	}
	next := "Inspect a binding here; apply, drift, or rollback from the CLI."
	details := make([]string, 0, len(visible)*3)
	if len(bindings) == 0 {
		details = append(details, "No Tailnet grant bindings.")
	} else {
		for i, binding := range visible {
			line := fmt.Sprintf("%d. %s %s", info.Start+i+1, firstTailnetNonEmpty(binding.Status, "unknown"), firstTailnetNonEmpty(binding.BindingID, "binding"))
			if grantID := strings.TrimSpace(binding.GrantID); grantID != "" {
				line += "; grant " + grantID
			}
			if surfaceID := strings.TrimSpace(binding.SurfaceID); surfaceID != "" {
				line += "; surface " + surfaceID
			}
			details = append(details, truncateOperatorLine(line, 220))
			target := strings.Trim(strings.TrimSpace(binding.CapabilityKind)+"/"+strings.TrimSpace(binding.TargetResource), "/")
			if target != "" {
				details = append(details, "Target: "+truncateOperatorLine(target, 160))
			}
			if drift := strings.TrimSpace(binding.DriftReason); drift != "" {
				details = append(details, "Reason: "+truncateOperatorLine(drift, 180))
			}
		}
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Status", CallbackData: encodeTailnetCallbackData(tailnetCallbackRefresh)},
		{Text: "Surfaces", CallbackData: encodeTailnetCallbackData(tailnetCallbackSurfaces)},
		{Text: "Refresh", CallbackData: encodeTailnetCallbackData(tailnetCallbackGrants)},
	}}
	rows = append(rows, tailnetGrantDetailRows(visible, info.Start)...)
	rows = append(rows, telegramPageNavigationRows(info, telegramPageSurfaceTailnet, telegramPageViewGrants)...)
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:   "Tailnet Grants",
		State:   state,
		Why:     "Grant bindings project approved Aphelion authority into private-network policy evidence.",
		Next:    next,
		Details: details,
	}, 12, 0), rows
}

func renderTailnetSurfaceDetail(surface core.TailnetSurfaceStatus) (string, [][]telegram.InlineButton) {
	surfaceID := strings.TrimSpace(surface.SurfaceID)
	label := firstTailnetNonEmpty(surface.Name, surfaceID, "surface")
	owner := strings.Trim(strings.TrimSpace(surface.OwnerKind)+"/"+strings.TrimSpace(surface.OwnerID), "/")
	state := firstTailnetNonEmpty(surface.Status, "unknown") + " " + label
	details := []string{
		"Surface: " + surfaceID,
	}
	if kind := strings.TrimSpace(surface.SurfaceKind); kind != "" {
		details = append(details, "Kind: "+kind)
	}
	if owner != "" {
		details = append(details, "Owner: "+owner)
	}
	if url := strings.TrimSpace(surface.URL); url != "" {
		details = append(details, "URL: "+truncateOperatorLine(url, 220))
	}
	if host := firstTailnetNonEmpty(surface.Hostname, surface.TailnetName); host != "" {
		details = append(details, "Host: "+truncateOperatorLine(host, 160))
	}
	if listen := strings.TrimSpace(surface.ListenAddr); listen != "" {
		details = append(details, "Listen: "+truncateOperatorLine(listen, 160))
	}
	if len(surface.Tags) > 0 {
		details = append(details, "Tags: "+strings.Join(surface.Tags, ", "))
	}
	evidence := tailnetTimeEvidence(
		tailnetEvidenceTime{Label: "Declared", At: surface.DeclaredAt},
		tailnetEvidenceTime{Label: "Activated", At: surface.ActivatedAt},
		tailnetEvidenceTime{Label: "Observed", At: surface.LastObservedAt},
		tailnetEvidenceTime{Label: "Revoked", At: surface.RevokedAt},
		tailnetEvidenceTime{Label: "Updated", At: surface.UpdatedAt},
	)
	if errText := strings.TrimSpace(surface.LastError); errText != "" {
		evidence = append(evidence, "Last error: "+truncateOperatorLine(errText, 180))
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Surfaces", CallbackData: encodeTailnetCallbackData(tailnetCallbackSurfaces)},
		{Text: "Grants", CallbackData: encodeTailnetCallbackData(tailnetCallbackGrants)},
	}}
	actionRow := []telegram.InlineButton{}
	if url := strings.TrimSpace(surface.URL); url != "" {
		actionRow = append(actionRow, telegram.InlineButton{Text: "Open", URL: url})
	}
	if surfaceID != "" && strings.TrimSpace(surface.Status) != "revoked" {
		actionRow = append(actionRow, telegram.InlineButton{Text: "Revoke", CallbackData: encodeTailnetRevokeTokenCallbackData(tailnetRevokeCallbackAsk, surfaceID)})
	}
	if len(actionRow) > 0 {
		rows = append(rows, actionRow)
	}
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    "Tailnet Surface",
		State:    state,
		Why:      "This is local registry evidence for a private Tailnet-facing surface.",
		Next:     "Refresh surfaces after external Tailnet or child policy changes.",
		Details:  details,
		Evidence: evidence,
	}, 12, 4), rows
}

func renderTailnetGrantDetail(binding core.TailnetGrantBindingStatus) (string, [][]telegram.InlineButton) {
	bindingID := strings.TrimSpace(binding.BindingID)
	state := firstTailnetNonEmpty(binding.Status, "unknown") + " " + firstTailnetNonEmpty(bindingID, "binding")
	details := []string{
		"Binding: " + bindingID,
	}
	if grantID := strings.TrimSpace(binding.GrantID); grantID != "" {
		details = append(details, "Grant: "+grantID)
	}
	if surfaceID := strings.TrimSpace(binding.SurfaceID); surfaceID != "" {
		details = append(details, "Surface: "+surfaceID)
	}
	target := strings.Trim(strings.TrimSpace(binding.CapabilityKind)+"/"+strings.TrimSpace(binding.TargetResource), "/")
	if target != "" {
		details = append(details, "Target: "+truncateOperatorLine(target, 160))
	}
	if grantedTo := strings.TrimSpace(binding.GrantedTo); grantedTo != "" {
		details = append(details, "Granted to: "+truncateOperatorLine(grantedTo, 160))
	}
	evidence := make([]string, 0, 6)
	if policyHash := strings.TrimSpace(binding.AppliedPolicyHash); policyHash != "" {
		evidence = append(evidence, "Applied policy: "+truncateOperatorLine(policyHash, 160))
	}
	if policyHash := strings.TrimSpace(binding.ObservedPolicyHash); policyHash != "" {
		evidence = append(evidence, "Observed policy: "+truncateOperatorLine(policyHash, 160))
	}
	if drift := strings.TrimSpace(binding.DriftReason); drift != "" {
		evidence = append(evidence, "Reason: "+truncateOperatorLine(drift, 180))
	}
	evidence = append(evidence, tailnetTimeEvidence(
		tailnetEvidenceTime{Label: "Created", At: binding.CreatedAt},
		tailnetEvidenceTime{Label: "Applied", At: binding.AppliedAt},
		tailnetEvidenceTime{Label: "Revoked", At: binding.RevokedAt},
		tailnetEvidenceTime{Label: "Updated", At: binding.UpdatedAt},
	)...)
	rows := [][]telegram.InlineButton{{
		{Text: "Grants", CallbackData: encodeTailnetCallbackData(tailnetCallbackGrants)},
		{Text: "Surfaces", CallbackData: encodeTailnetCallbackData(tailnetCallbackSurfaces)},
		{Text: "Status", CallbackData: encodeTailnetCallbackData(tailnetCallbackRefresh)},
	}}
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    "Tailnet Grant",
		State:    state,
		Why:      "This binding is the evidence that approved Aphelion authority maps to private-network policy.",
		Next:     "Use the CLI for apply, drift, or rollback actions after inspecting policy evidence.",
		Details:  details,
		Evidence: evidence,
	}, 12, 6), rows
}

func renderTailnetRevokeConfirmation(surfaceID string) (string, [][]telegram.InlineButton) {
	surfaceID = strings.TrimSpace(surfaceID)
	lines := []string{
		face.RenderOperatorPanel(face.OperatorPanel{
			Title: "Revoke tailnet surface?",
			State: "waiting for confirmation",
			Why:   "This marks the owned surface revoked in the local registry and writes an audit event.",
			Next:  "Cancel to leave it unchanged, or revoke to record the local registry change.",
			Evidence: []string{
				"Surface: " + surfaceID,
				"If a live listener still observes it, /status and /health diagnose will report that drift.",
			},
		}),
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Cancel", CallbackData: encodeTailnetRevokeTokenCallbackData(tailnetRevokeCallbackCancel, surfaceID)},
		{Text: "Revoke", CallbackData: encodeTailnetRevokeTokenCallbackData(tailnetRevokeCallbackConfirm, surfaceID)},
	}}
	return strings.Join(compactStatusDisplayLines(lines), "\n"), rows
}

func renderTailnetRevokeTokenConfirmation(surfaceID string) (string, [][]telegram.InlineButton) {
	return renderTailnetRevokeConfirmation(surfaceID)
}

func renderTailnetRevokeCanceled(surfaceID string) string {
	surfaceID = strings.TrimSpace(surfaceID)
	if surfaceID == "" {
		return "Tailnet surface revoke canceled."
	}
	return "Tailnet surface revoke canceled.\nSurface: " + surfaceID
}

func renderTailnetRevokeResult(requestedID string, surface core.TailnetSurfaceStatus, found bool) string {
	surfaceID := firstTailnetNonEmpty(surface.SurfaceID, requestedID)
	if !found {
		return face.RenderOperatorPanel(face.OperatorPanel{
			Title:    "Tailnet surface",
			State:    "not found",
			Next:     "Refresh surfaces and check the surface ID before retrying.",
			Evidence: []string{"Surface: " + strings.TrimSpace(surfaceID)},
		})
	}
	evidence := []string{"Surface: " + strings.TrimSpace(surfaceID)}
	if status := strings.TrimSpace(surface.Status); status != "" {
		evidence = append(evidence, "Registry status: "+status)
	}
	if errText := strings.TrimSpace(surface.LastError); errText != "" {
		evidence = append(evidence, "Reason: "+truncateOperatorLine(errText, 180))
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title:    "Tailnet surface revoked",
		State:    "revoked",
		Why:      "The local registry now treats this surface as revoked.",
		Next:     "Refresh surfaces; any still-observed listener will be reported as drift.",
		Evidence: evidence,
	})
}
