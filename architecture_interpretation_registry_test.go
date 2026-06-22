//go:build linux

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type interpretationSurfaceRegistryEntry struct {
	ID               string   `json:"id"`
	Surface          string   `json:"surface"`
	Status           string   `json:"status"`
	Owners           []string `json:"owners"`
	CodeAnchors      []string `json:"code_anchors"`
	JudgmentKinds    []string `json:"judgment_kinds"`
	Consumers        []string `json:"consumers"`
	Consequences     []string `json:"consequences"`
	ChallengeAdapter string   `json:"challenge_adapter"`
}

func TestArchitectureInterpretationSurfaceRegistryIsComplete(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("docs", "architecture", "interpretation-surfaces.json"))
	if err != nil {
		t.Fatalf("read interpretation surface registry: %v", err)
	}
	var entries []interpretationSurfaceRegistryEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode interpretation surface registry: %v", err)
	}
	if len(entries) < 25 {
		t.Fatalf("registry has %d entries, want seeded complete map including follow-up surfaces", len(entries))
	}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			t.Fatalf("entry missing id: %#v", entry)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate registry id %q", id)
		}
		seen[id] = struct{}{}
		switch entry.Status {
		case "satisfies", "not_applicable":
		default:
			t.Fatalf("entry %s status = %q, want satisfies or not_applicable", id, entry.Status)
		}
		if strings.TrimSpace(entry.Surface) == "" || len(entry.Owners) == 0 || len(entry.CodeAnchors) == 0 || len(entry.Consequences) == 0 || strings.TrimSpace(entry.ChallengeAdapter) == "" {
			t.Fatalf("entry %s missing required registry metadata: %#v", id, entry)
		}
		for _, anchor := range entry.CodeAnchors {
			anchor = strings.TrimSpace(anchor)
			if anchor == "" {
				t.Fatalf("entry %s has empty code anchor", id)
			}
			if _, err := os.Stat(anchor); err != nil {
				t.Fatalf("entry %s code anchor %q does not resolve: %v", id, anchor, err)
			}
		}
		if entry.Status == "satisfies" && len(entry.Consumers) == 0 {
			t.Fatalf("entry %s satisfies but has no consumer ids", id)
		}
	}
	required := []string{
		"dependency_decorrelation_adjudication",
		"memory_context_governor",
		"material_floor_continuity",
		"budget_recovery_scope",
		"semantic_memory_classification",
		"effect_outcome_verification",
		"tool_input_parsing_repair",
		"capability_principal_matching",
		"path_sandbox_containment",
		"provider_retry_classification",
		"operation_completion_objective",
		"continuation_supersession_projection",
	}
	for _, id := range required {
		if _, ok := seen[id]; !ok {
			t.Fatalf("registry missing required surface id %q", id)
		}
	}
}
