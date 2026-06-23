//go:build linux

package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type executionFrictionSurfaceEntry struct {
	ObservationID       int      `json:"observation_id"`
	Plane               string   `json:"plane"`
	Classification      string   `json:"classification"`
	Title               string   `json:"title"`
	PrincipleDebtIDs    []string `json:"principle_debt_ids"`
	ExistingContracts   []string `json:"existing_contracts"`
	CurrentStatus       string   `json:"current_status"`
	AlwaysOnAnchors     []string `json:"always_on_anchors"`
	ScenarioSpecAnchors []string `json:"scenario_spec_anchors"`
	IdealInvariant      string   `json:"ideal_invariant"`
}

func TestExecutionFrictionSurfaceCoversEveryObservedProblem(t *testing.T) {
	entries := loadExecutionFrictionSurface(t)
	if len(entries) != 24 {
		t.Fatalf("execution friction surface has %d rows, want one for each of 24 observations", len(entries))
	}
	seen := map[int]executionFrictionSurfaceEntry{}
	debtIDs := principleDebtIDs(t)
	for _, entry := range entries {
		if entry.ObservationID < 1 || entry.ObservationID > 24 {
			t.Fatalf("observation id %d out of range in %#v", entry.ObservationID, entry)
		}
		if _, exists := seen[entry.ObservationID]; exists {
			t.Fatalf("duplicate observation id %d", entry.ObservationID)
		}
		seen[entry.ObservationID] = entry
		if strings.TrimSpace(entry.Title) == "" || strings.TrimSpace(entry.Plane) == "" || strings.TrimSpace(entry.IdealInvariant) == "" {
			t.Fatalf("observation %d missing title, plane, or ideal invariant: %#v", entry.ObservationID, entry)
		}
		switch entry.Plane {
		case "authority", "workflow", "presentation", "exposure":
		default:
			t.Fatalf("observation %d plane = %q, want authority/workflow/presentation/exposure", entry.ObservationID, entry.Plane)
		}
		switch entry.Classification {
		case "principle_debt":
			if len(entry.PrincipleDebtIDs) == 0 {
				t.Fatalf("observation %d is principle_debt but has no debt ids: %#v", entry.ObservationID, entry)
			}
		case "incident", "operational_tension":
		default:
			t.Fatalf("observation %d classification = %q, want principle_debt/incident/operational_tension", entry.ObservationID, entry.Classification)
		}
		if len(entry.ExistingContracts) == 0 {
			t.Fatalf("observation %d missing existing contracts: %#v", entry.ObservationID, entry)
		}
		for _, id := range entry.PrincipleDebtIDs {
			if !debtIDs[strings.TrimSpace(id)] {
				t.Fatalf("observation %d references unknown principle debt id %q", entry.ObservationID, id)
			}
		}
		if len(entry.AlwaysOnAnchors) == 0 && len(entry.ScenarioSpecAnchors) == 0 {
			t.Fatalf("observation %d has no executable anchors", entry.ObservationID)
		}
		for _, anchor := range append([]string{}, entry.AlwaysOnAnchors...) {
			assertGoTestAnchorResolves(t, entry.ObservationID, anchor)
		}
		for _, anchor := range entry.ScenarioSpecAnchors {
			assertGoTestAnchorResolves(t, entry.ObservationID, anchor)
		}
		if entry.CurrentStatus != "unassessed" && len(entry.AlwaysOnAnchors) == 0 {
			t.Fatalf("observation %d current_status=%q must have at least one always-on anchor", entry.ObservationID, entry.CurrentStatus)
		}
	}
	for i := 1; i <= 24; i++ {
		if _, ok := seen[i]; !ok {
			t.Fatalf("missing observation id %d", i)
		}
	}
}

