//go:build linux

package tool

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
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

	out, err = registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"notes/one.txt","full":true}`), scope, scope.Principal, session.SessionKey{})
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

	_, err = registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"../outside-secret.txt","full":true}`), scope, scope.Principal, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("read_file escape err = %v, want scoped rejection", err)
	}
}

func TestWriteFileAcceptsJSONStringWrappedObjectInputWithEscapedNewline(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 2*time.Second)
	input := stringWrappedJSON(t, `{"path":"reports/one.txt","content":"line one\nline two\n","create_dirs":true}`)

	out, err := registry.Execute(context.Background(), "write_file", input)
	if err != nil {
		t.Fatalf("Execute(write_file) err = %v", err)
	}
	if !strings.Contains(out, "write_file_ok") {
		t.Fatalf("write_file out = %q, want success marker", out)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "reports", "one.txt"))
	if err != nil {
		t.Fatalf("ReadFile() err = %v", err)
	}
	if string(data) != "line one\nline two\n" {
		t.Fatalf("written content = %q, want newline-preserving content", string(data))
	}
}

func TestWriteFileRejectsTruncatedStringWrappedObjectBeforeExecution(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 2*time.Second)
	input := json.RawMessage(`"{\"path\":\"reports/one.txt\",\"content\":\"line one"`)

	_, err := registry.Execute(context.Background(), "write_file", input)
	if err == nil || !strings.Contains(err.Error(), "invalid tool arguments") {
		t.Fatalf("Execute(write_file) err = %v, want invalid tool arguments", err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "reports", "one.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("truncated input created file, stat err = %v", statErr)
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

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(global, "public.txt"))+`","full":true}`), scope, p, session.SessionKey{})
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

	_, err = registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(userMemory, "hidden", "secret.txt"))+`","full":true}`), scope, p, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "hidden by the sandbox profile") {
		t.Fatalf("read_file hidden err = %v, want hidden-path rejection", err)
	}
}

func TestNativeFileToolsHonorAdminReadonlyPathsForSourceCheckout(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sourceCheckout := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceCheckout, "AGENTS.md"), []byte("source checkout visible"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	registry := NewRegistry(workspace, 2*time.Second)
	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       filepath.Join(workspace, "prompt"),
		SharedMemoryRoot: filepath.Join(workspace, "shared"),
		WorkingRoot:      workspace,
	}

	_, err := registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(sourceCheckout, "AGENTS.md"))+`","full":true}`), scope, p, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("read_file without admin readonly path err = %v, want read-root rejection", err)
	}

	scope.Profile.ReadonlyPaths = []string{sourceCheckout}
	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(sourceCheckout, "AGENTS.md"))+`","full":true}`), scope, p, session.SessionKey{})
	if err != nil {
		t.Fatalf("read_file with admin readonly path err = %v", err)
	}
	if !strings.Contains(out, "source checkout visible") {
		t.Fatalf("read_file out = %q, want source checkout content", out)
	}
}

