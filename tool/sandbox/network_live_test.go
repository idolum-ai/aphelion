//go:build linux

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
)

func TestRunnerLiveNetworkAllowlistBackend(t *testing.T) {
	if strings.TrimSpace(os.Getenv("APHELION_SANDBOX_NET_LIVE")) != "1" {
		t.Skip("set APHELION_SANDBOX_NET_LIVE=1 to run the privileged network allowlist integration test")
	}

	scope := buildHomeBackedLiveScope(t)
	scope.Profile.Network = NetworkAllowlist
	scope.Profile.NetworkAllow = MustParseNetworkDestinations([]string{"example.com:443"})
	for _, path := range []string{
		scope.GlobalRoot,
		scope.SharedMemoryRoot,
		scope.UserWorkspace,
		scope.UserMemory,
		scope.WorkingRoot,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) err = %v", path, err)
		}
	}
	runner := NewRunner()
	if status := runner.NetworkBackendStatus(context.Background()); !status.Available {
		t.Skipf("network backend unavailable: %s", status.Reason)
	}

	result, err := runner.Run(context.Background(), ExecRequest{
		Scope: scope,
		Command: `echo ok > helper-write-test &&
test "$(cat helper-write-test)" = ok &&
timeout 15 bash -lc ':</dev/tcp/example.com/443' &&
if timeout 3 bash -lc ':</dev/tcp/1.1.1.1/443'; then
  echo "unexpected denied destination success" >&2
  exit 17
fi`,
		Workdir: scope.WorkingRoot,
	})
	if err != nil {
		t.Fatalf("Run() err = %v stdout=%q stderr=%q", err, result.Stdout, result.Stderr)
	}
	if result.Network == nil || result.Network.Backend == "" {
		t.Fatalf("network evidence = %#v, want backend evidence", result.Network)
	}
	written, err := os.ReadFile(filepath.Join(scope.WorkingRoot, "helper-write-test"))
	if err != nil {
		t.Fatalf("ReadFile(helper-write-test) err = %v", err)
	}
	if strings.TrimSpace(string(written)) != "ok" {
		t.Fatalf("helper-write-test = %q, want ok", string(written))
	}
}

func buildHomeBackedLiveScope(t *testing.T) Scope {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() err = %v", err)
	}
	stateRoot := filepath.Join(home, ".aphelion", "state", "sandbox-net-live-tests")
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) err = %v", stateRoot, err)
	}
	root, err := os.MkdirTemp(stateRoot, "allowlist-*")
	if err != nil {
		t.Fatalf("MkdirTemp(%s) err = %v", stateRoot, err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
	})

	resolver, err := NewResolver(Roots{
		GlobalRoot:        filepath.Join(root, "global"),
		SharedMemoryRoot:  filepath.Join(root, "shared"),
		UserWorkspaceRoot: filepath.Join(root, "workspaces"),
		UserMemoryRoot:    filepath.Join(root, "memory"),
	}, DefaultProfiles())
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	scope, err := resolver.Resolve(principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 42})
	if err != nil {
		t.Fatalf("Resolve() err = %v", err)
	}
	return scope
}
