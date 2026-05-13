//go:build linux

package config

import (
	"net"
	"strings"
)

func durableAgentControlPlaneListenIsLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return false
	}
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "."))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
