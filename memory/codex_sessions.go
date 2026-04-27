//go:build linux

package memory

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const CodexSessionImportProvenance = "codex_session_import"

const (
	defaultCodexSessionLookback    = 14 * 24 * time.Hour
	defaultCodexSessionActiveGrace = 5 * time.Minute
	defaultCodexSessionMaxSessions = 50
	codexSessionExcerptLimit       = 700
	codexSessionSummaryMaxChars    = 24000
)

type CodexSessionImportOptions struct {
	CodexHome   string
	Lookback    time.Duration
	ActiveGrace time.Duration
	MaxSessions int
	Scope       string
	PrincipalID string
	ImportState SemanticImportState
	Now         time.Time
}

type CodexSessionImportResult struct {
	CodexHome              string
	SessionsDir            string
	Scanned                int
	Eligible               int
	Imported               int
	Updated                int
	SkippedAlreadyImported int
	SkippedOld             int
	SkippedActive          int
	SkippedEmpty           int
	Failed                 int
	Failures               []CodexSessionImportFailure
}

type CodexSessionImportFailure struct {
	Path  string
	Error string
}

type codexSessionCandidate struct {
	path       string
	sourcePath string
	modTime    time.Time
	size       int64
}

type codexSessionDraft struct {
	sessionID     string
	source        string
	modelProvider string
	cwd           string
	startedAt     time.Time
	updatedAt     time.Time
	lineCount     int
	userMessages  []string
	assistantMsgs []string
	toolCounts    map[string]int
	toolFailures  []string
}

func (e *SemanticEngine) ImportCodexSessions(ctx context.Context, opts CodexSessionImportOptions) (*CodexSessionImportResult, error) {
	if e == nil {
		return nil, fmt.Errorf("semantic engine is nil")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lookback := opts.Lookback
	if lookback <= 0 {
		lookback = defaultCodexSessionLookback
	}
	activeGrace := opts.ActiveGrace
	if activeGrace <= 0 {
		activeGrace = defaultCodexSessionActiveGrace
	}
	maxSessions := opts.MaxSessions
	if maxSessions <= 0 {
		maxSessions = defaultCodexSessionMaxSessions
	}
	scope := normalizeSemanticScope(opts.Scope)
	principalID := normalizePrincipalID(opts.PrincipalID)
	if scope == "principal" && principalID == "" {
		return nil, fmt.Errorf("principal_id is required for principal imports")
	}
	importState := opts.ImportState
	if importState == "" {
		importState = SemanticImportStateQuarantine
	}
	if err := validateImportState(importState); err != nil {
		return nil, err
	}

	codexHome, ok := ResolveCodexHome(opts.CodexHome)
	result := &CodexSessionImportResult{CodexHome: codexHome}
	if !ok {
		return result, nil
	}
	sessionsDir := filepath.Join(codexHome, "sessions")
	result.SessionsDir = sessionsDir
	info, err := os.Stat(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, fmt.Errorf("stat codex sessions dir: %w", err)
	}
	if !info.IsDir() {
		return result, nil
	}

	candidates, err := discoverCodexSessionCandidates(sessionsDir, now, lookback, activeGrace, maxSessions, result)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return result, nil
	}
	db, err := e.ensureDB()
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		record, err := loadCodexSessionImportRecord(ctx, db, scope, principalID, candidate.sourcePath)
		if err != nil {
			return nil, err
		}
		if codexSessionCandidateAlreadyImported(record, candidate) {
			result.SkippedAlreadyImported++
			continue
		}
		draft, err := parseCodexSessionFile(candidate.path)
		if err != nil {
			result.Failed++
			result.Failures = append(result.Failures, CodexSessionImportFailure{Path: candidate.sourcePath, Error: err.Error()})
			continue
		}
		if draft.empty() {
			result.SkippedEmpty++
			continue
		}
		content := renderCodexSessionSummary(draft, candidate)
		if strings.TrimSpace(content) == "" {
			result.SkippedEmpty++
			continue
		}
		metadata, err := codexSessionMetadataJSON(draft, candidate)
		if err != nil {
			return nil, err
		}
		if _, err := e.ImportDocument(ctx, SemanticImportRequest{
			Scope:            scope,
			PrincipalID:      principalID,
			SourcePath:       candidate.sourcePath,
			SourceKind:       "codex_session",
			SourceClass:      "imported_archive",
			ProvenanceSource: CodexSessionImportProvenance,
			ImportState:      importState,
			Content:          content,
			MTime:            candidate.modTime,
			MetadataJSON:     metadata,
		}); err != nil {
			result.Failed++
			result.Failures = append(result.Failures, CodexSessionImportFailure{Path: candidate.sourcePath, Error: err.Error()})
			continue
		}
		if record.Exists {
			result.Updated++
		} else {
			result.Imported++
		}
	}
	return result, nil
}

