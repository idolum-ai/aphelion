//go:build linux

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

const (
	tailnetMaintenanceChatID int64 = -1
)

type tailnetCommandReport struct {
	Action          string                 `json:"action"`
	Status          string                 `json:"status"`
	ConfigPath      string                 `json:"config_path,omitempty"`
	Enabled         bool                   `json:"enabled"`
	Backend         string                 `json:"backend,omitempty"`
	ExpectedTailnet string                 `json:"expected_tailnet,omitempty"`
	SurfaceID       string                 `json:"surface_id,omitempty"`
	Reason          string                 `json:"reason,omitempty"`
	Surfaces        []tailnetSurfaceReport `json:"surfaces,omitempty"`
}

type tailnetSurfaceReport struct {
	SurfaceID   string   `json:"surface_id"`
	OwnerKind   string   `json:"owner_kind,omitempty"`
	OwnerID     string   `json:"owner_id,omitempty"`
	SurfaceKind string   `json:"surface_kind,omitempty"`
	Name        string   `json:"name,omitempty"`
	Hostname    string   `json:"hostname,omitempty"`
	TailnetName string   `json:"tailnet_name,omitempty"`
	ListenAddr  string   `json:"listen_addr,omitempty"`
	URL         string   `json:"url,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Status      string   `json:"status,omitempty"`
	LastError   string   `json:"last_error,omitempty"`
}

func runTailnetCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("tailnet requires a subcommand: status, surfaces, or revoke")
	}
	switch strings.TrimSpace(args[0]) {
	case "status":
		return runTailnetStatusCommand(args[1:])
	case "surfaces":
		return runTailnetSurfacesCommand(args[1:])
	case "revoke":
		return runTailnetRevokeCommand(args[1:])
	default:
		return fmt.Errorf("tailnet subcommand must be one of status|surfaces|revoke")
	}
}

func runTailnetStatusCommand(args []string) error {
	report, format, err := loadTailnetReport(args, "tailnet status")
	if err != nil {
		return err
	}
	return renderTailnetCommandReport(os.Stdout, report, format)
}

func runTailnetSurfacesCommand(args []string) error {
	report, format, err := loadTailnetReport(args, "tailnet surfaces")
	if err != nil {
		return err
	}
	report.Action = "tailnet surfaces"
	return renderTailnetCommandReport(os.Stdout, report, format)
}

func runTailnetRevokeCommand(args []string) error {
	fs := flag.NewFlagSet("tailnet revoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config path")
	formatRaw := fs.String("format", commandOutputHuman, "output format: human, kv, or json")
	jsonOutput := fs.Bool("json", false, "print report as JSON")
	surfaceIDFlag := fs.String("surface-id", "", "tailnet surface id")
	reason := fs.String("reason", "CLI tailnet revoke", "revocation reason")
	positionalSurfaceID := ""
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		positionalSurfaceID = strings.TrimSpace(args[0])
		parseArgs = args[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	format, err := normalizeCommandOutputFormat(*formatRaw, *jsonOutput)
	if err != nil {
		return err
	}
	surfaceID := strings.TrimSpace(firstNonEmpty(*surfaceIDFlag, positionalSurfaceID, firstArg(fs.Args())))
	if surfaceID == "" {
		return fmt.Errorf("tailnet revoke requires --surface-id or a surface id argument")
	}
	cfg, resolvedPath, err := loadConfigForCommand(*configPath)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return fmt.Errorf("open sessions store: %w", err)
	}
	defer func() { _ = store.Close() }()
	revoked, ok, err := store.RevokeTailnetSurface(surfaceID, strings.TrimSpace(*reason), time.Now().UTC())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("tailnet surface %q not found", surfaceID)
	}
	if err := appendTailnetMaintenanceEvent(store, revoked, strings.TrimSpace(*reason)); err != nil {
		return err
	}
	report := tailnetCommandReport{
		Action:          "tailnet revoke",
		Status:          "revoked",
		ConfigPath:      resolvedPath,
		Enabled:         cfg.Tailscale.Enabled,
		Backend:         cfg.Tailscale.Backend,
		ExpectedTailnet: cfg.Tailscale.ExpectedTailnet,
		SurfaceID:       revoked.SurfaceID,
		Reason:          strings.TrimSpace(*reason),
		Surfaces:        tailnetSurfaceReports([]session.TailnetSurfaceRecord{revoked}),
	}
	return renderTailnetCommandReport(os.Stdout, report, format)
}

func loadTailnetReport(args []string, action string) (tailnetCommandReport, string, error) {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config path")
	formatRaw := fs.String("format", commandOutputHuman, "output format: human, kv, or json")
	jsonOutput := fs.Bool("json", false, "print report as JSON")
	limit := fs.Int("limit", 50, "maximum surface rows")
	status := fs.String("status", "", "surface status filter")
	if err := fs.Parse(args); err != nil {
		return tailnetCommandReport{}, "", err
	}
	format, err := normalizeCommandOutputFormat(*formatRaw, *jsonOutput)
	if err != nil {
		return tailnetCommandReport{}, "", err
	}
	cfg, resolvedPath, err := loadConfigForCommand(*configPath)
	if err != nil {
		return tailnetCommandReport{}, "", err
	}
	store, err := openStoreIfExists(cfg.Sessions.DBPath)
	if err != nil {
		return tailnetCommandReport{}, "", err
	}
	if store != nil {
		defer func() { _ = store.Close() }()
	}
	surfaces := []session.TailnetSurfaceRecord{}
	if store != nil {
		surfaces, err = store.TailnetSurfaces(session.TailnetSurfaceFilter{
			Status: strings.TrimSpace(*status),
			Limit:  *limit,
		})
		if err != nil {
			return tailnetCommandReport{}, "", err
		}
	}
	report := tailnetCommandReport{
		Action:          action,
		Status:          tailnetRegistryStatus(cfg.Tailscale.Enabled, surfaces),
		ConfigPath:      resolvedPath,
		Enabled:         cfg.Tailscale.Enabled,
		Backend:         cfg.Tailscale.Backend,
		ExpectedTailnet: cfg.Tailscale.ExpectedTailnet,
		Surfaces:        tailnetSurfaceReports(surfaces),
	}
	return report, format, nil
}

func renderTailnetCommandReport(w io.Writer, report tailnetCommandReport, format string) error {
	switch format {
	case commandOutputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case commandOutputKV:
		renderTailnetCommandReportKV(w, report)
		return nil
	default:
		fmt.Fprintln(w, renderTailnetCommandReportHuman(report))
		return nil
	}
}

func renderTailnetCommandReportKV(w io.Writer, report tailnetCommandReport) {
	fmt.Fprintf(w, "action: %s\n", report.Action)
	fmt.Fprintf(w, "status: %s\n", report.Status)
	fmt.Fprintf(w, "enabled: %t\n", report.Enabled)
	fmt.Fprintf(w, "backend: %s\n", firstNonEmpty(report.Backend, "-"))
	fmt.Fprintf(w, "expected_tailnet: %s\n", firstNonEmpty(report.ExpectedTailnet, "-"))
	if report.SurfaceID != "" {
		fmt.Fprintf(w, "surface_id: %s\n", report.SurfaceID)
	}
	if report.Reason != "" {
		fmt.Fprintf(w, "reason: %s\n", report.Reason)
	}
	fmt.Fprintf(w, "surface_count: %d\n", len(report.Surfaces))
	for _, surface := range report.Surfaces {
		fmt.Fprintf(w, "surface: %s status=%s owner=%s:%s kind=%s url=%s\n",
			surface.SurfaceID,
			firstNonEmpty(surface.Status, "-"),
			firstNonEmpty(surface.OwnerKind, "-"),
			firstNonEmpty(surface.OwnerID, "-"),
			firstNonEmpty(surface.SurfaceKind, "-"),
			firstNonEmpty(surface.URL, "-"),
		)
	}
}

func renderTailnetCommandReportHuman(report tailnetCommandReport) string {
	details := []string{
		"Backend: " + firstNonEmpty(report.Backend, "-"),
		"Expected tailnet: " + firstNonEmpty(report.ExpectedTailnet, "-"),
		fmt.Sprintf("Registered surfaces: %d", len(report.Surfaces)),
	}
	if report.SurfaceID != "" {
		details = append(details, "Surface: "+report.SurfaceID)
	}
	if report.Reason != "" {
		details = append(details, "Reason: "+report.Reason)
	}
	for _, surface := range report.Surfaces {
		details = append(details, fmt.Sprintf("%s: %s %s", surface.SurfaceID, firstNonEmpty(surface.Status, "-"), firstNonEmpty(surface.URL, surface.Name, "-")))
	}
	evidence := []string{"Source: Tailnet surface registry in the Aphelion session store."}
	for _, status := range sortedTailnetSurfaceStatuses(report.Surfaces) {
		evidence = append(evidence, fmt.Sprintf("%s surfaces: %d", status, countTailnetSurfaces(report.Surfaces, status)))
	}
	next := "Use Telegram /tailnet controls or `aphelion tailnet revoke <surface>` for explicit revocation."
	if report.Status == "empty" {
		next = "Declare or observe a Tailnet surface before expecting private reachability."
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title:    "Tailnet Registry",
		State:    report.Status,
		Why:      "Private network state is evidence, not authority; Aphelion records what it has declared or observed.",
		Next:     next,
		Details:  details,
		Evidence: evidence,
	})
}

func tailnetSurfaceReports(records []session.TailnetSurfaceRecord) []tailnetSurfaceReport {
	out := make([]tailnetSurfaceReport, 0, len(records))
	for _, record := range records {
		record = session.NormalizeTailnetSurfaceRecord(record)
		out = append(out, tailnetSurfaceReport{
			SurfaceID:   record.SurfaceID,
			OwnerKind:   record.OwnerKind,
			OwnerID:     record.OwnerID,
			SurfaceKind: record.SurfaceKind,
			Name:        record.Name,
			Hostname:    record.Hostname,
			TailnetName: record.TailnetName,
			ListenAddr:  record.ListenAddr,
			URL:         record.URL,
			Tags:        record.Tags,
			Status:      record.Status,
			LastError:   record.LastError,
		})
	}
	return out
}

func tailnetRegistryStatus(enabled bool, surfaces []session.TailnetSurfaceRecord) string {
	if !enabled {
		return "disabled"
	}
	if len(surfaces) == 0 {
		return "empty"
	}
	for _, surface := range surfaces {
		switch surface.Status {
		case session.TailnetSurfaceStatusDegraded:
			return "degraded"
		case session.TailnetSurfaceStatusActive:
			return "active"
		}
	}
	return "declared"
}

func appendTailnetMaintenanceEvent(store *session.SQLiteStore, surface session.TailnetSurfaceRecord, reason string) error {
	if store == nil {
		return nil
	}
	payload := map[string]any{
		"action":       "cli_revoke",
		"surface_id":   strings.TrimSpace(surface.SurfaceID),
		"owner_kind":   strings.TrimSpace(surface.OwnerKind),
		"owner_id":     strings.TrimSpace(surface.OwnerID),
		"surface_kind": strings.TrimSpace(surface.SurfaceKind),
		"name":         strings.TrimSpace(surface.Name),
		"status":       strings.TrimSpace(surface.Status),
		"url":          strings.TrimSpace(surface.URL),
		"reason":       strings.TrimSpace(reason),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = store.AppendExecutionEvent(tailnetMaintenanceSessionKey(), session.ExecutionEventInput{
		EventType:   core.ExecutionEventTailnetSurfaceChanged,
		Stage:       "tailnet",
		Status:      strings.TrimSpace(surface.Status),
		PayloadJSON: string(raw),
		CreatedAt:   time.Now().UTC(),
	})
	return err
}

func tailnetMaintenanceSessionKey() session.SessionKey {
	return session.SessionKey{
		ChatID: tailnetMaintenanceChatID,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind: session.ScopeKindHeartbeat,
			ID:   "admin-house",
		},
	}
}

func firstArg(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func sortedTailnetSurfaceStatuses(surfaces []tailnetSurfaceReport) []string {
	seen := map[string]bool{}
	for _, surface := range surfaces {
		status := strings.TrimSpace(surface.Status)
		if status == "" {
			continue
		}
		seen[status] = true
	}
	values := make([]string, 0, len(seen))
	for status := range seen {
		values = append(values, status)
	}
	sort.Strings(values)
	return values
}

func countTailnetSurfaces(surfaces []tailnetSurfaceReport, status string) int {
	count := 0
	for _, surface := range surfaces {
		if strings.TrimSpace(surface.Status) == status {
			count++
		}
	}
	return count
}
