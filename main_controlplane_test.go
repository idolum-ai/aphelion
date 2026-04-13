//go:build linux

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

func TestDurableAgentControlPlaneServerDisabledByDefault(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	server := durableAgentControlPlaneServer(&config.Config{}, store)
	if server != nil {
		t.Fatalf("durableAgentControlPlaneServer() = %#v, want nil when disabled", server)
	}
}

func TestDurableAgentControlPlaneServerUsesConfiguredListenAddress(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	cfg := &config.Config{
		DurableAgents: config.DurableAgentsConfig{
			ControlPlane: config.DurableAgentControlPlaneConfig{
				Enabled: true,
				Listen:  "127.0.0.1:8787",
			},
		},
	}
	server := durableAgentControlPlaneServer(cfg, store)
	if server == nil {
		t.Fatal("durableAgentControlPlaneServer() = nil, want configured server")
	}
	if server.Addr != "127.0.0.1:8787" {
		t.Fatalf("server.Addr = %q, want 127.0.0.1:8787", server.Addr)
	}
	if server.Handler == nil {
		t.Fatal("server.Handler = nil, want durable agent control-plane handler")
	}
}

func TestDurableAgentControlPlaneServerMountsConfiguredBasePath(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	cfg := &config.Config{
		DurableAgents: config.DurableAgentsConfig{
			ControlPlane: config.DurableAgentControlPlaneConfig{
				Enabled:  true,
				Listen:   "127.0.0.1:8787",
				BasePath: "/control",
			},
		},
	}
	server := durableAgentControlPlaneServer(cfg, store)
	if server == nil {
		t.Fatal("durableAgentControlPlaneServer() = nil, want configured server")
	}

	rootReq := httptest.NewRequest(http.MethodPost, durableagent.ControlPlaneEnrollPath, nil)
	rootRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusNotFound {
		t.Fatalf("root path status = %d, want 404", rootRec.Code)
	}

	prefixedReq := httptest.NewRequest(http.MethodPost, "/control"+durableagent.ControlPlaneEnrollPath, nil)
	prefixedRec := httptest.NewRecorder()
	server.Handler.ServeHTTP(prefixedRec, prefixedReq)
	if prefixedRec.Code == http.StatusNotFound {
		t.Fatalf("prefixed path status = %d, want mounted handler", prefixedRec.Code)
	}
}

func TestDurableAgentControlPlaneServerLoadsTLSCertificate(t *testing.T) {
	t.Parallel()

	certPath, keyPath := writeTestTLSCertPair(t)
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	cfg := &config.Config{
		DurableAgents: config.DurableAgentsConfig{
			ControlPlane: config.DurableAgentControlPlaneConfig{
				Enabled:  true,
				Listen:   "127.0.0.1:8787",
				BasePath: "/control",
				CertFile: certPath,
				KeyFile:  keyPath,
			},
		},
	}
	server := durableAgentControlPlaneServer(cfg, store)
	if server == nil {
		t.Fatal("durableAgentControlPlaneServer() = nil, want configured tls server")
	}
	if server.TLSConfig == nil || len(server.TLSConfig.Certificates) != 1 {
		t.Fatalf("server.TLSConfig = %#v, want loaded certificate", server.TLSConfig)
	}
}

func TestServeDurableAgentControlPlaneUsesTLSWhenConfigured(t *testing.T) {
	t.Parallel()

	certPath, keyPath := writeTestTLSCertPair(t)
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	cfg := &config.Config{
		DurableAgents: config.DurableAgentsConfig{
			ControlPlane: config.DurableAgentControlPlaneConfig{
				Enabled:  true,
				Listen:   "127.0.0.1:0",
				BasePath: "/control",
				CertFile: certPath,
				KeyFile:  keyPath,
			},
		},
	}
	server := durableAgentControlPlaneServer(cfg, store)
	if server == nil {
		t.Fatal("durableAgentControlPlaneServer() = nil, want tls server")
	}

	origHTTP := serveDurableAgentHTTP
	origHTTPS := serveDurableAgentHTTPS
	defer func() {
		serveDurableAgentHTTP = origHTTP
		serveDurableAgentHTTPS = origHTTPS
	}()

	called := ""
	serveDurableAgentHTTP = func(server *http.Server, ln net.Listener) error {
		called = "http"
		return http.ErrServerClosed
	}
	serveDurableAgentHTTPS = func(server *http.Server, ln net.Listener) error {
		called = "https"
		return http.ErrServerClosed
	}

	err = serveDurableAgentControlPlane(server, fakeControlPlaneListener{})
	if !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serveDurableAgentControlPlane() err = %v, want http.ErrServerClosed", err)
	}
	if called != "https" {
		t.Fatalf("serveDurableAgentControlPlane() path = %q, want https", called)
	}
}

type fakeControlPlaneListener struct{}

func (fakeControlPlaneListener) Accept() (net.Conn, error) {
	return nil, errors.New("accept not implemented")
}
func (fakeControlPlaneListener) Close() error   { return nil }
func (fakeControlPlaneListener) Addr() net.Addr { return fakeControlPlaneAddr("127.0.0.1:0") }

type fakeControlPlaneAddr string

func (a fakeControlPlaneAddr) Network() string { return "tcp" }
func (a fakeControlPlaneAddr) String() string  { return string(a) }

func writeTestTLSCertPair(t *testing.T) (string, string) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() err = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           nil,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate() err = %v", err)
	}
	certPath := filepath.Join(t.TempDir(), "cert.pem")
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(cert.pem) err = %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(key.pem) err = %v", err)
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatalf("tls.LoadX509KeyPair() err = %v", err)
	}
	return certPath, keyPath
}