func ResolveCodexHome(override string) (string, bool) {
	candidate := strings.TrimSpace(override)
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if candidate == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", false
		}
		candidate = filepath.Join(home, ".codex")
	}
	candidate = expandLeadingTilde(candidate)
	if candidate == "" {
		return "", false
	}
	abs, err := filepath.Abs(candidate)
	if err == nil {
		candidate = abs
	}
	return filepath.Clean(candidate), true
}

func discoverCodexSessionCandidates(sessionsDir string, now time.Time, lookback time.Duration, activeGrace time.Duration, maxSessions int, result *CodexSessionImportResult) ([]codexSessionCandidate, error) {
	var candidates []codexSessionCandidate
	err := filepath.WalkDir(sessionsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		result.Scanned++
		info, err := entry.Info()
		if err != nil {
			return err
		}
		age := now.Sub(info.ModTime())
		if age > lookback {
			result.SkippedOld++
			return nil
		}
		if age < activeGrace {
			result.SkippedActive++
			return nil
		}
		rel, err := filepath.Rel(sessionsDir, path)
		if err != nil {
			return err
		}
		candidates = append(candidates, codexSessionCandidate{
			path:       path,
			sourcePath: filepath.ToSlash(filepath.Join("codex_sessions", rel)),
			modTime:    info.ModTime().UTC(),
			size:       info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk codex sessions: %w", err)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	result.Eligible = len(candidates)
	if maxSessions > 0 && len(candidates) > maxSessions {
		candidates = candidates[:maxSessions]
	}
	return candidates, nil
}

type codexSessionImportRecord struct {
	Exists       bool
	MTime        time.Time
	MetadataJSON string
}

type codexSessionImportMetadata struct {
	SourceSize  *int64 `json:"source_size"`
	SourceMTime string `json:"source_mtime"`
}

func loadCodexSessionImportRecord(ctx context.Context, db *sql.DB, scope string, principalID string, sourcePath string) (codexSessionImportRecord, error) {
	var (
		mtimeRaw     string
		metadataJSON string
	)
	if err := db.QueryRowContext(ctx, `
		SELECT mtime, metadata_json
		FROM semantic_documents
		WHERE scope = ? AND principal_id = ? AND source_path = ? AND provenance_source = ?
	`, scope, principalID, filepath.ToSlash(strings.TrimSpace(sourcePath)), CodexSessionImportProvenance).Scan(&mtimeRaw, &metadataJSON); err != nil {
		if err == sql.ErrNoRows {
			return codexSessionImportRecord{}, nil
		}
		return codexSessionImportRecord{}, fmt.Errorf("query semantic document import ledger: %w", err)
	}
	mtime, err := parseOptionalTime(mtimeRaw)
	if err != nil {
		return codexSessionImportRecord{}, fmt.Errorf("parse codex session import mtime: %w", err)
	}
	return codexSessionImportRecord{
		Exists:       true,
		MTime:        mtime,
		MetadataJSON: metadataJSON,
	}, nil
}

func codexSessionCandidateAlreadyImported(record codexSessionImportRecord, candidate codexSessionCandidate) bool {
	if !record.Exists {
		return false
	}
	metadata, _ := parseCodexSessionImportMetadata(record.MetadataJSON)
	if metadata.SourceSize != nil && *metadata.SourceSize != candidate.size {
		return false
	}
	if codexSessionImportTimesEqual(record.MTime, candidate.modTime) {
		return true
	}
	if metadata.SourceMTime != "" {
		storedMTime, err := parseOptionalTime(metadata.SourceMTime)
		if err == nil && codexSessionImportTimesEqual(storedMTime, candidate.modTime) {
			return true
		}
	}
	return false
}

func parseCodexSessionImportMetadata(raw string) (codexSessionImportMetadata, error) {
	var metadata codexSessionImportMetadata
	if strings.TrimSpace(raw) == "" {
		return metadata, nil
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return metadata, err
	}
	return metadata, nil
}

func codexSessionImportTimesEqual(stored time.Time, current time.Time) bool {
	if stored.IsZero() || current.IsZero() {
		return false
	}
	stored = stored.UTC()
	current = current.UTC()
	if stored.Equal(current) {
		return true
	}
	if stored.Nanosecond() == 0 || current.Nanosecond() == 0 {
		return stored.Truncate(time.Second).Equal(current.Truncate(time.Second))
	}
	return false
}

func parseCodexSessionFile(path string) (codexSessionDraft, error) {
	file, err := os.Open(path)
	if err != nil {
		return codexSessionDraft{}, err
	}
	defer file.Close()

	draft := codexSessionDraft{toolCounts: make(map[string]int)}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		draft.lineCount++
		var event struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(event.Timestamp)); err == nil {
			if draft.startedAt.IsZero() || ts.Before(draft.startedAt) {
				draft.startedAt = ts.UTC()
			}
			if draft.updatedAt.IsZero() || ts.After(draft.updatedAt) {
				draft.updatedAt = ts.UTC()
			}
		}
		switch event.Type {
		case "session_meta":
			applyCodexSessionMeta(&draft, event.Payload)
		case "response_item":
			applyCodexResponseItem(&draft, event.Payload)
		case "event_msg":
			applyCodexEventMessage(&draft, event.Payload)
		}
	}
	if err := scanner.Err(); err != nil {
		return codexSessionDraft{}, err
	}
	return draft, nil
}

func (d codexSessionDraft) empty() bool {
	return len(d.userMessages) == 0 &&
		len(d.assistantMsgs) == 0 &&
		len(d.toolCounts) == 0 &&
		len(d.toolFailures) == 0
}

func applyCodexSessionMeta(draft *codexSessionDraft, raw json.RawMessage) {
	var meta struct {
		ID            string `json:"id"`
		Source        string `json:"source"`
		ModelProvider string `json:"model_provider"`
		CWD           string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return
	}
	draft.sessionID = strings.TrimSpace(meta.ID)
	draft.source = strings.TrimSpace(meta.Source)
	draft.modelProvider = strings.TrimSpace(meta.ModelProvider)
	draft.cwd = strings.TrimSpace(meta.CWD)
}

func applyCodexResponseItem(draft *codexSessionDraft, raw json.RawMessage) {
	var payload struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Name    string          `json:"name"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	switch strings.TrimSpace(payload.Type) {
	case "message":
		text := extractCodexContentText(payload.Content)
		if text == "" {
			return
		}
		switch strings.TrimSpace(payload.Role) {
		case "user":
			draft.userMessages = appendLimitedStrings(draft.userMessages, redactImportedCodexText(text), 8)
		case "assistant":
			draft.assistantMsgs = appendLimitedStrings(draft.assistantMsgs, redactImportedCodexText(text), 8)
		}
	case "function_call":
		name := strings.TrimSpace(payload.Name)
		if name == "" {
			name = "tool"
		}
		draft.toolCounts[name]++
	}
}

func applyCodexEventMessage(draft *codexSessionDraft, raw json.RawMessage) {
	var payload struct {
		Type     string          `json:"type"`
		Status   string          `json:"status"`
		ExitCode *int            `json:"exit_code"`
		Command  json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	if strings.TrimSpace(payload.Type) != "exec_command_end" {
		return
	}
	if payload.ExitCode == nil || *payload.ExitCode == 0 {
		return
	}
	preview := truncateCodexImportText(extractCodexCommandPreview(payload.Command), 220)
	if preview == "" {
		preview = "exec_command"
	}
	draft.toolFailures = appendLimitedStrings(draft.toolFailures, fmt.Sprintf("exit_code=%d command=%s", *payload.ExitCode, preview), 8)
}

func extractCodexContentText(raw json.RawMessage) string {
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		text := strings.TrimSpace(part.Text)
		if text != "" {
			out = append(out, text)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n\n"))
}

func extractCodexCommandPreview(raw json.RawMessage) string {
	var asStrings []string
	if err := json.Unmarshal(raw, &asStrings); err == nil && len(asStrings) > 0 {
		return strings.Join(asStrings, " ")
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	return ""
}

func renderCodexSessionSummary(draft codexSessionDraft, candidate codexSessionCandidate) string {
	var b strings.Builder
	b.WriteString("# Imported Codex Session\n\n")
	writeCodexImportKV(&b, "source_path", candidate.sourcePath)
	if draft.sessionID != "" {
		writeCodexImportKV(&b, "session_id", draft.sessionID)
	}
	if !draft.startedAt.IsZero() {
		writeCodexImportKV(&b, "started_at", draft.startedAt.Format(time.RFC3339))
	}
	writeCodexImportKV(&b, "updated_at", utcTimestamp(candidate.modTime))
	if draft.source != "" {
		writeCodexImportKV(&b, "source", draft.source)
	}
	if draft.modelProvider != "" {
		writeCodexImportKV(&b, "model_provider", draft.modelProvider)
	}
	if draft.cwd != "" {
		writeCodexImportKV(&b, "cwd", draft.cwd)
	}
	if draft.lineCount > 0 {
		writeCodexImportKV(&b, "jsonl_events", fmt.Sprintf("%d", draft.lineCount))
	}

	writeCodexImportSection(&b, "User Goals")
	writeCodexImportList(&b, draft.userMessages, "none captured")

	writeCodexImportSection(&b, "Assistant Outcomes")
	writeCodexImportList(&b, draft.assistantMsgs, "none captured")

	writeCodexImportSection(&b, "Tool Activity")
	if len(draft.toolCounts) == 0 {
		b.WriteString("- none captured\n")
	} else {
		names := make([]string, 0, len(draft.toolCounts))
		for name := range draft.toolCounts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "- %s: %d\n", name, draft.toolCounts[name])
		}
	}

	if len(draft.toolFailures) > 0 {
		writeCodexImportSection(&b, "Failed Tool Outcomes")
		writeCodexImportList(&b, draft.toolFailures, "")
	}

	return truncateCodexImportText(b.String(), codexSessionSummaryMaxChars)
}

func writeCodexImportSection(b *strings.Builder, title string) {
	b.WriteString("\n## ")
	b.WriteString(strings.TrimSpace(title))
	b.WriteString("\n")
}

func writeCodexImportKV(b *strings.Builder, key string, value string) {
	key = strings.TrimSpace(key)
	value = redactImportedCodexText(strings.TrimSpace(value))
	if key == "" || value == "" {
		return
	}
	fmt.Fprintf(b, "- %s: %s\n", key, truncateCodexImportText(value, codexSessionExcerptLimit))
}

func writeCodexImportList(b *strings.Builder, values []string, empty string) {
	if len(values) == 0 {
		if strings.TrimSpace(empty) != "" {
			fmt.Fprintf(b, "- %s\n", empty)
		}
		return
	}
	for _, value := range values {
		value = truncateCodexImportText(redactImportedCodexText(value), codexSessionExcerptLimit)
		if value != "" {
			fmt.Fprintf(b, "- %s\n", value)
		}
	}
}

func codexSessionMetadataJSON(draft codexSessionDraft, candidate codexSessionCandidate) (string, error) {
	payload := map[string]any{
		"import_contract": "codex_session_summary_v1",
		"session_id":      draft.sessionID,
		"source_path":     candidate.sourcePath,
		"source_size":     candidate.size,
		"source_mtime":    utcTimestamp(candidate.modTime),
		"jsonl_events":    draft.lineCount,
		"redacted":        true,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func appendLimitedStrings(values []string, next string, limit int) []string {
	next = truncateCodexImportText(strings.TrimSpace(next), codexSessionExcerptLimit)
	if next == "" {
		return values
	}
	if limit <= 0 || len(values) < limit {
		return append(values, next)
	}
	return values
}

func truncateCodexImportText(raw string, limit int) string {
	raw = strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if raw == "" || limit <= 0 {
		return raw
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-3])) + "..."
}

var codexImportSecretRedactions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)((?:bot_token|telegram_bot_token|api_key|openai_api_key|elevenlabs_api_key|access_token|refresh_token|secret|password)\s*=\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)("(?:bot_token|telegram_bot_token|api_key|openai_api_key|elevenlabs_api_key|access_token|refresh_token|secret|password)"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY)[A-Z0-9_]*=)[^\s]+`),
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;"}]+`),
}

var codexImportEmailRedaction = regexp.MustCompile(`(?i)[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}`)

func redactImportedCodexText(text string) string {
	out := text
	for _, re := range codexImportSecretRedactions {
		out = re.ReplaceAllString(out, `${1}<redacted>${2}`)
	}
	out = codexImportEmailRedaction.ReplaceAllString(out, "<email-redacted>")
	return out
}

func expandLeadingTilde(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}
