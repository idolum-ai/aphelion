//go:build linux

package standalonecli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/releaseinfo"
)

type statusCommandOptions struct {
	ConfigPath   string
	Format       string
	JSON         bool
	Timeout      time.Duration
	Runner       serviceGuardRunner
	Readlink     func(string) (string, error)
	ExecVersion  func(context.Context, string) (versionInfo, error)
	MetadataPath string
	BuildVersion versionInfo
}

type statusSnapshot struct {
	Action          string                    `json:"action"`
	Status          string                    `json:"status"`
	ConfigPath      string                    `json:"config_path,omitempty"`
	Version         versionInfo               `json:"version"`
	Release         statusReleaseInfo         `json:"release"`
	Service         statusServiceInfo         `json:"service"`
	DurableChildren statusDurableChildrenInfo `json:"durable_children"`
	DuplicateUnits  []string                  `json:"duplicate_units,omitempty"`
	Issues          []string                  `json:"issues,omitempty"`
	IssueRecords    []statusIssue             `json:"issue_records,omitempty"`
	NextAction      string                    `json:"next_action"`
}

type statusReleaseInfo struct {
	MetadataPath     string `json:"metadata_path,omitempty"`
	CurrentVersion   string `json:"current_version,omitempty"`
	CurrentRevision  string `json:"current_revision,omitempty"`
	LatestVersion    string `json:"latest_version,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	RunningRevision  string `json:"running_revision,omitempty"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
	CheckedAt        string `json:"checked_at,omitempty"`
	Source           string `json:"source,omitempty"`
	UpdateAvailable  bool   `json:"update_available"`
	Notice           string `json:"notice,omitempty"`
	MetadataStatus   string `json:"metadata_status"`
	MetadataError    string `json:"metadata_error,omitempty"`
	SourceStatus     string `json:"source_status,omitempty"`
	StatusClass      string `json:"status_class,omitempty"`
	FailureClass     string `json:"failure_class,omitempty"`
	RetryPolicy      string `json:"retry_policy,omitempty"`
	NextAction       string `json:"next_action,omitempty"`
	ServiceStatus    string `json:"service_status,omitempty"`
	ServiceClass     string `json:"service_class,omitempty"`
	ServiceFailure   string `json:"service_failure,omitempty"`
	ServiceRetry     string `json:"service_retry,omitempty"`
	ServiceNext      string `json:"service_next,omitempty"`
	FreshnessStatus  string `json:"freshness_status,omitempty"`
	FreshnessClass   string `json:"freshness_class,omitempty"`
	FreshnessFailure string `json:"freshness_failure,omitempty"`
	FreshnessRetry   string `json:"freshness_retry,omitempty"`
	FreshnessNext    string `json:"freshness_next,omitempty"`
}

type statusIssue struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Class      string `json:"class,omitempty"`
	NextAction string `json:"next_action,omitempty"`
}

