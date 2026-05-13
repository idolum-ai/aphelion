//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool"
)

type capabilityGrantRepairManifest struct {
	Path     string
	Manifest tool.ExternalToolManifest
}

type capabilityGrantRepairRow struct {
	GrantID        string
	GrantedTo      string
	Kind           string
	TargetResource string
	Action         string
	Reason         string
	ManifestPath   string
}

type capabilityGrantRepairResult struct {
	Inspected        int
	Healthy          int
	RepairCandidates int
	RepairsApplied   int
	RevokeCandidates int
	RevokesApplied   int
	Skipped          int
	Errors           int
	Rows             []capabilityGrantRepairRow
}

func runRepairCapabilityGrantsCommand(args []string) error {
	fs := flag.NewFlagSet("repair-capability-grants", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	limitFlag := fs.Int("limit", 500, "maximum active capability grants to inspect")
	manifestDirFlag := fs.String("manifest-dir", "", "external tool manifest directory override")
	sourceFlag := fs.String("source", "capability_grant_repair", "repair source label recorded in cleanup events")
	dryRunFlag := fs.Bool("dry-run", true, "inspect and report without updating capability grants")
	applyFlag := fs.Bool("apply", false, "apply repairs/revocations; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*dryRunFlag && !*applyFlag {
		return fmt.Errorf("repair-capability-grants requires --apply to mutate capability grants")
	}

	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	manifestDir := firstNonEmpty(*manifestDirFlag, cfg.Tools.ExternalManifestDir)
	manifests, err := loadCapabilityRepairManifests(manifestDir)
	if err != nil {
		return err
	}
	dryRun := true
	if *applyFlag {
		dryRun = false
	}
	result, err := repairCapabilityGrantDrift(context.Background(), store, manifests, capabilityGrantRepairOptions{
		Limit:  *limitFlag,
		DryRun: dryRun,
		Source: *sourceFlag,
		Now:    time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: repair-capability-grants\n")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "dry_run: %t\n", dryRun)
	fmt.Fprintf(os.Stdout, "manifest_dir: %s\n", strings.TrimSpace(manifestDir))
	fmt.Fprintf(os.Stdout, "manifests: %d\n", len(manifests))
	fmt.Fprintf(os.Stdout, "inspected: %d\n", result.Inspected)
	fmt.Fprintf(os.Stdout, "healthy: %d\n", result.Healthy)
	fmt.Fprintf(os.Stdout, "repair_candidates: %d\n", result.RepairCandidates)
	fmt.Fprintf(os.Stdout, "repairs_applied: %d\n", result.RepairsApplied)
	fmt.Fprintf(os.Stdout, "revoke_candidates: %d\n", result.RevokeCandidates)
	fmt.Fprintf(os.Stdout, "revokes_applied: %d\n", result.RevokesApplied)
	fmt.Fprintf(os.Stdout, "skipped: %d\n", result.Skipped)
	fmt.Fprintf(os.Stdout, "errors: %d\n", result.Errors)
	for _, row := range result.Rows {
		fmt.Fprintf(os.Stdout, "- grant_id=%s action=%s granted_to=%s kind=%s target=%s reason=%s",
			row.GrantID,
			row.Action,
			row.GrantedTo,
			row.Kind,
			row.TargetResource,
			row.Reason,
		)
		if strings.TrimSpace(row.ManifestPath) != "" {
			fmt.Fprintf(os.Stdout, " manifest=%s", row.ManifestPath)
		}
		fmt.Fprintln(os.Stdout)
	}
	return nil
}

type capabilityGrantRepairOptions struct {
	Limit  int
	DryRun bool
	Source string
	Now    time.Time
}

func repairCapabilityGrantDrift(ctx context.Context, store *session.SQLiteStore, manifests []capabilityGrantRepairManifest, opts capabilityGrantRepairOptions) (capabilityGrantRepairResult, error) {
	result := capabilityGrantRepairResult{}
	if store == nil {
		return result, fmt.Errorf("repair capability grants requires session store")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		source = "capability_grant_repair"
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 500
	}
	byName := capabilityRepairManifestsByName(manifests)
	grants, err := store.CapabilityGrants(limit, session.CapabilityGrantStatusActive, "", "")
	if err != nil {
		return result, err
	}
	for _, grant := range grants {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		grant = session.NormalizeCapabilityGrant(grant)
		if !strings.HasPrefix(strings.TrimSpace(grant.GrantedTo), "durable_agent:") || !durableAgentGrantNeedsChildRuntime(grant) {
			result.Skipped++
			continue
		}
		result.Inspected++
		row := capabilityGrantRepairRow{
			GrantID:        grant.GrantID,
			GrantedTo:      grant.GrantedTo,
			Kind:           string(grant.Kind),
			TargetResource: grant.TargetResource,
		}
		if !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now) {
			row.Action = "revoke"
			row.Reason = "active grant is expired"
			result.RevokeCandidates++
			if !opts.DryRun {
				if err := revokeCapabilityGrantForRepair(store, grant, row.Reason, source, now); err != nil {
					result.Errors++
					row.Action = "error"
					row.Reason = err.Error()
				} else {
					result.RevokesApplied++
				}
			}
			result.Rows = append(result.Rows, row)
			continue
		}
		if _, found, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints); err != nil {
			manifest, ok := byName[strings.TrimSpace(grant.TargetResource)]
			if ok {
				material, materialOK, materialReason := childRuntimeFromRepairManifest(manifest)
				row.ManifestPath = manifest.Path
				if materialOK {
					row.Action = "repair"
					row.Reason = "invalid child_runtime replaced from external tool manifest"
					result.RepairCandidates++
					if !opts.DryRun {
						if err := repairCapabilityGrantChildRuntime(store, grant, material, row.ManifestPath, source, now); err != nil {
							result.Errors++
							row.Action = "error"
							row.Reason = err.Error()
						} else {
							result.RepairsApplied++
						}
					}
					result.Rows = append(result.Rows, row)
					continue
				}
				row.Reason = "invalid child_runtime and manifest cannot prove runtime material: " + materialReason
			} else {
				row.Reason = "invalid child_runtime and no matching external tool manifest: " + err.Error()
			}
			row.Action = "revoke"
			result.RevokeCandidates++
			if !opts.DryRun {
				if err := revokeCapabilityGrantForRepair(store, grant, row.Reason, source, now); err != nil {
					result.Errors++
					row.Action = "error"
					row.Reason = err.Error()
				} else {
					result.RevokesApplied++
				}
			}
			result.Rows = append(result.Rows, row)
			continue
		} else if found {
			result.Healthy++
			continue
		}
		manifest, ok := byName[strings.TrimSpace(grant.TargetResource)]
		if !ok {
			row.Action = "revoke"
			row.Reason = "missing child_runtime and no matching external tool manifest"
			result.RevokeCandidates++
			if !opts.DryRun {
				if err := revokeCapabilityGrantForRepair(store, grant, row.Reason, source, now); err != nil {
					result.Errors++
					row.Action = "error"
					row.Reason = err.Error()
				} else {
					result.RevokesApplied++
				}
			}
			result.Rows = append(result.Rows, row)
			continue
		}
		material, materialOK, materialReason := childRuntimeFromRepairManifest(manifest)
		row.ManifestPath = manifest.Path
		if !materialOK {
			row.Action = "revoke"
			row.Reason = "missing child_runtime and manifest cannot prove runtime material: " + materialReason
			result.RevokeCandidates++
			if !opts.DryRun {
				if err := revokeCapabilityGrantForRepair(store, grant, row.Reason, source, now); err != nil {
					result.Errors++
					row.Action = "error"
					row.Reason = err.Error()
				} else {
					result.RevokesApplied++
				}
			}
			result.Rows = append(result.Rows, row)
			continue
		}
		row.Action = "repair"
		row.Reason = "missing child_runtime repaired from external tool manifest"
		result.RepairCandidates++
		if !opts.DryRun {
			if err := repairCapabilityGrantChildRuntime(store, grant, material, row.ManifestPath, source, now); err != nil {
				result.Errors++
				row.Action = "error"
				row.Reason = err.Error()
			} else {
				result.RepairsApplied++
			}
		}
		result.Rows = append(result.Rows, row)
	}
	return result, nil
}

func revokeCapabilityGrantForRepair(store *session.SQLiteStore, grant session.CapabilityGrant, reason string, source string, now time.Time) error {
	grant = session.NormalizeCapabilityGrant(grant)
	grant.Status = session.CapabilityGrantStatusRevoked
	grant.StaleReason = strings.TrimSpace(reason)
	grant.RevokedAt = now.UTC()
	grant.UpdatedAt = now.UTC()
	if _, err := store.UpsertCapabilityGrant(grant); err != nil {
		return err
	}
	return appendMaintenanceExecutionEvent(store, maintenanceRepairKey(), core.ExecutionEventCapabilityGrantChanged, "capability_grants", "revoked", map[string]any{
		"source":          strings.TrimSpace(source),
		"cleanup_reason":  "capability_grant_child_runtime_repair",
		"grant_id":        grant.GrantID,
		"granted_to":      grant.GrantedTo,
		"kind":            string(grant.Kind),
		"target_resource": grant.TargetResource,
		"reason":          strings.TrimSpace(reason),
	}, now.UTC())
}

func repairCapabilityGrantChildRuntime(store *session.SQLiteStore, grant session.CapabilityGrant, material core.ChildRuntimeContract, manifestPath string, source string, now time.Time) error {
	grant = session.NormalizeCapabilityGrant(grant)
	nextContract, nextConstraints, err := mergeChildRuntimeIntoCapabilityMaterial(grant.Contract, grant.Constraints, material)
	if err != nil {
		return err
	}
	grant.Contract = nextContract
	grant.Constraints = nextConstraints
	if _, found, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints); err != nil {
		return fmt.Errorf("validate repaired child_runtime: %w", err)
	} else if !found {
		return fmt.Errorf("validate repaired child_runtime: no active child_runtime material")
	}
	policyHash := capabilityRepairGrantPolicyHash(grant)
	grant.BaselinePolicyHash = policyHash
	grant.CurrentPolicyHash = policyHash
	grant.AnchorFingerprint = policyHash
	grant.StaleReason = ""
	grant.UpdatedAt = now.UTC()
	if _, err := store.UpsertCapabilityGrant(grant); err != nil {
		return err
	}
	return appendMaintenanceExecutionEvent(store, maintenanceRepairKey(), core.ExecutionEventCapabilityGrantChanged, "capability_grants", "repaired", map[string]any{
		"source":          strings.TrimSpace(source),
		"cleanup_reason":  "capability_grant_child_runtime_repair",
		"grant_id":        grant.GrantID,
		"granted_to":      grant.GrantedTo,
		"kind":            string(grant.Kind),
		"target_resource": grant.TargetResource,
		"manifest_path":   strings.TrimSpace(manifestPath),
	}, now.UTC())
}

