//go:build linux

package config

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

func validateSandboxProfileConfig(path string, profile SandboxProfileConfig) error {
	switch profile.Mode {
	case "trusted", "isolated":
	default:
		return fmt.Errorf("%s.mode must be one of trusted|isolated", path)
	}
	switch profile.Network {
	case "allowlist", "deny":
	default:
		return fmt.Errorf("%s.network must be one of allowlist|deny", path)
	}
	for i, destination := range profile.NetworkAllow {
		if err := validateSandboxNetworkDestination(destination); err != nil {
			return fmt.Errorf("%s.network_allow[%d]: %w", path, i, err)
		}
	}
	if profile.Network == "allowlist" && profile.Mode == "isolated" && len(profile.NetworkAllow) == 0 {
		return fmt.Errorf("%s.network_allow must contain at least one host:port, ip:port, or cidr:port when isolated network=allowlist", path)
	}
	return nil
}

func validateSandboxNetworkDestination(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fmt.Errorf("destination is required")
	}
	host, portRaw, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("destination must include an explicit port: %w", err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("host is required")
	}
	if _, err := netip.ParsePrefix(host); err != nil {
		if _, err := netip.ParseAddr(host); err != nil {
			if err := validateSandboxNetworkHostname(host); err != nil {
				return err
			}
		}
	}
	port, err := strconv.Atoi(strings.TrimSpace(portRaw))
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

func validateSandboxNetworkHostname(host string) error {
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
