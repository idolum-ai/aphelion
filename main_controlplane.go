//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

var (
	serveDurableAgentHTTP = func(server *http.Server, ln net.Listener) error {
		return server.Serve(ln)
	}
	serveDurableAgentHTTPS = func(server *http.Server, ln net.Listener) error {
		return server.ServeTLS(ln, "", "")
	}
)

func durableAgentControlPlaneServer(cfg *config.Config, store *session.SQLiteStore) (*http.Server, error) {
	if cfg == nil || store == nil || !cfg.DurableAgents.ControlPlane.Enabled {
		return nil, nil
	}
	addr := strings.TrimSpace(cfg.DurableAgents.ControlPlane.Listen)
	if addr == "" {
		return nil, fmt.Errorf("durable_agents.control_plane.listen is required when durable_agents.control_plane.enabled = true")
	}
	handler := durableagent.NewHTTPHandler(store)
	var tlsConfig *tls.Config
	if certFile := strings.TrimSpace(cfg.DurableAgents.ControlPlane.CertFile); certFile != "" {
		keyFile := strings.TrimSpace(cfg.DurableAgents.ControlPlane.KeyFile)
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("durable agent control plane tls load failed: %w", err)
		}
		tlsConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		}
	}
	return &http.Server{
		Addr:              addr,
		Handler:           handler.HandlerWithBasePath(cfg.DurableAgents.ControlPlane.BasePath),
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
	}, nil
}

func startDurableAgentControlPlane(ctx context.Context, server *http.Server) error {
	if server == nil {
		return nil
	}
	ln, err := net.Listen("tcp", strings.TrimSpace(server.Addr))
	if err != nil {
		return err
	}
	server.Addr = ln.Addr().String()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("WARN durable agent control plane shutdown failed: %v", err)
		}
	}()
	go func() {
		err := serveDurableAgentControlPlane(server, ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("ERROR durable agent control plane serve failed: %v", err)
		}
	}()
	return nil
}

func serveDurableAgentControlPlane(server *http.Server, ln net.Listener) error {
	if server != nil && server.TLSConfig != nil && len(server.TLSConfig.Certificates) > 0 {
		return serveDurableAgentHTTPS(server, ln)
	}
	return serveDurableAgentHTTP(server, ln)
}
