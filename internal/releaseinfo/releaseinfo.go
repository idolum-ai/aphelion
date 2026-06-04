//go:build linux

package releaseinfo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

type Metadata struct {
	Repo             string `json:"repo,omitempty"`
	LatestVersion    string `json:"latest_version,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	CheckedAt        string `json:"checked_at,omitempty"`
	Source           string `json:"source,omitempty"`
}

type Current struct {
	Version  string
	Revision string
	Modified string
}

type Notice struct {
	Available      bool
	CurrentVersion string
	LatestVersion  string
	MetadataPath   string
	CheckedAt      time.Time
	Source         string
	Reason         string
}

func CurrentBuild() Current {
	out := Current{}
	build, ok := debug.ReadBuildInfo()
	if !ok || build == nil {
		return out
	}
	out.Version = strings.TrimSpace(build.Main.Version)
	for _, setting := range build.Settings {
		switch strings.TrimSpace(setting.Key) {
		case "vcs.revision":
			out.Revision = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			out.Modified = strings.TrimSpace(setting.Value)
		}
	}
	return out
}

func DefaultMetadataPath() string {
	if override := strings.TrimSpace(os.Getenv("APHELION_RELEASE_METADATA")); override != "" {
		return override
	}
	cache, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cache) == "" {
		return ""
	}
	return filepath.Join(cache, "aphelion", "release.json")
}

func ReadMetadata(path string) (Metadata, string, bool, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultMetadataPath()
	}
	if strings.TrimSpace(path) == "" {
		return Metadata{}, "", false, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Metadata{}, path, false, nil
	}
	if err != nil {
		return Metadata{}, path, false, err
	}
	var meta Metadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Metadata{}, path, true, err
	}
	return meta, path, true, nil
}

func NewerReleaseNotice(current Current, metadataPath string) (Notice, error) {
	meta, path, ok, err := ReadMetadata(metadataPath)
	notice := Notice{MetadataPath: path, CurrentVersion: cleanVersion(current.Version)}
	if err != nil {
		notice.Reason = "release metadata unreadable: " + err.Error()
		return notice, err
	}
	if !ok {
		return notice, nil
	}
	notice.LatestVersion = cleanVersion(meta.LatestVersion)
	notice.Source = strings.TrimSpace(meta.Source)
	if ts, err := time.Parse(time.RFC3339, strings.TrimSpace(meta.CheckedAt)); err == nil {
		notice.CheckedAt = ts.UTC()
	}
	if notice.LatestVersion == "" {
		return notice, nil
	}
	if notice.CurrentVersion == "" || notice.CurrentVersion == "(devel)" {
		notice.Reason = "running binary version is not a release tag"
		return notice, nil
	}
	if tagGreater(notice.LatestVersion, notice.CurrentVersion) {
		notice.Available = true
		notice.Reason = fmt.Sprintf("latest known release %s is newer than running %s", notice.LatestVersion, notice.CurrentVersion)
	}
	return notice, nil
}

func cleanVersion(v string) string { return strings.TrimSpace(v) }

func tagGreater(a string, b string) bool {
	av, aok := parseTag(a)
	bv, bok := parseTag(b)
	if !aok || !bok {
		return false
	}
	for i := 0; i < len(av) && i < len(bv); i++ {
		if av[i] > bv[i] {
			return true
		}
		if av[i] < bv[i] {
			return false
		}
	}
	return false
}

func parseTag(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) == 0 {
		return nil, false
	}
	out := make([]int, 3)
	for i := 0; i < len(out) && i < len(parts); i++ {
		part := parts[i]
		if j := strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }); j >= 0 {
			part = part[:j]
		}
		if part == "" {
			return nil, false
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}
