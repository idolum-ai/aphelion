//go:build linux

package core

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func ValidateDurableAgentParentControlURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("durable agent parent_control_url is invalid: %w", err)
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	host := strings.ToLower(strings.Trim(strings.TrimSpace(parsed.Hostname()), "."))
	switch scheme {
	case "https":
		if host == "" {
			return fmt.Errorf("durable agent parent_control_url host is required")
		}
		return nil
	case "http":
		if durableAgentControlURLHostIsLoopback(host) || durableAgentControlURLHostIsTailnet(host) {
			return nil
		}
		return fmt.Errorf("durable agent parent_control_url may use http only for loopback or tailnet .ts.net hosts")
	default:
		return fmt.Errorf("durable agent parent_control_url scheme must be https, or http for loopback/tailnet")
	}
}

func durableAgentControlURLHostIsLoopback(host string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(host), ".")) {
	case "localhost":
		return true
	case "":
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func durableAgentControlURLHostIsTailnet(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "."))
	return strings.HasSuffix(host, ".ts.net")
}
