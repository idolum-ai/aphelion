//go:build linux

package runtime

import (
	"encoding/json"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const audioAsVoiceDirective = "[[audio_as_voice]]"

type outboundReplyMediaDirective struct {
	Path string `json:"path"`
	Type string `json:"type,omitempty"`
}

func extractOutboundReplyMedia(scope sandbox.Scope, text string, existing []core.Media) (string, []core.Media) {
	wantsVoice := strings.Contains(text, audioAsVoiceDirective)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	mediaItems := append([]core.Media(nil), existing...)

	for _, rawLine := range lines {
		lineWithoutDirective := strings.ReplaceAll(rawLine, audioAsVoiceDirective, "")
		trimmed := strings.TrimSpace(lineWithoutDirective)
		switch {
		case strings.EqualFold(strings.TrimSpace(rawLine), audioAsVoiceDirective):
			continue
		case strings.HasPrefix(trimmed, "MEDIA:"):
			directive, structured := parseOutboundReplyMediaDirective(strings.TrimSpace(trimmed[len("MEDIA:"):]))
			if !structured {
				kept = append(kept, strings.TrimRight(lineWithoutDirective, " \t"))
				continue
			}
			media, ok := normalizeOutboundReplyMediaPath(scope, directive.Path, wantsVoice || strings.EqualFold(directive.Type, "voice"))
			if ok {
				if mediaType := normalizeOutboundReplyMediaDirectiveType(directive.Type); mediaType != "" {
					media.Type = mediaType
				}
				mediaItems = append(mediaItems, media)
			}
			continue
		default:
			if strings.TrimSpace(lineWithoutDirective) == "" {
				kept = append(kept, "")
				continue
			}
			kept = append(kept, strings.TrimRight(lineWithoutDirective, " \t"))
		}
	}

	return compactReplyText(strings.Join(kept, "\n")), dedupeOutboundReplyMedia(mediaItems)
}

func parseOutboundReplyMediaDirective(raw string) (outboundReplyMediaDirective, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return outboundReplyMediaDirective{}, false
	}
	var directive outboundReplyMediaDirective
	if err := json.Unmarshal([]byte(raw), &directive); err != nil {
		return outboundReplyMediaDirective{}, false
	}
	directive.Path = strings.TrimSpace(directive.Path)
	directive.Type = strings.TrimSpace(directive.Type)
	if directive.Path == "" {
		return outboundReplyMediaDirective{}, false
	}
	return directive, true
}

func normalizeOutboundReplyMediaDirectiveType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "image", "photo":
		return "image"
	case "voice":
		return "voice"
	case "audio":
		return "audio"
	case "video":
		return "video"
	case "animation":
		return "animation"
	case "document", "file":
		return "document"
	default:
		return ""
	}
}

func normalizeOutboundReplyMediaPath(scope sandbox.Scope, rawPath string, wantsVoice bool) (core.Media, bool) {
	candidate := unwrapReplyMediaPath(rawPath)
	if candidate == "" {
		return core.Media{}, false
	}
	lower := strings.ToLower(candidate)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "file://") {
		log.Printf("WARN dropping outbound media %q: remote media urls are not supported in normal replies", candidate)
		return core.Media{}, false
	}
	if strings.TrimSpace(scope.WorkingRoot) == "" {
		log.Printf("WARN dropping outbound media %q: working root is unavailable", candidate)
		return core.Media{}, false
	}

	resolved := candidate
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(scope.WorkingRoot, resolved)
	}
	resolved = filepath.Clean(resolved)
	evaluated, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		log.Printf("WARN dropping outbound media %q: %v", candidate, err)
		return core.Media{}, false
	}
	if !isPathWithinAny(evaluated, allowedOutboundReplyRoots(scope)) {
		log.Printf("WARN dropping outbound media %q: path is outside allowed outbound media roots", evaluated)
		return core.Media{}, false
	}
	info, err := os.Stat(evaluated)
	if err != nil {
		log.Printf("WARN dropping outbound media %q: %v", evaluated, err)
		return core.Media{}, false
	}
	if info.IsDir() {
		log.Printf("WARN dropping outbound media %q: directories cannot be sent as Telegram media", evaluated)
		return core.Media{}, false
	}

	filename := filepath.Base(evaluated)
	mimeType := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))))
	mediaType := classifyOutboundReplyMedia(filename, mimeType, wantsVoice)
	return core.Media{
		Type:     mediaType,
		Path:     evaluated,
		MimeType: mimeType,
		Filename: filename,
	}, true
}

func allowedOutboundReplyRoots(scope sandbox.Scope) []string {
	roots := []string{
		strings.TrimSpace(scope.WorkingRoot),
		strings.TrimSpace(scope.SharedMemoryRoot),
		strings.TrimSpace(scope.UserMemory),
	}
	out := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		cleaned := filepath.Clean(root)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func isPathWithinAny(candidate string, roots []string) bool {
	for _, root := range roots {
		if pathWithinRoot(candidate, root) {
			return true
		}
	}
	return false
}

func pathWithinRoot(candidate string, root string) bool {
	if strings.TrimSpace(candidate) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func unwrapReplyMediaPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) >= 2 {
		if (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') ||
			(trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') ||
			(trimmed[0] == '`' && trimmed[len(trimmed)-1] == '`') {
			trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		}
	}
	return strings.TrimSpace(trimmed)
}

func classifyOutboundReplyMedia(filename string, mimeType string, wantsVoice bool) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))
	lowerMime := strings.ToLower(strings.TrimSpace(mimeType))
	if wantsVoice && (isAudioExtension(ext) || strings.HasPrefix(lowerMime, "audio/")) {
		return "voice"
	}
	switch {
	case ext == ".gif" || lowerMime == "image/gif":
		return "animation"
	case isImageExtension(ext) || strings.HasPrefix(lowerMime, "image/"):
		return "image"
	case isVideoExtension(ext) || strings.HasPrefix(lowerMime, "video/"):
		return "video"
	case isAudioExtension(ext) || strings.HasPrefix(lowerMime, "audio/"):
		return "audio"
	default:
		return "document"
	}
}

func isImageExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp":
		return true
	default:
		return false
	}
}

func isVideoExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".mp4", ".mov", ".avi", ".mkv", ".webm", ".3gp":
		return true
	default:
		return false
	}
}

func isAudioExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".ogg", ".opus", ".mp3", ".wav", ".m4a", ".flac":
		return true
	default:
		return false
	}
}

func dedupeOutboundReplyMedia(items []core.Media) []core.Media {
	if len(items) == 0 {
		return nil
	}
	out := make([]core.Media, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, media := range items {
		key := strings.Join([]string{
			strings.TrimSpace(media.Type),
			strings.TrimSpace(media.Path),
			strings.TrimSpace(media.URL),
			strings.TrimSpace(media.Filename),
		}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, media)
	}
	return out
}

func compactReplyText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	compact := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if strings.TrimSpace(trimmed) == "" {
			if len(compact) == 0 || blank {
				continue
			}
			compact = append(compact, "")
			blank = true
			continue
		}
		compact = append(compact, trimmed)
		blank = false
	}
	return strings.TrimSpace(strings.Join(compact, "\n"))
}
