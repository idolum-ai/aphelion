//go:build linux

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const DefaultToolOutputDigestInlineLimit = 16 * 1024

type ToolOutputDigest struct {
	Bytes        int    `json:"bytes"`
	Lines        int    `json:"lines"`
	SHA256       string `json:"sha256"`
	EvidenceRef  string `json:"evidence_ref,omitempty"`
	Head         string `json:"head"`
	Tail         string `json:"tail"`
	HeadBytes    int    `json:"head_bytes"`
	TailBytes    int    `json:"tail_bytes"`
	OmittedBytes int    `json:"omitted_bytes"`
	OmittedLines int    `json:"omitted_lines"`
}

func BuildToolOutputDigest(output string, inlineLimit int) (ToolOutputDigest, bool) {
	if inlineLimit <= 0 {
		inlineLimit = DefaultToolOutputDigestInlineLimit
	}
	if len(output) <= inlineLimit {
		return ToolOutputDigest{}, false
	}
	headBudget := inlineLimit / 2
	tailBudget := inlineLimit - headBudget
	head, headEnd := prefixUTF8ByBytes(output, headBudget)
	tail, tailStart := suffixUTF8ByBytes(output, tailBudget)
	if headEnd >= tailStart {
		return ToolOutputDigest{}, false
	}
	omitted := output[headEnd:tailStart]
	sum := sha256.Sum256([]byte(output))
	return ToolOutputDigest{
		Bytes:        len(output),
		Lines:        countToolOutputLines(output),
		SHA256:       "sha256:" + hex.EncodeToString(sum[:]),
		Head:         head,
		Tail:         tail,
		HeadBytes:    len(head),
		TailBytes:    len(tail),
		OmittedBytes: len(omitted),
		OmittedLines: countToolOutputLines(omitted),
	}, true
}

func FormatToolOutputForHistory(output string, inlineLimit int) string {
	digest, ok := BuildToolOutputDigest(output, inlineLimit)
	if !ok {
		return output
	}
	return digest.Render()
}

func (d ToolOutputDigest) Render() string {
	var b strings.Builder
	b.WriteString("[TOOL_OUTPUT_DIGEST]\n")
	fmt.Fprintf(&b, "bytes: %d\n", d.Bytes)
	fmt.Fprintf(&b, "lines: %d\n", d.Lines)
	fmt.Fprintf(&b, "sha256: %s\n", d.SHA256)
	if strings.TrimSpace(d.EvidenceRef) != "" {
		fmt.Fprintf(&b, "evidence_ref: %s\n", strings.TrimSpace(d.EvidenceRef))
	}
	fmt.Fprintf(&b, "head_bytes: %d\n", d.HeadBytes)
	fmt.Fprintf(&b, "tail_bytes: %d\n", d.TailBytes)
	fmt.Fprintf(&b, "omitted_bytes: %d\n", d.OmittedBytes)
	fmt.Fprintf(&b, "omitted_lines: %d\n", d.OmittedLines)
	b.WriteString("head:\n")
	b.WriteString(d.Head)
	if !strings.HasSuffix(d.Head, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("[... omitted middle tool output ...]\n")
	b.WriteString("tail:\n")
	b.WriteString(d.Tail)
	if !strings.HasSuffix(d.Tail, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("[/TOOL_OUTPUT_DIGEST]")
	return b.String()
}

func (d ToolOutputDigest) Payload() map[string]any {
	return map[string]any{
		"bytes":         d.Bytes,
		"lines":         d.Lines,
		"sha256":        d.SHA256,
		"evidence_ref":  strings.TrimSpace(d.EvidenceRef),
		"head":          d.Head,
		"tail":          d.Tail,
		"head_bytes":    d.HeadBytes,
		"tail_bytes":    d.TailBytes,
		"omitted_bytes": d.OmittedBytes,
		"omitted_lines": d.OmittedLines,
	}
}

func prefixUTF8ByBytes(raw string, limit int) (string, int) {
	if limit <= 0 {
		return "", 0
	}
	if len(raw) <= limit {
		return strings.ToValidUTF8(raw, "�"), len(raw)
	}
	return strings.ToValidUTF8(raw[:limit], "�"), limit
}

func suffixUTF8ByBytes(raw string, limit int) (string, int) {
	if limit <= 0 {
		return "", len(raw)
	}
	if len(raw) <= limit {
		return strings.ToValidUTF8(raw, "�"), 0
	}
	start := len(raw) - limit
	return strings.ToValidUTF8(raw[start:], "�"), start
}

func countToolOutputLines(raw string) int {
	if raw == "" {
		return 0
	}
	return strings.Count(raw, "\n") + 1
}
