//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
)

type fakeCommandNetworkBackend struct {
	status NetworkBackendStatus
	run    func(context.Context, NetworkCommandRequest) (ExecResult, error)
}

func (b fakeCommandNetworkBackend) Status(context.Context) NetworkBackendStatus {
	if strings.TrimSpace(b.status.Name) == "" {
		return NetworkBackendStatus{Name: "fake_command", Available: true}
	}
	return b.status
}

func (b fakeCommandNetworkBackend) Prepare(context.Context, CompiledNetworkPolicy) (*NetworkLease, error) {
	return nil, fmt.Errorf("Prepare should not be called for command network backend")
}

func (b fakeCommandNetworkBackend) RunNetworkCommand(ctx context.Context, req NetworkCommandRequest) (ExecResult, error) {
	if b.run != nil {
		return b.run(ctx, req)
	}
	return ExecResult{}, fmt.Errorf("RunNetworkCommand test hook is not configured")
}

func TestNetworkHelperBackendStatusUnavailableWhenSocketMissing(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "missing.sock")
	status := NewNetworkHelperBackend(socketPath).Status(context.Background())
	if status.Available {
		t.Fatalf("helper status available = true, want unavailable")
	}
	if status.Name != networkHelperBackendName {
		t.Fatalf("helper status name = %q, want %q", status.Name, networkHelperBackendName)
	}
	if !strings.Contains(status.Reason, "helper unavailable") {
		t.Fatalf("helper status reason = %q, want helper unavailable", status.Reason)
	}
}

func TestNetworkHelperRequestRejectsNonBubblewrapCommand(t *testing.T) {
	t.Parallel()

	_, err := validateNetworkHelperRunRequest(networkHelperRequest{
		Action:    "run",
		BwrapPath: "/bin/bash",
		BwrapArgs: []string{"--", "bash", "-lc", "true"},
		Rules:     []networkHelperRule{{Prefix: "93.184.216.34/32", Port: 443}},
	})
	if err == nil {
		t.Fatal("validateNetworkHelperRunRequest err = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "only run bubblewrap") {
		t.Fatalf("err = %v, want bubblewrap-only rejection", err)
	}
}

func TestNetworkHelperTrustedBwrapRejectsDifferentExecutable(t *testing.T) {
	t.Parallel()

	err := validateNetworkHelperTrustedBwrap("/tmp/bwrap", func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	})
	if err == nil {
		t.Fatal("validateNetworkHelperTrustedBwrap err = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "trusted path") {
		t.Fatalf("err = %v, want trusted path rejection", err)
	}
}

func TestNetworkHelperRequestRoundTripPreservesCompiledPolicy(t *testing.T) {
	t.Parallel()

	policy := CompiledNetworkPolicy{
		Destinations: MustParseNetworkDestinations([]string{"example.com:443"}),
		Rules: []NetworkRule{{
			Prefix: netip.MustParsePrefix("93.184.216.34/32"),
			Port:   443,
		}},
		Hosts: map[string][]netip.Addr{
			"example.com": {netip.MustParseAddr("93.184.216.34")},
		},
	}
	req, err := networkHelperRequestFromCommand(NetworkCommandRequest{
		Policy:    policy,
		BwrapPath: "/usr/bin/bwrap",
		BwrapArgs: []string{"--die-with-parent", "--", "bash", "-lc", "true"},
	})
	if err != nil {
		t.Fatalf("networkHelperRequestFromCommand() err = %v", err)
	}
	got, err := validateNetworkHelperRunRequest(req)
	if err != nil {
		t.Fatalf("validateNetworkHelperRunRequest() err = %v", err)
	}
	if got.RuleStrings()[0] != "93.184.216.34/32:443" {
		t.Fatalf("rules = %#v, want compiled IPv4 rule", got.RuleStrings())
	}
	if got.Hosts["example.com"][0].String() != "93.184.216.34" {
		t.Fatalf("hosts = %#v, want helper host mapping", got.Hosts)
	}
}

func TestRunnerRunIsolatedAllowlistUsesCommandBackend(t *testing.T) {
	t.Parallel()

	scope := buildScope(t, principal.RoleApprovedUser)
	scope.Profile.Network = NetworkAllowlist
	scope.Profile.NetworkAllow = MustParseNetworkDestinations([]string{"example.com:443"})
	if err := os.MkdirAll(scope.WorkingRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(working root) err = %v", err)
	}

	var seen NetworkCommandRequest
	runner := NewRunnerWithLookPath(func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}).WithNetworkResolver(func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}).WithNetworkBackend(fakeCommandNetworkBackend{
		status: NetworkBackendStatus{Name: "fake_command", Available: true},
		run: func(_ context.Context, req NetworkCommandRequest) (ExecResult, error) {
			seen = req
			evidence := NetworkExecutionEvidence{
				Policy:       string(NetworkAllowlist),
				Backend:      "fake_command",
				Destinations: req.Policy.DestinationStrings(),
				Rules:        req.Policy.RuleStrings(),
			}
			return ExecResult{Stage: StageIsolatedBwrap, Stdout: "ok", Network: &evidence}, nil
		},
	})

	result, err := runner.Run(context.Background(), ExecRequest{
		Scope:   scope,
		Command: "curl https://example.com",
		Workdir: scope.WorkingRoot,
		Stdin:   []byte("input"),
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if result.Stdout != "ok" || result.Network == nil || result.Network.Backend != "fake_command" {
		t.Fatalf("result = %#v, want command backend result", result)
	}
	if seen.BwrapPath != "/usr/bin/bwrap" {
		t.Fatalf("bwrap path = %q, want /usr/bin/bwrap", seen.BwrapPath)
	}
	if strings.Contains(strings.Join(seen.BwrapArgs, " "), "--unshare-net") {
		t.Fatalf("allowlist bwrap args include network denial: %v", seen.BwrapArgs)
	}
	if got := strings.Join(seen.Policy.RuleStrings(), ","); got != "93.184.216.34/32:443" {
		t.Fatalf("policy rules = %q, want resolved allowlist rule", got)
	}
	if string(seen.Stdin) != "input" {
		t.Fatalf("stdin = %q, want forwarded stdin", string(seen.Stdin))
	}
}
