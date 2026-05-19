//go:build linux

package telegramcommands

import (
	"crypto/sha1"
	"encoding/hex"
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
const tailnetRevokeCallbackPrefix = "tailnet_revoke:"
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
	case tailnetCallbackGrants:
		return action, true
	default:
		return "", false
	}
}

func renderTailnetSurfacesCommand(surfaces []core.TailnetSurfaceStatus) (string, [][]telegram.InlineButton) {
	return renderTailnetSurfacesCommandWithMode(surfaces, false)
}

func renderTailnetSurfacesCommandWithMode(surfaces []core.TailnetSurfaceStatus, detailed bool) (string, [][]telegram.InlineButton) {
	state := fmt.Sprintf("%d registered surface(s)", len(surfaces))
	next := "Refresh after child or Tailnet policy changes."
	details := make([]string, 0, len(surfaces)*3)
	if len(surfaces) == 0 {
		details = append(details, "No registered surfaces.")
	} else {
		limit := len(surfaces)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			surface := surfaces[i]
			label := firstTailnetNonEmpty(surface.Name, surface.SurfaceID, "surface")
			owner := strings.Trim(strings.TrimSpace(surface.OwnerKind)+"/"+strings.TrimSpace(surface.OwnerID), "/")
			line := fmt.Sprintf("%s %s", firstTailnetNonEmpty(surface.Status, "unknown"), label)
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
		if len(surfaces) > limit {
			details = append(details, fmt.Sprintf("%d more surface(s) omitted", len(surfaces)-limit))
		}
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Status", CallbackData: encodeTailnetCallbackData(tailnetCallbackRefresh)},
		{Text: "Refresh", CallbackData: encodeTailnetCallbackData(tailnetCallbackSurfaces)},
		{Text: "Grants", CallbackData: encodeTailnetCallbackData(tailnetCallbackGrants)},
	}}
	revokeRows := tailnetSurfaceRevokeRows(surfaces)
	rows = append(rows, revokeRows...)
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:   "Tailnet Surfaces",
		State:   state,
		Why:     "Registered surfaces are private mirrors of approved child or parent grants.",
		Next:    next,
		Details: details,
	}, detailed), rows
}

func renderTailnetGrantBindingsCommand(bindings []core.TailnetGrantBindingStatus) (string, [][]telegram.InlineButton) {
	return renderTailnetGrantBindingsCommandWithMode(bindings, false)
}

func renderTailnetGrantBindingsCommandWithMode(bindings []core.TailnetGrantBindingStatus, detailed bool) (string, [][]telegram.InlineButton) {
	state := fmt.Sprintf("%d grant binding(s)", len(bindings))
	next := "Apply, drift, or rollback Tailnet grant bindings from the CLI after policy evidence changes."
	details := make([]string, 0, len(bindings)*3)
	if len(bindings) == 0 {
		details = append(details, "No Tailnet grant bindings.")
	} else {
		limit := len(bindings)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			binding := bindings[i]
			line := fmt.Sprintf("%s %s", firstTailnetNonEmpty(binding.Status, "unknown"), firstTailnetNonEmpty(binding.BindingID, "binding"))
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
		if len(bindings) > limit {
			details = append(details, fmt.Sprintf("%d more grant binding(s) omitted", len(bindings)-limit))
		}
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Status", CallbackData: encodeTailnetCallbackData(tailnetCallbackRefresh)},
		{Text: "Surfaces", CallbackData: encodeTailnetCallbackData(tailnetCallbackSurfaces)},
		{Text: "Refresh", CallbackData: encodeTailnetCallbackData(tailnetCallbackGrants)},
	}}
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:   "Tailnet Grants",
		State:   state,
		Why:     "Grant bindings project approved Aphelion authority into private-network policy evidence.",
		Next:    next,
		Details: details,
	}, detailed), rows
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
		{Text: "Cancel", CallbackData: encodeTailnetRevokeCallbackData(tailnetRevokeCallbackCancel, surfaceID)},
		{Text: "Revoke", CallbackData: encodeTailnetRevokeCallbackData(tailnetRevokeCallbackConfirm, surfaceID)},
	}}
	return strings.Join(compactStatusDisplayLines(lines), "\n"), rows
}

func renderTailnetRevokeTokenConfirmation(surfaceID string) (string, [][]telegram.InlineButton) {
	surfaceID = strings.TrimSpace(surfaceID)
	text, _ := renderTailnetRevokeConfirmation(surfaceID)
	rows := [][]telegram.InlineButton{{
		{Text: "Cancel", CallbackData: encodeTailnetRevokeTokenCallbackData(tailnetRevokeCallbackCancel, surfaceID)},
		{Text: "Revoke", CallbackData: encodeTailnetRevokeTokenCallbackData(tailnetRevokeCallbackConfirm, surfaceID)},
	}}
	return text, rows
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

func encodeTailnetRevokeCallbackData(action string, surfaceID string) string {
	return tailnetRevokeCallbackPrefix + strings.TrimSpace(action) + ":" + strings.TrimSpace(surfaceID)
}

func encodeTailnetRevokeTokenCallbackData(action string, surfaceID string) string {
	return tailnetRevokeTokenCallbackPrefix + strings.TrimSpace(action) + ":" + tailnetSurfaceCallbackToken(surfaceID)
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

func decodeTailnetRevokeTokenCallbackData(data string) (string, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, tailnetRevokeTokenCallbackPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(trimmed, tailnetRevokeTokenCallbackPrefix)
	idx := strings.Index(rest, ":")
	if idx <= 0 {
		return "", "", false
	}
	action := strings.TrimSpace(rest[:idx])
	token := strings.TrimSpace(rest[idx+1:])
	if token == "" {
		return "", "", false
	}
	switch action {
	case tailnetRevokeCallbackAsk, tailnetRevokeCallbackConfirm, tailnetRevokeCallbackCancel:
		return action, token, true
	default:
		return "", "", false
	}
}

func tailnetSurfaceRevokeRows(surfaces []core.TailnetSurfaceStatus) [][]telegram.InlineButton {
	limit := len(surfaces)
	if limit > 6 {
		limit = 6
	}
	rows := make([][]telegram.InlineButton, 0, (limit+1)/2)
	row := make([]telegram.InlineButton, 0, 2)
	for i := 0; i < limit; i++ {
		surfaceID := strings.TrimSpace(surfaces[i].SurfaceID)
		if surfaceID == "" {
			continue
		}
		row = append(row, telegram.InlineButton{
			Text:         fmt.Sprintf("Revoke %d", i+1),
			CallbackData: encodeTailnetRevokeTokenCallbackData(tailnetRevokeCallbackAsk, surfaceID),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = make([]telegram.InlineButton, 0, 2)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

func tailnetSurfaceCallbackToken(surfaceID string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(surfaceID)))
	return hex.EncodeToString(sum[:])[:12]
}

func resolveTailnetSurfaceCallbackToken(surfaces []core.TailnetSurfaceStatus, token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	found := ""
	for _, surface := range surfaces {
		surfaceID := strings.TrimSpace(surface.SurfaceID)
		if surfaceID == "" || tailnetSurfaceCallbackToken(surfaceID) != token {
			continue
		}
		if found != "" {
			return "", false
		}
		found = surfaceID
	}
	return found, found != ""
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
