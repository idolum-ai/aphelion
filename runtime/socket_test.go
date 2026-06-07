//go:build linux

package runtime

import (
	"net"
	"testing"
)

func requireLocalTCPListener(t *testing.T, address string) {
	t.Helper()
	ln, err := net.Listen("tcp", address)
	if err != nil {
		t.Skipf("local TCP listener unavailable for integration test: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close local TCP listener probe: %v", err)
	}
}
