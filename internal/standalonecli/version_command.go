//go:build linux

package standalonecli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/face"
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
	formatFlag := fs.String("format", commandOutputHuman, "output format: human, kv, json")
	jsonOutput := fs.Bool("json", false, "print version metadata as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for version", extra)
	}
	format, err := normalizeCommandOutputFormat(*formatFlag, *jsonOutput)
	if err != nil {
		return err
	}

	info := readVersionInfo()
	switch format {
	case commandOutputJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	case commandOutputKV:
		renderVersionKV(os.Stdout, info)
		return nil
	default:
		fmt.Fprintln(os.Stdout, renderVersionHuman(info))
		return nil
	}
}

func renderVersionKV(out *os.File, info versionInfo) {
	fmt.Fprintf(out, "name: %s\n", info.Name)
	fmt.Fprintf(out, "module: %s\n", firstNonEmpty(info.Module, "unknown"))
	fmt.Fprintf(out, "version: %s\n", firstNonEmpty(info.Version, "unknown"))
	fmt.Fprintf(out, "go_version: %s\n", firstNonEmpty(info.GoVersion, "unknown"))
	fmt.Fprintf(out, "vcs_revision: %s\n", firstNonEmpty(info.VCSRevision, "unknown"))
	fmt.Fprintf(out, "vcs_time: %s\n", firstNonEmpty(info.VCSTime, "unknown"))
	fmt.Fprintf(out, "vcs_modified: %s\n", firstNonEmpty(info.VCSModified, "unknown"))
}

func renderVersionHuman(info versionInfo) string {
	state := "available"
	if strings.EqualFold(strings.TrimSpace(info.VCSModified), "true") {
		state = "modified checkout"
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title: "Aphelion Version",
		State: state,
		Why:   "This is the binary and source revision metadata used for release and rollback checks.",
		Next:  "Use --json for machine reads or compare the revision before deploy/rollback.",
		Details: []string{
			"Module: " + firstNonEmpty(info.Module, "unknown"),
			"Version: " + firstNonEmpty(info.Version, "unknown"),
			"Go: " + firstNonEmpty(info.GoVersion, "unknown"),
		},
		Evidence: []string{
			"VCS revision: " + firstNonEmpty(info.VCSRevision, "unknown"),
			"VCS time: " + firstNonEmpty(info.VCSTime, "unknown"),
			"VCS modified: " + firstNonEmpty(info.VCSModified, "unknown"),
		},
	})
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
