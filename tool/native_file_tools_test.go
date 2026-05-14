//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestNativeFileToolsStayInsideScopedRoots(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(workspace), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside secret: %v", err)
	}

	registry := NewRegistry(workspace, 2*time.Second)
	scope := sandbox.Scope{
		Principal:        principal.Principal{Role: principal.RoleAdmin},
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       workspace,
		SharedMemoryRoot: workspace,
		WorkingRoot:      workspace,
	}

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "write_file", json.RawMessage(`{"path":"notes/one.txt","content":"alpha\nneedle\n","create_dirs":true}`), scope, scope.Principal, session.SessionKey{})
	if err != nil {
		t.Fatalf("write_file err = %v", err)
	}
	if !strings.Contains(out, "write_file_ok") {
		t.Fatalf("write_file out = %q", out)
	}

	out, err = registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"notes/one.txt"}`), scope, scope.Principal, session.SessionKey{})
	if err != nil {
		t.Fatalf("read_file err = %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "[READ_FILE]") {
		t.Fatalf("read_file out = %q", out)
	}

	out, err = registry.executeWithScopeAndPrincipal(context.Background(), "list_dir", json.RawMessage(`{"path":"notes"}`), scope, scope.Principal, session.SessionKey{})
	if err != nil {
		t.Fatalf("list_dir err = %v", err)
	}
	if !strings.Contains(out, "one.txt file") {
		t.Fatalf("list_dir out = %q", out)
	}

	out, err = registry.executeWithScopeAndPrincipal(context.Background(), "search", json.RawMessage(`{"query":"needle","path":"."}`), scope, scope.Principal, session.SessionKey{})
	if err != nil {
		t.Fatalf("search err = %v", err)
	}
	if !strings.Contains(out, "one.txt:2: needle") {
		t.Fatalf("search out = %q", out)
	}

	_, err = registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"../outside-secret.txt"}`), scope, scope.Principal, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("read_file escape err = %v, want scoped rejection", err)
	}
}

func TestNativeFileToolsHonorApprovedUserProfile(t *testing.T) {
	t.Parallel()

	global := t.TempDir()
	shared := t.TempDir()
	workspace := t.TempDir()
	userMemory := t.TempDir()
	if err := os.WriteFile(filepath.Join(global, "public.txt"), []byte("visible"), 0o600); err != nil {
		t.Fatalf("write global fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(userMemory, "hidden"), 0o755); err != nil {
		t.Fatalf("mkdir hidden fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userMemory, "hidden", "secret.txt"), []byte("hidden"), 0o600); err != nil {
		t.Fatalf("write hidden fixture: %v", err)
	}

	profile := sandbox.DefaultProfiles().ApprovedUser
	profile.WritablePaths = []string{"{user_workspace}", "{user_memory}"}
	profile.HiddenPaths = append(profile.HiddenPaths, "{user_memory}/hidden")
	p := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 42}
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          profile,
		GlobalRoot:       global,
		SharedMemoryRoot: shared,
		UserWorkspace:    workspace,
		UserMemory:       userMemory,
		WorkingRoot:      workspace,
	}
	registry := NewRegistry(workspace, 2*time.Second)

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(global, "public.txt"))+`"}`), scope, p, session.SessionKey{})
	if err != nil {
		t.Fatalf("read_file global readonly err = %v", err)
	}
	if !strings.Contains(out, "visible") {
		t.Fatalf("read_file readonly out = %q", out)
	}

	_, err = registry.executeWithScopeAndPrincipal(context.Background(), "write_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(global, "public.txt"))+`","content":"mutate"}`), scope, p, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "outside the write roots") {
		t.Fatalf("write_file readonly err = %v, want write-root rejection", err)
	}

	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "write_file", json.RawMessage(`{"path":"note.txt","content":"ok"}`), scope, p, session.SessionKey{}); err != nil {
		t.Fatalf("write_file workspace err = %v", err)
	}

	_, err = registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(userMemory, "hidden", "secret.txt"))+`"}`), scope, p, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "hidden by the sandbox profile") {
		t.Fatalf("read_file hidden err = %v, want hidden-path rejection", err)
	}
}

