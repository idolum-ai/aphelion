//go:build linux

package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	StoreMemory    = "memory"
	StoreKnowledge = "knowledge"
	StoreDecisions = "decisions"
	StoreQuestions = "questions"
	StoreRhizome   = "rhizome"
)

type WriteRequest struct {
	Root       string
	Store      string
	Action     string
	Content    string
	Match      string
	SourceTag  string
	Confidence *float64
}

type WriteResult struct {
	Path   string
	Store  string
	Action string
}

func ResolveStorePath(root string, store string) (string, string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", "", fmt.Errorf("memory root is required")
	}

	switch normalizeStore(store) {
	case StoreMemory:
		return filepath.Join(root, "MEMORY.md"), StoreMemory, nil
	case StoreKnowledge:
		return filepath.Join(root, "memory", "knowledge.md"), StoreKnowledge, nil
	case StoreDecisions:
		return filepath.Join(root, "memory", "decisions.md"), StoreDecisions, nil
	case StoreQuestions:
		return filepath.Join(root, "memory", "questions.md"), StoreQuestions, nil
	case StoreRhizome:
		return filepath.Join(root, "memory", "rhizome.md"), StoreRhizome, nil
	default:
		return "", "", fmt.Errorf("unsupported memory store %q", store)
	}
}

func ApplyWrite(req WriteRequest) (*WriteResult, error) {
	path, store, err := ResolveStorePath(req.Root, req.Store)
	if err != nil {
		return nil, err
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "add"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create memory store directory: %w", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read memory store %s: %w", path, err)
	}
	current := string(raw)

	switch action {
	case "add":
		entry := formatEntry(store, req.Content, req.SourceTag, req.Confidence)
		if entry == "" {
			return nil, fmt.Errorf("memory content is required for add")
		}
		current = appendEntry(current, entry)
	case "replace":
		if strings.TrimSpace(req.Match) == "" {
			return nil, fmt.Errorf("memory match is required for replace")
		}
		replacement := formatEntry(store, req.Content, req.SourceTag, req.Confidence)
		if replacement == "" {
			return nil, fmt.Errorf("memory content is required for replace")
		}
		next, ok := replaceOnce(current, req.Match, replacement)
		if !ok {
			return nil, fmt.Errorf("memory match not found")
		}
		current = next
	case "remove":
		if strings.TrimSpace(req.Match) == "" {
			return nil, fmt.Errorf("memory match is required for remove")
		}
		next, ok := replaceOnce(current, req.Match, "")
		if !ok {
			return nil, fmt.Errorf("memory match not found")
		}
		current = normalizeSpacing(next)
	default:
		return nil, fmt.Errorf("unsupported memory action %q", req.Action)
	}

	if err := os.WriteFile(path, []byte(strings.TrimSpace(current)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write memory store %s: %w", path, err)
	}

	return &WriteResult{
		Path:   path,
		Store:  store,
		Action: action,
	}, nil
}

func normalizeStore(store string) string {
	switch strings.ToLower(strings.TrimSpace(store)) {
	case "", "memory":
		return StoreMemory
	case "knowledge":
		return StoreKnowledge
	case "decisions":
		return StoreDecisions
	case "questions":
		return StoreQuestions
	case "rhizome":
		return StoreRhizome
	default:
		return strings.ToLower(strings.TrimSpace(store))
	}
}

func formatEntry(store string, content string, sourceTag string, confidence *float64) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	tag := renderProvenance(sourceTag, confidence)
	if normalizeStore(store) == StoreMemory {
		if tag == "" {
			return content
		}
		if strings.Contains(content, "\n") {
			return content + "\n" + tag
		}
		return content + " " + tag
	}

	entry := content
	if strings.Contains(entry, "\n") {
		return entry
	}
	if !strings.HasPrefix(entry, "- ") {
		entry = "- " + entry
	}
	if tag != "" {
		entry += " " + tag
	}
	return entry
}

func renderProvenance(sourceTag string, confidence *float64) string {
	sourceTag = strings.ToLower(strings.TrimSpace(sourceTag))
	parts := make([]string, 0, 2)
	if sourceTag != "" {
		parts = append(parts, sourceTag)
	}
	if confidence != nil {
		clamped := *confidence
		if clamped < 0 {
			clamped = 0
		}
		if clamped > 1 {
			clamped = 1
		}
		parts = append(parts, "confidence: "+strconv.FormatFloat(clamped, 'f', 2, 64))
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func appendEntry(current string, entry string) string {
	current = strings.TrimSpace(current)
	entry = strings.TrimSpace(entry)
	if current == "" {
		return entry
	}
	if entry == "" {
		return current
	}
	return current + "\n\n" + entry
}

func replaceOnce(current string, match string, replacement string) (string, bool) {
	idx := strings.Index(current, match)
	if idx < 0 {
		return current, false
	}
	return current[:idx] + replacement + current[idx+len(match):], true
}

func normalizeSpacing(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if strings.TrimSpace(trimmed) == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