func TestNativeFileToolsUseActiveFileAccessGrantAsReadRoot(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	workspace := t.TempDir()
	childRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(childRoot, "runtime-bin"), 0o755); err != nil {
		t.Fatalf("mkdir child runtime fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(childRoot, "runtime-bin", "gogcli"), []byte("needle child runtime\n"), 0o600); err != nil {
		t.Fatalf("write child runtime fixture: %v", err)
	}
	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       filepath.Join(workspace, "global"),
		SharedMemoryRoot: filepath.Join(workspace, "shared"),
		WorkingRoot:      workspace,
	}
	target := filepath.Join(childRoot, "runtime-bin")
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-child-runtime-read",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: target,
		AllowedActions: []string{"read"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(file_access) err = %v", err)
	}

	_, err := registry.executeWithScopeAndPrincipal(context.Background(), "list_dir", json.RawMessage(`{"path":"`+filepath.ToSlash(target)+`"}`), scope, p, key)
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("list_dir without lease err = %v, want read-root rejection", err)
	}

	grantAuthorityUseLeaseWithID(t, store, key, "lease-child-runtime-read")
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, p, "lease-child-runtime-read", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_file_access")
	out, err := registry.executeWithScopeAndPrincipal(ctx, "list_dir", json.RawMessage(`{"path":"`+filepath.ToSlash(target)+`"}`), scope, p, key)
	if err != nil {
		t.Fatalf("list_dir with file_access grant err = %v", err)
	}
	if !strings.Contains(out, "gogcli file") {
		t.Fatalf("list_dir out = %q, want granted child runtime entry", out)
	}

	out, err = registry.executeWithScopeAndPrincipal(ctx, "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(target, "gogcli"))+`","full":true}`), scope, p, key)
	if err != nil {
		t.Fatalf("read_file with file_access grant err = %v", err)
	}
	if !strings.Contains(out, "needle child runtime") {
		t.Fatalf("read_file out = %q, want granted child runtime content", out)
	}

	out, err = registry.executeWithScopeAndPrincipal(ctx, "search", json.RawMessage(`{"query":"needle","path":"`+filepath.ToSlash(target)+`"}`), scope, p, key)
	if err != nil {
		t.Fatalf("search with file_access grant err = %v", err)
	}
	if !strings.Contains(out, "gogcli:1: needle child runtime") {
		t.Fatalf("search out = %q, want granted child runtime match", out)
	}

	_, err = registry.executeWithScopeAndPrincipal(ctx, "write_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(target, "created.txt"))+`","content":"no"}`), scope, p, key)
	if err == nil || !strings.Contains(err.Error(), "outside the write roots") {
		t.Fatalf("write_file with file_access grant err = %v, want write-root rejection", err)
	}

	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-child-runtime-write",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: target,
		AllowedActions: []string{"write"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(write file_access) err = %v", err)
	}
	noLeaseKey := session.SessionKey{ChatID: 1002, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "1002"}}
	_, err = registry.executeWithScopeAndPrincipal(context.Background(), "write_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(target, "created-without-lease.txt"))+`","content":"no"}`), scope, p, noLeaseKey)
	if err == nil || !strings.Contains(err.Error(), "outside the write roots") {
		t.Fatalf("write_file write grant without lease err = %v, want write-root rejection", err)
	}
	out, err = registry.executeWithScopeAndPrincipal(ctx, "write_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(target, "config", "created.txt"))+`","content":"created under approved child slot","create_dirs":true}`), scope, p, key)
	if err != nil {
		t.Fatalf("write_file with write file_access grant err = %v", err)
	}
	if !strings.Contains(out, "write_file_ok") {
		t.Fatalf("write_file out = %q, want success marker", out)
	}
	if data, err := os.ReadFile(filepath.Join(target, "config", "created.txt")); err != nil || string(data) != "created under approved child slot" {
		t.Fatalf("created file data = %q err=%v, want approved child slot write", string(data), err)
	}

	assertNativeFileAccessInvocations(t, store, "capg-child-runtime-read", map[string]string{
		"list_dir":  "lease-child-runtime-read",
		"read_file": "lease-child-runtime-read",
		"search":    "lease-child-runtime-read",
	})
	assertNativeFileAccessInvocations(t, store, "capg-child-runtime-write", map[string]string{
		"write_file": "lease-child-runtime-read",
	})
}

func TestNativeFileAccessGrantKeepsNarrowActionsNarrow(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	workspace := t.TempDir()
	childRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(childRoot, "note.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatalf("write child fixture: %v", err)
	}
	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	grantAuthorityUseLeaseWithID(t, store, key, "lease-narrow-file-access")
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, p, "lease-narrow-file-access", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_file_access")
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       filepath.Join(workspace, "global"),
		SharedMemoryRoot: filepath.Join(workspace, "shared"),
		WorkingRoot:      workspace,
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-child-list-only",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: childRoot,
		AllowedActions: []string{"list"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(list) err = %v", err)
	}

	out, err := registry.executeWithScopeAndPrincipal(ctx, "list_dir", json.RawMessage(`{"path":"`+filepath.ToSlash(childRoot)+`"}`), scope, p, key)
	if err != nil {
		t.Fatalf("list_dir with list grant err = %v", err)
	}
	if !strings.Contains(out, "note.txt file") {
		t.Fatalf("list_dir out = %q, want note entry", out)
	}
	_, err = registry.executeWithScopeAndPrincipal(ctx, "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(childRoot, "note.txt"))+`","full":true}`), scope, p, key)
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("read_file with list-only grant err = %v, want read-root rejection", err)
	}

	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-child-search-only",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: childRoot,
		AllowedActions: []string{"search"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(search) err = %v", err)
	}
	out, err = registry.executeWithScopeAndPrincipal(ctx, "search", json.RawMessage(`{"query":"needle","path":"`+filepath.ToSlash(childRoot)+`"}`), scope, p, key)
	if err != nil {
		t.Fatalf("search with search grant err = %v", err)
	}
	if !strings.Contains(out, "note.txt:1: needle") {
		t.Fatalf("search out = %q, want note match", out)
	}
	_, err = registry.executeWithScopeAndPrincipal(ctx, "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(childRoot, "note.txt"))+`","full":true}`), scope, p, key)
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("read_file with list/search grants err = %v, want read-root rejection", err)
	}
}

