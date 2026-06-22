//go:build linux

package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type interpretationSurfaceRegistryEntry struct {
	ID               string            `json:"id"`
	Surface          string            `json:"surface"`
	Status           string            `json:"status"`
	Wiring           string            `json:"wiring"`
	Owners           []string          `json:"owners"`
	CodeAnchors      []string          `json:"code_anchors"`
	JudgmentKinds    []string          `json:"judgment_kinds"`
	Consumers        []string          `json:"consumers"`
	ConsumerAnchors  map[string]string `json:"consumer_anchors"`
	Consequences     []string          `json:"consequences"`
	ChallengeAdapter string            `json:"challenge_adapter"`
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
		switch entry.Wiring {
		case "wired", "not_applicable":
		default:
			t.Fatalf("entry %s wiring = %q, want wired or not_applicable", id, entry.Wiring)
		}
		if entry.Status == "satisfies" && entry.Wiring != "wired" {
			t.Fatalf("entry %s satisfies but wiring = %q, want wired", id, entry.Wiring)
		}
		if entry.Status == "not_applicable" && entry.Wiring != "not_applicable" {
			t.Fatalf("entry %s not_applicable but wiring = %q, want not_applicable", id, entry.Wiring)
		}
		if strings.TrimSpace(entry.Surface) == "" || len(entry.Owners) == 0 || len(entry.CodeAnchors) == 0 || len(entry.Consequences) == 0 || strings.TrimSpace(entry.ChallengeAdapter) == "" {
			t.Fatalf("entry %s missing required registry metadata: %#v", id, entry)
		}
		if _, ok := allowedInterpretationChallengeAdapters()[entry.ChallengeAdapter]; !ok {
			t.Fatalf("entry %s challenge_adapter = %q, want registered adapter token", id, entry.ChallengeAdapter)
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
		for _, consumer := range entry.Consumers {
			consumer = strings.TrimSpace(consumer)
			if consumer == "" {
				t.Fatalf("entry %s has empty consumer id", id)
			}
			anchor := strings.TrimSpace(entry.ConsumerAnchors[consumer])
			if anchor == "" {
				t.Fatalf("entry %s consumer %q has no consumer_anchors entry", id, consumer)
			}
			assertInterpretationConsumerAnchor(t, id, consumer, anchor)
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

func assertInterpretationConsumerAnchor(t *testing.T, entryID string, consumer string, anchor string) {
	t.Helper()
	path, token, ok := strings.Cut(anchor, "#")
	path = strings.TrimSpace(path)
	token = strings.TrimSpace(token)
	if !ok || path == "" || token == "" {
		t.Fatalf("entry %s consumer %q anchor %q must be path#token", entryID, consumer, anchor)
	}
	if strings.HasSuffix(path, "_test.go") {
		t.Fatalf("entry %s consumer %q anchor %q points at a test file; consumers must be runtime/doc gate call sites", entryID, consumer, anchor)
	}
	if !strings.HasSuffix(path, ".go") {
		t.Fatalf("entry %s consumer %q anchor %q must point at a Go declaration", entryID, consumer, anchor)
	}
	ok, err := goFileDeclaresSymbol(path, token)
	if err != nil {
		t.Fatalf("entry %s consumer %q anchor %q cannot be parsed: %v", entryID, consumer, anchor, err)
	}
	if !ok {
		t.Fatalf("entry %s consumer %q anchor %q does not declare Go symbol %q", entryID, consumer, anchor, token)
	}
}

func goFileDeclaresSymbol(path string, symbol string) (bool, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return false, err
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == symbol {
				return true, nil
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.Name == symbol {
						return true, nil
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.Name == symbol {
							return true, nil
						}
					}
				}
			}
		}
	}
	return false, nil
}

func allowedInterpretationChallengeAdapters() map[string]struct{} {
	values := []string{
		"append_only_challenge_events",
		"bounded_retry_and_failover",
		"current_request_disambiguation",
		"decorrelated_evidence_challenge",
		"deny_escape_or_symlink",
		"deny_unmatched_principal",
		"deterministic_candidate_suppression",
		"deterministic_decorrelation_rules",
		"drop_unsafe_media",
		"effect_attempt_and_durable_state_challenge",
		"effect_attempt_reconciliation",
		"effect_evidence_reconciliation",
		"eval_replay",
		"local_argumentation_and_stable_contract",
		"local_repair_and_visible_fallback",
		"missing_evidence_partial_judgment",
		"non_hydratable_or_operator_only_class",
		"operator_disambiguation",
		"operator_explicit_resume_or_current_intent",
		"operator_override_and_bakeoff",
		"phase_repair_or_block",
		"promotion_abstain_or_operator_review",
		"reject_partial_input",
		"stale_callback_terminalization",
		"stranded_handoff_diagnostics",
		"supersession_terminalization",
		"typed_effect_decision_regeneration",
		"typed_repair_or_block",
		"verification_required",
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
