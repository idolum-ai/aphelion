//go:build linux

package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/workspace"
)

func writeDoctorFileStat(b *strings.Builder, root string, rel string) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			WriteLine(b, fmt.Sprintf("- file=%s missing=true", rel))
			return
		}
		WriteLine(b, fmt.Sprintf("- file=%s error=%q", rel, err.Error()))
		return
	}
	if info.IsDir() {
		WriteLine(b, fmt.Sprintf("- file=%s directory=true", rel))
		return
	}
	WriteLine(b, fmt.Sprintf("- file=%s bytes=%d modified=%s", rel, info.Size(), info.ModTime().UTC().Format(time.RFC3339)))
}

func writeDoctorDirStat(b *strings.Builder, dir string, label string) {
	var count int
	var bytes int64
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		bytes += info.Size()
		_ = path
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			WriteLine(b, fmt.Sprintf("- dir=%s missing=true", label))
			return
		}
		WriteLine(b, fmt.Sprintf("- dir=%s error=%q", label, err.Error()))
		return
	}
	WriteLine(b, fmt.Sprintf("- dir=%s files=%d bytes=%d", label, count, bytes))
}

func (r *Runtime) writeDoctorExecutionEvents(ctx context.Context, b *strings.Builder, key session.SessionKey, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	chatEvents, err := r.store.ExecutionEventsByChat(key.ChatID, now.Add(-24*time.Hour), 60)
	if err != nil {
		WriteLine(b, "chat_events_error="+strconv.Quote(err.Error()))
	} else {
		WriteLine(b, "chat_events_last_24h:")
		writeDoctorEvents(b, chatEvents, 20)
	}
	recentEvents, err := r.store.ExecutionEventsRecent(80)
	if err != nil {
		WriteLine(b, "recent_events_error="+strconv.Quote(err.Error()))
	} else {
		WriteLine(b, "recent_system_events:")
		writeDoctorEvents(b, recentEvents, 25)
	}
	_ = ctx
}

func (r *Runtime) writeDoctorRuntimeAdjudications(ctx context.Context, b *strings.Builder, key session.SessionKey, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	chatEvents, err := r.store.ExecutionEventsByChat(key.ChatID, now.Add(-24*time.Hour), 120)
	if err != nil {
		WriteLine(b, "adjudications_error="+strconv.Quote(err.Error()))
		return
	}
	adjudications := statusAdjudicationsFromExecutionEvents(chatEvents, 8)
	if len(adjudications) == 0 {
		WriteLine(b, "- none")
		return
	}
	for _, adjudication := range adjudications {
		findingKinds := make([]string, 0, len(adjudication.Findings))
		details := make([]string, 0, len(adjudication.Findings))
		for _, finding := range adjudication.Findings {
			finding = core.NormalizeRuntimeFinding(finding)
			if finding.Kind != "" {
				findingKinds = append(findingKinds, finding.Kind)
			}
			if finding.Detail != "" {
				details = append(details, finding.Detail)
			}
		}
		WriteLine(b, fmt.Sprintf("- time=%s chat_id=%d seq=%d kind=%s surface=%s action=%s label=%q subject=%s refs=%q findings=%q detail=%q next=%q",
			adjudication.CreatedAt.UTC().Format(time.RFC3339),
			adjudication.ChatID,
			adjudication.Seq,
			strings.TrimSpace(adjudication.Kind),
			strings.TrimSpace(adjudication.Surface),
			strings.TrimSpace(adjudication.VisibleAction),
			strings.TrimSpace(adjudication.OperatorLabel),
			strings.TrimSpace(adjudication.SubjectID),
			strings.Join(adjudication.EvidenceRefs, ","),
			strings.Join(findingKinds, ","),
			truncatePreview(strings.Join(details, "; "), 260),
			doctorAdjudicationNextAction(adjudication),
		))
	}
	_ = ctx
}

