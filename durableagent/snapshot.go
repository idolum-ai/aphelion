//go:build linux

package durableagent

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

const durableAgentSnapshotSchemaVersion = 1

var durableSnapshotIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-z]+$`)

type SnapshotManifest struct {
	SchemaVersion int                     `json:"schema_version"`
	SnapshotID    string                  `json:"snapshot_id"`
	AgentID       string                  `json:"agent_id"`
	Reason        string                  `json:"reason,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
	Agent         core.DurableAgent       `json:"agent"`
	State         *core.DurableAgentState `json:"state,omitempty"`
}

type SnapshotRecord struct {
	SnapshotID string    `json:"snapshot_id"`
	Reason     string    `json:"reason,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type SnapshotMigrationResult struct {
	Scanned        int
	Migrated       int
	AlreadyPresent int
	Rejected       int
	SourceRemoved  bool
}

func SnapshotBaseDir(agent core.DurableAgent, dbPath string) (string, error) {
	agentID := strings.TrimSpace(agent.AgentID)
	if err := core.ValidateDurableAgentID(agentID); err != nil {
		return "", err
	}
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return "", fmt.Errorf("sessions db path is required for durable agent snapshots")
	}
	return filepath.Join(filepath.Dir(dbPath), "durable_agent_snapshots", agentID), nil
}

func CreateSnapshot(agent core.DurableAgent, state *core.DurableAgentState, dbPath string, reason string, now time.Time) (*SnapshotManifest, error) {
	workspaceRoot, memoryRoot := LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(workspaceRoot) == "" || strings.TrimSpace(memoryRoot) == "" {
		workspaceRoot, memoryRoot = DefaultLocalRoots(strings.TrimSpace(dbPath), strings.TrimSpace(agent.AgentID))
	}
	if strings.TrimSpace(workspaceRoot) == "" || strings.TrimSpace(memoryRoot) == "" {
		return nil, fmt.Errorf("durable agent %q has no local roots for snapshot", strings.TrimSpace(agent.AgentID))
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	snapshotID := durableSnapshotID(now)
	baseDir, err := SnapshotBaseDir(agent, dbPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, fmt.Errorf("create snapshot base dir: %w", err)
	}
	snapshotDir := filepath.Join(baseDir, snapshotID)
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		return nil, fmt.Errorf("create snapshot dir: %w", err)
	}
	if err := copyTree(workspaceRoot, filepath.Join(snapshotDir, "workspace"), nil); err != nil {
		return nil, fmt.Errorf("snapshot workspace root: %w", err)
	}
	if err := copyTree(memoryRoot, filepath.Join(snapshotDir, "memory"), snapshotMemorySkipRel(memoryRoot, baseDir)); err != nil {
		return nil, fmt.Errorf("snapshot memory root: %w", err)
	}
	manifest := &SnapshotManifest{
		SchemaVersion: durableAgentSnapshotSchemaVersion,
		SnapshotID:    snapshotID,
		AgentID:       strings.TrimSpace(agent.AgentID),
		Reason:        strings.TrimSpace(reason),
		CreatedAt:     now,
		Agent:         agent,
		State:         state,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, "manifest.json"), raw, 0o600); err != nil {
		return nil, fmt.Errorf("write snapshot manifest: %w", err)
	}
	return manifest, nil
}

func ListSnapshots(agent core.DurableAgent, dbPath string, limit int) ([]SnapshotRecord, error) {
	baseDir, err := SnapshotBaseDir(agent, dbPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot base dir: %w", err)
	}
	out := make([]SnapshotRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest, _, err := LoadSnapshot(agent, dbPath, entry.Name())
		if err != nil {
			continue
		}
		out = append(out, SnapshotRecord{
			SnapshotID: strings.TrimSpace(manifest.SnapshotID),
			Reason:     strings.TrimSpace(manifest.Reason),
			CreatedAt:  manifest.CreatedAt.UTC(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].SnapshotID > out[j].SnapshotID
	})
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func LoadSnapshot(agent core.DurableAgent, dbPath string, snapshotID string) (*SnapshotManifest, string, error) {
	normalizedID, err := NormalizeSnapshotID(snapshotID)
	if err != nil {
		return nil, "", err
	}
	baseDir, err := SnapshotBaseDir(agent, dbPath)
	if err != nil {
		return nil, "", err
	}
	return loadSnapshotFromBaseDir(agent, baseDir, normalizedID)
}

func RestoreSnapshot(agent core.DurableAgent, dbPath string, snapshotID string, now time.Time) (*SnapshotManifest, error) {
	manifest, snapshotDir, err := LoadSnapshot(agent, dbPath, snapshotID)
	if err != nil {
		return nil, err
	}
	workspaceRoot, memoryRoot := LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(workspaceRoot) == "" || strings.TrimSpace(memoryRoot) == "" {
		workspaceRoot, memoryRoot = DefaultLocalRoots(strings.TrimSpace(dbPath), strings.TrimSpace(agent.AgentID))
	}
	if strings.TrimSpace(workspaceRoot) == "" || strings.TrimSpace(memoryRoot) == "" {
		return nil, fmt.Errorf("durable agent %q has no local roots for restore", strings.TrimSpace(agent.AgentID))
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := restoreTree(filepath.Join(snapshotDir, "workspace"), workspaceRoot, now); err != nil {
		return nil, fmt.Errorf("restore workspace root: %w", err)
	}
	if err := restoreTree(filepath.Join(snapshotDir, "memory"), memoryRoot, now); err != nil {
		return nil, fmt.Errorf("restore memory root: %w", err)
	}
	return manifest, nil
}

func NormalizeSnapshotID(snapshotID string) (string, error) {
	snapshotID = strings.TrimSpace(snapshotID)
	switch {
	case snapshotID == "":
		return "", fmt.Errorf("snapshot_id is required")
	case filepath.IsAbs(snapshotID):
		return "", fmt.Errorf("snapshot_id %q must not be an absolute path", snapshotID)
	case strings.Contains(snapshotID, "/") || strings.Contains(snapshotID, `\`):
		return "", fmt.Errorf("snapshot_id %q must not contain path separators", snapshotID)
	case snapshotID == "." || snapshotID == ".." || strings.Contains(snapshotID, ".."):
		return "", fmt.Errorf("snapshot_id %q is invalid", snapshotID)
	case !durableSnapshotIDPattern.MatchString(snapshotID):
		return "", fmt.Errorf("snapshot_id %q is not a generated durable snapshot id", snapshotID)
	}
	prefix, _, _ := strings.Cut(snapshotID, "-")
	if _, err := time.Parse("20060102T150405.000000000Z", prefix); err != nil {
		return "", fmt.Errorf("snapshot_id %q timestamp is invalid", snapshotID)
	}
	return snapshotID, nil
}

func MigrateChildMemorySnapshots(agent core.DurableAgent, dbPath string) (SnapshotMigrationResult, error) {
	var result SnapshotMigrationResult
	sourceBase, err := childMemorySnapshotBaseDir(agent, dbPath)
	if err != nil {
		return result, err
	}
	targetBase, err := SnapshotBaseDir(agent, dbPath)
	if err != nil {
		return result, err
	}
	if sameFilesystemPath(sourceBase, targetBase) {
		return result, nil
	}
	entries, err := os.ReadDir(sourceBase)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("read child memory snapshots: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		result.Scanned++
		snapshotID, err := NormalizeSnapshotID(entry.Name())
		if err != nil {
			result.Rejected++
			continue
		}
		if _, _, err := loadSnapshotFromBaseDir(agent, sourceBase, snapshotID); err != nil {
			result.Rejected++
			continue
		}
		targetDir := filepath.Join(targetBase, snapshotID)
		if _, _, err := loadSnapshotFromBaseDir(agent, targetBase, snapshotID); err == nil {
			result.AlreadyPresent++
			continue
		} else if _, statErr := os.Stat(targetDir); statErr == nil {
			return result, fmt.Errorf("target snapshot %q exists but is not valid: %w", snapshotID, err)
		} else if !os.IsNotExist(statErr) {
			return result, fmt.Errorf("inspect target snapshot %q: %w", snapshotID, statErr)
		}
		if err := os.MkdirAll(targetBase, 0o700); err != nil {
			return result, fmt.Errorf("create snapshot migration target: %w", err)
		}
		tempDir := filepath.Join(targetBase, "."+snapshotID+".tmp")
		_ = os.RemoveAll(tempDir)
		if err := copyTree(filepath.Join(sourceBase, snapshotID), tempDir, nil); err != nil {
			_ = os.RemoveAll(tempDir)
			return result, fmt.Errorf("copy snapshot %q: %w", snapshotID, err)
		}
		if err := os.Chmod(tempDir, 0o700); err != nil {
			_ = os.RemoveAll(tempDir)
			return result, fmt.Errorf("chmod migrated snapshot %q: %w", snapshotID, err)
		}
		if err := os.Rename(tempDir, targetDir); err != nil {
			_ = os.RemoveAll(tempDir)
			return result, fmt.Errorf("install migrated snapshot %q: %w", snapshotID, err)
		}
		if _, _, err := loadSnapshotFromBaseDir(agent, targetBase, snapshotID); err != nil {
			return result, fmt.Errorf("validate migrated snapshot %q: %w", snapshotID, err)
		}
		result.Migrated++
	}
	if err := os.RemoveAll(sourceBase); err != nil {
		return result, fmt.Errorf("remove child memory snapshots: %w", err)
	}
	result.SourceRemoved = true
	return result, nil
}

func loadSnapshotFromBaseDir(agent core.DurableAgent, baseDir string, snapshotID string) (*SnapshotManifest, string, error) {
	snapshotID, err := NormalizeSnapshotID(snapshotID)
	if err != nil {
		return nil, "", err
	}
	snapshotDir := filepath.Join(baseDir, snapshotID)
	if !pathWithinRoot(baseDir, snapshotDir) {
		return nil, "", fmt.Errorf("snapshot_id %q escapes snapshot base", snapshotID)
	}
	raw, err := os.ReadFile(filepath.Join(snapshotDir, "manifest.json"))
	if err != nil {
		return nil, "", fmt.Errorf("read snapshot manifest %q: %w", snapshotID, err)
	}
	var manifest SnapshotManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, "", fmt.Errorf("parse snapshot manifest %q: %w", snapshotID, err)
	}
	if manifest.SchemaVersion == 0 {
		manifest.SchemaVersion = durableAgentSnapshotSchemaVersion
	}
	if manifest.SchemaVersion != durableAgentSnapshotSchemaVersion {
		return nil, "", fmt.Errorf("snapshot %q schema_version=%d is unsupported", snapshotID, manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.SnapshotID) != snapshotID {
		return nil, "", fmt.Errorf("snapshot %q manifest snapshot_id=%q does not match", snapshotID, strings.TrimSpace(manifest.SnapshotID))
	}
	if strings.TrimSpace(manifest.AgentID) != strings.TrimSpace(agent.AgentID) {
		return nil, "", fmt.Errorf("snapshot %q belongs to agent %q, not %q", snapshotID, strings.TrimSpace(manifest.AgentID), strings.TrimSpace(agent.AgentID))
	}
	return &manifest, snapshotDir, nil
}

func durableSnapshotID(now time.Time) string {
	return now.UTC().Format("20060102T150405.000000000Z") + "-" + strings.ToLower(strconv.FormatInt(now.UTC().UnixNano(), 36))
}

func childMemorySnapshotBaseDir(agent core.DurableAgent, dbPath string) (string, error) {
	_, memoryRoot := LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(memoryRoot) == "" {
		_, memoryRoot = DefaultLocalRoots(strings.TrimSpace(dbPath), strings.TrimSpace(agent.AgentID))
	}
	if strings.TrimSpace(memoryRoot) == "" {
		return "", fmt.Errorf("durable agent %q has no memory root for child memory snapshots", strings.TrimSpace(agent.AgentID))
	}
	return filepath.Join(memoryRoot, ".snapshots"), nil
}

func snapshotMemorySkipRel(memoryRoot string, snapshotBaseDir string) map[string]struct{} {
	out := map[string]struct{}{".snapshots": {}}
	rel, err := filepath.Rel(strings.TrimSpace(memoryRoot), strings.TrimSpace(snapshotBaseDir))
	if err != nil {
		return out
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return out
	}
	out[filepath.ToSlash(rel)] = struct{}{}
	return out
}

func sameFilesystemPath(left string, right string) bool {
	leftAbs, leftErr := filepath.Abs(filepath.Clean(strings.TrimSpace(left)))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(strings.TrimSpace(right)))
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(strings.TrimSpace(left)) == filepath.Clean(strings.TrimSpace(right))
	}
	return leftAbs == rightAbs
}

func pathWithinRoot(root string, path string) bool {
	rootAbs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(root)))
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return false
	}
	if rootAbs == pathAbs {
		return true
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func copyTree(srcRoot string, dstRoot string, skipRel map[string]struct{}) error {
	srcRoot = strings.TrimSpace(srcRoot)
	dstRoot = strings.TrimSpace(dstRoot)
	if srcRoot == "" || dstRoot == "" {
		return fmt.Errorf("copy roots are required")
	}
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return err
	}
	_, statErr := os.Stat(srcRoot)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return statErr
	}
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.Clean(rel)
		if rel == "." {
			return nil
		}
		if shouldSkipSnapshotRelative(rel, skipRel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dstPath := filepath.Join(dstRoot, rel)
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(dstPath, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyFile(path, dstPath, info.Mode().Perm())
		default:
			return nil
		}
	})
}

func shouldSkipSnapshotRelative(rel string, skipRel map[string]struct{}) bool {
	if len(skipRel) == 0 {
		return false
	}
	normalized := filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	for candidate := range skipRel {
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if normalized == candidate || strings.HasPrefix(normalized, candidate+"/") {
			return true
		}
	}
	return false
}

func copyFile(src string, dst string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func restoreTree(snapshotRoot string, targetRoot string, now time.Time) error {
	snapshotRoot = strings.TrimSpace(snapshotRoot)
	targetRoot = strings.TrimSpace(targetRoot)
	if snapshotRoot == "" || targetRoot == "" {
		return fmt.Errorf("restore roots are required")
	}
	tempRoot := targetRoot + ".restore-tmp-" + strconv.FormatInt(now.UnixNano(), 36)
	backupRoot := targetRoot + ".restore-bak-" + strconv.FormatInt(now.UnixNano(), 36)
	_ = os.RemoveAll(tempRoot)
	if err := copyTree(snapshotRoot, tempRoot, nil); err != nil {
		return err
	}
	if _, err := os.Stat(targetRoot); err == nil {
		_ = os.RemoveAll(backupRoot)
		if err := os.Rename(targetRoot, backupRoot); err != nil {
			_ = os.RemoveAll(tempRoot)
			return err
		}
	}
	if err := os.Rename(tempRoot, targetRoot); err != nil {
		return err
	}
	return nil
}
