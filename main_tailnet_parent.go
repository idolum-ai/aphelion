//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/tailnet"
)

func tailnetParentService(cfg *config.Config, router commandRouter) (*tailnet.ParentService, error) {
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
		Handler:          tailnetPrivateHTTPHandler(router, adminID),
		Logf:             log.Printf,
	}), nil
}

func startTailnetParent(ctx context.Context, service *tailnet.ParentService) error {
	if service == nil {
		return nil
	}
	if err := service.Start(ctx); err != nil {
		log.Printf("WARN parent tsnet start failed; continuing without private tailnet listener: %v", err)
		return nil
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

func tailnetPrivateHTTPHandler(router commandRouter, adminID int64) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeTailnetPrivateJSON(w, map[string]any{
			"ok":      true,
			"service": "aphelion",
		})
	})
	mux.HandleFunc("/tailnet", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if router == nil || adminID == 0 {
			http.Error(w, "tailnet router unavailable", http.StatusServiceUnavailable)
			return
		}
		snapshot, err := router.TailnetStatus(r.Context(), adminID)
		if err != nil {
			http.Error(w, "tailnet status unavailable", http.StatusInternalServerError)
			return
		}
		writeTailnetPrivateJSON(w, snapshot)
	})
	mux.HandleFunc("/tailnet/surfaces", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if router == nil || adminID == 0 {
			http.Error(w, "tailnet router unavailable", http.StatusServiceUnavailable)
			return
		}
		surfaces, err := router.TailnetSurfaces(adminID)
		if err != nil {
			http.Error(w, "tailnet surfaces unavailable", http.StatusInternalServerError)
			return
		}
		writeTailnetPrivateJSON(w, map[string]any{
			"surfaces": surfaces,
		})
	})
	mux.HandleFunc("/tailnet/grants", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if router == nil || adminID == 0 {
			http.Error(w, "tailnet router unavailable", http.StatusServiceUnavailable)
			return
		}
		bindings, err := router.TailnetGrantBindings(adminID)
		if err != nil {
			http.Error(w, "tailnet grant bindings unavailable", http.StatusInternalServerError)
			return
		}
		writeTailnetPrivateJSON(w, map[string]any{
			"grant_bindings": bindings,
		})
	})
	mux.HandleFunc("/tailnet/surfaces/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "tailnet private endpoints are read-only mirrors; use Telegram /tailnet controls for mutations", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if router == nil || adminID == 0 {
			http.Error(w, "status router unavailable", http.StatusServiceUnavailable)
			return
		}
		status, err := router.StatusSystem(adminID)
		if err != nil {
			http.Error(w, "system status unavailable", http.StatusInternalServerError)
			return
		}
		personaEffort, governorEffort := router.CurrentEfforts()
		writeTailnetPrivateJSON(w, map[string]any{
			"status":          status,
			"telegram_text":   face.RenderTelegramStatusSystem(status, personaEffort, governorEffort),
			"persona_effort":  personaEffort,
			"governor_effort": governorEffort,
		})
	})
	mux.HandleFunc("/doctor/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if router == nil || adminID == 0 {
			http.Error(w, "doctor router unavailable", http.StatusServiceUnavailable)
			return
		}
		report, ok, err := router.LatestDoctorReport(r.Context(), adminID, adminID)
		if err != nil {
			http.Error(w, "doctor latest report unavailable", http.StatusInternalServerError)
			return
		}
		if ok {
			writeTailnetPrivateJSON(w, map[string]any{
				"available": true,
				"report":    report,
			})
			return
		}
		writeTailnetPrivateJSON(w, map[string]any{
			"available": false,
			"message":   "no doctor report has been recorded for the configured admin chat",
		})
	})
	return mux
}

func writeTailnetPrivateJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, "encode response failed", http.StatusInternalServerError)
	}
}
