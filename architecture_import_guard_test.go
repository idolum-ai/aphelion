//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

const aphelionModulePath = "github.com/idolum-ai/aphelion"

type architecturePackage struct {
	ImportPath string
	Imports    []string
}

func TestArchitectureImportBoundaries(t *testing.T) {
	t.Parallel()

	packages := loadArchitecturePackages(t)
	for _, pkg := range packages {
		assertRuntimeImportBoundary(t, pkg)
		assertLayerImportBoundaries(t, pkg)
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

	if pkg.ImportPath == aphelionModulePath {
		return
	}
	if importsPackage(pkg, aphelionModulePath+"/runtime") {
		t.Fatalf("%s imports runtime; only the root composition package may import runtime", pkg.ImportPath)
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
		if imported == target {
			return true
		}
	}
	return false
}
