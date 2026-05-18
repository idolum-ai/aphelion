//go:build linux

package tool

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type nativeFetchDialContext func(context.Context, string, string) (net.Conn, error)

type nativeFetchNetworkPolicy struct {
	compiled    sandbox.CompiledNetworkPolicy
	resolver    sandbox.NetworkResolver
	rejectLocal bool
}

func (r *Registry) fetchURLAllowlistTransport(ctx context.Context, profile sandbox.Profile, rejectLocal bool) (http.RoundTripper, *nativeFetchNetworkPolicy, error) {
	if len(profile.NetworkAllow) == 0 {
		return nil, nil, fmt.Errorf("fetch_url denied because sandbox network allowlist has no destinations")
	}

	resolver := r.nativeFetchResolver
	if resolver == nil {
		resolver = defaultNativeFetchResolver
	}
	compiled, err := sandbox.CompileNetworkPolicy(ctx, profile.NetworkAllow, resolver)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch_url compile network allowlist: %w", err)
	}
	policy := &nativeFetchNetworkPolicy{
		compiled:    compiled,
		resolver:    resolver,
		rejectLocal: rejectLocal,
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		return nil, nil, fmt.Errorf("fetch_url default transport is not configurable")
	}
	transport := base.Clone()
	transport.Proxy = nil
	transport.DialContext = policy.dialContext(r.nativeFetchDialer())
	return transport, policy, nil
}

func (r *Registry) nativeFetchDialer() nativeFetchDialContext {
	if r.nativeFetchDialContext != nil {
		return r.nativeFetchDialContext
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	return dialer.DialContext
}

func (p *nativeFetchNetworkPolicy) dialContext(dial nativeFetchDialContext) nativeFetchDialContext {
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, portRaw, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("fetch_url split dial address: %w", err)
		}
		port, err := parseFetchURLPortRaw(portRaw)
		if err != nil {
			return nil, err
		}
		addrs, err := p.authorizedAddrs(ctx, host, port)
		if err != nil {
			return nil, err
		}
		return dialNativeFetchAuthorizedAddrs(ctx, dial, network, addrs, portRaw)
	}
}

func (p *nativeFetchNetworkPolicy) authorizeURL(ctx context.Context, parsed *url.URL) error {
	port, err := fetchURLPort(parsed)
	if err != nil {
		return err
	}
	_, err = p.authorizedAddrs(ctx, parsed.Hostname(), port)
	return err
}

func (p *nativeFetchNetworkPolicy) authorizedAddrs(ctx context.Context, host string, port uint16) ([]netip.Addr, error) {
	addrs, err := resolveNativeFetchHost(ctx, host, p.resolver)
	if err != nil {
		return nil, fmt.Errorf("fetch_url resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("fetch_url resolve host %q: no addresses", host)
	}

	allowed := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		addr = addr.Unmap()
		if !addr.IsValid() {
			continue
		}
		if p.rejectLocal && nativeFetchRejectsNonAdminResolvedAddr(addr) {
			return nil, fmt.Errorf("fetch_url rejects local/private/special resolved destinations for non-admin principals")
		}
		if nativeFetchRuleAllows(p.compiled, addr, port) {
			allowed = append(allowed, addr)
		}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("fetch_url denied by sandbox network allowlist")
	}
	sort.Slice(allowed, func(i, j int) bool { return allowed[i].String() < allowed[j].String() })
	return allowed, nil
}

func nativeFetchRuleAllows(policy sandbox.CompiledNetworkPolicy, addr netip.Addr, port uint16) bool {
	addr = addr.Unmap()
	for _, rule := range policy.Rules {
		if rule.Port == port && rule.Prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func dialNativeFetchAuthorizedAddrs(ctx context.Context, dial nativeFetchDialContext, network string, addrs []netip.Addr, portRaw string) (net.Conn, error) {
	candidates := nativeFetchDialCandidates(addrs, network)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("fetch_url denied by sandbox network allowlist")
	}
	failures := make([]string, 0, len(candidates))
	for _, addr := range candidates {
		target := net.JoinHostPort(addr.String(), portRaw)
		conn, err := dial(ctx, network, target)
		if err == nil {
			return conn, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", target, err))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("fetch_url dial authorized destination failed: %w", ctxErr)
		}
	}
	return nil, fmt.Errorf("fetch_url dial authorized destinations failed: %s", strings.Join(failures, "; "))
}

func nativeFetchDialCandidates(addrs []netip.Addr, network string) []netip.Addr {
	network = strings.TrimSpace(network)
	out := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		switch network {
		case "", "tcp":
		case "tcp4":
			if !addr.Is4() {
				continue
			}
		case "tcp6":
			if !addr.Is6() {
				continue
			}
		default:
			continue
		}
		out = append(out, addr)
	}
	return out
}

func resolveNativeFetchHost(ctx context.Context, host string, resolver sandbox.NetworkResolver) ([]netip.Addr, error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}
	addrs, err := resolver(ctx, host)
	if err != nil {
		return nil, err
	}
	return dedupeNativeFetchAddrs(addrs), nil
}

func defaultNativeFetchResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	results, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(results))
	for _, result := range results {
		addr, ok := netip.AddrFromSlice(result.IP)
		if !ok {
			continue
		}
		out = append(out, addr.Unmap())
	}
	return out, nil
}

func dedupeNativeFetchAddrs(addrs []netip.Addr) []netip.Addr {
	seen := map[netip.Addr]struct{}{}
	out := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		addr = addr.Unmap()
		if !addr.IsValid() {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func parseFetchURLPortRaw(raw string) (uint16, error) {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("fetch_url invalid dial port %q", raw)
	}
	return uint16(port), nil
}

func nativeFetchRejectsNonAdminResolvedAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() {
		return true
	}
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return true
	}
	return nativeFetchTailnetCGNATPrefix.Contains(addr)
}

var nativeFetchTailnetCGNATPrefix = netip.MustParsePrefix("100.64.0.0/10")
