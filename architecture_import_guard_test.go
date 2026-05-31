//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const aphelionModulePath = "github.com/idolum-ai/aphelion"

type architecturePackage struct {
	ImportPath string
	Imports    []string
}

func TestRootPackageHasNoShimFiles(t *testing.T) {
	t.Parallel()

	matches, err := filepath.Glob("*_shims.go")
	if err != nil {
		t.Fatalf("glob root shim files: %v", err)
	}
	if len(matches) > 0 {
		t.Fatalf("root shim files are forbidden; found %s", strings.Join(matches, ", "))
	}
}

func TestRootPackageDoesNotAliasInternalExportedTypes(t *testing.T) {
	t.Parallel()

	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob root go files: %v", err)
	}
	fset := token.NewFileSet()
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports for %s: %v", path, err)
		}
		internalImports := importedInternalPackageNames(file)
		if len(internalImports) == 0 {
			continue
		}
		file, err = parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || !typeSpec.Assign.IsValid() {
					continue
				}
				sel, ok := typeSpec.Type.(*ast.SelectorExpr)
				if !ok || !ast.IsExported(typeSpec.Name.Name) || !ast.IsExported(sel.Sel.Name) {
					continue
				}
				ident, ok := sel.X.(*ast.Ident)
				if ok && internalImports[ident.Name] {
					t.Fatalf("%s aliases internal exported type %s = %s.%s; root back-compat aliases are forbidden", path, typeSpec.Name.Name, ident.Name, sel.Sel.Name)
				}
			}
		}
	}
}

func importedInternalPackageNames(file *ast.File) map[string]bool {
	out := make(map[string]bool)
	for _, imported := range file.Imports {
		path := strings.Trim(imported.Path.Value, "\"")
		if !strings.HasPrefix(path, aphelionModulePath+"/internal/") {
			continue
		}
		name := ""
		if imported.Name != nil {
			name = strings.TrimSpace(imported.Name.Name)
		}
		if name == "" {
			parts := strings.Split(path, "/")
			name = parts[len(parts)-1]
		}
		if name != "" && name != "." && name != "_" {
			out[name] = true
		}
	}
	return out
}

func TestArchitectureImportBoundaries(t *testing.T) {
	t.Parallel()

	packages := loadArchitecturePackages(t)
	for _, pkg := range packages {
		assertRuntimeImportBoundary(t, pkg)
		assertLayerImportBoundaries(t, pkg)
		assertCredentialAndBackendMembraneBoundaries(t, pkg)
		assertDurableAgentDoesNotOwnToolAuthority(t, pkg)
	}
}

func loadArchitecturePackages(t *testing.T) []architecturePackage {
	t.Helper()

	cmd := exec.Command("go", "list", "-json", "./...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list packages failed: %v\n%s", err, string(out))
	}

	dec := json.NewDecoder(bytes.NewReader(out))
	var packages []architecturePackage
	for {
		var pkg architecturePackage
		err := dec.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode go list package JSON: %v", err)
		}
		if pkg.ImportPath == "" || !strings.HasPrefix(pkg.ImportPath, aphelionModulePath) {
			continue
		}
		packages = append(packages, pkg)
	}
	return packages
}

func assertRuntimeImportBoundary(t *testing.T, pkg architecturePackage) {
	t.Helper()

	runtimeRoot := aphelionModulePath + "/runtime"
	if pkg.ImportPath == aphelionModulePath {
		return
	}
	for _, imported := range pkg.Imports {
		if imported == runtimeRoot {
			t.Fatalf("%s imports runtime; only the root composition package may import the runtime shell", pkg.ImportPath)
		}
		// Runtime leaf packages such as runtime/codex, runtime/doctor, and
		// runtime/mission are private helpers for the runtime shell. Keeping them
		// root-runtime-only prevents leaf extraction from becoming a new public
		// orchestration API.
		if strings.HasPrefix(imported, runtimeRoot+"/") && pkg.ImportPath != runtimeRoot {
			t.Fatalf("%s imports runtime internals; only the runtime shell may import runtime leaf packages", pkg.ImportPath)
		}
	}
}

func assertLayerImportBoundaries(t *testing.T, pkg architecturePackage) {
	t.Helper()

	switch pkg.ImportPath {
	case aphelionModulePath + "/turn":
		assertDoesNotImportAny(t, pkg, []string{
			"runtime",
			"telegram",
			"provider",
			"openai",
			"tool",
			"tool/sandbox",
			"tailnet",
			"config",
		})
	case aphelionModulePath + "/pipeline":
		assertDoesNotImportAny(t, pkg, []string{
			"runtime",
			"turn",
			"telegram",
			"session",
			"durableagent",
			"provider",
			"openai",
			"tool",
			"tool/sandbox",
			"memory",
			"tailnet",
			"config",
		})
	case aphelionModulePath + "/telegram",
		aphelionModulePath + "/tool",
		aphelionModulePath + "/session",
		aphelionModulePath + "/durableagent":
		assertDoesNotImportAny(t, pkg, []string{
			"runtime",
			"turn",
			"pipeline",
		})
	}
}