type statusServiceInfo struct {
	Name             string `json:"name"`
	LoadState        string `json:"load_state,omitempty"`
	ActiveState      string `json:"active_state,omitempty"`
	SubState         string `json:"sub_state,omitempty"`
	FragmentPath     string `json:"fragment_path,omitempty"`
	MainPID          string `json:"main_pid,omitempty"`
	ExecStart        string `json:"exec_start,omitempty"`
	RunningExecPath  string `json:"running_exec_path,omitempty"`
	ExpectedExecPath string `json:"expected_exec_path,omitempty"`
	RunningVersion   string `json:"running_version,omitempty"`
	RunningRevision  string `json:"running_revision,omitempty"`
	ExpectedVersion  string `json:"expected_version,omitempty"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
	BinaryMatches    bool   `json:"binary_matches"`
}

type statusDurableChildrenInfo struct {
	MetadataPath string `json:"metadata_path,omitempty"`
	Status       string `json:"status"`
	TotalCount   int    `json:"total_count"`
}

func runStatusCommand(args []string) error {
	return runStatusCommandWithOptions(args, statusCommandOptions{})
}

func runStatusCommandWithOptions(args []string, opts statusCommandOptions) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	configFlag := fs.String("config", opts.ConfigPath, "path to aphelion config")
	formatFlag := fs.String("format", firstNonEmpty(opts.Format, commandOutputKV), "output format: kv or json")
	jsonOutput := fs.Bool("json", opts.JSON, "print status as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for status", extra)
	}
	format, err := normalizeStatusOutputFormat(*formatFlag, *jsonOutput)
	if err != nil {
		return err
	}
	snapshot, err := buildStatusSnapshot(context.Background(), statusCommandOptions{
		ConfigPath:   *configFlag,
		Format:       format,
		JSON:         *jsonOutput,
		Timeout:      opts.Timeout,
		Runner:       opts.Runner,
		Readlink:     opts.Readlink,
		ExecVersion:  opts.ExecVersion,
		MetadataPath: opts.MetadataPath,
		BuildVersion: opts.BuildVersion,
	})
	if err != nil {
		return err
	}
	if format == commandOutputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(snapshot)
	}
	renderStatusKV(os.Stdout, snapshot)
	return nil
}

func normalizeStatusOutputFormat(raw string, jsonAlias bool) (string, error) {
	if jsonAlias {
		return commandOutputJSON, nil
	}
	format := strings.ToLower(strings.TrimSpace(raw))
	if format == "" {
		return commandOutputKV, nil
	}
	switch format {
	case commandOutputKV, commandOutputJSON:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported output format %q; use kv or json", raw)
	}
}

func appendStatusIssue(s *statusSnapshot, code string, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	code = strings.TrimSpace(code)
	if code == "" {
		code = "unknown"
	}
	s.Issues = append(s.Issues, message)
	s.IssueRecords = append(s.IssueRecords, statusIssue{
		Code:       code,
		Message:    message,
		Class:      core.StatusClassOperationalTension,
		NextAction: "run doctor",
	})
}

func finalizeStatusSnapshot(s *statusSnapshot) {
	if s == nil {
		return
	}
	if len(s.Issues) > 0 {
		s.Status = "degraded"
		s.NextAction = "run doctor"
		return
	}
	s.Status = "ready"
	s.NextAction = "none"
}

func buildStatusSnapshot(ctx context.Context, opts statusCommandOptions) (statusSnapshot, error) {
	cfg, configPath, configErr := loadConfigForCommand(opts.ConfigPath)
	if configPath == "" {
		if resolved, err := config.ResolveConfigPath(opts.ConfigPath); err == nil {
			configPath = resolved
		} else {
			configPath = opts.ConfigPath
		}
	}
	version := readVersionInfo()
	if statusVersionInfoPresent(opts.BuildVersion) {
		version = opts.BuildVersion
	}
	current := releaseinfo.Current{Version: version.Version, Revision: version.VCSRevision, Modified: version.VCSModified}
	if current.Version == "" && current.Revision == "" && current.Modified == "" {
		current = releaseinfo.CurrentBuild()
	}
	meta, metaPath, metaOK, metaErr := releaseinfo.ReadMetadata(opts.MetadataPath)
	notice, noticeErr := releaseinfo.NewerReleaseNotice(current, metaPath)
	if metaPath == "" {
		metaPath = notice.MetadataPath
	}

	expectedExec, _ := os.Executable()
	report, guardErr := verifyAphelionServiceGuard(ctx, serviceGuardCheck{
		ExpectedExecPath: expectedExec,
		ExpectedRevision: version.VCSRevision,
		ExpectedVersion:  version.Version,
		Timeout:          opts.Timeout,
		Runner:           opts.Runner,
		Readlink:         opts.Readlink,
		ExecVersion:      opts.ExecVersion,
		Lenient:          true,
	})

	s := statusSnapshot{
		Action:     "status",
		Status:     "ready",
		ConfigPath: configPath,
		Version:    version,
		Release: statusReleaseInfo{
			MetadataPath:     metaPath,
			CurrentVersion:   current.Version,
			CurrentRevision:  current.Revision,
			LatestVersion:    meta.LatestVersion,
			InstalledVersion: meta.InstalledVersion,
			CheckedAt:        meta.CheckedAt,
			Source:           meta.Source,
			MetadataStatus:   "missing",
		},
		Service: statusServiceInfo{
			Name:             firstNonEmpty(report.ServiceName, aphelionUserServiceName),
			LoadState:        report.LoadState,
			ActiveState:      report.ActiveState,
			SubState:         report.SubState,
			FragmentPath:     report.FragmentPath,
			MainPID:          report.MainPID,
			ExecStart:        report.ExecStart,
			RunningExecPath:  report.RunningExecPath,
			ExpectedExecPath: report.ExpectedExecPath,
			RunningVersion:   report.RunningVersion,
			RunningRevision:  report.RunningRevision,
			ExpectedVersion:  report.ExpectedVersion,
			ExpectedRevision: report.ExpectedRevision,
		},
		DuplicateUnits: report.DuplicateUnitNames,
	}
	if cfg != nil {
		s.DurableChildren = readDurableChildrenSummary(cfg.Sessions.DBPath)
	} else {
		s.DurableChildren = statusDurableChildrenInfo{Status: "unavailable"}
	}
	if configErr != nil {
		appendStatusIssue(&s, "config_load_failed", "config load failed: "+configErr.Error())
	}
	if metaOK {
		s.Release.MetadataStatus = "present"
	}
	if metaErr != nil {
		s.Release.MetadataStatus = "unreadable"
		s.Release.MetadataError = metaErr.Error()
		appendStatusIssue(&s, "release_metadata_unreadable", "release metadata unreadable")
	}
	if noticeErr != nil && metaErr == nil {
		s.Release.MetadataStatus = "unreadable"
		s.Release.MetadataError = noticeErr.Error()
		appendStatusIssue(&s, "release_metadata_unreadable", "release metadata unreadable")
	}
	s.Release.UpdateAvailable = notice.Available
	if notice.LatestVersion != "" {
		s.Release.LatestVersion = notice.LatestVersion
	}
	if !notice.CheckedAt.IsZero() && s.Release.CheckedAt == "" {
		s.Release.CheckedAt = notice.CheckedAt.Format(time.RFC3339)
	}
	if notice.Source != "" {
		s.Release.Source = notice.Source
	}
	if notice.Reason != "" {
		s.Release.Notice = notice.Reason
	}

	if guardErr != nil {
		appendStatusIssue(&s, "service_guard_failed", guardErr.Error())
	}
	if len(s.DuplicateUnits) > 0 {
		appendStatusIssue(&s, "duplicate_primary_units", "duplicate/stale Aphelion primary units present")
	}
	s.Service.BinaryMatches = serviceBinaryMatches(s.Service)
	s.Release.RunningRevision = s.Service.RunningRevision
	s.Release.ExpectedRevision = s.Service.ExpectedRevision
	if !s.Service.BinaryMatches {
		appendStatusIssue(&s, "service_binary_mismatch", "running service binary does not match expected binary")
	}
	if strings.TrimSpace(s.Service.MainPID) == "" || strings.TrimSpace(s.Service.MainPID) == "0" {
		appendStatusIssue(&s, "service_not_running", "aphelion service is not running")
	}
	releaseAxes := core.ClassifySourceInstallReliabilityAxes(core.SourceInstallStatusInput{
		CurrentRevision:  s.Release.CurrentRevision,
		RunningRevision:  s.Service.RunningRevision,
		ExpectedRevision: s.Service.ExpectedRevision,
		LatestVersion:    s.Release.LatestVersion,
		MetadataStatus:   s.Release.MetadataStatus,
		UpdateAvailable:  s.Release.UpdateAvailable,
	})
	s.Release.SourceStatus = releaseAxes.Overall.Condition
	s.Release.StatusClass = releaseAxes.Overall.StatusClass
	s.Release.FailureClass = releaseAxes.Overall.FailureClass
	s.Release.RetryPolicy = releaseAxes.Overall.RetryPolicy
	s.Release.NextAction = releaseAxes.Overall.NextAction
	s.Release.ServiceStatus = releaseAxes.ServiceConsistency.Condition
	s.Release.ServiceClass = releaseAxes.ServiceConsistency.StatusClass
	s.Release.ServiceFailure = releaseAxes.ServiceConsistency.FailureClass
	s.Release.ServiceRetry = releaseAxes.ServiceConsistency.RetryPolicy
	s.Release.ServiceNext = releaseAxes.ServiceConsistency.NextAction
	s.Release.FreshnessStatus = releaseAxes.ReleaseFreshness.Condition
	s.Release.FreshnessClass = releaseAxes.ReleaseFreshness.StatusClass
	s.Release.FreshnessFailure = releaseAxes.ReleaseFreshness.FailureClass
	s.Release.FreshnessRetry = releaseAxes.ReleaseFreshness.RetryPolicy
	s.Release.FreshnessNext = releaseAxes.ReleaseFreshness.NextAction
	if s.Release.UpdateAvailable {
		appendStatusIssue(&s, "release_update_available", "newer release available in cached metadata")
	}
	finalizeStatusSnapshot(&s)
	return s, nil
}

func statusVersionInfoPresent(info versionInfo) bool {
	return strings.TrimSpace(info.Version) != "" ||
		strings.TrimSpace(info.VCSRevision) != "" ||
		strings.TrimSpace(info.VCSModified) != ""
}

func readDurableChildrenSummary(dbPath string) statusDurableChildrenInfo {
	root := ""
	if strings.TrimSpace(dbPath) != "" {
		root = filepath.Join(filepath.Dir(dbPath), "durable_agents")
	}
	info := statusDurableChildrenInfo{MetadataPath: root, Status: "missing"}
	if root == "" {
		return info
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return info
	}
	if err != nil {
		info.Status = "unreadable"
		return info
	}
	info.Status = "present"
	for _, entry := range entries {
		if entry.IsDir() && strings.TrimSpace(entry.Name()) != "" {
			info.TotalCount++
		}
	}
	return info
}

func serviceBinaryMatches(info statusServiceInfo) bool {
	if strings.TrimSpace(info.RunningExecPath) == "" || strings.TrimSpace(info.ExpectedExecPath) == "" {
		return false
	}
	if info.RunningExecPath != info.ExpectedExecPath {
		return false
	}
	if strings.TrimSpace(info.ExpectedRevision) == "" || strings.TrimSpace(info.RunningRevision) == "" {
		return false
	}
	if info.ExpectedRevision != info.RunningRevision {
		return false
	}
	if strings.TrimSpace(info.ExpectedVersion) == "" || strings.TrimSpace(info.RunningVersion) == "" {
		return false
	}
	if info.ExpectedVersion != info.RunningVersion {
		return false
	}
	return true
}

func renderStatusKV(out *os.File, s statusSnapshot) {
	fmt.Fprintf(out, "action: %s\n", s.Action)
	fmt.Fprintf(out, "status: %s\n", s.Status)
	fmt.Fprintf(out, "config_path: %s\n", firstNonEmpty(s.ConfigPath, "unknown"))
	fmt.Fprintf(out, "binary_version: %s\n", firstNonEmpty(s.Version.Version, "unknown"))
	fmt.Fprintf(out, "binary_revision: %s\n", firstNonEmpty(s.Version.VCSRevision, "unknown"))
	fmt.Fprintf(out, "source_current_revision: %s\n", firstNonEmpty(s.Release.CurrentRevision, "unknown"))
	fmt.Fprintf(out, "service_name: %s\n", firstNonEmpty(s.Service.Name, aphelionUserServiceName))
	fmt.Fprintf(out, "service_load_state: %s\n", firstNonEmpty(s.Service.LoadState, "unknown"))
	fmt.Fprintf(out, "service_active_state: %s\n", firstNonEmpty(s.Service.ActiveState, "unknown"))
	fmt.Fprintf(out, "service_sub_state: %s\n", firstNonEmpty(s.Service.SubState, "unknown"))
	fmt.Fprintf(out, "service_main_pid: %s\n", firstNonEmpty(s.Service.MainPID, "unknown"))
	fmt.Fprintf(out, "service_running_exec: %s\n", firstNonEmpty(s.Service.RunningExecPath, "unknown"))
	fmt.Fprintf(out, "service_expected_exec: %s\n", firstNonEmpty(s.Service.ExpectedExecPath, "unknown"))
	fmt.Fprintf(out, "service_running_version: %s\n", firstNonEmpty(s.Service.RunningVersion, "unknown"))
	fmt.Fprintf(out, "service_running_revision: %s\n", firstNonEmpty(s.Service.RunningRevision, "unknown"))
	fmt.Fprintf(out, "install_running_revision: %s\n", firstNonEmpty(s.Release.RunningRevision, "unknown"))
	fmt.Fprintf(out, "install_expected_revision: %s\n", firstNonEmpty(s.Release.ExpectedRevision, "unknown"))
	fmt.Fprintf(out, "service_binary_matches: %t\n", s.Service.BinaryMatches)
	fmt.Fprintf(out, "duplicate_units: %s\n", strings.Join(s.DuplicateUnits, ","))
	fmt.Fprintf(out, "release_metadata_path: %s\n", firstNonEmpty(s.Release.MetadataPath, "unknown"))
	fmt.Fprintf(out, "release_metadata_status: %s\n", firstNonEmpty(s.Release.MetadataStatus, "unknown"))
	fmt.Fprintf(out, "release_installed_version: %s\n", firstNonEmpty(s.Release.InstalledVersion, "unknown"))
	fmt.Fprintf(out, "release_latest_version: %s\n", firstNonEmpty(s.Release.LatestVersion, "unknown"))
	fmt.Fprintf(out, "release_update_available: %t\n", s.Release.UpdateAvailable)
	fmt.Fprintf(out, "release_source_status: %s\n", firstNonEmpty(s.Release.SourceStatus, "unknown"))
	fmt.Fprintf(out, "release_status_class: %s\n", firstNonEmpty(s.Release.StatusClass, "unknown"))
	fmt.Fprintf(out, "release_failure_class: %s\n", firstNonEmpty(s.Release.FailureClass, "unknown"))
	fmt.Fprintf(out, "release_retry_policy: %s\n", firstNonEmpty(s.Release.RetryPolicy, "unknown"))
	fmt.Fprintf(out, "release_next_action: %s\n", firstNonEmpty(s.Release.NextAction, "unknown"))
	fmt.Fprintf(out, "source_service_status: %s\n", firstNonEmpty(s.Release.ServiceStatus, "unknown"))
	fmt.Fprintf(out, "source_service_status_class: %s\n", firstNonEmpty(s.Release.ServiceClass, "unknown"))
	fmt.Fprintf(out, "source_service_failure_class: %s\n", firstNonEmpty(s.Release.ServiceFailure, "unknown"))
	fmt.Fprintf(out, "source_service_retry_policy: %s\n", firstNonEmpty(s.Release.ServiceRetry, "unknown"))
	fmt.Fprintf(out, "source_service_next_action: %s\n", firstNonEmpty(s.Release.ServiceNext, "unknown"))
	fmt.Fprintf(out, "release_freshness_status: %s\n", firstNonEmpty(s.Release.FreshnessStatus, "unknown"))
	fmt.Fprintf(out, "release_freshness_status_class: %s\n", firstNonEmpty(s.Release.FreshnessClass, "unknown"))
	fmt.Fprintf(out, "release_freshness_failure_class: %s\n", firstNonEmpty(s.Release.FreshnessFailure, "unknown"))
	fmt.Fprintf(out, "release_freshness_retry_policy: %s\n", firstNonEmpty(s.Release.FreshnessRetry, "unknown"))
	fmt.Fprintf(out, "release_freshness_next_action: %s\n", firstNonEmpty(s.Release.FreshnessNext, "unknown"))
	fmt.Fprintf(out, "durable_children_metadata_path: %s\n", firstNonEmpty(s.DurableChildren.MetadataPath, "unknown"))
	fmt.Fprintf(out, "durable_children_status: %s\n", firstNonEmpty(s.DurableChildren.Status, "unknown"))
	fmt.Fprintf(out, "durable_children_total: %d\n", s.DurableChildren.TotalCount)
	fmt.Fprintf(out, "next_action: %s\n", firstNonEmpty(s.NextAction, "unknown"))
	if len(s.Issues) == 0 {
		fmt.Fprintln(out, "issues: none")
		return
	}
	fmt.Fprintf(out, "issues: %s\n", strings.Join(s.Issues, "; "))
}