func TestExecutionFrictionScenarioSpecAnchorsCoverAllPlanes(t *testing.T) {
	entries := loadExecutionFrictionSurface(t)
	planes := map[string]int{}
	for _, entry := range entries {
		if len(entry.ScenarioSpecAnchors) == 0 {
			t.Fatalf("observation %d missing opt-in scenario spec anchor", entry.ObservationID)
		}
		planes[entry.Plane]++
	}
	for _, plane := range []string{"authority", "workflow", "presentation", "exposure"} {
		if planes[plane] == 0 {
			t.Fatalf("scenario spec surface has no %s rows", plane)
		}
	}
}

func loadExecutionFrictionSurface(t *testing.T) []executionFrictionSurfaceEntry {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("docs", "operations", "execution-friction-test-surface.json"))
	if err != nil {
		t.Fatalf("read execution friction test surface: %v", err)
	}
	var entries []executionFrictionSurfaceEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode execution friction test surface: %v", err)
	}
	return entries
}

func principleDebtIDs(t *testing.T) map[string]bool {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("docs", "architecture", "principle-debt.md"))
	if err != nil {
		t.Fatalf("read principle debt ledger: %v", err)
	}
	re := regexp.MustCompile(`(?m)^### (PD-[A-Za-z0-9._-]+)`)
	out := map[string]bool{}
	for _, match := range re.FindAllStringSubmatch(string(raw), -1) {
		out[match[1]] = true
	}
	return out
}

func assertGoTestAnchorResolves(t *testing.T, observationID int, anchor string) {
	t.Helper()

	path, name, ok := strings.Cut(strings.TrimSpace(anchor), "#")
	if !ok || strings.TrimSpace(path) == "" || strings.TrimSpace(name) == "" {
		t.Fatalf("observation %d anchor %q must be file#TestName", observationID, anchor)
	}
	path = filepath.Clean(path)
	if !strings.HasSuffix(path, "_test.go") {
		t.Fatalf("observation %d anchor %q must point at a Go test file", observationID, anchor)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("observation %d anchor file %q does not resolve: %v", observationID, path, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse anchor file %q: %v", path, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return
		}
	}
	t.Fatalf("observation %d anchor %q does not resolve to a top-level test function", observationID, anchor)
}

func executionFrictionEntriesByObservation(entries []executionFrictionSurfaceEntry, ids ...int) []executionFrictionSurfaceEntry {
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []executionFrictionSurfaceEntry
	for _, entry := range entries {
		if want[entry.ObservationID] {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObservationID < out[j].ObservationID })
	return out
}

func assertExecutionFrictionDebtSpecShape(t *testing.T, ids ...int) {
	t.Helper()

	if os.Getenv("APHELION_RUN_FRICTION_EVALS") != "1" {
		t.Skip("set APHELION_RUN_FRICTION_EVALS=1 to run the execution-friction executable debt specs")
	}
	entries := executionFrictionEntriesByObservation(loadExecutionFrictionSurface(t), ids...)
	if len(entries) != len(ids) {
		got := make([]string, 0, len(entries))
		for _, entry := range entries {
			got = append(got, strconv.Itoa(entry.ObservationID))
		}
		t.Fatalf("debt spec selected observations %v, want %v", got, ids)
	}
	for _, entry := range entries {
		if len(entry.ScenarioSpecAnchors) == 0 {
			t.Fatalf("observation %d has no scenario spec anchor", entry.ObservationID)
		}
		if strings.TrimSpace(entry.CurrentStatus) == "" || strings.TrimSpace(entry.IdealInvariant) == "" {
			t.Fatalf("observation %d lacks current status or ideal invariant: %#v", entry.ObservationID, entry)
		}
		if entry.Classification == "principle_debt" && len(entry.PrincipleDebtIDs) == 0 {
			t.Fatalf("observation %d is principle_debt but lacks debt lineage: %#v", entry.ObservationID, entry)
		}
		if len(entry.ExistingContracts) == 0 {
			t.Fatalf("observation %d lacks contract lineage: %#v", entry.ObservationID, entry)
		}
	}
}
