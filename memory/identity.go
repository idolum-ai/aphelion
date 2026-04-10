//go:build linux

package memory

import "strings"

const (
	IdentityBeginMarker = "<!-- APHELION:IDENTITY-BEGIN -->"
	IdentityEndMarker   = "<!-- APHELION:IDENTITY-END -->"
)

func ExtractIdentityBlock(raw string) string {
	start := strings.Index(raw, IdentityBeginMarker)
	if start < 0 {
		return ""
	}
	end := strings.Index(raw[start+len(IdentityBeginMarker):], IdentityEndMarker)
	if end < 0 {
		return ""
	}
	end += start + len(IdentityBeginMarker) + len(IdentityEndMarker)
	return strings.TrimSpace(raw[start:end])
}

func PreserveMemoryIdentity(raw string) (string, bool) {
	preserved := ExtractIdentityBlock(raw)
	if strings.TrimSpace(preserved) == "" {
		return "", false
	}

	content := strings.TrimSpace(strings.Join([]string{
		"# MEMORY.md — Shared Curated Memory",
		"",
		"Keep this file concise.",
		"",
		preserved,
	}, "\n"))
	return content + "\n", true
}
