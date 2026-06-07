//go:build linux

package tailnet

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestParentServiceDisabledDoesNotStartNode(t *testing.T) {
	t.Parallel()

	node := &fakeParentNode{}
	service := NewParentService(ParentOptions{
		Enabled: false,
		Node:    node,
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("Start() err = %v", err)
	}
	if node.started {
		t.Fatal("node started = true, want disabled parent not to start")
	}
}

func TestParentServiceRequiresAuthKeyForFirstStart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	node := &fakeParentNode{}
	service := NewParentService(ParentOptions{
		Enabled:  true,
		Hostname: "aphelion-test",
		StateDir: dir,
		Node:     node,
	})
	err := service.Start(context.Background())
	if err == nil {
		t.Fatal("Start() err = nil, want missing auth key failure")
	}
	if node.started {
		t.Fatal("node started = true, want auth gate before start")
	}
	if status := service.Status(); status.Running || !strings.Contains(status.LastError, "auth key") {
		t.Fatalf("status = %#v, want stopped auth-key error", status)
	}
}

func TestParentServiceReusesExistingStateWithoutAuthKey(t *testing.T) {
	requireLocalTCPListener(t, "127.0.0.1:0")
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tailscaled.state"), []byte("existing"), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	node := &fakeParentNode{}
	service := NewParentService(ParentOptions{
		Enabled:         true,
		Hostname:        "aphelion-test",
		StateDir:        dir,
		ListenAddr:      "127.0.0.1:0",
		ExpectedTailnet: "example.ts.net",
		Node:            node,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("Start() err = %v", err)
	}
	status := service.Status()
	if !status.Running || status.ListenAddr == "" {
		t.Fatalf("status = %#v, want running listener", status)
	}
	if status.MagicDNSURL != "http://aphelion-test.example.ts.net:0" {
		t.Fatalf("magic url = %q, want configured MagicDNS hint", status.MagicDNSURL)
	}
	resp, err := http.Get("http://" + status.ListenAddr)
	if err != nil {
		t.Fatalf("GET health handler: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("Close() err = %v", err)
	}
	if status := service.Status(); status.Running {
		t.Fatalf("status = %#v, want stopped after close", status)
	}
}

func TestParentServiceStartsWithAuthKey(t *testing.T) {
	requireLocalTCPListener(t, "127.0.0.1:0")
	t.Parallel()

	node := &fakeParentNode{}
	service := NewParentService(ParentOptions{
		Enabled:       true,
		Hostname:      "aphelion-test",
		StateDir:      t.TempDir(),
		ListenAddr:    "127.0.0.1:0",
		AuthKey:       "tskey-auth-test",
		AuthKeySource: "env:APHELION_TS_AUTHKEY",
		Node:          node,
		Handler:       http.NewServeMux(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("Start() err = %v", err)
	}
	defer func() {
		if err := service.Close(context.Background()); err != nil {
			t.Fatalf("Close() err = %v", err)
		}
	}()
	if !node.started || !node.listened {
		t.Fatalf("node state = %#v, want started and listened", node)
	}
	status := service.Status()
	if status.AuthKeySource != "env:APHELION_TS_AUTHKEY" {
		t.Fatalf("auth key source = %q, want env source", status.AuthKeySource)
	}
}

func TestParentServiceStartIsIdempotentWhileRunning(t *testing.T) {
	requireLocalTCPListener(t, "127.0.0.1:0")
	t.Parallel()

	node := &fakeParentNode{}
	service := NewParentService(ParentOptions{
		Enabled:    true,
		Hostname:   "aphelion-test",
		StateDir:   t.TempDir(),
		ListenAddr: "127.0.0.1:0",
		AuthKey:    "tskey-auth-test",
		Node:       node,
		Handler:    http.NewServeMux(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("first Start() err = %v", err)
	}
	defer func() {
		if err := service.Close(context.Background()); err != nil {
			t.Fatalf("Close() err = %v", err)
		}
	}()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("second Start() err = %v", err)
	}
	if node.startCount != 1 || node.listenCount != 1 {
		t.Fatalf("node start/listen = %d/%d, want idempotent 1/1", node.startCount, node.listenCount)
	}
}

func TestParentServiceCanRestartAfterClose(t *testing.T) {
	requireLocalTCPListener(t, "127.0.0.1:0")
	t.Parallel()

	node := &fakeParentNode{}
	service := NewParentService(ParentOptions{
		Enabled:    true,
		Hostname:   "aphelion-test",
		StateDir:   t.TempDir(),
		ListenAddr: "127.0.0.1:0",
		AuthKey:    "tskey-auth-test",
		Node:       node,
		Handler:    http.NewServeMux(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("first Start() err = %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("first Close() err = %v", err)
	}
	if err := service.Start(ctx); err != nil {
		t.Fatalf("second Start() err = %v", err)
	}
	defer func() {
		if err := service.Close(context.Background()); err != nil {
			t.Fatalf("second Close() err = %v", err)
		}
	}()
	if node.startCount != 2 || node.listenCount != 2 {
		t.Fatalf("node start/listen = %d/%d, want restart 2/2", node.startCount, node.listenCount)
	}
}

func TestRealParentNodeAdvertisesConfiguredTags(t *testing.T) {
	t.Parallel()

	node := newRealParentNode(ParentOptions{
		Hostname: "aphelion-test",
		StateDir: t.TempDir(),
		AuthKey:  "tskey-auth-test",
		Tags:     []string{"tag:admin", "tag:aphelion", "tag:admin"},
	})
	if got, want := node.server.AdvertiseTags, []string{"tag:admin", "tag:aphelion"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AdvertiseTags = %#v, want %#v", got, want)
	}
}

func TestAttachPeerIdentityBypassesHealthAndRejectsUnknownPeers(t *testing.T) {
	t.Parallel()

	handler := attachPeerIdentity(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), nil)

	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK || healthRec.Body.String() != "ok" {
		t.Fatalf("health = %d %q, want identity-free ok", healthRec.Code, healthRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%q, want 403 without peer identity", rec.Code, rec.Body.String())
	}
}

type fakeParentNode struct {
	started     bool
	listened    bool
	closed      bool
	startCount  int
	listenCount int
	startErr    error
	listenErr   error
	localErr    error
	ln          net.Listener
}

func (n *fakeParentNode) Start() error {
	n.started = true
	n.closed = false
	n.startCount++
	return n.startErr
}

func (n *fakeParentNode) Listen(network string, addr string) (net.Listener, error) {
	n.listened = true
	n.listenCount++
	if n.listenErr != nil {
		return nil, n.listenErr
	}
	if n.ln != nil && !n.closed {
		return n.ln, nil
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, err
	}
	n.ln = ln
	return ln, nil
}

func (n *fakeParentNode) LocalClient() (PeerIdentifier, error) {
	if n.localErr != nil {
		return nil, n.localErr
	}
	return fakePeerIdentifier{}, nil
}

func (n *fakeParentNode) Close() error {
	n.closed = true
	if n.ln != nil {
		err := n.ln.Close()
		n.ln = nil
		return err
	}
	return nil
}

type fakePeerIdentifier struct{}

func (fakePeerIdentifier) IdentifyPeer(context.Context, string) (core.TailnetPeerIdentity, error) {
	return core.TailnetPeerIdentity{
		StableNodeID: "node-stable-1",
		NodeName:     "child.example.ts.net",
		ComputedName: "child",
		LoginName:    "admin@example.com",
	}, nil
}
