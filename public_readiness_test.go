//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
)

func TestConfigExamplePreparesFilesystemUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load("config.example.toml")
	if err != nil {
		t.Fatalf("Load(config.example.toml) err = %v", err)
	}
	if err := prepareFilesystem(cfg); err != nil {
		t.Fatalf("prepareFilesystem(config.example.toml) err = %v", err)
	}

	wantRoot := filepath.Join(home, ".aphelion", "workspace")
	if cfg.Agent.ExecRoot != wantRoot {
		t.Fatalf("exec_root = %q, want %q", cfg.Agent.ExecRoot, wantRoot)
	}
	if _, err := os.Stat(wantRoot); err != nil {
		t.Fatalf("stat prepared exec_root: %v", err)
	}
	if strings.HasPrefix(cfg.Agent.ExecRoot, "/absolute/") {
		t.Fatalf("exec_root still uses placeholder absolute path: %q", cfg.Agent.ExecRoot)
	}
}