func TestNativeFileAccessGrantRejectsSymlinkRootRetarget(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	workspace := t.TempDir()
	safeRoot := t.TempDir()
	otherRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(otherRoot, "secret.txt"), []byte("retargeted\n"), 0o600); err != nil {
		t.Fatalf("write retarget fixture: %v", err)
	}
	linkRoot := filepath.Join(t.TempDir(), "child-slot")
	if err := os.Symlink(safeRoot, linkRoot); err != nil {
		t.Fatalf("symlink safe root: %v", err)
	}
	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	grantAuthorityUseLeaseWithID(t, store, key, "lease-symlink-file-access")
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, p, "lease-symlink-file-access", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_file_access")
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       filepath.Join(workspace, "global"),
		SharedMemoryRoot: filepath.Join(workspace, "shared"),
		WorkingRoot:      workspace,
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-symlink-root",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: linkRoot,
		AllowedActions: []string{"read"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(symlink) err = %v", err)
	}
	if err := os.Remove(linkRoot); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}
	if err := os.Symlink(otherRoot, linkRoot); err != nil {
		t.Fatalf("retarget symlink: %v", err)
	}
	_, err := registry.executeWithScopeAndPrincipal(ctx, "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(otherRoot, "secret.txt"))+`","full":true}`), scope, p, key)
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("read_file retargeted symlink grant err = %v, want read-root rejection", err)
	}
}

func TestNativeFileAccessGrantRejectsAncestorSymlinkRetarget(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	workspace := t.TempDir()
	linkParent := filepath.Join(workspace, "link-parent")
	safeParent := t.TempDir()
	otherParent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(safeParent, "slot"), 0o755); err != nil {
		t.Fatalf("mkdir safe slot: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(otherParent, "slot"), 0o755); err != nil {
		t.Fatalf("mkdir other slot: %v", err)
	}
	otherSecret := filepath.Join(otherParent, "slot", "secret.txt")
	if err := os.WriteFile(otherSecret, []byte("retargeted ancestor\n"), 0o600); err != nil {
		t.Fatalf("write retarget fixture: %v", err)
	}
	if err := os.Symlink(safeParent, linkParent); err != nil {
		t.Fatalf("symlink safe parent: %v", err)
	}

	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	grantAuthorityUseLeaseWithID(t, store, key, "lease-ancestor-symlink-file-access")
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, p, "lease-ancestor-symlink-file-access", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_file_access")
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       filepath.Join(workspace, "global"),
		SharedMemoryRoot: filepath.Join(workspace, "shared"),
		WorkingRoot:      workspace,
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-ancestor-symlink-root",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: filepath.Join(linkParent, "slot"),
		AllowedActions: []string{"read_file"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(ancestor symlink) err = %v", err)
	}
	if err := os.Remove(linkParent); err != nil {
		t.Fatalf("remove ancestor symlink: %v", err)
	}
	if err := os.Symlink(otherParent, linkParent); err != nil {
		t.Fatalf("retarget ancestor symlink: %v", err)
	}

	_, err := registry.executeWithScopeAndPrincipal(ctx, "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(otherSecret)+`","full":true}`), scope, p, key)
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("read_file retargeted ancestor symlink grant err = %v, want read-root rejection", err)
	}
	invocations, err := store.CapabilityInvocationsByGrant("capg-ancestor-symlink-root", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(ancestor symlink) err = %v", err)
	}
	if len(invocations) != 0 {
		t.Fatalf("ancestor symlink invocations = %#v, want none because no grant root was authorized", invocations)
	}
}

