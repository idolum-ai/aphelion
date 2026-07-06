//go:build linux

package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

type interpretationSurfaceRegistryEntry struct {
	ID               string                         `json:"id"`
	Surface          string                         `json:"surface"`
	Status           string                         `json:"status"`
	Wiring           string                         `json:"wiring"`
	Readiness        interpretationSurfaceReadiness `json:"readiness"`
	Owners           []string                       `json:"owners"`
	CodeAnchors      []string                       `json:"code_anchors"`
	JudgmentKinds    []string                       `json:"judgment_kinds"`
	Consumers        []string                       `json:"consumers"`
	ConsumerAnchors  map[string]string              `json:"consumer_anchors"`
	Consequences     []string                       `json:"consequences"`
	ChallengeAdapter string                         `json:"challenge_adapter"`
	BehaviorTests    []string                       `json:"behavior_tests"`
}

type interpretationSurfaceReadiness struct {
	Tier           string `json:"tier"`
	Emission       string `json:"emission"`
	Consumption    string `json:"consumption"`
	Reconciliation string `json:"reconciliation"`
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
	facts := collectInterpretationRuntimeFacts(t)
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
		assertInterpretationReadiness(t, entry, facts)
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
		for _, anchor := range entry.BehaviorTests {
			assertInterpretationBehaviorTestAnchor(t, id, strings.TrimSpace(anchor))
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

func TestArchitectureConsequentialJudgmentWritesUseCentralService(t *testing.T) {
	t.Parallel()

	rawStoreMethods := map[string]struct{}{
		"RecordJudgment":                            {},
		"RecordJudgmentUseCommitment":               {},
		"UpsertEffectAttemptWithJudgmentUse":        {},
		"AppendJudgmentChallengeEvent":              {},
		"MarkJudgmentUsesForJudgmentReconciliation": {},
	}
	for _, path := range repoGoFiles(t, false) {
		if strings.HasPrefix(path, "session"+string(filepath.Separator)) ||
			strings.HasPrefix(path, "interpretation"+string(filepath.Separator)) {
			continue
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			serviceVars := interpretationServiceVars(fn.Body)
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if _, ok := rawStoreMethods[selector.Sel.Name]; !ok {
					return true
				}
				if interpretationServiceReceiver(selector.X, serviceVars) {
					return true
				}
				pos := fileSet.Position(selector.Pos())
				t.Fatalf("%s calls %s directly at %s; use interpretation.Service so consequential judgment writes share one contract", path, selector.Sel.Name, pos)
				return false
			})
		}
	}
}

func interpretationServiceVars(body *ast.BlockStmt) map[string]struct{} {
	vars := make(map[string]struct{})
	ast.Inspect(body, func(node ast.Node) bool {
		switch stmt := node.(type) {
		case *ast.AssignStmt:
			for i, rhs := range stmt.Rhs {
				if !interpretationNewServiceCall(rhs) || i >= len(stmt.Lhs) {
					continue
				}
				if ident, ok := stmt.Lhs[i].(*ast.Ident); ok {
					vars[ident.Name] = struct{}{}
				}
			}
		case *ast.ValueSpec:
			for i, rhs := range stmt.Values {
				if !interpretationNewServiceCall(rhs) || i >= len(stmt.Names) {
					continue
				}
				vars[stmt.Names[i].Name] = struct{}{}
			}
		}
		return true
	})
	return vars
}

func interpretationNewServiceCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if selector.Sel.Name == "interpretationService" {
		return true
	}
	if selector.Sel.Name != "NewService" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "interpretation"
}

func interpretationServiceReceiver(expr ast.Expr, serviceVars map[string]struct{}) bool {
	switch value := expr.(type) {
	case *ast.Ident:
		_, ok := serviceVars[value.Name]
		return ok
	default:
		return interpretationNewServiceCall(value)
	}
}

type interpretationRuntimeFacts struct {
	ProducedJudgmentKinds map[string]struct{}
	ConsumedConsumerIDs   map[string]struct{}
}

