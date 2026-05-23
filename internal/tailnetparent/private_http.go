//go:build linux

package tailnetparent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

// Router is the parent-private HTTP surface's narrow view of Telegram command control.
type Router interface {
	TailnetStatus(ctx context.Context, senderID int64) (core.TailnetStatusSnapshot, error)
	TailnetSurfaces(senderID int64) ([]core.TailnetSurfaceStatus, error)
	TailnetGrantBindings(senderID int64) ([]core.TailnetGrantBindingStatus, error)
	StatusSystem(senderID int64) (core.SystemStatusSnapshot, error)
	CurrentEfforts() (persona string, governor string)
	LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error)
}

// NewDurableAgentControlHandler returns the durable-agent parent control handler mounted under /control.
func NewDurableAgentControlHandler(store *session.SQLiteStore) http.Handler {
	if store == nil {
		return nil
	}
	handler := durableagent.NewHTTPHandler(store)
	handler.RequirePeerIdentity = true
	return handler.HandlerWithBasePath("/control")
}

// NewPrivateHTTPHandler builds the parent tailnet's private read-only HTTP mirror.
func NewPrivateHTTPHandler(router Router, adminID int64, adminLoginNames []string, control http.Handler) http.Handler {
	mux := http.NewServeMux()
	if control != nil {
		mux.Handle("/control/", control)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"ok":      true,
			"service": "aphelion",
		})
	})
	mux.HandleFunc("/tailnet", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireAdminLogin(w, r, adminLoginNames) {
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
		writeJSON(w, snapshot)
	})
	mux.HandleFunc("/tailnet/surfaces", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireAdminLogin(w, r, adminLoginNames) {
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
		writeJSON(w, map[string]any{
			"surfaces": surfaces,
		})
	})
	mux.HandleFunc("/tailnet/grants", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireAdminLogin(w, r, adminLoginNames) {
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
		writeJSON(w, map[string]any{
			"grant_bindings": bindings,
		})
	})
	mux.HandleFunc("/tailnet/surfaces/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if !requireAdminLogin(w, r, adminLoginNames) {
			return
		}
		http.Error(w, "tailnet private endpoints are read-only mirrors; use Telegram /tailnet controls for mutations", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireAdminLogin(w, r, adminLoginNames) {
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
		writeJSON(w, map[string]any{
			"status":          status,
			"telegram_text":   face.RenderTelegramStatusSystem(status, personaEffort, governorEffort),
			"persona_effort":  personaEffort,
			"governor_effort": governorEffort,
		})
	})
	mux.HandleFunc("/health/diagnosis/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireAdminLogin(w, r, adminLoginNames) {
			return
		}
		if router == nil || adminID == 0 {
			http.Error(w, "health diagnosis router unavailable", http.StatusServiceUnavailable)
			return
		}
		report, ok, err := router.LatestDoctorReport(r.Context(), adminID, adminID)
		if err != nil {
			http.Error(w, "health diagnosis latest report unavailable", http.StatusInternalServerError)
			return
		}
		if ok {
			writeJSON(w, map[string]any{
				"available": true,
				"report":    report,
			})
			return
		}
		writeJSON(w, map[string]any{
			"available": false,
			"message":   "no doctor report has been recorded for the configured admin chat",
		})
	})
	return mux
}

func requireAdminLogin(w http.ResponseWriter, r *http.Request, allowed []string) bool {
	identity, ok := core.TailnetPeerIdentityFromContext(r.Context())
	if !ok {
		http.Error(w, "tailnet peer identity required", http.StatusForbidden)
		return false
	}
	login := strings.ToLower(strings.TrimSpace(identity.LoginName))
	for _, allowedLogin := range allowed {
		if login != "" && login == strings.ToLower(strings.TrimSpace(allowedLogin)) {
			return true
		}
	}
	http.Error(w, "tailnet admin login is not authorized", http.StatusForbidden)
	return false
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, "encode response failed", http.StatusInternalServerError)
	}
}