func TestNativeFileAccessGrantRevalidatesAfterStoreReopen(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	workspace := t.TempDir()
	target := t.TempDir()
	targetFile := filepath.Join(target, "note.txt")
	if err := os.WriteFile(targetFile, []byte("lease-bound\n"), 0o600); err != nil {
		t.Fatalf("write target fixture: %v", err)
	}
	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	leaseID := "lease-file-access-reopen"
	grantAuthorityUseLeaseWithID(t, store, key, leaseID)
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, p, leaseID, session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_file_access")
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       filepath.Join(workspace, "global"),
		SharedMemoryRoot: filepath.Join(workspace, "shared"),
		WorkingRoot:      workspace,
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-file-access-reopen",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: target,
		AllowedActions: []string{"read_file"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(reopen) err = %v", err)
	}

	out, err := registry.executeWithScopeAndPrincipal(ctx, "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(targetFile)+`","full":true}`), scope, p, key)
	if err != nil {
		t.Fatalf("read_file before reopen err = %v", err)
	}
	if !strings.Contains(out, "lease-bound") {
		t.Fatalf("read_file before reopen out = %q, want fixture content", out)
	}
	dbPath := store.DBPath()
	if err := store.Close(); err != nil {
		t.Fatalf("close store before reopen: %v", err)
	}
	reopened, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer reopened.Close()
	storeContinuationLeaseForMatrix(t, reopened, key, session.ContinuationLease{
		ID:             leaseID,
		Status:         session.ContinuationLeaseStatusRevoked,
		RemainingTurns: 1,
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	})
	reopenedRegistry := NewRegistry(registry.workspace, 2*time.Second).WithSessionStore(reopened)
	_, err = reopenedRegistry.executeWithScopeAndPrincipal(ctx, "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(targetFile)+`","full":true}`), scope, p, key)
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("read_file after reopened revocation err = %v, want read-root rejection", err)
	}
	assertNativeFileAccessInvocations(t, reopened, "capg-file-access-reopen", map[string]string{
		"read_file": leaseID,
	})
}

func TestNativeFileAccessWriteGrantCanCreateGrantedRoot(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	workspace := t.TempDir()
	parent := t.TempDir()
	target := filepath.Join(parent, "new-child-slot")
	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	grantAuthorityUseLeaseWithID(t, store, key, "lease-create-file-access-root")
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, p, "lease-create-file-access-root", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_file_access")
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       filepath.Join(workspace, "global"),
		SharedMemoryRoot: filepath.Join(workspace, "shared"),
		WorkingRoot:      workspace,
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-create-root",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: target,
		AllowedActions: []string{"write"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(create root) err = %v", err)
	}
	out, err := registry.executeWithScopeAndPrincipal(ctx, "write_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(target, "config.json"))+`","content":"{}","create_dirs":true}`), scope, p, key)
	if err != nil {
		t.Fatalf("write_file create granted root err = %v", err)
	}
	if !strings.Contains(out, "write_file_ok") {
		t.Fatalf("write_file out = %q, want success marker", out)
	}
	if _, err := os.Stat(filepath.Join(target, "config.json")); err != nil {
		t.Fatalf("created granted root file stat err = %v", err)
	}
}

func TestNativeWriteFileHostPermissionFailureCreatesRepairNextAction(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("permission-denied fixture is not meaningful as root")
	}

	registry, store := newDurableAgentToolRegistry(t)
	workspace := t.TempDir()
	target := t.TempDir()
	if err := os.Chmod(target, 0o500); err != nil {
		t.Fatalf("chmod target readonly: %v", err)
	}
	defer func() { _ = os.Chmod(target, 0o700) }()
	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	grantAuthorityUseLeaseWithID(t, store, key, "lease-host-permission-write")
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, p, "lease-host-permission-write", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_file_access")
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          sandbox.DefaultProfiles().Admin,
		GlobalRoot:       filepath.Join(workspace, "global"),
		SharedMemoryRoot: filepath.Join(workspace, "shared"),
		WorkingRoot:      workspace,
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-host-permission-write",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: target,
		AllowedActions: []string{"write"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(host permission) err = %v", err)
	}
	_, err := registry.executeWithScopeAndPrincipal(ctx, "write_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(target, "blocked.txt"))+`","content":"no"}`), scope, p, key)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission") {
		t.Fatalf("write_file host permission err = %v, want permission denial", err)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].State != session.NextActionBlockedNeedsResourceRepair || open[0].ResourceBlocker != "host_permission_denied" {
		t.Fatalf("open next actions = %#v, want host_permission_denied repair", open)
	}
}