func assertInterpretationReadiness(t *testing.T, entry interpretationSurfaceRegistryEntry, facts interpretationRuntimeFacts) {
	t.Helper()
	id := strings.TrimSpace(entry.ID)
	readiness := entry.Readiness
	switch readiness.Tier {
	case "registered", "emitted", "consumed", "reconcilable", "not_applicable":
	default:
		t.Fatalf("entry %s readiness tier = %q, want registered/emitted/consumed/reconcilable/not_applicable", id, readiness.Tier)
	}
	switch readiness.Emission {
	case "registered", "emitted", "structural", "not_applicable":
	default:
		t.Fatalf("entry %s readiness emission = %q, want registered/emitted/structural/not_applicable", id, readiness.Emission)
	}
	switch readiness.Consumption {
	case "registered", "consumed", "structural", "not_applicable":
	default:
		t.Fatalf("entry %s readiness consumption = %q, want registered/consumed/structural/not_applicable", id, readiness.Consumption)
	}
	switch readiness.Reconciliation {
	case "registered", "reconcilable", "structural", "not_applicable":
	default:
		t.Fatalf("entry %s readiness reconciliation = %q, want registered/reconcilable/structural/not_applicable", id, readiness.Reconciliation)
	}
	if entry.Status == "not_applicable" && readiness.Tier != "not_applicable" {
		t.Fatalf("entry %s is not_applicable but readiness tier = %q", id, readiness.Tier)
	}
	if readiness.Tier == "reconcilable" && readiness.Reconciliation != "reconcilable" {
		t.Fatalf("entry %s readiness tier reconcilable but reconciliation = %q", id, readiness.Reconciliation)
	}
	if readiness.Reconciliation == "reconcilable" && len(entry.BehaviorTests) == 0 {
		t.Fatalf("entry %s claims reconcilable readiness but has no behavior_tests", id)
	}
	if readiness.Emission == "emitted" {
		if len(entry.JudgmentKinds) == 0 {
			t.Fatalf("entry %s claims emitted readiness but declares no judgment_kinds", id)
		}
		for _, kind := range entry.JudgmentKinds {
			kind = strings.TrimSpace(kind)
			if kind == "" || kind == "*" {
				continue
			}
			if _, ok := facts.ProducedJudgmentKinds[kind]; !ok {
				t.Fatalf("entry %s declares emitted judgment kind %q but no production RecordJudgment call emits it", id, kind)
			}
		}
	}
	if readiness.Consumption == "consumed" {
		if len(entry.Consumers) == 0 {
			t.Fatalf("entry %s claims consumed readiness but declares no consumers", id)
		}
		for _, consumer := range entry.Consumers {
			consumer = strings.TrimSpace(consumer)
			if consumer == "" {
				continue
			}
			if _, ok := facts.ConsumedConsumerIDs[consumer]; !ok {
				t.Fatalf("entry %s declares consumed consumer %q but no production RecordJudgmentUseCommitment path records it", id, consumer)
			}
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

func assertInterpretationBehaviorTestAnchor(t *testing.T, entryID string, anchor string) {
	t.Helper()
	path, token, ok := strings.Cut(anchor, "#")
	path = strings.TrimSpace(path)
	token = strings.TrimSpace(token)
	if !ok || path == "" || token == "" {
		t.Fatalf("entry %s behavior test anchor %q must be path#TestName", entryID, anchor)
	}
	if !strings.HasSuffix(path, "_test.go") {
		t.Fatalf("entry %s behavior test anchor %q must point at a test file", entryID, anchor)
	}
	ok, err := goFileDeclaresSymbol(path, token)
	if err != nil {
		t.Fatalf("entry %s behavior test anchor %q cannot be parsed: %v", entryID, anchor, err)
	}
	if !ok {
		t.Fatalf("entry %s behavior test anchor %q does not declare test %q", entryID, anchor, token)
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
	values := session.RegisteredInterpretationChallengeAdapters()
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func collectInterpretationRuntimeFacts(t *testing.T) interpretationRuntimeFacts {
	t.Helper()
	paths := repoGoFiles(t, false)
	constants := collectStringConstants(t, paths)
	facts := interpretationRuntimeFacts{
		ProducedJudgmentKinds: make(map[string]struct{}),
		ConsumedConsumerIDs:   make(map[string]struct{}),
	}
	for _, path := range paths {
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			switch compositeTypeName(lit.Type) {
			case "JudgmentInput":
				if kind, ok := compositeStringField(lit, "Kind", constants); ok {
					facts.ProducedJudgmentKinds[kind] = struct{}{}
				}
			case "JudgmentUseInput":
				if consumer, ok := compositeStringField(lit, "ConsumerID", constants); ok {
					facts.ConsumedConsumerIDs[consumer] = struct{}{}
				}
			case "runtimeJudgmentUseInput":
				if kind, ok := compositeStringField(lit, "Kind", constants); ok {
					facts.ProducedJudgmentKinds[kind] = struct{}{}
				}
				if consumer, ok := compositeStringField(lit, "ConsumerID", constants); ok {
					facts.ConsumedConsumerIDs[consumer] = struct{}{}
				}
			}
			return true
		})
	}
	return facts
}

func collectStringConstants(t *testing.T, paths []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, path := range paths {
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok || len(valueSpec.Values) != 1 {
					continue
				}
				value, ok := stringLiteralValue(valueSpec.Values[0])
				if !ok {
					continue
				}
				for _, name := range valueSpec.Names {
					out[name.Name] = value
					out[file.Name.Name+"."+name.Name] = value
				}
			}
		}
	}
	return out
}

func compositeStringField(lit *ast.CompositeLit, field string, constants map[string]string) (string, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != field {
			continue
		}
		return stringExprValue(kv.Value, constants)
	}
	return "", false
}

func compositeTypeName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func stringExprValue(expr ast.Expr, constants map[string]string) (string, bool) {
	if value, ok := stringLiteralValue(expr); ok {
		return value, true
	}
	switch value := expr.(type) {
	case *ast.Ident:
		out, ok := constants[value.Name]
		return out, ok
	case *ast.SelectorExpr:
		if out, ok := constants[value.Sel.Name]; ok {
			return out, true
		}
		if pkg, ok := value.X.(*ast.Ident); ok {
			out, ok := constants[pkg.Name+"."+value.Sel.Name]
			return out, ok
		}
	}
	return "", false
}

func stringLiteralValue(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(value), value != ""
}

func repoGoFiles(t *testing.T, includeTests bool) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch path {
			case ".git", "tmp":
				return filepath.SkipDir
			}
			if strings.HasPrefix(path, ".git"+string(filepath.Separator)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo Go files: %v", err)
	}
	return out
}
