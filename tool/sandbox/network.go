//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type NetworkDestinationKind string

const (
	NetworkDestinationHost NetworkDestinationKind = "host"
	NetworkDestinationIP   NetworkDestinationKind = "ip"
	NetworkDestinationCIDR NetworkDestinationKind = "cidr"
)

type NetworkDestination struct {
	Kind   NetworkDestinationKind
	Host   string
	IP     netip.Addr
	Prefix netip.Prefix
	Port   uint16
}

func (d NetworkDestination) Canonical() string {
	port := strconv.Itoa(int(d.Port))
	switch d.Kind {
	case NetworkDestinationCIDR:
		return net.JoinHostPort(d.Prefix.String(), port)
	case NetworkDestinationIP:
		return net.JoinHostPort(d.IP.String(), port)
	default:
		return net.JoinHostPort(strings.ToLower(strings.TrimSuffix(d.Host, ".")), port)
	}
}

func (d NetworkDestination) String() string {
	return d.Canonical()
}

func ParseNetworkDestination(raw string) (NetworkDestination, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return NetworkDestination{}, fmt.Errorf("network destination is required")
	}
	host, portRaw, err := net.SplitHostPort(value)
	if err != nil {
		return NetworkDestination{}, fmt.Errorf("network destination %q must be host:port, ip:port, or cidr:port with an explicit port: %w", raw, err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return NetworkDestination{}, fmt.Errorf("network destination %q host is required", raw)
	}
	portInt, err := strconv.Atoi(strings.TrimSpace(portRaw))
	if err != nil || portInt <= 0 || portInt > 65535 {
		return NetworkDestination{}, fmt.Errorf("network destination %q port must be between 1 and 65535", raw)
	}

	if prefix, err := netip.ParsePrefix(host); err == nil {
		return NetworkDestination{Kind: NetworkDestinationCIDR, Prefix: prefix.Masked(), Port: uint16(portInt)}, nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return NetworkDestination{Kind: NetworkDestinationIP, IP: ip, Port: uint16(portInt)}, nil
	}
	if err := validateNetworkHostname(host); err != nil {
		return NetworkDestination{}, fmt.Errorf("network destination %q host is invalid: %w", raw, err)
	}
	return NetworkDestination{Kind: NetworkDestinationHost, Host: strings.ToLower(strings.TrimSuffix(host, ".")), Port: uint16(portInt)}, nil
}

func ParseNetworkDestinations(raw []string) ([]NetworkDestination, error) {
	out := make([]NetworkDestination, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		if strings.TrimSpace(value) == "" {
			continue
		}
		destination, err := ParseNetworkDestination(value)
		if err != nil {
			return nil, err
		}
		key := destination.Canonical()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, destination)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Canonical() < out[j].Canonical() })
	return out, nil
}

func MustParseNetworkDestinations(raw []string) []NetworkDestination {
	destinations, err := ParseNetworkDestinations(raw)
	if err != nil {
		panic(err)
	}
	return destinations
}

func validateNetworkHostname(host string) error {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || len(host) > 253 {
		return fmt.Errorf("hostname length is invalid")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("hostname contains an empty label")
		}
		if len(label) > 63 {
			return fmt.Errorf("hostname label %q is too long", label)
		}
		for i, ch := range label {
			valid := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-'
			if !valid {
				return fmt.Errorf("hostname label %q contains %q", label, ch)
			}
			if (i == 0 || i == len(label)-1) && ch == '-' {
				return fmt.Errorf("hostname label %q starts or ends with '-'", label)
			}
		}
	}
	return nil
}

type NetworkResolver func(context.Context, string) ([]netip.Addr, error)

type NetworkRule struct {
	Prefix netip.Prefix
	Port   uint16
}

func (r NetworkRule) String() string {
	return net.JoinHostPort(r.Prefix.String(), strconv.Itoa(int(r.Port)))
}

type CompiledNetworkPolicy struct {
	Destinations []NetworkDestination
	Rules        []NetworkRule
	Hosts        map[string][]netip.Addr
}

func (p CompiledNetworkPolicy) DestinationStrings() []string {
	out := make([]string, 0, len(p.Destinations))
	for _, destination := range p.Destinations {
		out = append(out, destination.Canonical())
	}
	sort.Strings(out)
	return out
}

func (p CompiledNetworkPolicy) RuleStrings() []string {
	out := make([]string, 0, len(p.Rules))
	for _, rule := range p.Rules {
		out = append(out, rule.String())
	}
	sort.Strings(out)
	return out
}