func TestNativeFileAccessGrantDoesNotBypassHiddenPaths(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	workspace := t.TempDir()
	secretRoot := t.TempDir()
	secretPath := filepath.Join(secretRoot, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("hidden secret\n"), 0o600); err != nil {
		t.Fatalf("write hidden fixture: %v", err)
	}
	p := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	profile := sandbox.DefaultProfiles().Admin
	profile.HiddenPaths = append(profile.HiddenPaths, secretRoot)
	scope := sandbox.Scope{
		Principal:        p,
		Profile:          profile,
		GlobalRoot:       filepath.Join(workspace, "global"),
		SharedMemoryRoot: filepath.Join(workspace, "shared"),
		WorkingRoot:      workspace,
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-hidden-read",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: secretRoot,
		AllowedActions: []string{"read"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(hidden file_access) err = %v", err)
	}
	grantAuthorityUseLeaseWithID(t, store, key, "lease-hidden-read")
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, p, "lease-hidden-read", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_file_access")
	_, err := registry.executeWithScopeAndPrincipal(ctx, "read_file", json.RawMessage(`{"path":"`+filepath.ToSlash(secretPath)+`","full":true}`), scope, p, key)
	if err == nil || !strings.Contains(err.Error(), "hidden by the sandbox profile") {
		t.Fatalf("read_file hidden grant err = %v, want hidden-path rejection", err)
	}

	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-hidden-write",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindFileAccess,
		TargetResource: secretRoot,
		AllowedActions: []string{"write"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(hidden write file_access) err = %v", err)
	}
	_, err = registry.executeWithScopeAndPrincipal(ctx, "write_file", json.RawMessage(`{"path":"`+filepath.ToSlash(filepath.Join(secretRoot, "created.txt"))+`","content":"hidden","create_dirs":true}`), scope, p, key)
	if err == nil || !strings.Contains(err.Error(), "hidden by the sandbox profile") {
		t.Fatalf("write_file hidden grant err = %v, want hidden-path rejection", err)
	}
}

func assertNativeFileAccessInvocations(t *testing.T, store *session.SQLiteStore, grantID string, want map[string]string) {
	t.Helper()

	invocations, err := store.CapabilityInvocationsByGrant(grantID, len(want)+5)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(%s) err = %v", grantID, err)
	}
	seen := make(map[string]session.CapabilityInvocation, len(invocations))
	for _, invocation := range invocations {
		seen[invocation.Action] = invocation
	}
	for action, leaseID := range want {
		invocation, ok := seen[action]
		if !ok {
			t.Fatalf("grant %s missing invocation action %s; got %#v", grantID, action, invocations)
		}
		if invocation.Status != "succeeded" {
			t.Fatalf("grant %s action %s status = %q, want succeeded", grantID, action, invocation.Status)
		}
		if invocation.AuthoritySource != "continuation_lease" {
			t.Fatalf("grant %s action %s authority source = %q, want continuation_lease", grantID, action, invocation.AuthoritySource)
		}
		if invocation.ContinuationLeaseID != leaseID {
			t.Fatalf("grant %s action %s continuation lease = %q, want %q", grantID, action, invocation.ContinuationLeaseID, leaseID)
		}
		if invocation.SessionID != session.SessionIDForKey(adminSessionKey()) {
			t.Fatalf("grant %s action %s session = %q, want admin session", grantID, action, invocation.SessionID)
		}
	}
}

func TestReadFileRequiresExplicitWindowOrFull(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.txt"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	registry := NewRegistry(workspace, 2*time.Second)
	scope := sandbox.Scope{Principal: principal.Principal{Role: principal.RoleAdmin}, Profile: sandbox.DefaultProfiles().Admin, GlobalRoot: workspace, WorkingRoot: workspace}

	_, err := registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"sample.txt"}`), scope, scope.Principal, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "offset+limit or full=true") {
		t.Fatalf("read_file err = %v, want explicit-window rejection", err)
	}

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "read_file", json.RawMessage(`{"path":"sample.txt","offset":1,"limit":1}`), scope, scope.Principal, session.SessionKey{})
	if err != nil {
		t.Fatalf("read_file window err = %v", err)
	}
	if !strings.Contains(out, "offset: 1") || !strings.Contains(out, "limit: 1") || !strings.Contains(out, "lines: 1") || !strings.Contains(out, "two") || strings.Contains(out, "one") || strings.Contains(out, "three") {
		t.Fatalf("read_file window out = %q", out)
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

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 2*time.Second)

	approved := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 42}
	deniedProfile := sandbox.DefaultProfiles().ApprovedUser
	deniedScope := sandbox.Scope{
		Principal:   approved,
		Profile:     deniedProfile,
		GlobalRoot:  workspace,
		WorkingRoot: workspace,
	}
	_, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"https://example.com"}`), deniedScope, approved, session.SessionKey{})
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