func doctorAdjudicationNextAction(adjudication core.AdjudicationStatusSnapshot) string {
	switch strings.TrimSpace(adjudication.VisibleAction) {
	case "repair_completed_or_superseded_approval":
		return "Ask for a new bounded follow-up if more work remains."
	case "repair_invalid_pending_approval", "repair_stale_continuation_projection":
		return "Use the fresh eligible proposal; do not press stale approval buttons."
	case "blocked_status":
		return "Resolve the named blocker, then request a fresh bounded approval."
	default:
		return ""
	}
}

func writeDoctorEvents(b *strings.Builder, events []session.ExecutionEvent, limit int) {
	if len(events) == 0 {
		WriteLine(b, "- none")
		return
	}
	if limit <= 0 || limit > len(events) {
		limit = len(events)
	}
	for i := 0; i < limit; i++ {
		event := events[i]
		WriteLine(b, fmt.Sprintf("- time=%s chat_id=%d seq=%d type=%s stage=%s status=%s payload=%s",
			event.CreatedAt.UTC().Format(time.RFC3339),
			event.ChatID,
			event.Seq,
			strings.TrimSpace(event.EventType),
			strings.TrimSpace(event.Stage),
			strings.TrimSpace(event.Status),
			strconv.Quote(truncatePreview(event.PayloadJSON, 500)),
		))
	}
}

func (r *Runtime) writeDoctorTurnRuns(ctx context.Context, b *strings.Builder, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	latest, latestErr := r.store.LatestTurnRunsByChat(40)
	if latestErr != nil {
		WriteLine(b, "latest_turn_runs_error="+strconv.Quote(latestErr.Error()))
	} else {
		WriteLine(b, "latest_turn_runs_by_chat:")
		writeDoctorRuns(b, latest, 20)
	}
	pending, pendingErr := r.store.PendingRecoveryTurnRuns(40)
	if pendingErr != nil {
		WriteLine(b, "pending_recovery_error="+strconv.Quote(pendingErr.Error()))
	} else {
		WriteLine(b, "pending_recovery_runs:")
		writeDoctorRuns(b, pending, 12)
	}
	var stale []session.TurnRun
	var staleErr error
	if r.staleRunningTurnRuns != nil {
		stale, staleErr = r.staleRunningTurnRuns(now)
	}
	if staleErr != nil {
		WriteLine(b, "stale_turn_runs_error="+strconv.Quote(staleErr.Error()))
	} else {
		WriteLine(b, "stale_running_turns:")
		writeDoctorRuns(b, stale, 12)
	}
	_ = ctx
}

func writeDoctorRuns(b *strings.Builder, runs []session.TurnRun, limit int) {
	if len(runs) == 0 {
		WriteLine(b, "- none")
		return
	}
	if limit <= 0 || limit > len(runs) {
		limit = len(runs)
	}
	for i := 0; i < limit; i++ {
		run := runs[i]
		WriteLine(b, fmt.Sprintf("- id=%d chat_id=%d kind=%s status=%s started=%s last_activity=%s tools=%d/%d request=%q last_tool=%q last_error=%q",
			run.ID,
			run.ChatID,
			run.Kind,
			run.Status,
			run.StartedAt.UTC().Format(time.RFC3339),
			run.LastActivityAt.UTC().Format(time.RFC3339),
			run.ToolCallsFinished,
			run.ToolCallsStarted,
			truncatePreview(run.RequestText, 260),
			truncatePreview(run.LastToolName, 120),
			truncatePreview(run.ErrorText, 220),
		))
	}
}

