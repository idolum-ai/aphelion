//go:build linux

package main

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

const tailnetCallbackPrefix = "tailnet:"
const tailnetCallbackRefresh = "refresh"
const tailnetCallbackSurfaces = "surfaces"
const tailnetCommandSurfaces = "surfaces"
const tailnetCommandRevoke = "revoke"
const tailnetRevokeCallbackPrefix = "tailnet_revoke:"
const tailnetRevokeCallbackConfirm = "confirm"
const tailnetRevokeCallbackCancel = "cancel"

func renderTailnetCommand(snapshot core.TailnetStatusSnapshot) (string, [][]telegram.InlineButton) {
	lines := []string{"Tailnet"}
	status := firstTailnetNonEmpty(snapshot.Status, "unknown")
	lines = append(lines, fmt.Sprintf("Status: %s", status))
	lines = append(lines, fmt.Sprintf("Enabled: %t", snapshot.Enabled))
	lines = append(lines, "Backend: "+firstTailnetNonEmpty(snapshot.Backend, "-"))
	if node := firstTailnetNonEmpty(snapshot.DNSName, snapshot.HostName); node != "" {
		lines = append(lines, "Node: "+node)
	}
	if tailnet := strings.TrimSpace(snapshot.TailnetName); tailnet != "" {
		lines = append(lines, "Tailnet: "+tailnet)
	}
	if len(snapshot.TailscaleIPs) > 0 {
		lines = append(lines, "IPs: "+strings.Join(snapshot.TailscaleIPs, ", "))
	}
	if len(snapshot.Tags) > 0 {
		lines = append(lines, "Tags: "+strings.Join(snapshot.Tags, ", "))
	}
	if snapshot.NetcheckAvailable && strings.TrimSpace(snapshot.NetcheckSummary) != "" {
		lines = append(lines, "Netcheck: "+truncateTailnetLine(snapshot.NetcheckSummary, 180))
	}
	if len(snapshot.Surfaces) > 0 {
		lines = append(lines, fmt.Sprintf("Surfaces: %d registered", len(snapshot.Surfaces)))
	}
	privateStatusURL := ""
	if snapshot.Parent != nil {
		parent := snapshot.Parent
		lines = append(lines, "", "Parent tsnet:")
		lines = append(lines, fmt.Sprintf("- enabled=%t running=%t", parent.Enabled, parent.Running))
		if host := strings.TrimSpace(parent.Hostname); host != "" {
			lines = append(lines, "- hostname: "+host)
		}
		if listen := strings.TrimSpace(parent.ListenAddr); listen != "" {
			lines = append(lines, "- listen: "+listen)
		}
		if magic := strings.TrimSpace(parent.MagicDNSURL); magic != "" {
			lines = append(lines, "- private URL: "+magic)
			privateStatusURL = strings.TrimRight(magic, "/") + "/status"
		}
		if errText := strings.TrimSpace(parent.LastError); errText != "" {
			lines = append(lines, "- error: "+truncateTailnetLine(errText, 220))
		}
	}
	if summary := strings.TrimSpace(snapshot.Summary); summary != "" {
		lines = append(lines, "", "Summary:", "- "+truncateTailnetLine(summary, 220))
	}
	lines = append(lines, "", "Issues:")
	if len(snapshot.Issues) == 0 {
		lines = append(lines, "- none")
	} else {
		limit := len(snapshot.Issues)
		if limit > 6 {
			limit = 6
		}
		for i := 0; i < limit; i++ {
			issue := snapshot.Issues[i]
			lines = append(lines, fmt.Sprintf("- %s/%s: %s", firstTailnetNonEmpty(issue.Severity, "unknown"), firstTailnetNonEmpty(issue.Code, "issue"), truncateTailnetLine(issue.Summary, 220)))
		}
		if len(snapshot.Issues) > limit {
			lines = append(lines, fmt.Sprintf("- %d more issue(s) omitted", len(snapshot.Issues)-limit))
		}
	}
	row := []telegram.InlineButton{{Text: "Refresh", CallbackData: encodeTailnetCallbackData(tailnetCallbackRefresh)}}
	row = append(row, telegram.InlineButton{Text: "Surfaces", CallbackData: encodeTailnetCallbackData(tailnetCallbackSurfaces)})
	if privateStatusURL != "" {
		row = append(row, telegram.InlineButton{Text: "Open Status", URL: privateStatusURL})
	}
	rows := [][]telegram.InlineButton{row}
	return strings.Join(compactStatusDisplayLines(lines), "\n"), rows
}

func encodeTailnetCallbackData(action string) string {
	return tailnetCallbackPrefix + strings.TrimSpace(action)
}

