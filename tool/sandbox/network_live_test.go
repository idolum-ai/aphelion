//go:build linux

package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
)

func TestRunnerLiveNetworkAllowlistBackend(t *testing.T) {
	if strings.TrimSpace(os.Getenv("APHELION_SANDBOX_NET_LIVE")) != "1" {
		t.Skip("set APHELION_SANDBOX_NET_LIVE=1 to run the privileged network allowlist integration test")
	}

	scope := buildScope(t, principal.RoleApprovedUser)
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
		Command: `timeout 15 bash -lc ':</dev/tcp/example.com/443' &&
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
}
