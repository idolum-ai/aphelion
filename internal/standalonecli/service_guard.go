//go:build linux

package standalonecli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const aphelionServiceGuardTimeout = 15 * time.Second

type serviceGuardRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

type serviceGuardCheck struct {
	ServiceName      string
	ExpectedExecPath string
	ExpectedRevision string
	ExpectedVersion  string
	Timeout          time.Duration
	Runner           serviceGuardRunner
	Readlink         func(string) (string, error)
	ExecVersion      func(context.Context, string) (versionInfo, error)
}

type serviceGuardReport struct {
	ServiceName        string
	MainPID            string
	ExecStart          string
	RunningExecPath    string
	ExpectedExecPath   string
	RunningVersion     string
	ExpectedVersion    string
	RunningRevision    string
	ExpectedRevision   string
	DuplicateUnitNames []string
}

func (r serviceGuardReport) Summary() string {
	parts := []string{
		fmt.Sprintf("service=%s", firstNonEmpty(r.ServiceName, aphelionUserServiceName)),
		fmt.Sprintf("pid=%s", firstNonEmpty(r.MainPID, "unknown")),
	}
	if r.RunningExecPath != "" {
		parts = append(parts, fmt.Sprintf("running_exec=%s", r.RunningExecPath))
	}
	if r.ExpectedExecPath != "" {
		parts = append(parts, fmt.Sprintf("expected_exec=%s", r.ExpectedExecPath))
	}
	if r.RunningVersion != "" || r.RunningRevision != "" {
		parts = append(parts, fmt.Sprintf("running_version=%s", firstNonEmpty(r.RunningVersion, "unknown")))
		parts = append(parts, fmt.Sprintf("running_revision=%s", firstNonEmpty(r.RunningRevision, "unknown")))
	}
	if r.ExpectedVersion != "" || r.ExpectedRevision != "" {
		parts = append(parts, fmt.Sprintf("expected_version=%s", firstNonEmpty(r.ExpectedVersion, "unknown")))
		parts = append(parts, fmt.Sprintf("expected_revision=%s", firstNonEmpty(r.ExpectedRevision, "unknown")))
	}
	if len(r.DuplicateUnitNames) > 0 {
		parts = append(parts, "duplicate_units="+strings.Join(r.DuplicateUnitNames, ","))
	}
	return strings.Join(parts, "; ")
}

