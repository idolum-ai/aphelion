//go:build linux

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

type versionInfo struct {
	Name        string `json:"name"`
	Module      string `json:"module,omitempty"`
	Version     string `json:"version,omitempty"`
	GoVersion   string `json:"go_version,omitempty"`
	VCSRevision string `json:"vcs_revision,omitempty"`
	VCSTime     string `json:"vcs_time,omitempty"`
	VCSModified string `json:"vcs_modified,omitempty"`
}

func runVersionCommand(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print version metadata as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for version", extra)
	}

	info := readVersionInfo()
	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}

	fmt.Fprintf(os.Stdout, "name: %s\n", info.Name)
	fmt.Fprintf(os.Stdout, "module: %s\n", firstNonEmpty(info.Module, "unknown"))
	fmt.Fprintf(os.Stdout, "version: %s\n", firstNonEmpty(info.Version, "unknown"))
	fmt.Fprintf(os.Stdout, "go_version: %s\n", firstNonEmpty(info.GoVersion, "unknown"))
	fmt.Fprintf(os.Stdout, "vcs_revision: %s\n", firstNonEmpty(info.VCSRevision, "unknown"))
	fmt.Fprintf(os.Stdout, "vcs_time: %s\n", firstNonEmpty(info.VCSTime, "unknown"))
	fmt.Fprintf(os.Stdout, "vcs_modified: %s\n", firstNonEmpty(info.VCSModified, "unknown"))
	return nil
}

func readVersionInfo() versionInfo {
	info := versionInfo{Name: "aphelion"}
	build, ok := debug.ReadBuildInfo()
	if !ok || build == nil {
		return info
	}

	info.Module = strings.TrimSpace(build.Main.Path)
	info.Version = strings.TrimSpace(build.Main.Version)
	info.GoVersion = strings.TrimSpace(build.GoVersion)
	info.VCSRevision = buildSetting(build, "vcs.revision")

	rawVCSTime := buildSetting(build, "vcs.time")
	if ts, err := time.Parse(time.RFC3339, rawVCSTime); err == nil {
		info.VCSTime = ts.UTC().Format(time.RFC3339)
	} else {
		info.VCSTime = rawVCSTime
	}

	info.VCSModified = buildSetting(build, "vcs.modified")
	return info
}

func buildSetting(build *debug.BuildInfo, key string) string {
	if build == nil {
		return ""
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	for _, setting := range build.Settings {
		if strings.TrimSpace(setting.Key) != key {
			continue
		}
		return strings.TrimSpace(setting.Value)
	}
	return ""
}
