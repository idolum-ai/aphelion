//go:build linux

package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestFetchURLAllowlistDialsResolvedDestination(t *testing.T) {
	t.Parallel()

	registry, scope, actor := newNativeFetchAllowlistRegistry(t, map[string][]netip.Addr{
		"allowed.test": {netip.MustParseAddr("203.0.113.10")},
	}, []string{"allowed.test:80"})
	dialer := newNativeFetchScriptedDialer(map[string]string{
		"203.0.113.10:80": "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 2\r\n\r\nok",
	})
	registry.nativeFetchDialContext = dialer.dial

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"http://allowed.test/path"}`), scope, actor, session.SessionKey{})
	if err != nil {
		t.Fatalf("fetch_url allowlist err = %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("fetch_url out = %q, want response body", out)
	}
	if got, want := strings.Join(dialer.dialed(), ","), "203.0.113.10:80"; got != want {
		t.Fatalf("dial targets = %q, want %q", got, want)
	}
}

func TestFetchURLAllowlistAllowsHostnameSharingApprovedResolvedDestination(t *testing.T) {
	t.Parallel()

	registry, scope, actor := newNativeFetchAllowlistRegistry(t, map[string][]netip.Addr{
		"allowed.test": {netip.MustParseAddr("203.0.113.10")},
		"shared.test":  {netip.MustParseAddr("203.0.113.10")},
	}, []string{"allowed.test:80"})
	dialer := newNativeFetchScriptedDialer(map[string]string{
		"203.0.113.10:80": "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 6\r\n\r\nshared",
	})
	registry.nativeFetchDialContext = dialer.dial

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"http://shared.test/"}`), scope, actor, session.SessionKey{})
	if err != nil {
		t.Fatalf("fetch_url shared hostname err = %v", err)
	}
	if !strings.Contains(out, "shared") {
		t.Fatalf("fetch_url out = %q, want shared response", out)
	}
	if got, want := strings.Join(dialer.dialed(), ","), "203.0.113.10:80"; got != want {
		t.Fatalf("dial targets = %q, want %q", got, want)
	}
}

func TestFetchURLAllowlistRejectsOutsideResolvedDestination(t *testing.T) {
	t.Parallel()

	registry, scope, actor := newNativeFetchAllowlistRegistry(t, map[string][]netip.Addr{
		"allowed.test": {netip.MustParseAddr("203.0.113.10")},
		"blocked.test": {netip.MustParseAddr("203.0.113.11")},
	}, []string{"allowed.test:80"})
	dialer := newNativeFetchScriptedDialer(nil)
	registry.nativeFetchDialContext = dialer.dial

	_, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"http://blocked.test/"}`), scope, actor, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "sandbox network allowlist") {
		t.Fatalf("fetch_url blocked err = %v, want allowlist rejection", err)
	}
	if got := dialer.dialed(); len(got) != 0 {
		t.Fatalf("dial targets = %#v, want no dial", got)
	}
}

func TestFetchURLAllowlistRejectsRedirectToUnauthorizedDestination(t *testing.T) {
	t.Parallel()

	registry, scope, actor := newNativeFetchAllowlistRegistry(t, map[string][]netip.Addr{
		"allowed.test": {netip.MustParseAddr("203.0.113.10")},
		"blocked.test": {netip.MustParseAddr("203.0.113.11")},
	}, []string{"allowed.test:80"})
	dialer := newNativeFetchScriptedDialer(map[string]string{
		"203.0.113.10:80": "HTTP/1.1 302 Found\r\nLocation: http://blocked.test/\r\nContent-Length: 0\r\n\r\n",
	})
	registry.nativeFetchDialContext = dialer.dial

	_, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"http://allowed.test/"}`), scope, actor, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "sandbox network allowlist") {
		t.Fatalf("fetch_url redirect err = %v, want allowlist rejection", err)
	}
	if got, want := strings.Join(dialer.dialed(), ","), "203.0.113.10:80"; got != want {
		t.Fatalf("dial targets = %q, want only initial dial %q", got, want)
	}
}

func TestFetchURLAllowlistRejectsResolvedPrivateDestinationForNonAdmin(t *testing.T) {
	t.Parallel()

	registry, scope, actor := newNativeFetchAllowlistRegistry(t, map[string][]netip.Addr{
		"loop.test": {netip.MustParseAddr("127.0.0.1")},
	}, []string{"loop.test:80"})
	dialer := newNativeFetchScriptedDialer(nil)
	registry.nativeFetchDialContext = dialer.dial

	_, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"http://loop.test/"}`), scope, actor, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "local/private resolved destinations") {
		t.Fatalf("fetch_url loopback err = %v, want resolved local/private rejection", err)
	}
	if got := dialer.dialed(); len(got) != 0 {
		t.Fatalf("dial targets = %#v, want no dial", got)
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

func newNativeFetchAllowlistRegistry(t *testing.T, records map[string][]netip.Addr, allow []string) (*Registry, sandbox.Scope, principal.Principal) {
	t.Helper()

	workspace := t.TempDir()
	actor := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 42}
	profile := sandbox.DefaultProfiles().ApprovedUser
	profile.Network = sandbox.NetworkAllowlist
	profile.NetworkAllow = sandbox.MustParseNetworkDestinations(allow)
	scope := sandbox.Scope{
		Principal:   actor,
		Profile:     profile,
		GlobalRoot:  workspace,
		WorkingRoot: workspace,
	}
	registry := NewRegistry(workspace, 2*time.Second)
	registry.nativeFetchResolver = func(_ context.Context, host string) ([]netip.Addr, error) {
		addrs, ok := records[strings.ToLower(strings.TrimSuffix(host, "."))]
		if !ok {
			return nil, fmt.Errorf("unexpected host %q", host)
		}
		return append([]netip.Addr(nil), addrs...), nil
	}
	return registry, scope, actor
}

type nativeFetchScriptedDialer struct {
	mu        sync.Mutex
	responses map[string]string
	dials     []string
}

func newNativeFetchScriptedDialer(responses map[string]string) *nativeFetchScriptedDialer {
	copyResponses := make(map[string]string, len(responses))
	for address, response := range responses {
		copyResponses[address] = response
	}
	return &nativeFetchScriptedDialer{responses: copyResponses}
}

func (d *nativeFetchScriptedDialer) dial(_ context.Context, _ string, address string) (net.Conn, error) {
	d.mu.Lock()
	response, ok := d.responses[address]
	d.dials = append(d.dials, address)
	d.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unexpected dial target %q", address)
	}

	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(2 * time.Second))
		reader := bufio.NewReader(server)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" || line == "\n" {
				break
			}
		}
		_, _ = server.Write([]byte(response))
	}()
	return client, nil
}

func (d *nativeFetchScriptedDialer) dialed() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dials...)
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
