//go:build linux

package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestInstallReleaseScriptSyntax(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("bash", "-n", "scripts/install-release.sh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n scripts/install-release.sh err = %v out = %s", err, out)
	}
}

func TestInstallReleaseScriptNormalizesArchitectures(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("bash", "-c", `APHELION_INSTALL_RELEASE_NO_RUN=1 source scripts/install-release.sh; normalize_arch x86_64; normalize_arch aarch64`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("normalize_arch err = %v out = %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "amd64\narm64" {
		t.Fatalf("normalize_arch output = %q", got)
	}
}
