//go:build linux

package core

import (
	"path/filepath"
	"strings"
)

const ArtifactMetadataMediaProcessingChoice = "aphelion_media_processing_choice"

func (a Artifact) HasCapability(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return false
	}
	for _, capability := range a.Capabilities {
		if strings.EqualFold(strings.TrimSpace(capability), name) {
			return true
		}
	}
	return false
}

func NormalizeArtifact(a Artifact) Artifact {
	a.Channel = strings.TrimSpace(strings.ToLower(a.Channel))
	a.RemoteID = strings.TrimSpace(a.RemoteID)
	a.SourceType = strings.TrimSpace(strings.ToLower(a.SourceType))
	a.Kind = strings.TrimSpace(strings.ToLower(a.Kind))
	a.Subtype = strings.TrimSpace(strings.ToLower(a.Subtype))
	a.MimeType = strings.TrimSpace(strings.ToLower(a.MimeType))
	a.Filename = strings.TrimSpace(a.Filename)
	a.Caption = strings.TrimSpace(a.Caption)
	a.Scope = strings.TrimSpace(strings.ToLower(a.Scope))
	a.PrincipalID = strings.TrimSpace(a.PrincipalID)
	if a.SizeBytes == 0 && len(a.Data) > 0 {
		a.SizeBytes = int64(len(a.Data))
	}
	if a.Metadata == nil {
		a.Metadata = map[string]string{}
	}

	if a.Kind == "" {
		a.Kind, a.Subtype = inferArtifactKind(a)
	} else if a.Subtype == "" {
		_, a.Subtype = inferArtifactKind(a)
	}

	if len(a.Capabilities) == 0 {
		a.Capabilities = artifactCapabilities(a)
	}
	if strings.TrimSpace(a.DefaultRetention) == "" {
		a.DefaultRetention = artifactDefaultRetention(a)
	}
	if strings.TrimSpace(a.RetentionCeiling) == "" {
		a.RetentionCeiling = artifactRetentionCeiling(a)
	}
	return a
}

func inferArtifactKind(a Artifact) (kind string, subtype string) {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(a.Filename)))
	switch a.SourceType {
	case "photo":
		return "image", ""
	case "voice":
		return "audio", "voice_note"
	case "audio":
		return "audio", ""
	case "video":
		return "video", ""
	case "video_note":
		return "video", "video_note"
	case "animation":
		return "video", "animation"
	case "sticker":
		switch {
		case strings.EqualFold(a.Metadata["telegram_sticker_type"], "video") || strings.EqualFold(a.Metadata["is_video"], "true"):
			return "sticker", "video_sticker"
		case strings.EqualFold(a.Metadata["telegram_sticker_type"], "animated") || strings.EqualFold(a.Metadata["is_animated"], "true"):
			return "sticker", "animated_sticker"
		default:
			return "sticker", "static_sticker"
		}
	case "contact", "location", "venue", "poll":
		return "structured", a.SourceType
	case "document":
		switch {
		case strings.HasPrefix(a.MimeType, "image/"):
			return "image", ""
		case a.MimeType == "application/pdf" || ext == ".pdf":
			return "document", "pdf"
		case isTextArtifactMIME(a.MimeType) || isTextArtifactExt(ext):
			return "document", "text"
		default:
			return "document", ""
		}
	}

	switch {
	case strings.HasPrefix(a.MimeType, "image/"):
		return "image", ""
	case strings.HasPrefix(a.MimeType, "audio/"):
		return "audio", ""
	case strings.HasPrefix(a.MimeType, "video/"):
		return "video", ""
	case a.MimeType == "application/pdf" || ext == ".pdf":
		return "document", "pdf"
	case isTextArtifactMIME(a.MimeType) || isTextArtifactExt(ext):
		return "document", "text"
	default:
		return firstNonEmpty(strings.TrimSpace(a.Kind), "document"), strings.TrimSpace(a.Subtype)
	}
}

func artifactCapabilities(a Artifact) []string {
	switch a.Kind {
	case "image":
		return []string{"vision", "inspect_metadata", "store_reference"}
	case "audio":
		return []string{"transcribe", "inspect_metadata", "store_reference"}
	case "video":
		return []string{"inspect_metadata", "store_reference"}
	case "sticker":
		if a.Subtype == "static_sticker" {
			return []string{"vision", "inspect_metadata", "store_reference"}
		}
		return []string{"inspect_metadata", "store_reference"}
	case "structured":
		return []string{"inspect_metadata", "store_reference"}
	case "archive":
		return []string{"inspect_metadata", "store_reference", "quarantine_for_review"}
	case "document":
		switch a.Subtype {
		case "pdf", "text":
			return []string{"extract_text", "inspect_metadata", "store_reference"}
		default:
			return []string{"inspect_metadata", "store_reference"}
		}
	default:
		return []string{"inspect_metadata", "store_reference"}
	}
}

func artifactDefaultRetention(a Artifact) string {
	switch a.Kind {
	case "audio":
		return "session_reference"
	case "archive":
		return "quarantine"
	default:
		return "session_reference"
	}
}

func artifactRetentionCeiling(a Artifact) string {
	switch a.Kind {
	case "audio":
		return "memory_candidate"
	case "document":
		if a.Subtype == "pdf" || a.Subtype == "text" {
			return "memory_candidate"
		}
		return "session_reference"
	case "archive":
		return "quarantine"
	default:
		return "session_reference"
	}
}

func isTextArtifactMIME(mediaType string) bool {
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	if mediaType == "" {
		return false
	}
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json",
		"application/xml",
		"application/x-yaml",
		"application/yaml",
		"application/toml",
		"application/x-toml",
		"application/javascript",
		"application/x-javascript",
		"application/sql",
		"application/x-sh",
		"application/x-httpd-php",
		"application/x-python-code":
		return true
	default:
		return false
	}
}

func isTextArtifactExt(ext string) bool {
	switch strings.TrimSpace(strings.ToLower(ext)) {
	case ".txt", ".md", ".markdown", ".json", ".yaml", ".yml", ".toml", ".xml", ".csv", ".tsv", ".log",
		".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".rb", ".rs", ".sh", ".bash", ".zsh",
		".c", ".h", ".cpp", ".hpp", ".cs", ".php", ".swift", ".kt", ".kts", ".scala", ".sql",
		".ini", ".cfg", ".conf":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