func TestFetchURLRendersDigestWithConfigurableExcerpt(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("a", 2300) + "TAIL-MARKER"
	out := renderFetchURLDigest("http://example.test", "200 OK", "text/plain; charset=utf-8", []byte(body), false, defaultNativeFetchExcerptBytes)
	for _, want := range []string{"status: 200 OK", "content_type: text/plain; charset=utf-8", "bytes_read: 2311", "sha256:", "excerpt_bytes: 2048", "excerpt_truncated: true", "excerpt:\naaaa"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fetch_url digest = %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "body_ref:") {
		t.Fatalf("fetch_url digest = %q, want no inaccessible body_ref", out)
	}
	if strings.Contains(out, "body:\n") {
		t.Fatalf("fetch_url digest leaked legacy raw body label: %q", out)
	}
	if len(out) >= len(body)+200 {
		t.Fatalf("fetch_url digest length = %d, raw body len = %d; want excerpt-first compact output", len(out), len(body))
	}

	expanded := renderFetchURLDigest("http://example.test", "200 OK", "text/plain; charset=utf-8", []byte(body), false, 4096)
	for _, want := range []string{"excerpt_bytes: 4096", "excerpt_truncated: false", "TAIL-MARKER"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded fetch_url digest = %q, want %q", expanded, want)
		}
	}
}

