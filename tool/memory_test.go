//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestMemoryToolAdminWritesSharedKnowledge(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"memory",
		json.RawMessage(`{"action":"add","scope":"shared","store":"knowledge","content":"Prefers concise updates","source_tag":"observed","confidence":0.9}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal(memory) err = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(tmp, "shared-memory", "memory", "knowledge.md"))
	if err != nil {
		t.Fatalf("ReadFile() err = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "Prefers concise updates") {
		t.Fatalf("knowledge.md = %q, want content", text)
	}
	if !strings.Contains(text, "[observed, confidence: 0.90]") {
		t.Fatalf("knowledge.md = %q, want provenance tag", text)
	}
}

func TestMemoryToolApprovedUserWritesPrincipalMemory(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"memory",
		json.RawMessage(`{"action":"add","store":"memory","content":"Private preference retained."}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal(memory) err = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(tmp, "users-memory", "42", "MEMORY.md"))
	if err != nil {
		t.Fatalf("ReadFile() err = %v", err)
	}
	if !strings.Contains(string(raw), "Private preference retained.") {
		t.Fatalf("MEMORY.md = %q, want content", string(raw))
	}
}

func TestMemoryToolApprovedUserCannotWriteSharedMemory(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"memory",
		json.RawMessage(`{"action":"add","scope":"shared","store":"memory","content":"should fail"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForPrincipal(memory) err = nil, want shared memory denial")
	}
	if !strings.Contains(err.Error(), "may not write shared memory") {
		t.Fatalf("err = %v, want shared memory denial", err)
	}
}