func (r *Runtime) writeDoctorSemanticStats(b *strings.Builder) {
	if r == nil || r.cfg == nil {
		return
	}
	dbPath := filepath.Join(filepath.Dir(r.cfg.Sessions.DBPath), "semantic.db")
	WriteKV(b, "semantic_enabled", strconv.FormatBool(r.cfg.Memory.Semantic.Enabled))
	WriteKV(b, "semantic_db_path", dbPath)
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			WriteLine(b, "semantic_db_missing=true")
			return
		}
		WriteLine(b, "semantic_db_stat_error="+strconv.Quote(err.Error()))
		return
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		WriteLine(b, "semantic_db_open_error="+strconv.Quote(err.Error()))
		return
	}
	defer db.Close()
	var docs, chunks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM semantic_documents`).Scan(&docs); err != nil {
		WriteLine(b, "semantic_documents_error="+strconv.Quote(err.Error()))
		return
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM semantic_chunks`).Scan(&chunks); err != nil {
		WriteLine(b, "semantic_chunks_error="+strconv.Quote(err.Error()))
		return
	}
	WriteKV(b, "semantic_documents", strconv.Itoa(docs))
	WriteKV(b, "semantic_chunks", strconv.Itoa(chunks))
	rows, err := db.Query(`SELECT import_state, COUNT(*) FROM semantic_documents GROUP BY import_state ORDER BY import_state`)
	if err != nil {
		WriteLine(b, "semantic_import_state_error="+strconv.Quote(err.Error()))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			WriteLine(b, "semantic_import_state_scan_error="+strconv.Quote(err.Error()))
			return
		}
		WriteLine(b, fmt.Sprintf("- import_state=%s documents=%d", state, count))
	}
}

func (r *Runtime) writeDoctorTailnetDiagnostics(ctx context.Context, b *strings.Builder) {
	if r == nil || r.cfg == nil {
		WriteLine(b, "tailnet: runtime unavailable")
		return
	}
	WriteKV(b, "tailscale_enabled", strconv.FormatBool(r.cfg.Tailscale.Enabled))
	WriteKV(b, "tailscale_backend", strings.TrimSpace(r.cfg.Tailscale.Backend))
	WriteKV(b, "tailscale_expected_tailnet", strings.TrimSpace(r.cfg.Tailscale.ExpectedTailnet))
	WriteKV(b, "tailscale_expected_hostname", strings.TrimSpace(r.cfg.Tailscale.ExpectedHostname))
	WriteKV(b, "tailscale_expected_tags", strings.Join(r.cfg.Tailscale.ExpectedTags, ","))
	if r.TailnetStatusSnapshot == nil {
		WriteLine(b, "tailnet_snapshot: unavailable")
		return
	}
	snapshot, err := r.TailnetStatusSnapshot(ctx)
	if err != nil {
		WriteLine(b, "tailnet_snapshot_error="+strconv.Quote(err.Error()))
		return
	}
	WriteKV(b, "tailnet_status", snapshot.Status)
	WriteKV(b, "tailnet_summary", snapshot.Summary)
	WriteKV(b, "tailnet_backend_state", snapshot.BackendState)
	WriteKV(b, "tailnet_node", firstNonEmpty(strings.TrimSpace(snapshot.DNSName), strings.TrimSpace(snapshot.HostName)))
	WriteKV(b, "tailnet_name", snapshot.TailnetName)
	WriteKV(b, "tailnet_ips", strings.Join(snapshot.TailscaleIPs, ","))
	WriteKV(b, "tailnet_tags", strings.Join(snapshot.Tags, ","))
	WriteKV(b, "tailnet_netcheck", snapshot.NetcheckSummary)
	if snapshot.Parent != nil {
		parent := snapshot.Parent
		WriteKV(b, "tailnet_parent_enabled", strconv.FormatBool(parent.Enabled))
		WriteKV(b, "tailnet_parent_running", strconv.FormatBool(parent.Running))
		WriteKV(b, "tailnet_parent_hostname", parent.Hostname)
		WriteKV(b, "tailnet_parent_state_dir", parent.StateDir)
		WriteKV(b, "tailnet_parent_listen_addr", parent.ListenAddr)
		WriteKV(b, "tailnet_parent_magic_url", parent.MagicDNSURL)
		WriteKV(b, "tailnet_parent_auth_key_source", parent.AuthKeySource)
		WriteKV(b, "tailnet_parent_last_error", parent.LastError)
	}
	if len(snapshot.Surfaces) == 0 {
		WriteLine(b, "tailnet_surfaces: none")
	} else {
		WriteLine(b, "tailnet_surfaces:")
		for _, surface := range snapshot.Surfaces {
			WriteLine(b, fmt.Sprintf("- id=%s status=%s kind=%s name=%s url=%q error=%q", strings.TrimSpace(surface.SurfaceID), strings.TrimSpace(surface.Status), strings.TrimSpace(surface.SurfaceKind), strings.TrimSpace(surface.Name), truncatePreview(surface.URL, 220), truncatePreview(surface.LastError, 220)))
		}
	}
	if len(snapshot.GrantBindings) == 0 {
		WriteLine(b, "tailnet_grant_bindings: none")
	} else {
		WriteLine(b, "tailnet_grant_bindings:")
		for _, binding := range snapshot.GrantBindings {
			WriteLine(b, fmt.Sprintf("- id=%s status=%s grant=%s surface=%s target=%s drift=%q", strings.TrimSpace(binding.BindingID), strings.TrimSpace(binding.Status), strings.TrimSpace(binding.GrantID), strings.TrimSpace(binding.SurfaceID), strings.TrimSpace(binding.TargetResource), truncatePreview(binding.DriftReason, 220)))
		}
	}
	if len(snapshot.Issues) == 0 {
		WriteLine(b, "tailnet_issues: none")
		return
	}
	WriteLine(b, "tailnet_issues:")
	for _, issue := range snapshot.Issues {
		WriteLine(b, fmt.Sprintf("- code=%s severity=%s summary=%q", strings.TrimSpace(issue.Code), strings.TrimSpace(issue.Severity), truncatePreview(issue.Summary, 300)))
	}
}