func verifyAphelionServiceGuard(ctx context.Context, check serviceGuardCheck) (serviceGuardReport, error) {
	runner := check.Runner
	if runner == nil {
		runner = execServiceGuardCommand
	}
	execVersion := check.ExecVersion
	if execVersion == nil {
		execVersion = func(ctx context.Context, execPath string) (versionInfo, error) {
			return readExecutableVersion(ctx, runner, execPath)
		}
	}
	readlink := check.Readlink
	if readlink == nil {
		readlink = filepath.EvalSymlinks
	}
	serviceName := strings.TrimSpace(check.ServiceName)
	if serviceName == "" {
		serviceName = aphelionUserServiceName
	}
	expectedExec, err := normalizeExistingPath(strings.TrimSpace(check.ExpectedExecPath))
	if err != nil {
		return serviceGuardReport{ServiceName: serviceName, ExpectedExecPath: check.ExpectedExecPath}, fmt.Errorf("resolve expected service executable: %w", err)
	}
	timeout := check.Timeout
	if timeout <= 0 {
		timeout = aphelionServiceGuardTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	report := serviceGuardReport{
		ServiceName:      serviceName,
		ExpectedExecPath: expectedExec,
		ExpectedRevision: strings.TrimSpace(check.ExpectedRevision),
		ExpectedVersion:  strings.TrimSpace(check.ExpectedVersion),
	}

	duplicates, err := aphelionDuplicatePrimaryUnits(runCtx, runner, serviceName)
	if err != nil {
		return report, err
	}
	report.DuplicateUnitNames = duplicates
	if len(duplicates) > 0 {
		return report, fmt.Errorf("duplicate/stale Aphelion primary unit(s) detected: %s", strings.Join(duplicates, ", "))
	}

	show, err := runner(runCtx, "systemctl", "--user", "show", serviceName, "-p", "MainPID", "-p", "ExecStart", "--no-pager")
	if err != nil {
		return report, fmt.Errorf("inspect %s: %w", serviceName, err)
	}
	props := parseSystemctlShowProperties(string(show))
	report.MainPID = strings.TrimSpace(props["MainPID"])
	report.ExecStart = strings.TrimSpace(props["ExecStart"])
	if report.MainPID == "" || report.MainPID == "0" {
		return report, fmt.Errorf("%s has no active MainPID", serviceName)
	}

	runningExec, err := readlink(filepath.Join("/proc", report.MainPID, "exe"))
	if err != nil {
		return report, fmt.Errorf("resolve running executable for %s pid %s: %w", serviceName, report.MainPID, err)
	}
	runningExec, err = normalizeExistingPath(runningExec)
	if err != nil {
		return report, fmt.Errorf("normalize running executable for %s pid %s: %w", serviceName, report.MainPID, err)
	}
	report.RunningExecPath = runningExec
	if expectedExec != "" && runningExec != expectedExec {
		return report, fmt.Errorf("%s running executable mismatch: got %s, want %s", serviceName, runningExec, expectedExec)
	}

	versionInfo, err := execVersion(runCtx, runningExec)
	if err != nil {
		return report, fmt.Errorf("read running executable version: %w", err)
	}
	report.RunningVersion = versionInfo.Version
	report.RunningRevision = versionInfo.VCSRevision
	if report.ExpectedVersion != "" && report.RunningVersion != report.ExpectedVersion {
		return report, fmt.Errorf("%s running version mismatch: got %s, want %s", serviceName, firstNonEmpty(report.RunningVersion, "unknown"), report.ExpectedVersion)
	}
	if report.ExpectedRevision != "" && report.RunningRevision != report.ExpectedRevision {
		return report, fmt.Errorf("%s running revision mismatch: got %s, want %s", serviceName, firstNonEmpty(report.RunningRevision, "unknown"), report.ExpectedRevision)
	}
	return report, nil
}

func aphelionDuplicatePrimaryUnits(ctx context.Context, runner serviceGuardRunner, canonical string) ([]string, error) {
	out, err := runner(ctx, "systemctl", "--user", "list-units", "--all", "--no-legend", "--plain")
	if err != nil {
		return nil, fmt.Errorf("list user units: %w", err)
	}
	canonical = strings.TrimSpace(canonical)
	canonicalUnit := canonical
	if canonicalUnit != "" && !strings.HasSuffix(canonicalUnit, ".service") {
		canonicalUnit += ".service"
	}
	seen := map[string]bool{}
	var duplicates []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if !isAphelionPrimaryUnitName(name) || name == canonical || name == canonicalUnit {
			continue
		}
		if !seen[name] {
			seen[name] = true
			duplicates = append(duplicates, name)
		}
	}
	out, err = runner(ctx, "systemctl", "--user", "list-unit-files", "--no-legend", "--plain")
	if err != nil {
		return nil, fmt.Errorf("list user unit files: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if !isAphelionPrimaryUnitName(name) || name == canonical || name == canonicalUnit {
			continue
		}
		if !seen[name] {
			seen[name] = true
			duplicates = append(duplicates, name)
		}
	}
	sort.Strings(duplicates)
	return duplicates, nil
}

func isAphelionPrimaryUnitName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(name, ".service") || !strings.Contains(name, "aphelion") {
		return false
	}
	if strings.Contains(name, "sandbox") || strings.Contains(name, "helper") {
		return false
	}
	return true
}

func parseSystemctlShowProperties(raw string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		props[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return props
}

func readExecutableVersion(ctx context.Context, runner serviceGuardRunner, execPath string) (versionInfo, error) {
	out, err := runner(ctx, execPath, "version", "--json")
	if err != nil {
		return versionInfo{}, err
	}
	var info versionInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return versionInfo{}, err
	}
	return info, nil
}

func normalizeExistingPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func execServiceGuardCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	return cmd.CombinedOutput()
}
