//go:build linux

package tailnet

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestParseStatusJSONHealthy(t *testing.T) {
	t.Parallel()

	parsed, err := ParseStatusJSON([]byte(healthyStatusJSON))
	if err != nil {
		t.Fatalf("ParseStatusJSON() err = %v", err)
	}
	if parsed.BackendState != "Running" || !parsed.Authenticated || !parsed.Online {
		t.Fatalf("parsed state = %#v, want running authenticated online", parsed)
	}
	if parsed.HostName != "aphelion" || parsed.DNSName != "aphelion.example.ts.net" || parsed.TailnetName != "example.ts.net" {
		t.Fatalf("parsed identity = %#v, want host/dns/tailnet", parsed)
	}
	if got, want := parsed.TailscaleIPs, []string{"100.64.0.10", "fd7a:115c:a1e0::10"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tailscale ips = %#v, want %#v", got, want)
	}
	if got, want := parsed.Tags, []string{"tag:admin", "tag:aphelion"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
}

func TestCLIBackendSnapshotHealthy(t *testing.T) {
	t.Parallel()

	runner := fakeRunner{
		outputs: map[string][]byte{
			"tailscale version":       []byte("1.80.0\n"),
			"tailscale status --json": []byte(healthyStatusJSON),
			"tailscale ip":            []byte("100.64.0.10\nfd7a:115c:a1e0::10\n"),
			"tailscale netcheck":      []byte("* UDP: true\n* IPv4: yes, 203.0.113.10:41641\n* IPv6: no\n"),
		},
	}
	backend := NewCLIBackend(CLIOptions{
		ExpectedTailnet:  "example.ts.net",
		ExpectedHostname: "aphelion",
		ExpectedTags:     []string{"tag:admin"},
		Runner:           runner,
		Now:              fixedTailnetNow,
	})

	snapshot, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() err = %v", err)
	}
	if snapshot.Status != "healthy" {
		t.Fatalf("status = %q issues=%#v, want healthy", snapshot.Status, snapshot.Issues)
	}
	if snapshot.TailscaleVersion != "1.80.0" || snapshot.HostName != "aphelion" || snapshot.TailnetName != "example.ts.net" {
		t.Fatalf("snapshot identity = %#v, want version/host/tailnet", snapshot)
	}
	if !snapshot.NetcheckAvailable || !strings.Contains(snapshot.NetcheckSummary, "UDP: true") {
		t.Fatalf("netcheck = (%t,%q), want summary", snapshot.NetcheckAvailable, snapshot.NetcheckSummary)
	}
}

func TestCLIBackendSnapshotMissingCLI(t *testing.T) {
	t.Parallel()

	backend := NewCLIBackend(CLIOptions{
		Runner: fakeRunner{
			errs: map[string]error{
				"tailscale version":       exec.ErrNotFound,
				"tailscale status --json": exec.ErrNotFound,
			},
		},
		Now: fixedTailnetNow,
	})
	snapshot, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() err = %v", err)
	}
	if snapshot.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", snapshot.Status)
	}
	if len(snapshot.Issues) == 0 || snapshot.Issues[0].Code != "cli_unavailable" {
		t.Fatalf("issues = %#v, want cli_unavailable", snapshot.Issues)
	}
}

func TestCLIBackendSnapshotReportsExpectedStateDrift(t *testing.T) {
	t.Parallel()

	backend := NewCLIBackend(CLIOptions{
		ExpectedTailnet:  "other.ts.net",
		ExpectedHostname: "other-host",
		ExpectedTags:     []string{"tag:missing"},
		Runner: fakeRunner{
			outputs: map[string][]byte{
				"tailscale status --json": []byte(healthyStatusJSON),
				"tailscale ip":            []byte("100.64.0.10\n"),
				"tailscale netcheck":      []byte("* UDP: true\n"),
			},
		},
		Now: fixedTailnetNow,
	})
	snapshot, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() err = %v", err)
	}
	for _, code := range []string{"tailnet_mismatch", "hostname_mismatch", "tag_missing"} {
		if !tailnetIssueCode(snapshot.Issues, code) {
			t.Fatalf("issues = %#v, want %s", snapshot.Issues, code)
		}
	}
}

func TestCLIBackendSnapshotReportsUnauthenticatedDaemon(t *testing.T) {
	t.Parallel()

	backend := NewCLIBackend(CLIOptions{
		Runner: fakeRunner{
			outputs: map[string][]byte{
				"tailscale status --json": []byte(`{"BackendState":"NeedsLogin","Self":{"HostName":"aphelion"}}`),
				"tailscale ip":            []byte(""),
				"tailscale netcheck":      []byte("* UDP: false\n"),
			},
		},
		Now: fixedTailnetNow,
	})
	snapshot, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() err = %v", err)
	}
	if snapshot.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", snapshot.Status)
	}
	if !tailnetIssueCode(snapshot.Issues, "daemon_not_authenticated") {
		t.Fatalf("issues = %#v, want daemon_not_authenticated", snapshot.Issues)
	}
}

func TestCLIBackendSnapshotMalformedStatusJSON(t *testing.T) {
	t.Parallel()

	backend := NewCLIBackend(CLIOptions{
		Runner: fakeRunner{
			outputs: map[string][]byte{
				"tailscale status --json": []byte(`{"BackendState":`),
			},
		},
		Now: fixedTailnetNow,
	})
	snapshot, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() err = %v", err)
	}
	if !tailnetIssueCode(snapshot.Issues, "status_json_invalid") {
		t.Fatalf("issues = %#v, want malformed JSON issue", snapshot.Issues)
	}
}

func TestReadOnlyCommandAllowedRejectsMutations(t *testing.T) {
	t.Parallel()

	allowed := [][]string{
		{"status", "--json"},
		{"ip"},
		{"netcheck"},
		{"version"},
		{"ping", "aphelion"},
	}
	for _, args := range allowed {
		if !ReadOnlyCommandAllowed(args) {
			t.Fatalf("ReadOnlyCommandAllowed(%#v) = false, want true", args)
		}
	}
	denied := [][]string{
		{"up"},
		{"set", "--ssh"},
		{"serve", "443"},
		{"funnel", "on"},
		{"ssh", "host"},
		{"status", "serve"},
	}
	for _, args := range denied {
		if ReadOnlyCommandAllowed(args) {
			t.Fatalf("ReadOnlyCommandAllowed(%#v) = true, want false", args)
		}
	}
}

type fakeRunner struct {
	outputs map[string][]byte
	errs    map[string]error
}

func (r fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	if err := r.errs[key]; err != nil {
		return r.outputs[key], err
	}
	if out, ok := r.outputs[key]; ok {
		return out, nil
	}
	return nil, errors.New("unexpected command: " + key)
}

func fixedTailnetNow() time.Time {
	return time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
}

func tailnetIssueCode(issues []core.TailnetIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

const healthyStatusJSON = `{
  "Version": "1.80.0",
  "BackendState": "Running",
  "CurrentTailnet": {"Name": "example.ts.net"},
  "Self": {
    "HostName": "aphelion",
    "DNSName": "aphelion.example.ts.net.",
    "UserID": 1,
    "TailscaleIPs": ["100.64.0.10", "fd7a:115c:a1e0::10"],
    "Tags": ["tag:aphelion", "tag:admin"],
    "Online": true
  },
  "User": {
    "1": {"LoginName": "admin@example.com", "DisplayName": "Admin"}
  }
}`
