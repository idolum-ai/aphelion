//go:build linux

package runtime

import (
	"net/http"
	"testing"
	"time"
)

func TestNewCodexHTTPClientDoesNotUseEndToEndTimeout(t *testing.T) {
	t.Parallel()

	client := newCodexHTTPClient()
	if client == nil {
		t.Fatal("newCodexHTTPClient() = nil")
	}
	if client.Timeout != 0 {
		t.Fatalf("client.Timeout = %v, want 0 for long-running Codex requests", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client.Transport = %T, want *http.Transport", client.Transport)
	}
	if transport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 30s", transport.ResponseHeaderTimeout)
	}
}