func decodeTailnetCallbackData(data string) (string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, tailnetCallbackPrefix) {
		return "", false
	}
	action := strings.TrimSpace(strings.TrimPrefix(trimmed, tailnetCallbackPrefix))
	switch action {
	case tailnetCallbackRefresh:
		return action, true
	case tailnetCallbackSurfaces:
		return action, true
	default:
		return "", false
	}
}

func renderTailnetSurfacesCommand(surfaces []core.TailnetSurfaceStatus) (string, [][]telegram.InlineButton) {
	lines := []string{"Tailnet Surfaces"}
	if len(surfaces) == 0 {
		lines = append(lines, "- none registered")
	} else {
		limit := len(surfaces)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			surface := surfaces[i]
			label := firstTailnetNonEmpty(surface.Name, surface.SurfaceID, "surface")
			owner := strings.Trim(strings.TrimSpace(surface.OwnerKind)+"/"+strings.TrimSpace(surface.OwnerID), "/")
			line := fmt.Sprintf("- %s %s", firstTailnetNonEmpty(surface.Status, "unknown"), label)
			if kind := strings.TrimSpace(surface.SurfaceKind); kind != "" {
				line += " kind=" + kind
			}
			if owner != "" {
				line += " owner=" + owner
			}
			lines = append(lines, truncateTailnetLine(line, 220))
			if url := strings.TrimSpace(surface.URL); url != "" {
				lines = append(lines, "  url: "+truncateTailnetLine(url, 220))
			}
			if host := firstTailnetNonEmpty(surface.Hostname, surface.TailnetName); host != "" {
				lines = append(lines, "  host: "+truncateTailnetLine(host, 160))
			}
			if errText := strings.TrimSpace(surface.LastError); errText != "" {
				lines = append(lines, "  error: "+truncateTailnetLine(errText, 180))
			}
		}
		if len(surfaces) > limit {
			lines = append(lines, fmt.Sprintf("- %d more surface(s) omitted", len(surfaces)-limit))
		}
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Status", CallbackData: encodeTailnetCallbackData(tailnetCallbackRefresh)},
		{Text: "Refresh", CallbackData: encodeTailnetCallbackData(tailnetCallbackSurfaces)},
	}}
	return strings.Join(compactStatusDisplayLines(lines), "\n"), rows
}

func renderTailnetRevokeConfirmation(surfaceID string) (string, [][]telegram.InlineButton) {
	surfaceID = strings.TrimSpace(surfaceID)
	lines := []string{
		"Revoke tailnet surface?",
		"Surface: " + surfaceID,
		"",
		"This marks the owned surface revoked in Aphelion's registry and writes an audit event. If a live listener still observes it, /status and /doctor will report that drift.",
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Cancel", CallbackData: encodeTailnetRevokeCallbackData(tailnetRevokeCallbackCancel, surfaceID)},
		{Text: "Revoke", CallbackData: encodeTailnetRevokeCallbackData(tailnetRevokeCallbackConfirm, surfaceID)},
	}}
	return strings.Join(compactStatusDisplayLines(lines), "\n"), rows
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
		return "Tailnet surface was not found.\nSurface: " + strings.TrimSpace(surfaceID)
	}
	lines := []string{
		"Tailnet surface revoked.",
		"Surface: " + strings.TrimSpace(surfaceID),
	}
	if status := strings.TrimSpace(surface.Status); status != "" {
		lines = append(lines, "Status: "+status)
	}
	if errText := strings.TrimSpace(surface.LastError); errText != "" {
		lines = append(lines, "Reason: "+truncateTailnetLine(errText, 180))
	}
	return strings.Join(compactStatusDisplayLines(lines), "\n")
}

func encodeTailnetRevokeCallbackData(action string, surfaceID string) string {
	return tailnetRevokeCallbackPrefix + strings.TrimSpace(action) + ":" + strings.TrimSpace(surfaceID)
}

func decodeTailnetRevokeCallbackData(data string) (string, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, tailnetRevokeCallbackPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(trimmed, tailnetRevokeCallbackPrefix)
	idx := strings.Index(rest, ":")
	if idx <= 0 {
		return "", "", false
	}
	action := strings.TrimSpace(rest[:idx])
	surfaceID := strings.TrimSpace(rest[idx+1:])
	if surfaceID == "" {
		return "", "", false
	}
	switch action {
	case tailnetRevokeCallbackConfirm, tailnetRevokeCallbackCancel:
		return action, surfaceID, true
	default:
		return "", "", false
	}
}

func nextTailnetToken(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if idx := strings.IndexAny(raw, " \n\t"); idx >= 0 {
		return strings.ToLower(strings.TrimSpace(raw[:idx])), strings.TrimSpace(raw[idx+1:])
	}
	return strings.ToLower(raw), ""
}

func firstTailnetNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func truncateTailnetLine(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return strings.TrimSpace(text[:max-3]) + "..."
}