func TestWriteFileCreateDirsValidatesSymlinkAncestorBeforeMkdir(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	registry := NewRegistry(workspace, 2*time.Second)
	scope := sandbox.Scope{
		Principal:        principal.Principal{Role: principal.RoleAdmin},
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       workspace,
		SharedMemoryRoot: workspace,
		WorkingRoot:      workspace,
	}

	_, err := registry.executeWithScopeAndPrincipal(context.Background(), "write_file", json.RawMessage(`{"path":"link/newdir/file.txt","content":"nope","create_dirs":true}`), scope, scope.Principal, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "outside writable sandbox roots") {
		t.Fatalf("write_file err = %v, want pre-mkdir writable-root rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "newdir")); !os.IsNotExist(statErr) {
		t.Fatalf("outside newdir stat err = %v, want directory not created", statErr)
	}
}

func TestFetchURLHonorsNetworkPolicy(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello from server"))
	}))
	defer server.Close()

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 2*time.Second)
	admin := principal.Principal{Role: principal.RoleAdmin}
	adminScope := sandbox.Scope{
		Principal:   admin,
		Profile:     sandbox.DefaultProfiles().Admin,
		GlobalRoot:  workspace,
		WorkingRoot: workspace,
	}

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"`+server.URL+`"}`), adminScope, admin, session.SessionKey{})
	if err != nil {
		t.Fatalf("fetch_url admin err = %v", err)
	}
	if !strings.Contains(out, "hello from server") || !strings.Contains(out, "[FETCH_URL]") {
		t.Fatalf("fetch_url out = %q", out)
	}

	approved := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 42}
	deniedProfile := sandbox.DefaultProfiles().ApprovedUser
	deniedScope := sandbox.Scope{
		Principal:   approved,
		Profile:     deniedProfile,
		GlobalRoot:  workspace,
		WorkingRoot: workspace,
	}
	_, err = registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"`+server.URL+`"}`), deniedScope, approved, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "network policy") {
		t.Fatalf("fetch_url denied err = %v, want network-policy rejection", err)
	}

	allowlistProfile := sandbox.DefaultProfiles().ApprovedUser
	allowlistProfile.Network = sandbox.NetworkAllowlist
	allowlistScope := deniedScope
	allowlistScope.Profile = allowlistProfile
	_, err = registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"https://example.com"}`), allowlistScope, approved, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "allowlist has no destinations") {
		t.Fatalf("fetch_url allowlist err = %v, want empty-allowlist rejection", err)
	}
}

func TestFetchURLUsesConfiguredUserAgent(t *testing.T) {
	t.Parallel()

	seen := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	workspace := t.TempDir()
	admin := principal.Principal{Role: principal.RoleAdmin}
	adminScope := sandbox.Scope{
		Principal:   admin,
		Profile:     sandbox.DefaultProfiles().Admin,
		GlobalRoot:  workspace,
		WorkingRoot: workspace,
	}
	registry := NewRegistry(workspace, 2*time.Second).WithUserAgent("custom-fetch/1")
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"`+server.URL+`"}`), adminScope, admin, session.SessionKey{}); err != nil {
		t.Fatalf("fetch_url custom user-agent err = %v", err)
	}
	if got := <-seen; got != "custom-fetch/1" {
		t.Fatalf("User-Agent = %q, want custom-fetch/1", got)
	}

	registry.WithUserAgent("")
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"`+server.URL+`"}`), adminScope, admin, session.SessionKey{}); err != nil {
		t.Fatalf("fetch_url anonymous user-agent err = %v", err)
	}
	if got := <-seen; strings.Contains(strings.ToLower(got), "aphelion") || got == "custom-fetch/1" {
		t.Fatalf("User-Agent = %q, want anonymous override without Aphelion/custom identity", got)
	}
}

func TestDefinitionsIncludeNativeFileTools(t *testing.T) {
	t.Parallel()

	defs := NewRegistry(t.TempDir(), 2*time.Second).Definitions()
	names := make(map[string]bool, len(defs))
	for _, def := range defs {
		names[def.Name] = true
	}
	for _, name := range []string{"read_file", "write_file", "list_dir", "search", "fetch_url"} {
		if !names[name] {
			t.Fatalf("Definitions() missing %s", name)
		}
	}
}
