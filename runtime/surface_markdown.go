//go:build linux

package runtime

import "strings"

func extractDeliberationSurfaceMarkdown(raw string) (surface string, cleaned string) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}

	lines := strings.Split(raw, "\n")
	kept := make([]string, 0, len(lines))
	surfaces := make([]string, 0, 2)
	for i := 0; i < len(lines); {
		if isSurfaceMarkdownHeading(lines[i]) {
			j := i + 1
			blockLines := make([]string, 0, 4)
			for j < len(lines) {
				line := lines[j]
				if isGenericMarkdownHeading(line) {
					break
				}
				if isSurfaceLabelLine(line) {
					break
				}
				if isStructuredDirectiveLine(line) {
					break
				}
				if strings.TrimSpace(line) == "" && len(blockLines) > 0 {
					break
				}
				if strings.TrimSpace(line) != "" {
					blockLines = append(blockLines, line)
				}
				j++
			}
			if block := strings.TrimSpace(strings.Join(blockLines, "\n")); block != "" {
				surfaces = append(surfaces, block)
			}
			i = j
			continue
		}
		if firstLine, ok := parseSurfaceLabelLine(lines[i]); ok {
			j := i + 1
			blockLines := make([]string, 0, 4)
			if firstLine != "" {
				blockLines = append(blockLines, firstLine)
			}
			for j < len(lines) {
				line := lines[j]
				if isGenericMarkdownHeading(line) {
					break
				}
				if isSurfaceLabelLine(line) {
					break
				}
				if isStructuredDirectiveLine(line) {
					break
				}
				if strings.TrimSpace(line) == "" && len(blockLines) > 0 {
					break
				}
				if strings.TrimSpace(line) != "" {
					blockLines = append(blockLines, line)
				}
				j++
			}
			if block := strings.TrimSpace(strings.Join(blockLines, "\n")); block != "" {
				surfaces = append(surfaces, block)
			}
			i = j
			continue
		}
		kept = append(kept, lines[i])
		i++
	}
	cleaned = strings.TrimSpace(strings.Join(kept, "\n"))
	return strings.TrimSpace(strings.Join(surfaces, "\n\n")), cleaned
}

func isSurfaceMarkdownHeading(line string) bool {
	title, ok := markdownHeadingTitle(line)
	if !ok {
		return false
	}
	title = strings.TrimSuffix(strings.TrimSpace(strings.ToLower(title)), ":")
	return title == "surface"
}

func isGenericMarkdownHeading(line string) bool {
	_, ok := markdownHeadingTitle(line)
	return ok
}

func markdownHeadingTitle(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed[0] != '#' {
		return "", false
	}
	hashes := 0
	for hashes < len(trimmed) && trimmed[hashes] == '#' {
		hashes++
	}
	if hashes == 0 || hashes > 6 || hashes >= len(trimmed) {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[hashes:])
	if rest == "" {
		return "", false
	}
	return rest, true
}

func isStructuredDirectiveLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) != 2 {
		return false
	}
	key := strings.TrimSpace(parts[0])
	if key == "" {
		return false
	}
	for _, r := range key {
		if r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') {
			continue
		}
		return false
	}
	return true
}

func isSurfaceLabelLine(line string) bool {
	_, ok := parseSurfaceLabelLine(line)
	return ok
}

func parseSurfaceLabelLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}
	colon := strings.IndexByte(trimmed, ':')
	if colon <= 0 {
		return "", false
	}
	label := strings.ToLower(strings.TrimSpace(trimmed[:colon]))
	if label != "surface" {
		return "", false
	}
	return strings.TrimSpace(trimmed[colon+1:]), true
}
