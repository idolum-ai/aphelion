//go:build linux

package sandbox

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

func TestParseNetworkDestinationsRequiresExplicitPort(t *testing.T) {
	t.Parallel()

	_, err := ParseNetworkDestination("example.com")
	if err == nil {
		t.Fatal("ParseNetworkDestination() err = nil, want explicit port rejection")
	}
	if !strings.Contains(err.Error(), "explicit port") {
		t.Fatalf("err = %v, want explicit port detail", err)
	}
}

func TestParseNetworkDestinationsNormalizesAndDedupes(t *testing.T) {
	t.Parallel()

	destinations, err := ParseNetworkDestinations([]string{"Example.COM:443", "example.com:443", "100.64.0.0/10:8787", "192.0.2.1:80"})
	if err != nil {
		t.Fatalf("ParseNetworkDestinations() err = %v", err)
	}
	got := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		got = append(got, destination.Canonical())
	}
	want := []string{"100.64.0.0/10:8787", "192.0.2.1:80", "example.com:443"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("destinations = %#v, want %#v", got, want)
	}
}

func TestCompileNetworkPolicyResolvesHostsToRules(t *testing.T) {
	t.Parallel()

	destinations := MustParseNetworkDestinations([]string{"example.com:443", "192.0.2.10:8443"})
	policy, err := CompileNetworkPolicy(context.Background(), destinations, func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946"), netip.MustParseAddr("93.184.216.34")}, nil
	})
	if err != nil {
		t.Fatalf("CompileNetworkPolicy() err = %v", err)
	}
	got := strings.Join(policy.RuleStrings(), ",")
	want := "192.0.2.10/32:8443,93.184.216.34/32:443,[2606:2800:220:1:248:1893:25c8:1946/128]:443"
	if got != want {
		t.Fatalf("rules = %q, want %q", got, want)
	}
	ipv4Only := policy.IPv4Only()
	if got, want := strings.Join(ipv4Only.RuleStrings(), ","), "192.0.2.10/32:8443,93.184.216.34/32:443"; got != want {
		t.Fatalf("ipv4 rules = %q, want %q", got, want)
	}
	if len(ipv4Only.Hosts["example.com"]) != 1 || ipv4Only.Hosts["example.com"][0] != netip.MustParseAddr("93.184.216.34") {
		t.Fatalf("hosts = %#v, want resolved example.com", policy.Hosts)
	}
}

func TestNetworkDestinationsContainAllUsesProfileCeiling(t *testing.T) {
	t.Parallel()

	ceiling := MustParseNetworkDestinations([]string{"api.openai.com:443", "github.com:443"})
	requested := MustParseNetworkDestinations([]string{"api.openai.com:443"})
	if !NetworkDestinationsContainAll(ceiling, requested) {
		t.Fatal("NetworkDestinationsContainAll() = false, want true for subset")
	}
	outside := MustParseNetworkDestinations([]string{"example.com:443"})
	if NetworkDestinationsContainAll(ceiling, outside) {
		t.Fatal("NetworkDestinationsContainAll() = true, want false for outside target")
	}
}