func (r *Runtime) writeDoctorLogTail(b *strings.Builder) {
	if r == nil || r.cfg == nil {
		return
	}
	logPath := filepath.Join(filepath.Dir(r.cfg.Sessions.DBPath), "aphelion.log")
	WriteKV(b, "log_path", logPath)
	data, err := readDoctorTail(logPath, LogTailBytes)
	if err != nil {
		if os.IsNotExist(err) {
			WriteLine(b, "log_missing=true")
			return
		}
		WriteLine(b, "log_tail_error="+strconv.Quote(err.Error()))
		return
	}
	text := strings.TrimSpace(RedactText(string(data)))
	if text == "" {
		WriteLine(b, "log_tail_empty=true")
		return
	}
	WriteLine(b, text)
}

func readDoctorTail(path string, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = LogTailBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	offset := int64(0)
	if size > limit {
		offset = size - limit
	}
	if _, err := file.Seek(offset, 0); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}

func doctorPathListContains(paths []string, want string) bool {
	want = filepath.ToSlash(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	for _, path := range paths {
		if filepath.ToSlash(strings.TrimSpace(path)) == want {
			return true
		}
	}
	return false
}

func doctorPromptContextHasFile(ctx *workspace.PromptContext, want string) bool {
	if ctx == nil {
		return false
	}
	want = filepath.ToSlash(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	for _, file := range append(append([]workspace.LoadedFile{}, ctx.Stable...), ctx.Dynamic...) {
		path := filepath.ToSlash(strings.TrimSpace(file.Path))
		if path == want || strings.HasSuffix(path, "/"+want) {
			return true
		}
	}
	return false
}

func doctorPromptIdentityStatus(ctx *workspace.PromptContext) (string, string) {
	if ctx == nil {
		return "unknown", "prompt context unavailable"
	}

	var stale []string
	var sawSystem bool
	var sawHarness bool
	for _, file := range ctx.Stable {
		path := filepath.ToSlash(strings.TrimSpace(file.Path))
		content := strings.TrimSpace(file.Content)
		if path == "" || content == "" {
			continue
		}
		lower := strings.ToLower(content)
		switch {
		case strings.Contains(lower, "aphelion is the governor"),
			strings.Contains(lower, "aphelion decides"),
			strings.Contains(lower, "final authority still belongs to aphelion"),
			strings.Contains(lower, "aphelion authorizes"):
			stale = append(stale, path)
		}
		if strings.Contains(content, "Idolum (System)") {
			sawSystem = true
		}
		if strings.Contains(lower, "aphelion") &&
			(strings.Contains(lower, "repo/service/harness") ||
				strings.Contains(lower, "repo") ||
				strings.Contains(lower, "service") ||
				strings.Contains(lower, "harness")) {
			sawHarness = true
		}
	}
	if len(stale) > 0 {
		return "active", "stable prompt files still contain stale Aphelion-governor claims: " + strings.Join(uniqueDoctorPaths(stale), ", ")
	}
	if sawSystem && sawHarness {
		return "likely_fixed", "stable prompt files identify Idolum (System) as governor/system and Aphelion as repo/service/harness"
	}
	if sawSystem {
		return "residual_risk", "stable prompt files name Idolum (System), but did not clearly bind Aphelion to repo/service/harness"
	}
	return "unknown", "canonical governor/system identity was not confirmed in loaded stable prompt files"
}

func doctorSourceContainsAll(root string, rel string, needles []string) bool {
	root = strings.TrimSpace(root)
	rel = filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if root == "" || rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return false
	}
	text := string(data)
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}

func doctorSourceMatches(root string, dirs []string, needles []string, includeTests bool, limit int) []string {
	root = strings.TrimSpace(root)
	if root == "" || limit == 0 {
		return nil
	}
	if limit < 0 {
		limit = 8
	}
	lowerNeedles := make([]string, 0, len(needles))
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" {
			lowerNeedles = append(lowerNeedles, needle)
		}
	}
	if len(lowerNeedles) == 0 {
		return nil
	}

	var matches []string
	for _, dir := range dirs {
		if len(matches) >= limit {
			break
		}
		relDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(dir)))
		if relDir == "" || relDir == "." || strings.HasPrefix(relDir, "..") {
			continue
		}
		base := filepath.Join(root, relDir)
		if err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil || len(matches) >= limit {
				return nil
			}
			if entry.IsDir() {
				name := entry.Name()
				if name == ".git" || name == "vendor" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || (!includeTests && strings.HasSuffix(name, "_test.go")) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			text := strings.ToLower(string(data))
			for _, needle := range lowerNeedles {
				if strings.Contains(text, needle) {
					if rel, relErr := filepath.Rel(root, path); relErr == nil {
						rel = filepath.ToSlash(rel)
						if rel == "runtime/doctor.go" {
							break
						}
						matches = append(matches, rel)
					} else {
						matches = append(matches, filepath.ToSlash(path))
					}
					break
				}
			}
			return nil
		}); err != nil {
			continue
		}
	}
	sort.Strings(matches)
	return matches
}

func uniqueDoctorPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func WriteSection(b *strings.Builder, title string) {
	WriteLine(b, "")
	WriteLine(b, "## "+strings.TrimSpace(title))
}

func WriteKV(b *strings.Builder, key string, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return
	}
	WriteLine(b, key+"="+strconv.Quote(value))
}

func WriteLine(b *strings.Builder, line string) {
	if b == nil {
		return
	}
	b.WriteString(strings.TrimRight(line, "\n"))
	b.WriteByte('\n')
}

var doctorSecretRedactions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)((?:bot_token|telegram_bot_token|api_key|openai_api_key|elevenlabs_api_key|access_token|refresh_token|secret|password)\s*=\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)("(?:bot_token|telegram_bot_token|api_key|openai_api_key|elevenlabs_api_key|access_token|refresh_token|secret|password)"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY)[A-Z0-9_]*=)[^\s]+`),
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;"}]+`),
	regexp.MustCompile(`(?i)("(?:authorization)"\s*:\s*"bearer\s+)[^"]*(")`),
	regexp.MustCompile(`(?i)((?:x-api-key|api-key)\s*[:=]\s*)[^\s,;"}]+`),
}

func RedactText(text string) string {
	out := text
	for _, re := range doctorSecretRedactions {
		out = re.ReplaceAllString(out, `${1}<redacted>${2}`)
	}
	return out
}
