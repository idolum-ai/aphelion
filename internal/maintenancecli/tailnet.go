//go:build linux

package maintenancecli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
	if commandGroupHelpRequested(args) {
		printCommandGroupHelp("tailnet", []string{"status", "surfaces", "grants", "bind-grant", "apply-binding", "drift-binding", "rollback-binding", "revoke"})
		return nil
	}
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
