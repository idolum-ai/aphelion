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
	Action          string                      `json:"action"`
	Status          string                      `json:"status"`
	ConfigPath      string                      `json:"config_path,omitempty"`
	Enabled         bool                        `json:"enabled"`
	Backend         string                      `json:"backend,omitempty"`
	ExpectedTailnet string                      `json:"expected_tailnet,omitempty"`
	SurfaceID       string                      `json:"surface_id,omitempty"`
	BindingID       string                      `json:"binding_id,omitempty"`
	Reason          string                      `json:"reason,omitempty"`
	Surfaces        []tailnetSurfaceReport      `json:"surfaces,omitempty"`
	Bindings        []tailnetGrantBindingReport `json:"grant_bindings,omitempty"`
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

type tailnetGrantBindingReport struct {
	BindingID          string `json:"binding_id"`
	GrantID            string `json:"grant_id,omitempty"`
	SurfaceID          string `json:"surface_id,omitempty"`
	GrantedTo          string `json:"granted_to,omitempty"`
	CapabilityKind     string `json:"capability_kind,omitempty"`
	TargetResource     string `json:"target_resource,omitempty"`
	DesiredPolicyJSON  string `json:"desired_policy_json,omitempty"`
	AppliedPolicyHash  string `json:"applied_policy_hash,omitempty"`
	ObservedPolicyHash string `json:"observed_policy_hash,omitempty"`
	Status             string `json:"status,omitempty"`
	DriftReason        string `json:"drift_reason,omitempty"`
}

func runTailnetCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("tailnet requires a subcommand: status, surfaces, grants, bind-grant, apply-binding, drift-binding, rollback-binding, or revoke")
	}
	switch strings.TrimSpace(args[0]) {
	case "status":
		return runTailnetStatusCommand(args[1:])
	case "surfaces":
		return runTailnetSurfacesCommand(args[1:])
	case "grants":
		return runTailnetGrantsCommand(args[1:])
	case "bind-grant":
		return runTailnetBindGrantCommand(args[1:])
	case "apply-binding":
		return runTailnetApplyBindingCommand(args[1:])
	case "drift-binding":
		return runTailnetDriftBindingCommand(args[1:])
	case "rollback-binding":
		return runTailnetRollbackBindingCommand(args[1:])
	case "revoke":
		return runTailnetRevokeCommand(args[1:])
	default:
		return fmt.Errorf("tailnet subcommand must be one of status|surfaces|grants|bind-grant|apply-binding|drift-binding|rollback-binding|revoke")
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

func runTailnetGrantsCommand(args []string) error {
	report, format, err := loadTailnetReport(args, "tailnet grants")
	if err != nil {
		return err
	}
	report.Action = "tailnet grants"
	report.Status = tailnetGrantBindingRegistryStatus(report.Bindings)
	return renderTailnetCommandReport(os.Stdout, report, format)
}

func runTailnetBindGrantCommand(args []string) error {
	fs := flag.NewFlagSet("tailnet bind-grant", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config path")
	formatRaw := fs.String("format", commandOutputHuman, "output format: human, kv, or json")
	jsonOutput := fs.Bool("json", false, "print report as JSON")
	grantID := fs.String("grant-id", "", "approved Aphelion capability grant id")
	surfaceID := fs.String("surface-id", "", "declared or observed tailnet surface id")
	reason := fs.String("reason", "CLI Tailnet grant binding proposal", "operator rationale")
	if err := fs.Parse(args); err != nil {
		return err
	}
	format, err := normalizeCommandOutputFormat(*formatRaw, *jsonOutput)
	if err != nil {
		return err
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
	grant, ok, err := store.CapabilityGrant(*grantID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("capability grant %q not found", strings.TrimSpace(*grantID))
	}
	grant = session.NormalizeCapabilityGrant(grant)
	if grant.Status != session.CapabilityGrantStatusActive {
		return fmt.Errorf("tailnet bind-grant requires an active capability grant, got %q", grant.Status)
	}
	surface, ok, err := store.TailnetSurface(*surfaceID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("tailnet surface %q not found", strings.TrimSpace(*surfaceID))
	}
	if surface.Status == session.TailnetSurfaceStatusRevoked {
		return fmt.Errorf("tailnet surface %q is revoked", strings.TrimSpace(*surfaceID))
	}
	binding := tailnetBindingFromGrantAndSurface(grant, surface)
	stored, err := store.UpsertTailnetGrantBinding(binding)
	if err != nil {
		return err
	}
	if err := appendTailnetGrantMaintenanceEvent(store, stored, "proposed", strings.TrimSpace(*reason)); err != nil {
		return err
	}
	report := tailnetCommandReport{
		Action:          "tailnet bind-grant",
		Status:          stored.Status,
		ConfigPath:      resolvedPath,
		Enabled:         cfg.Tailscale.Enabled,
		Backend:         cfg.Tailscale.Backend,
		ExpectedTailnet: cfg.Tailscale.ExpectedTailnet,
		BindingID:       stored.BindingID,
		SurfaceID:       stored.SurfaceID,
		Reason:          strings.TrimSpace(*reason),
		Bindings:        tailnetGrantBindingReports([]session.TailnetGrantBinding{stored}),
	}
	return renderTailnetCommandReport(os.Stdout, report, format)
}

func runTailnetApplyBindingCommand(args []string) error {
	fs := flag.NewFlagSet("tailnet apply-binding", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config path")
	formatRaw := fs.String("format", commandOutputHuman, "output format: human, kv, or json")
	jsonOutput := fs.Bool("json", false, "print report as JSON")
	policyHash := fs.String("policy-hash", "", "hash of applied Tailscale policy evidence")
	observedPolicyHash := fs.String("observed-policy-hash", "", "hash of observed Tailscale policy after apply")
	reason := fs.String("reason", "CLI Tailnet policy apply evidence", "operator rationale")
	bindingID := ""
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		bindingID = strings.TrimSpace(args[0])
		parseArgs = args[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	if strings.TrimSpace(bindingID) == "" {
		return fmt.Errorf("tailnet apply-binding requires a binding id")
	}
	if strings.TrimSpace(*policyHash) == "" {
		return fmt.Errorf("tailnet apply-binding requires --policy-hash")
	}
	format, err := normalizeCommandOutputFormat(*formatRaw, *jsonOutput)
	if err != nil {
		return err
	}
	report, err := mutateTailnetBinding(*configPath, bindingID, *reason, func(store *session.SQLiteStore) (session.TailnetGrantBinding, bool, error) {
		return store.ApplyTailnetGrantBinding(bindingID, *policyHash, firstNonEmpty(*observedPolicyHash, *policyHash), time.Now().UTC())
	})
	if err != nil {
		return err
	}
	report.Action = "tailnet apply-binding"
	return renderTailnetCommandReport(os.Stdout, report, format)
}

func runTailnetDriftBindingCommand(args []string) error {
	fs := flag.NewFlagSet("tailnet drift-binding", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config path")
	formatRaw := fs.String("format", commandOutputHuman, "output format: human, kv, or json")
	jsonOutput := fs.Bool("json", false, "print report as JSON")
	observedPolicyHash := fs.String("observed-policy-hash", "", "hash of observed Tailscale policy")
	reason := fs.String("reason", "", "drift reason")
	bindingID := ""
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		bindingID = strings.TrimSpace(args[0])
		parseArgs = args[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	if strings.TrimSpace(bindingID) == "" {
		return fmt.Errorf("tailnet drift-binding requires a binding id")
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("tailnet drift-binding requires --reason")
	}
	format, err := normalizeCommandOutputFormat(*formatRaw, *jsonOutput)
	if err != nil {
		return err
	}
	report, err := mutateTailnetBinding(*configPath, bindingID, *reason, func(store *session.SQLiteStore) (session.TailnetGrantBinding, bool, error) {
		return store.DriftTailnetGrantBinding(bindingID, *reason, *observedPolicyHash, time.Now().UTC())
	})
	if err != nil {
		return err
	}
	report.Action = "tailnet drift-binding"
	return renderTailnetCommandReport(os.Stdout, report, format)
}

func runTailnetRollbackBindingCommand(args []string) error {
	fs := flag.NewFlagSet("tailnet rollback-binding", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config path")
	formatRaw := fs.String("format", commandOutputHuman, "output format: human, kv, or json")
	jsonOutput := fs.Bool("json", false, "print report as JSON")
	reason := fs.String("reason", "CLI Tailnet binding rollback", "rollback reason")
	bindingID := ""
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		bindingID = strings.TrimSpace(args[0])
		parseArgs = args[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	if strings.TrimSpace(bindingID) == "" {
		return fmt.Errorf("tailnet rollback-binding requires a binding id")
	}
	format, err := normalizeCommandOutputFormat(*formatRaw, *jsonOutput)
	if err != nil {
		return err
	}
	report, err := mutateTailnetBinding(*configPath, bindingID, *reason, func(store *session.SQLiteStore) (session.TailnetGrantBinding, bool, error) {
		return store.RevokeTailnetGrantBinding(bindingID, *reason, time.Now().UTC())
	})
	if err != nil {
		return err
	}
	report.Action = "tailnet rollback-binding"
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
		bindings, err := store.TailnetGrantBindings(session.TailnetGrantBindingFilter{Limit: *limit})
		if err != nil {
			return tailnetCommandReport{}, "", err
		}
		reportBindings := tailnetGrantBindingReports(bindings)
		report := tailnetCommandReport{
			Action:          action,
			Status:          tailnetRegistryStatus(cfg.Tailscale.Enabled, surfaces),
			ConfigPath:      resolvedPath,
			Enabled:         cfg.Tailscale.Enabled,
			Backend:         cfg.Tailscale.Backend,
			ExpectedTailnet: cfg.Tailscale.ExpectedTailnet,
			Surfaces:        tailnetSurfaceReports(surfaces),
			Bindings:        reportBindings,
		}
		return report, format, nil
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
	if report.BindingID != "" {
		fmt.Fprintf(w, "binding_id: %s\n", report.BindingID)
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
	fmt.Fprintf(w, "grant_binding_count: %d\n", len(report.Bindings))
	for _, binding := range report.Bindings {
		fmt.Fprintf(w, "grant_binding: %s status=%s grant=%s surface=%s target=%s\n",
			binding.BindingID,
			firstNonEmpty(binding.Status, "-"),
			firstNonEmpty(binding.GrantID, "-"),
			firstNonEmpty(binding.SurfaceID, "-"),
			firstNonEmpty(binding.TargetResource, "-"),
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
	if report.BindingID != "" {
		details = append(details, "Binding: "+report.BindingID)
	}
	for _, surface := range report.Surfaces {
		details = append(details, fmt.Sprintf("%s: %s %s", surface.SurfaceID, firstNonEmpty(surface.Status, "-"), firstNonEmpty(surface.URL, surface.Name, "-")))
	}
	for _, binding := range report.Bindings {
		details = append(details, fmt.Sprintf("%s: %s grant=%s surface=%s", binding.BindingID, firstNonEmpty(binding.Status, "-"), firstNonEmpty(binding.GrantID, "-"), firstNonEmpty(binding.SurfaceID, "-")))
	}
	evidence := []string{"Source: Tailnet surface and grant-binding registries in the Aphelion session store."}
	for _, status := range sortedTailnetSurfaceStatuses(report.Surfaces) {
		evidence = append(evidence, fmt.Sprintf("%s surfaces: %d", status, countTailnetSurfaces(report.Surfaces, status)))
	}
	for _, status := range sortedTailnetGrantBindingStatuses(report.Bindings) {
		evidence = append(evidence, fmt.Sprintf("%s grant bindings: %d", status, countTailnetGrantBindings(report.Bindings, status)))
	}
	next := "Use Telegram /tailnet controls for inspection, or CLI revoke/rollback commands for explicit local registry changes."
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

func tailnetGrantBindingReports(records []session.TailnetGrantBinding) []tailnetGrantBindingReport {
	out := make([]tailnetGrantBindingReport, 0, len(records))
	for _, record := range records {
		record = session.NormalizeTailnetGrantBinding(record)
		out = append(out, tailnetGrantBindingReport{
			BindingID:          record.BindingID,
			GrantID:            record.GrantID,
			SurfaceID:          record.SurfaceID,
			GrantedTo:          record.GrantedTo,
			CapabilityKind:     record.CapabilityKind,
			TargetResource:     record.TargetResource,
			DesiredPolicyJSON:  record.DesiredPolicyJSON,
			AppliedPolicyHash:  record.AppliedPolicyHash,
			ObservedPolicyHash: record.ObservedPolicyHash,
			Status:             record.Status,
			DriftReason:        record.DriftReason,
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

func tailnetGrantBindingRegistryStatus(bindings []tailnetGrantBindingReport) string {
	if len(bindings) == 0 {
		return "empty"
	}
	revoked := 0
	for _, binding := range bindings {
		switch strings.TrimSpace(binding.Status) {
		case session.TailnetGrantBindingStatusDrifted, session.TailnetGrantBindingStatusFailed:
			return "needs_attention"
		case session.TailnetGrantBindingStatusProposed:
			return "pending"
		case session.TailnetGrantBindingStatusRevoked:
			revoked++
		}
	}
	if revoked == len(bindings) {
		return "revoked"
	}
	return "ready"
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

func appendTailnetGrantMaintenanceEvent(store *session.SQLiteStore, binding session.TailnetGrantBinding, status string, reason string) error {
	if store == nil {
		return nil
	}
	payload := map[string]any{
		"binding_id":           strings.TrimSpace(binding.BindingID),
		"grant_id":             strings.TrimSpace(binding.GrantID),
		"surface_id":           strings.TrimSpace(binding.SurfaceID),
		"status":               strings.TrimSpace(binding.Status),
		"reason":               strings.TrimSpace(reason),
		"applied_policy_hash":  strings.TrimSpace(binding.AppliedPolicyHash),
		"observed_policy_hash": strings.TrimSpace(binding.ObservedPolicyHash),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = store.AppendExecutionEvent(tailnetMaintenanceSessionKey(), session.ExecutionEventInput{
		EventType:   core.ExecutionEventTailnetGrantChanged,
		Stage:       "tailnet",
		Status:      strings.TrimSpace(status),
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

func sortedTailnetGrantBindingStatuses(bindings []tailnetGrantBindingReport) []string {
	seen := make(map[string]bool, len(bindings))
	values := make([]string, 0)
	for _, binding := range bindings {
		status := strings.TrimSpace(binding.Status)
		if status == "" || seen[status] {
			continue
		}
		seen[status] = true
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

func countTailnetGrantBindings(bindings []tailnetGrantBindingReport, status string) int {
	count := 0
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Status) == status {
			count++
		}
	}
	return count
}

func tailnetBindingFromGrantAndSurface(grant session.CapabilityGrant, surface session.TailnetSurfaceRecord) session.TailnetGrantBinding {
	grant = session.NormalizeCapabilityGrant(grant)
	surface = session.NormalizeTailnetSurfaceRecord(surface)
	desired := map[string]any{
		"schema_version":  "aphelion.tailnet.grant_binding.v1",
		"grant_id":        strings.TrimSpace(grant.GrantID),
		"granted_to":      strings.TrimSpace(grant.GrantedTo),
		"capability_kind": string(grant.Kind),
		"target_resource": strings.TrimSpace(grant.TargetResource),
		"allowed_actions": append([]string(nil), grant.AllowedActions...),
		"surface_id":      strings.TrimSpace(surface.SurfaceID),
		"surface_kind":    strings.TrimSpace(surface.SurfaceKind),
		"hostname":        strings.TrimSpace(surface.Hostname),
		"tags":            append([]string(nil), surface.Tags...),
	}
	raw, _ := json.Marshal(desired)
	return session.TailnetGrantBinding{
		BindingID:         deterministicTailnetBindingID(grant.GrantID, surface.SurfaceID),
		GrantID:           strings.TrimSpace(grant.GrantID),
		SurfaceID:         strings.TrimSpace(surface.SurfaceID),
		GrantedTo:         strings.TrimSpace(grant.GrantedTo),
		CapabilityKind:    string(grant.Kind),
		TargetResource:    strings.TrimSpace(grant.TargetResource),
		DesiredPolicyJSON: string(raw),
		Status:            session.TailnetGrantBindingStatusProposed,
	}
}

func deterministicTailnetBindingID(grantID string, surfaceID string) string {
	return "tailnet-bind-" + safeTailnetBindingToken(grantID) + "-" + safeTailnetBindingToken(surfaceID)
}

func safeTailnetBindingToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unnamed"
	}
	if len(out) > 80 {
		return strings.Trim(out[:80], "-")
	}
	return out
}

func mutateTailnetBinding(configPath string, bindingID string, reason string, mutate func(*session.SQLiteStore) (session.TailnetGrantBinding, bool, error)) (tailnetCommandReport, error) {
	cfg, resolvedPath, err := loadConfigForCommand(configPath)
	if err != nil {
		return tailnetCommandReport{}, err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return tailnetCommandReport{}, fmt.Errorf("open sessions store: %w", err)
	}
	defer func() { _ = store.Close() }()
	stored, ok, err := mutate(store)
	if err != nil {
		return tailnetCommandReport{}, err
	}
	if !ok {
		return tailnetCommandReport{}, fmt.Errorf("tailnet grant binding %q not found", strings.TrimSpace(bindingID))
	}
	if err := appendTailnetGrantMaintenanceEvent(store, stored, stored.Status, reason); err != nil {
		return tailnetCommandReport{}, err
	}
	return tailnetCommandReport{
		Status:          stored.Status,
		ConfigPath:      resolvedPath,
		Enabled:         cfg.Tailscale.Enabled,
		Backend:         cfg.Tailscale.Backend,
		ExpectedTailnet: cfg.Tailscale.ExpectedTailnet,
		BindingID:       stored.BindingID,
		SurfaceID:       stored.SurfaceID,
		Reason:          strings.TrimSpace(reason),
		Bindings:        tailnetGrantBindingReports([]session.TailnetGrantBinding{stored}),
	}, nil
}
