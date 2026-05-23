//go:build linux

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/internal/tailnetparent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
)

func tailnetParentService(cfg *config.Config, router commandRouter, store *session.SQLiteStore) (*tailnet.ParentService, error) {
	if cfg == nil || !cfg.Tailscale.Enabled || !cfg.Tailscale.Parent.Enabled {
		return nil, nil
	}
	authKey, authSource, authErr := tailnetParentAuthKey(cfg.Tailscale.Parent)
	if authErr != nil {
		log.Printf("WARN parent tsnet auth key unavailable: %v", authErr)
	}
	adminID := firstConfiguredAdminID(cfg)
	return tailnet.NewParentService(tailnet.ParentOptions{
		Enabled:          true,
		Hostname:         cfg.Tailscale.Parent.Hostname,
		StateDir:         cfg.Tailscale.Parent.StateDir,
		ListenAddr:       cfg.Tailscale.Parent.ListenAddr,
		AuthKey:          authKey,
		AuthKeySource:    authSource,
		AuthKeyLoadError: authErr,
		Tags:             cfg.Tailscale.Parent.Tags,
		ExpectedTailnet:  cfg.Tailscale.ExpectedTailnet,
		Handler:          tailnetparent.NewPrivateHTTPHandler(router, adminID, cfg.Tailscale.Parent.AdminLoginNames, tailnetparent.NewDurableAgentControlHandler(store)),
		Logf:             log.Printf,
	}), nil
}

func startTailnetParent(ctx context.Context, service *tailnet.ParentService) error {
	if service == nil {
		return nil
	}
	if err := service.Start(ctx); err != nil {
		return fmt.Errorf("start parent tsnet listener: %w", err)
	}
	status := service.Status()
	if status.Running {
		log.Printf("INFO parent tsnet listening hostname=%s addr=%s magic_url=%s", status.Hostname, status.ListenAddr, status.MagicDNSURL)
	}
	return nil
}

func tailnetParentAuthKey(cfg config.TailscaleParentConfig) (string, string, error) {
	if envName := strings.TrimSpace(cfg.AuthKeyEnv); envName != "" {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			return value, "env:" + envName, nil
		}
	}
	if path := strings.TrimSpace(cfg.AuthKeyFile); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", "", fmt.Errorf("read tailscale.parent.auth_key_file: %w", err)
		}
		if value := strings.TrimSpace(string(data)); value != "" {
			return value, "file:" + path, nil
		}
	}
	return "", "", nil
}

func firstConfiguredAdminID(cfg *config.Config) int64 {
	if cfg == nil {
		return 0
	}
	for _, id := range cfg.Principals.Telegram.AdminUserIDs {
		if id > 0 {
			return id
		}
	}
	return 0
}