func mergeChildRuntimeIntoCapabilityMaterial(contract string, constraints string, material core.ChildRuntimeContract) (string, string, error) {
	nextContract, err := mergeChildRuntimeIntoCapabilityContract(contract, material)
	if err != nil {
		return "", "", err
	}
	nextConstraints, err := removeChildRuntimeFromCapabilityJSON(constraints)
	if err != nil {
		return "", "", err
	}
	return nextContract, nextConstraints, nil
}

func mergeChildRuntimeIntoCapabilityContract(contract string, material core.ChildRuntimeContract) (string, error) {
	material = core.NormalizeChildRuntimeContract(material)
	if err := core.ValidateChildRuntimeContract(material); err != nil {
		return "", err
	}
	obj := map[string]json.RawMessage{}
	if raw := strings.TrimSpace(contract); raw != "" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return "", fmt.Errorf("decode capability contract for child_runtime repair: %w", err)
		}
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", err
	}
	obj["child_runtime"] = encoded
	raw, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func removeChildRuntimeFromCapabilityJSON(rawJSON string) (string, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" || rawJSON == "{}" {
		return "{}", nil
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(rawJSON), &obj); err != nil {
		return "", fmt.Errorf("decode capability constraints for child_runtime repair: %w", err)
	}
	delete(obj, "child_runtime")
	if len(obj) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func capabilityRepairGrantPolicyHash(grant session.CapabilityGrant) string {
	grant = session.NormalizeCapabilityGrant(grant)
	payload := map[string]any{
		"kind":            string(grant.Kind),
		"target_resource": strings.TrimSpace(grant.TargetResource),
		"principal":       strings.TrimSpace(grant.GrantedTo),
		"allowed_actions": session.NormalizeCapabilityActions(grant.AllowedActions),
		"contract":        strings.TrimSpace(grant.Contract),
		"constraints":     strings.TrimSpace(grant.Constraints),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func loadCapabilityRepairManifests(dir string) ([]capabilityGrantRepairManifest, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries := make([]capabilityGrantRepairManifest, 0)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := strings.ToLower(strings.TrimSpace(entry.Name()))
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		isRootJSON := filepath.Dir(rel) == "." && strings.HasSuffix(name, ".json")
		isNestedManifest := name == "manifest.json"
		if !isRootJSON && !isNestedManifest {
			return nil
		}
		manifest, err := tool.LoadExternalToolManifest(path)
		if err != nil {
			return err
		}
		entries = append(entries, capabilityGrantRepairManifest{Path: path, Manifest: manifest})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load capability repair manifests: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	seen := make(map[string]string, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Manifest.Name)
		if prior, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate external tool manifest name %q in %s and %s", name, prior, entry.Path)
		}
		seen[name] = entry.Path
	}
	return entries, nil
}

