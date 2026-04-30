//go:build linux

package runtime

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func materializeGeneratedReplyMedia(scope sandbox.Scope, media []core.Media) ([]core.Media, error) {
	if len(media) == 0 {
		return nil, nil
	}
	out := make([]core.Media, 0, len(media))
	for i, item := range media {
		if len(item.Data) == 0 || strings.TrimSpace(item.Path) != "" {
			out = append(out, item)
			continue
		}
		root := strings.TrimSpace(scope.WorkingRoot)
		if root == "" {
			return nil, fmt.Errorf("materialize generated media: working root is unavailable")
		}
		filename := generatedMediaFilename(item, i)
		path := filepath.Join(root, "generated", "image-generation", filename)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("materialize generated media: mkdir: %w", err)
		}
		if err := os.WriteFile(path, item.Data, 0o600); err != nil {
			return nil, fmt.Errorf("materialize generated media: write %s: %w", path, err)
		}
		item.Path = path
		item.Data = nil
		if strings.TrimSpace(item.MimeType) == "" {
			item.MimeType = http.DetectContentType(media[i].Data)
		}
		out = append(out, item)
	}
	return out, nil
}

func generatedMediaFilename(media core.Media, index int) string {
	name := sanitizeGeneratedMediaFilename(media.Filename)
	if name == "" {
		ext := strings.TrimSpace(filepath.Ext(name))
		if ext == "" {
			ext = generatedMediaExtension(media)
		}
		name = fmt.Sprintf("generated-image-%d%s", index+1, ext)
	}
	if filepath.Ext(name) == "" {
		name += generatedMediaExtension(media)
	}
	return name
}

func generatedMediaExtension(media core.Media) string {
	mimeType := strings.TrimSpace(media.MimeType)
	if mimeType == "" && len(media.Data) > 0 {
		mimeType = http.DetectContentType(media.Data)
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	if strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return ".png"
	}
	return ".bin"
}

func sanitizeGeneratedMediaFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}