func assertCredentialAndBackendMembraneBoundaries(t *testing.T, pkg architecturePackage) {
	t.Helper()

	for _, rule := range []struct {
		prefix    string
		forbidden []string
		why       string
	}{
		{
			prefix: "governorauth",
			forbidden: []string{
				"governorbackend",
				"runtime",
				"tool",
				"session",
				"telegram",
				"internal/telegramcommands",
				"internal/telegramcontrol",
				"internal/telegramdecision",
				"internal/telegramruntime",
			},
			why: "governorauth resolves auth material; backend transport, runtime orchestration, tool authority, session state, and Telegram adapters must stay outside that membrane",
		},
		{
			prefix: "governorbackend",
			forbidden: []string{
				"runtime",
				"tool",
				"session",
				"telegram",
				"internal/telegramcommands",
				"internal/telegramcontrol",
				"internal/telegramdecision",
				"internal/telegramruntime",
			},
			why: "governorbackend is a provider-shaped backend adapter; runtime wiring, tools, sessions, and Telegram surfaces must not leak inward",
		},
		{
			prefix: "githubapp",
			forbidden: []string{
				"runtime",
				"tool",
				"session",
				"telegram",
				"internal/telegramcommands",
				"internal/telegramcontrol",
				"internal/telegramdecision",
				"internal/telegramruntime",
			},
			why: "githubapp is a credential membrane; PR/workflow authority, tool invocation, session state, and UI adapters must stay outside it",
		},
	} {
		if !isPackageOrSubpackage(pkg, rule.prefix) {
			continue
		}
		for _, forbidden := range rule.forbidden {
			if importsPackage(pkg, aphelionModulePath+"/"+forbidden) {
				t.Fatalf("%s imports %s; %s", pkg.ImportPath, forbidden, rule.why)
			}
		}
	}
}

func assertDurableAgentDoesNotOwnToolAuthority(t *testing.T, pkg architecturePackage) {
	t.Helper()
	if !isPackageOrSubpackage(pkg, "durableagent") {
		return
	}
	// durableagent may depend on session storage contracts, but tool authority,
	// runtime orchestration, and Telegram adapters must stay outside the child
	// continuation substrate.
	assertDoesNotImportAny(t, pkg, []string{
		"tool",
		"runtime",
		"telegram",
		"internal/telegramcommands",
		"internal/telegramcontrol",
		"internal/telegramdecision",
		"internal/telegramruntime",
	})
}

func isPackageOrSubpackage(pkg architecturePackage, localTarget string) bool {
	target := aphelionModulePath + "/" + strings.Trim(localTarget, "/")
	return pkg.ImportPath == target || strings.HasPrefix(pkg.ImportPath, target+"/")
}

func assertDoesNotImportAny(t *testing.T, pkg architecturePackage, localTargets []string) {
	t.Helper()

	for _, localTarget := range localTargets {
		target := aphelionModulePath + "/" + strings.Trim(localTarget, "/")
		if importsPackage(pkg, target) {
			t.Fatalf("%s imports %s; architecture boundary forbids that dependency", pkg.ImportPath, target)
		}
	}
}

func importsPackage(pkg architecturePackage, target string) bool {
	for _, imported := range pkg.Imports {
		if imported == target || strings.HasPrefix(imported, target+"/") {
			return true
		}
	}
	return false
}

func TestImportsPackageMatchesExactAndSubpackages(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		imports []string
		target  string
		want    bool
	}{
		{
			name:    "exact",
			imports: []string{aphelionModulePath + "/runtime"},
			target:  aphelionModulePath + "/runtime",
			want:    true,
		},
		{
			name:    "subpackage",
			imports: []string{aphelionModulePath + "/tool/sandbox"},
			target:  aphelionModulePath + "/tool",
			want:    true,
		},
		{
			name:    "sibling prefix is not subpackage",
			imports: []string{aphelionModulePath + "/runtimechild"},
			target:  aphelionModulePath + "/runtime",
			want:    false,
		},
		{
			name:    "absent",
			imports: []string{aphelionModulePath + "/session"},
			target:  aphelionModulePath + "/telegram",
			want:    false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pkg := architecturePackage{
				ImportPath: aphelionModulePath + "/turn",
				Imports:    tc.imports,
			}
			if got := importsPackage(pkg, tc.target); got != tc.want {
				t.Fatalf("importsPackage(%q) = %t, want %t", tc.target, got, tc.want)
			}
		})
	}
}
