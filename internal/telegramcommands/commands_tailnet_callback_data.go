//go:build linux

package telegramcommands

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

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

func encodeTailnetRevokeTokenCallbackData(action string, surfaceID string) string {
	return tailnetRevokeTokenCallbackPrefix + strings.TrimSpace(action) + ":" + tailnetCallbackToken(surfaceID)
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

func encodeTailnetSurfaceCallbackData(surfaceID string) string {
	return tailnetSurfaceCallbackPrefix + tailnetCallbackToken(surfaceID)
}

func decodeTailnetSurfaceCallbackData(data string) (string, bool) {
	return decodeTailnetTokenCallbackData(data, tailnetSurfaceCallbackPrefix)
}

func encodeTailnetGrantCallbackData(bindingID string) string {
	return tailnetGrantCallbackPrefix + tailnetCallbackToken(bindingID)
}

func decodeTailnetGrantCallbackData(data string) (string, bool) {
	return decodeTailnetTokenCallbackData(data, tailnetGrantCallbackPrefix)
}

func decodeTailnetTokenCallbackData(data string, prefix string) (string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

func tailnetSurfaceDetailRows(surfaces []core.TailnetSurfaceStatus, start int) [][]telegram.InlineButton {
	rows := make([][]telegram.InlineButton, 0, (len(surfaces)+1)/2)
	row := make([]telegram.InlineButton, 0, 2)
	for i := 0; i < len(surfaces); i++ {
		surfaceID := strings.TrimSpace(surfaces[i].SurfaceID)
		if surfaceID == "" {
			continue
		}
		row = append(row, telegram.InlineButton{
			Text:         fmt.Sprintf("Surface %d", start+i+1),
			CallbackData: encodeTailnetSurfaceCallbackData(surfaceID),
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

func tailnetGrantDetailRows(bindings []core.TailnetGrantBindingStatus, start int) [][]telegram.InlineButton {
	rows := make([][]telegram.InlineButton, 0, (len(bindings)+1)/2)
	row := make([]telegram.InlineButton, 0, 2)
	for i := 0; i < len(bindings); i++ {
		bindingID := strings.TrimSpace(bindings[i].BindingID)
		if bindingID == "" {
			continue
		}
		row = append(row, telegram.InlineButton{
			Text:         fmt.Sprintf("Grant %d", start+i+1),
			CallbackData: encodeTailnetGrantCallbackData(bindingID),
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

func tailnetCallbackToken(value string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])[:12]
}

func resolveTailnetSurfaceCallbackToken(surfaces []core.TailnetSurfaceStatus, token string) (core.TailnetSurfaceStatus, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return core.TailnetSurfaceStatus{}, false
	}
	var found core.TailnetSurfaceStatus
	foundOK := false
	for _, surface := range surfaces {
		surfaceID := strings.TrimSpace(surface.SurfaceID)
		if surfaceID == "" || tailnetCallbackToken(surfaceID) != token {
			continue
		}
		if foundOK {
			return core.TailnetSurfaceStatus{}, false
		}
		found = surface
		foundOK = true
	}
	return found, foundOK
}

func resolveTailnetGrantCallbackToken(bindings []core.TailnetGrantBindingStatus, token string) (core.TailnetGrantBindingStatus, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return core.TailnetGrantBindingStatus{}, false
	}
	var found core.TailnetGrantBindingStatus
	foundOK := false
	for _, binding := range bindings {
		bindingID := strings.TrimSpace(binding.BindingID)
		if bindingID == "" || tailnetCallbackToken(bindingID) != token {
			continue
		}
		if foundOK {
			return core.TailnetGrantBindingStatus{}, false
		}
		found = binding
		foundOK = true
	}
	return found, foundOK
}

func findTailnetSurfaceByID(surfaces []core.TailnetSurfaceStatus, surfaceID string) (core.TailnetSurfaceStatus, bool) {
	surfaceID = strings.TrimSpace(surfaceID)
	if surfaceID == "" {
		return core.TailnetSurfaceStatus{}, false
	}
	for _, surface := range surfaces {
		if strings.TrimSpace(surface.SurfaceID) == surfaceID {
			return surface, true
		}
	}
	return core.TailnetSurfaceStatus{}, false
}

type tailnetEvidenceTime struct {
	Label string
	At    time.Time
}

func tailnetTimeEvidence(items ...tailnetEvidenceTime) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		label := strings.TrimSpace(item.Label)
		if label == "" || item.At.IsZero() {
			continue
		}
		out = append(out, label+": "+item.At.UTC().Format("2006-01-02 15:04Z"))
	}
	return out
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
