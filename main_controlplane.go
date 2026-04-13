//go:build linux

package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

func durableAgentControlPlaneServer(cfg *config.Config, store *session.SQLiteStore) *http.Server {
	if cfg == nil || store == nil || !cfg.DurableAgents.ControlPlane.Enabled {
		return nil
	}
	addr := strings.TrimSpace(cfg.DurableAgents.ControlPlane.Listen)
	if addr == "" {
		return nil
	}
	handler := durableagent.NewHTTPHandler(store)
	return &http.Server{
		Addr:              addr,
		Handler:           handler.HandlerWithBasePath(cfg.DurableAgents.ControlPlane.BasePath),
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func startDurableAgentControlPlane(ctx context.Context, server *http.Server) error {
	if server == nil {
		return nil
	}
	ln, err := net.Listen("tcp", strings.TrimSpace(server.Addr))
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("WARN durable agent control plane shutdown failed: %v", err)
		}
	}()
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("ERROR durable agent control plane serve failed: %v", err)
		}
	}()
	return nil
}