func TestFetchURLDigestGoldenContract(t *testing.T) {
	t.Parallel()

	sum := sha256.Sum256([]byte("ok"))
	want := strings.Join([]string{
		"[FETCH_URL]",
		"url: https://example.test/status",
		"status: 200 OK",
		"content_type: text/plain",
		"bytes_read: 2",
		"sha256: " + hex.EncodeToString(sum[:]),
		"truncated: false",
		"excerpt_bytes: 2",
		"excerpt_truncated: false",
		"excerpt:",
		"ok",
		"[/FETCH_URL]",
	}, "\n")

	got := renderFetchURLDigest("https://example.test/status", "200 OK", "text/plain", []byte("ok"), false, 2)
	if got != want {
		t.Fatalf("fetch_url digest = %q, want %q", got, want)
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
	if !strings.Contains(out, "excerpt:\nok") || strings.Contains(out, "body:\n") {
		t.Fatalf("fetch_url out = %q, want excerpt-first digest", out)
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

func TestFetchURLAllowlistRetriesAuthorizedResolvedDestinations(t *testing.T) {
	t.Parallel()

	registry, scope, actor := newNativeFetchAllowlistRegistry(t, map[string][]netip.Addr{
		"allowed.test": {netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("203.0.113.11")},
	}, []string{"allowed.test:80"})
	dialer := newNativeFetchScriptedDialer(map[string]string{
		"203.0.113.11:80": "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 8\r\n\r\nfallback",
	})
	registry.nativeFetchDialContext = dialer.dial

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"http://allowed.test/"}`), scope, actor, session.SessionKey{})
	if err != nil {
		t.Fatalf("fetch_url fallback err = %v", err)
	}
	if !strings.Contains(out, "fallback") {
		t.Fatalf("fetch_url out = %q, want fallback response", out)
	}
	if got, want := strings.Join(dialer.dialed(), ","), "203.0.113.10:80,203.0.113.11:80"; got != want {
		t.Fatalf("dial targets = %q, want %q", got, want)
	}
}

func TestFetchURLAllowlistDialsOnlyAuthorizedResolvedDestinations(t *testing.T) {
	t.Parallel()

	registry, scope, actor := newNativeFetchAllowlistRegistry(t, map[string][]netip.Addr{
		"mixed.test": {netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("203.0.113.11")},
	}, []string{"203.0.113.10:80"})
	dialer := newNativeFetchScriptedDialer(map[string]string{
		"203.0.113.10:80": "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 7\r\n\r\nallowed",
	})
	registry.nativeFetchDialContext = dialer.dial

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"http://mixed.test/"}`), scope, actor, session.SessionKey{})
	if err != nil {
		t.Fatalf("fetch_url mixed err = %v", err)
	}
	if !strings.Contains(out, "excerpt:\nallowed") {
		t.Fatalf("fetch_url out = %q, want allowed excerpt", out)
	}
	if got, want := strings.Join(dialer.dialed(), ","), "203.0.113.10:80"; got != want {
		t.Fatalf("dial targets = %q, want only authorized destination %q", got, want)
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

func TestFetchURLAllowlistRejectsResolvedSpecialDestinationsForNonAdmin(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		host string
		addr string
	}{
		{name: "unspecified_v4", host: "unspecified4.test", addr: "0.0.0.0"},
		{name: "unspecified_v6", host: "unspecified6.test", addr: "::"},
		{name: "multicast_v4", host: "multicast4.test", addr: "224.0.0.1"},
		{name: "multicast_v6", host: "multicast6.test", addr: "ff02::1"},
		{name: "loopback", host: "loop.test", addr: "127.0.0.1"},
		{name: "link_local", host: "linklocal.test", addr: "169.254.1.1"},
		{name: "rfc1918", host: "private.test", addr: "192.168.1.5"},
		{name: "ula", host: "ula.test", addr: "fc00::1"},
		{name: "tailnet_cgnat", host: "tailnet.test", addr: "100.64.0.1"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			registry, scope, actor := newNativeFetchAllowlistRegistry(t, map[string][]netip.Addr{
				tc.host: {netip.MustParseAddr(tc.addr)},
			}, []string{tc.host + ":80"})
			dialer := newNativeFetchScriptedDialer(nil)
			registry.nativeFetchDialContext = dialer.dial

			_, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"http://`+tc.host+`/"}`), scope, actor, session.SessionKey{})
			if err == nil || !strings.Contains(err.Error(), "local/private/special resolved destinations") {
				t.Fatalf("fetch_url %s err = %v, want resolved special-destination rejection", tc.name, err)
			}
			if got := dialer.dialed(); len(got) != 0 {
				t.Fatalf("dial targets = %#v, want no dial", got)
			}
		})
	}
}

func TestFetchURLAllowlistAllowsResolvedPrivateDestinationForAdmin(t *testing.T) {
	t.Parallel()

	registry, scope, _ := newNativeFetchAllowlistRegistry(t, map[string][]netip.Addr{
		"loop.test": {netip.MustParseAddr("127.0.0.1")},
	}, []string{"loop.test:80"})
	admin := principal.Principal{Role: principal.RoleAdmin}
	scope.Principal = admin
	dialer := newNativeFetchScriptedDialer(map[string]string{
		"127.0.0.1:80": "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 5\r\n\r\nadmin",
	})
	registry.nativeFetchDialContext = dialer.dial

	out, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"http://loop.test/"}`), scope, admin, session.SessionKey{})
	if err != nil {
		t.Fatalf("fetch_url admin private err = %v", err)
	}
	if !strings.Contains(out, "admin") {
		t.Fatalf("fetch_url out = %q, want admin response", out)
	}
	if got, want := strings.Join(dialer.dialed(), ","), "127.0.0.1:80"; got != want {
		t.Fatalf("dial targets = %q, want %q", got, want)
	}
}