func (p CompiledNetworkPolicy) IPv4Only() CompiledNetworkPolicy {
	out := CompiledNetworkPolicy{
		Destinations: append([]NetworkDestination(nil), p.Destinations...),
		Hosts:        make(map[string][]netip.Addr, len(p.Hosts)),
	}
	for _, rule := range p.Rules {
		if rule.Prefix.Addr().Is4() {
			out.Rules = append(out.Rules, rule)
		}
	}
	for host, addrs := range p.Hosts {
		for _, addr := range addrs {
			if addr.Is4() {
				out.Hosts[host] = append(out.Hosts[host], addr)
			}
		}
	}
	return out
}

func CompileNetworkPolicy(ctx context.Context, destinations []NetworkDestination, resolver NetworkResolver) (CompiledNetworkPolicy, error) {
	if len(destinations) == 0 {
		return CompiledNetworkPolicy{}, fmt.Errorf("network allowlist requires at least one destination")
	}
	if resolver == nil {
		resolver = defaultNetworkResolver
	}
	policy := CompiledNetworkPolicy{
		Destinations: append([]NetworkDestination(nil), destinations...),
		Hosts:        make(map[string][]netip.Addr),
	}
	seenRules := map[string]struct{}{}
	for _, destination := range destinations {
		switch destination.Kind {
		case NetworkDestinationCIDR:
			addNetworkRule(&policy, seenRules, NetworkRule{Prefix: destination.Prefix, Port: destination.Port})
		case NetworkDestinationIP:
			addNetworkRule(&policy, seenRules, NetworkRule{Prefix: addrPrefix(destination.IP), Port: destination.Port})
		case NetworkDestinationHost:
			addrs, err := resolver(ctx, destination.Host)
			if err != nil {
				return CompiledNetworkPolicy{}, fmt.Errorf("resolve network allowlist host %q: %w", destination.Host, err)
			}
			addrs = dedupeAddrs(addrs)
			if len(addrs) == 0 {
				return CompiledNetworkPolicy{}, fmt.Errorf("resolve network allowlist host %q: no addresses", destination.Host)
			}
			policy.Hosts[destination.Host] = append([]netip.Addr(nil), addrs...)
			for _, addr := range addrs {
				addNetworkRule(&policy, seenRules, NetworkRule{Prefix: addrPrefix(addr), Port: destination.Port})
			}
		default:
			return CompiledNetworkPolicy{}, fmt.Errorf("network destination %q has unsupported kind %q", destination.Canonical(), destination.Kind)
		}
	}
	sort.Slice(policy.Rules, func(i, j int) bool { return policy.Rules[i].String() < policy.Rules[j].String() })
	return policy, nil
}

func defaultNetworkResolver(ctx context.Context, host string) ([]netip.Addr, error) {
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

func addNetworkRule(policy *CompiledNetworkPolicy, seen map[string]struct{}, rule NetworkRule) {
	if !rule.Prefix.IsValid() || rule.Port == 0 {
		return
	}
	key := rule.String()
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	policy.Rules = append(policy.Rules, rule)
}

func dedupeAddrs(addrs []netip.Addr) []netip.Addr {
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

func addrPrefix(addr netip.Addr) netip.Prefix {
	addr = addr.Unmap()
	if addr.Is4() {
		return netip.PrefixFrom(addr, 32)
	}
	return netip.PrefixFrom(addr, 128)
}

func NetworkDestinationsContainAll(ceiling []NetworkDestination, requested []NetworkDestination) bool {
	if len(requested) == 0 {
		return true
	}
	allowed := make(map[string]struct{}, len(ceiling))
	for _, destination := range ceiling {
		allowed[destination.Canonical()] = struct{}{}
	}
	for _, destination := range requested {
		if _, ok := allowed[destination.Canonical()]; !ok {
			return false
		}
	}
	return true
}

func NetworkAllowsHostPort(ctx context.Context, destinations []NetworkDestination, host string, port uint16, resolver NetworkResolver) (bool, error) {
	if resolver == nil {
		resolver = defaultNetworkResolver
	}
	target, err := ParseNetworkDestination(net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return false, err
	}
	if target.Kind == NetworkDestinationHost {
		target.Host = strings.ToLower(strings.TrimSuffix(target.Host, "."))
	}
	for _, destination := range destinations {
		if destination.Port != target.Port {
			continue
		}
		if destination.Canonical() == target.Canonical() {
			return true, nil
		}
		if destination.Kind == NetworkDestinationCIDR {
			addr := target.IP
			if target.Kind == NetworkDestinationHost {
				addrs, err := resolver(ctx, target.Host)
				if err != nil {
					return false, err
				}
				for _, resolved := range addrs {
					if destination.Prefix.Contains(resolved.Unmap()) {
						return true, nil
					}
				}
				continue
			}
			if addr.IsValid() && destination.Prefix.Contains(addr.Unmap()) {
				return true, nil
			}
		}
	}
	return false, nil
}
