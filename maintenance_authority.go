//go:build linux

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

func runAuthorityCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: authority <doctor|repair> [--config path]")
	}
	switch strings.TrimSpace(args[0]) {
	case "doctor":
		return runAuthorityDoctorCommand(args[1:])
	case "repair":
		return runAuthorityRepairCommand(args[1:])
	default:
		return fmt.Errorf("unknown authority command %q (known: doctor|repair)", args[0])
	}
}

func runAuthorityDoctorCommand(args []string) error {
	fs := flag.NewFlagSet("authority doctor", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	limitFlag := fs.Int("limit", 50, "maximum findings to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	snapshot, configPath, err := authoritySnapshotForCommand(*configFlag)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "action: authority-doctor")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	writeAuthoritySnapshot(os.Stdout, snapshot, *limitFlag, false)
	return nil
}

func runAuthorityRepairCommand(args []string) error {
	fs := flag.NewFlagSet("authority repair", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	limitFlag := fs.Int("limit", 50, "maximum repair previews to print")
	applyFlag := fs.Bool("apply", false, "apply supported repairs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *applyFlag {
		return fmt.Errorf("authority repair apply is not enabled yet; use the typed repair previews or targeted repair commands")
	}
	snapshot, configPath, err := authoritySnapshotForCommand(*configFlag)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "action: authority-repair")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintln(os.Stdout, "dry_run: true")
	writeAuthoritySnapshot(os.Stdout, snapshot, *limitFlag, true)
	return nil
}

func authoritySnapshotForCommand(configPathFlag string) (core.AuthorityStatusSnapshot, string, error) {
	cfg, configPath, err := loadConfigForCommand(configPathFlag)
	if err != nil {
		return core.AuthorityStatusSnapshot{}, "", err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return core.AuthorityStatusSnapshot{}, "", err
	}
	defer store.Close()
	snapshot, err := aphruntime.AuthorityStatusSnapshotFromStore(store, time.Now().UTC())
	if err != nil {
		return core.AuthorityStatusSnapshot{}, "", err
	}
	return snapshot, configPath, nil
}

func writeAuthoritySnapshot(out *os.File, snapshot core.AuthorityStatusSnapshot, limit int, repairOnly bool) {
	if limit <= 0 {
		limit = 50
	}
	fmt.Fprintf(out, "status: %s\n", firstNonEmpty(strings.TrimSpace(snapshot.Status), "healthy"))
	fmt.Fprintf(out, "findings: %d\n", snapshot.FindingCount)
	fmt.Fprintf(out, "errors: %d\n", snapshot.ErrorCount)
	fmt.Fprintf(out, "warnings: %d\n", snapshot.WarningCount)
	fmt.Fprintf(out, "continuation_records: %d\n", snapshot.ContinuationRecords)
	fmt.Fprintf(out, "operation_records: %d\n", snapshot.OperationRecords)
	fmt.Fprintf(out, "pending_decisions: %d\n", snapshot.PendingDecisions)
	fmt.Fprintf(out, "active_autoapproval_leases: %d\n", snapshot.AutoApprovalLeases)
	fmt.Fprintf(out, "active_capability_grants: %d\n", snapshot.CapabilityGrants)
	printed := 0
	repairable := 0
	for _, finding := range snapshot.Findings {
		if finding.Repairable {
			repairable++
		}
	}
	if repairOnly {
		fmt.Fprintf(out, "repairable: %d\n", repairable)
	}
	for _, finding := range snapshot.Findings {
		if repairOnly && strings.TrimSpace(finding.RepairAction) == "" {
			continue
		}
		if printed >= limit {
			break
		}
		printed++
		fmt.Fprintf(out, "- code=%s severity=%s source=%s:%s", finding.Code, finding.Severity, finding.SourceKind, finding.SourceID)
		if finding.ChatID != 0 {
			fmt.Fprintf(out, " chat_id=%d", finding.ChatID)
		}
		if strings.TrimSpace(finding.SessionID) != "" {
			fmt.Fprintf(out, " session_id=%s", finding.SessionID)
		}
		if strings.TrimSpace(finding.RepairAction) != "" {
			fmt.Fprintf(out, " repair_action=%s", finding.RepairAction)
		}
		if finding.Repairable {
			fmt.Fprint(out, " repairable=true")
		}
		if strings.TrimSpace(finding.NextRepairAction) != "" {
			fmt.Fprintf(out, " next_repair=%q", finding.NextRepairAction)
		}
		fmt.Fprintln(out)
	}
	if printed == 0 {
		fmt.Fprintln(out, "- none")
	}
}