func TestFetchURLUsesConfiguredUserAgent(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	admin := principal.Principal{Role: principal.RoleAdmin}
	adminScope := sandbox.Scope{
		Principal:   admin,
		Profile:     sandbox.DefaultProfiles().Admin,
		GlobalRoot:  workspace,
		WorkingRoot: workspace,
	}
	transport := &recordingFetchTransport{}
	registry := NewRegistry(workspace, 2*time.Second).WithUserAgent("custom-fetch/1")
	registry.nativeFetchTransport = transport
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"https://example.test/"}`), adminScope, admin, session.SessionKey{}); err != nil {
		t.Fatalf("fetch_url custom user-agent err = %v", err)
	}
	if got := transport.lastUserAgent(); got != "custom-fetch/1" {
		t.Fatalf("User-Agent = %q, want custom-fetch/1", got)
	}

	registry.WithUserAgent("")
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "fetch_url", json.RawMessage(`{"url":"https://example.test/"}`), adminScope, admin, session.SessionKey{}); err != nil {
		t.Fatalf("fetch_url anonymous user-agent err = %v", err)
	}
	if got := transport.lastUserAgent(); strings.Contains(strings.ToLower(got), "aphelion") || got == "custom-fetch/1" {
		t.Fatalf("User-Agent = %q, want anonymous override without Aphelion/custom identity", got)
	}
}

type recordingFetchTransport struct {
	mu        sync.Mutex
	userAgent string
}

func (t *recordingFetchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.userAgent = req.Header.Get("User-Agent")
	t.mu.Unlock()
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    req,
	}, nil
}

func (t *recordingFetchTransport) lastUserAgent() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.userAgent
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
	names := make(map[string]agent.ToolDef, len(defs))
	for _, def := range defs {
		names[def.Name] = def
	}
	for _, name := range []string{"read_file", "write_file", "list_dir", "search", "fetch_url"} {
		if _, ok := names[name]; !ok {
			t.Fatalf("Definitions() missing %s", name)
		}
	}
	for _, name := range []string{"read_file", "list_dir", "search"} {
		desc := strings.ToLower(names[name].Description)
		if !strings.Contains(desc, "parallel-safe") || !strings.Contains(desc, "together in one response") {
			t.Fatalf("%s description = %q, want parallel-safe batch affordance", name, names[name].Description)
		}
	}
}

func TestReadFileDefinitionAdvertisesProviderCompatibleWindowContract(t *testing.T) {
	t.Parallel()

	readFile := nativeToolDefForTest(t, "read_file")
	var schema map[string]any
	if err := json.Unmarshal(readFile.Parameters, &schema); err != nil {
		t.Fatalf("decode read_file schema: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprint(schema["type"])); got != "object" {
		t.Fatalf("read_file schema type = %q, want object", got)
	}
	for _, keyword := range []string{"oneOf", "anyOf", "allOf", "enum", "not"} {
		if _, ok := schema[keyword]; ok {
			t.Fatalf("read_file schema has top-level %s, which provider function schemas reject", keyword)
		}
	}
	if !toolSchemaRequiredContains(t, readFile, "path") {
		t.Fatalf("read_file schema missing required path")
	}
	rendered := string(readFile.Parameters)
	for _, want := range []string{`"offset"`, `"limit"`, `"full"`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("read_file schema = %s, missing %s", rendered, want)
		}
	}
}

func TestNativeToolSchemasMatchRuntimeRequiredInputs(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 2*time.Second)
	scope := sandbox.Scope{
		Principal:   principal.Principal{Role: principal.RoleAdmin},
		Profile:     sandbox.DefaultProfiles().Admin,
		GlobalRoot:  workspace,
		WorkingRoot: workspace,
	}

	cases := []struct {
		name         string
		required     []string
		input        json.RawMessage
		errorSnippet string
	}{
		{name: "exec", required: []string{"command"}, input: json.RawMessage(`{}`), errorSnippet: "exec command is required"},
		{name: "fetch_url", required: []string{"url"}, input: json.RawMessage(`{}`), errorSnippet: "fetch_url url is required"},
		{name: "search", required: []string{"query"}, input: json.RawMessage(`{}`), errorSnippet: "search query is required"},
	}

	for _, tc := range cases {
		def := nativeToolDefForTest(t, tc.name)
		for _, field := range tc.required {
			if !toolSchemaRequiredContains(t, def, field) {
				t.Fatalf("%s schema missing required field %q", tc.name, field)
			}
		}
		_, err := registry.executeWithScopeAndPrincipal(context.Background(), tc.name, tc.input, scope, scope.Principal, session.SessionKey{})
		if err == nil || !strings.Contains(err.Error(), tc.errorSnippet) {
			t.Fatalf("%s err = %v, want %q", tc.name, err, tc.errorSnippet)
		}
	}

	fetchURL := nativeToolDefForTest(t, "fetch_url")
	rendered := string(fetchURL.Parameters)
	for _, want := range []string{`"excerpt_bytes"`, `"maximum": 65536`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("fetch_url schema = %s, missing %s", rendered, want)
		}
	}
}

func nativeToolDefForTest(t *testing.T, name string) agent.ToolDef {
	t.Helper()
	defs := NewRegistry(t.TempDir(), 2*time.Second).Definitions()
	for _, def := range defs {
		if def.Name == name {
			return def
		}
	}
	t.Fatalf("Definitions() missing %s", name)
	return agent.ToolDef{}
}

func toolSchemaRequiredContains(t *testing.T, def agent.ToolDef, field string) bool {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		t.Fatalf("decode %s schema: %v", def.Name, err)
	}
	required, ok := schema["required"].([]any)
	if !ok {
		return false
	}
	for _, raw := range required {
		if strings.TrimSpace(fmt.Sprint(raw)) == field {
			return true
		}
	}
	return false
}