func capabilityRepairManifestsByName(manifests []capabilityGrantRepairManifest) map[string]capabilityGrantRepairManifest {
	out := make(map[string]capabilityGrantRepairManifest, len(manifests))
	for _, manifest := range manifests {
		name := strings.TrimSpace(manifest.Manifest.Name)
		if name == "" {
			continue
		}
		if _, exists := out[name]; !exists {
			out[name] = manifest
		}
	}
	return out
}

func childRuntimeFromRepairManifest(entry capabilityGrantRepairManifest) (core.ChildRuntimeContract, bool, string) {
	manifest := tool.NormalizeExternalToolManifest(entry.Manifest)
	switch manifest.Execution.Mode {
	case "process", "subprocess":
	default:
		return core.ChildRuntimeContract{}, false, "manifest execution mode is not process/subprocess"
	}
	resolved, ok := resolveRepairManifestExecutable(entry.Path, manifest.Execution.Workdir, manifest.Execution.Entry)
	if !ok {
		return core.ChildRuntimeContract{}, false, "manifest executable is missing or not a file"
	}
	material := core.NormalizeChildRuntimeContract(core.ChildRuntimeContract{
		Executable:     resolved,
		CapabilityNote: "materialized from external tool manifest " + strings.TrimSpace(manifest.Name),
	})
	if err := core.ValidateChildRuntimeContract(material); err != nil {
		return core.ChildRuntimeContract{}, false, err.Error()
	}
	return material, true, ""
}

func resolveRepairManifestExecutable(manifestPath string, workdir string, entry string) (string, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", false
	}
	manifestDir := filepath.Dir(manifestPath)
	candidates := []string{entry}
	if !filepath.IsAbs(entry) {
		candidates = append(candidates, filepath.Join(manifestDir, entry))
		workdir = strings.TrimSpace(workdir)
		if workdir != "" {
			if filepath.IsAbs(workdir) {
				candidates = append(candidates, filepath.Join(workdir, entry))
			} else {
				candidates = append(candidates, filepath.Join(manifestDir, workdir, entry))
			}
		}
		for ancestor := manifestDir; ; ancestor = filepath.Dir(ancestor) {
			candidates = append(candidates, filepath.Join(ancestor, entry))
			next := filepath.Dir(ancestor)
			if next == ancestor {
				break
			}
		}
		if abs, err := filepath.Abs(entry); err == nil {
			candidates = append(candidates, abs)
		}
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if !filepath.IsAbs(candidate) {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		return candidate, true
	}
	return "", false
}
