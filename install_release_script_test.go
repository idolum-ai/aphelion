//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestInstallReleaseScriptDownloadFailureIsClean(t *testing.T) {
	t.Parallel()

	binDir := t.TempDir()
	curlPath := filepath.Join(binDir, "curl")
	if err := os.WriteFile(curlPath, []byte("#!/usr/bin/env bash\necho fake curl failure >&2\nexit 22\n"), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}

	cmd := exec.Command("bash", "-c", `APHELION_INSTALL_RELEASE_NO_RUN=1 source scripts/install-release.sh; install_release v0.0.0`)
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install_release unexpectedly succeeded; out = %s", out)
	}
	text := string(out)
	if !strings.Contains(text, "ERROR: download failed: https://github.com/idolum-ai/aphelion/releases/download/v0.0.0/aphelion-linux-") {
		t.Fatalf("install_release output = %q, want clean download failure", text)
	}
	if strings.Contains(text, "unbound variable") {
		t.Fatalf("install_release output leaked shell trap failure: %q", text)
	}
}
