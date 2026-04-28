//go:build linux

package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

const (
	telegramMiniAppPath               = "/telegram/status-app"
	telegramMiniAppStatusAPIPath      = "/telegram/status-app/api/status"
	telegramMiniAppInitDataHeader     = "X-Telegram-Init-Data"
	telegramMiniAppDefaultAuthMaxAge  = 24 * time.Hour
	telegramMiniAppStateSkewAllowance = 5 * time.Minute
)

var serveTelegramMiniAppHTTP = func(server *http.Server, ln net.Listener) error {
	return server.Serve(ln)
}

type telegramStatusMiniAppHandler struct {
	router     commandRouter
	botToken   string
	authMaxAge time.Duration
	now        func() time.Time
}

type telegramMiniAppAuth struct {
	UserID   int64
	AuthDate time.Time
}

type telegramMiniAppStatusResponse struct {
	GeneratedAt string                      `json:"generated_at"`
	ChatID      int64                       `json:"chat_id"`
	SenderID    int64                       `json:"sender_id"`
	IsAdmin     bool                        `json:"is_admin"`
	ActiveView  string                      `json:"active_view"`
	Views       []telegramMiniAppStatusView `json:"views"`
	StatusText  string                      `json:"status_text"`
}

type telegramMiniAppStatusView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func telegramMiniAppPublicURL(cfg *config.Config) string {
	if cfg == nil || !cfg.Telegram.MiniApp.Enabled {
		return ""
	}
	return strings.TrimSpace(cfg.Telegram.MiniApp.PublicURL)
}

func telegramMiniAppServer(cfg *config.Config, router commandRouter) *http.Server {
	if cfg == nil || !cfg.Telegram.MiniApp.Enabled {
		return nil
	}
	addr := strings.TrimSpace(cfg.Telegram.MiniApp.ListenAddr)
	if addr == "" {
		return nil
	}
	authMaxAge := telegramMiniAppDefaultAuthMaxAge
	if raw := strings.TrimSpace(cfg.Telegram.MiniApp.AuthMaxAge); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			authMaxAge = parsed
		}
	}
	handler := &telegramStatusMiniAppHandler{
		router:     router,
		botToken:   cfg.Telegram.BotToken,
		authMaxAge: authMaxAge,
		now:        time.Now,
	}
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func startTelegramMiniApp(ctx context.Context, server *http.Server) error {
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
			log.Printf("WARN telegram mini app shutdown failed: %v", err)
		}
	}()
	go func() {
		err := serveTelegramMiniAppHTTP(server, ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("ERROR telegram mini app serve failed: %v", err)
		}
	}()
	log.Printf("INFO telegram mini app listening addr=%s", server.Addr)
	return nil
}

func (h *telegramStatusMiniAppHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case telegramMiniAppPath:
		h.serveShell(w, r)
	case telegramMiniAppStatusAPIPath:
		h.serveStatusAPI(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *telegramStatusMiniAppHandler) serveShell(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(telegramMiniAppHTML))
}

func (h *telegramStatusMiniAppHandler) serveStatusAPI(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.router == nil {
		http.Error(w, "status router unavailable", http.StatusServiceUnavailable)
		return
	}
	now := h.currentTime()
	auth, err := validateTelegramMiniAppInitData(r.Header.Get(telegramMiniAppInitDataHeader), h.botToken, h.authMaxAge, now)
	if err != nil {
		http.Error(w, "telegram mini app authentication failed", http.StatusUnauthorized)
		return
	}
	chatID, senderID, err := validateTelegramStatusMiniAppState(r.URL.Query(), h.botToken, h.authMaxAge, now)
	if err != nil {
		http.Error(w, "telegram mini app state failed", http.StatusUnauthorized)
		return
	}
	if auth.UserID != senderID {
		http.Error(w, "telegram mini app sender mismatch", http.StatusForbidden)
		return
	}

	isAdmin := h.router.CanRestart(senderID)
	view := parseTelegramMiniAppStatusView(r.URL.Query().Get("view"))
	if statusViewRequiresAdmin(view, chatID, chatID) && !isAdmin {
		http.Error(w, "admin status view denied", http.StatusForbidden)
		return
	}
	personaEffort, governorEffort := h.router.CurrentEfforts()
	statusText, _, err := renderStatusView(r.Context(), h.router, chatID, senderID, view, chatID, personaEffort, governorEffort)
	if err != nil {
		http.Error(w, "status render failed", http.StatusInternalServerError)
		return
	}
	response := telegramMiniAppStatusResponse{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		ChatID:      chatID,
		SenderID:    senderID,
		IsAdmin:     isAdmin,
		ActiveView:  string(view),
		Views:       telegramMiniAppStatusViews(isAdmin),
		StatusText:  statusText,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("WARN telegram mini app encode failed: %v", err)
	}
}

func (h *telegramStatusMiniAppHandler) currentTime() time.Time {
	if h != nil && h.now != nil {
		return h.now().UTC()
	}
	return time.Now().UTC()
}

func parseTelegramMiniAppStatusView(raw string) statusView {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "pending":
		return statusViewPending
	case "system":
		return statusViewSystem
	case "hot":
		return statusViewHotChats
	case "find":
		return statusViewFindChat
	case "durables":
		return statusViewDurables
	default:
		return statusViewChat
	}
}

func telegramMiniAppStatusViews(isAdmin bool) []telegramMiniAppStatusView {
	views := []telegramMiniAppStatusView{
		{ID: string(statusViewChat), Label: "This Chat"},
		{ID: string(statusViewPending), Label: "Pending"},
	}
	if isAdmin {
		views = append(views,
			telegramMiniAppStatusView{ID: string(statusViewSystem), Label: "System"},
			telegramMiniAppStatusView{ID: string(statusViewHotChats), Label: "Hot Chats"},
			telegramMiniAppStatusView{ID: string(statusViewFindChat), Label: "Find Chat"},
			telegramMiniAppStatusView{ID: string(statusViewDurables), Label: "Durables"},
		)
	}
	return views
}

func buildTelegramStatusMiniAppURL(baseURL string, botToken string, chatID int64, senderID int64, issuedAt time.Time) string {
	baseURL = strings.TrimSpace(baseURL)
	botToken = strings.TrimSpace(botToken)
	if baseURL == "" || botToken == "" || chatID == 0 || senderID == 0 {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	issuedUnix := issuedAt.UTC().Unix()
	q := u.Query()
	q.Set("chat_id", strconv.FormatInt(chatID, 10))
	q.Set("sender_id", strconv.FormatInt(senderID, 10))
	q.Set("state_iat", strconv.FormatInt(issuedUnix, 10))
	q.Set("state_sig", signTelegramStatusMiniAppState(botToken, chatID, senderID, issuedUnix))
	u.RawQuery = q.Encode()
	return u.String()
}

func validateTelegramStatusMiniAppState(values url.Values, botToken string, maxAge time.Duration, now time.Time) (int64, int64, error) {
	botToken = strings.TrimSpace(botToken)
	if botToken == "" {
		return 0, 0, fmt.Errorf("bot token is required")
	}
	chatID, err := strconv.ParseInt(strings.TrimSpace(values.Get("chat_id")), 10, 64)
	if err != nil || chatID == 0 {
		return 0, 0, fmt.Errorf("chat_id is required")
	}
	senderID, err := strconv.ParseInt(strings.TrimSpace(values.Get("sender_id")), 10, 64)
	if err != nil || senderID == 0 {
		return 0, 0, fmt.Errorf("sender_id is required")
	}
	issuedUnix, err := strconv.ParseInt(strings.TrimSpace(values.Get("state_iat")), 10, 64)
	if err != nil || issuedUnix <= 0 {
		return 0, 0, fmt.Errorf("state_iat is required")
	}
	provided, err := hex.DecodeString(strings.TrimSpace(values.Get("state_sig")))
	if err != nil {
		return 0, 0, fmt.Errorf("state_sig is invalid")
	}
	expectedHex := signTelegramStatusMiniAppState(botToken, chatID, senderID, issuedUnix)
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return 0, 0, fmt.Errorf("state signature generation failed")
	}
	if !hmac.Equal(provided, expected) {
		return 0, 0, fmt.Errorf("state signature mismatch")
	}
	if err := validateMiniAppTimestamp(time.Unix(issuedUnix, 0).UTC(), maxAge, now); err != nil {
		return 0, 0, err
	}
	return chatID, senderID, nil
}

func signTelegramStatusMiniAppState(botToken string, chatID int64, senderID int64, issuedUnix int64) string {
	payload := fmt.Sprintf("chat_id=%d\nsender_id=%d\nstate_iat=%d", chatID, senderID, issuedUnix)
	mac := hmac.New(sha256.New, []byte("AphelionTelegramMiniAppState:"+strings.TrimSpace(botToken)))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func validateTelegramMiniAppInitData(raw string, botToken string, maxAge time.Duration, now time.Time) (telegramMiniAppAuth, error) {
	raw = strings.TrimSpace(raw)
	botToken = strings.TrimSpace(botToken)
	if raw == "" {
		return telegramMiniAppAuth{}, fmt.Errorf("init data is required")
	}
	if botToken == "" {
		return telegramMiniAppAuth{}, fmt.Errorf("bot token is required")
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return telegramMiniAppAuth{}, fmt.Errorf("parse init data: %w", err)
	}
	providedHash, err := hex.DecodeString(strings.TrimSpace(values.Get("hash")))
	if err != nil || len(providedHash) == 0 {
		return telegramMiniAppAuth{}, fmt.Errorf("hash is required")
	}
	dataCheckString := telegramMiniAppDataCheckString(values)
	if dataCheckString == "" {
		return telegramMiniAppAuth{}, fmt.Errorf("data check string is empty")
	}
	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(botToken))
	secret := secretMAC.Sum(nil)
	dataMAC := hmac.New(sha256.New, secret)
	_, _ = dataMAC.Write([]byte(dataCheckString))
	if !hmac.Equal(providedHash, dataMAC.Sum(nil)) {
		return telegramMiniAppAuth{}, fmt.Errorf("hash mismatch")
	}
	authUnix, err := strconv.ParseInt(strings.TrimSpace(values.Get("auth_date")), 10, 64)
	if err != nil || authUnix <= 0 {
		return telegramMiniAppAuth{}, fmt.Errorf("auth_date is required")
	}
	authDate := time.Unix(authUnix, 0).UTC()
	if err := validateMiniAppTimestamp(authDate, maxAge, now); err != nil {
		return telegramMiniAppAuth{}, err
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil {
		return telegramMiniAppAuth{}, fmt.Errorf("user is invalid: %w", err)
	}
	if user.ID == 0 {
		return telegramMiniAppAuth{}, fmt.Errorf("user id is required")
	}
	return telegramMiniAppAuth{UserID: user.ID, AuthDate: authDate}, nil
}

func telegramMiniAppDataCheckString(values url.Values) string {
	parts := make([]string, 0, len(values))
	for key, vals := range values {
		if key == "hash" {
			continue
		}
		for _, value := range vals {
			parts = append(parts, key+"="+value)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func validateMiniAppTimestamp(ts time.Time, maxAge time.Duration, now time.Time) error {
	if ts.IsZero() {
		return fmt.Errorf("timestamp is required")
	}
	if maxAge <= 0 {
		maxAge = telegramMiniAppDefaultAuthMaxAge
	}
	now = now.UTC()
	if ts.After(now.Add(telegramMiniAppStateSkewAllowance)) {
		return fmt.Errorf("timestamp is in the future")
	}
	if now.Sub(ts.UTC()) > maxAge {
		return fmt.Errorf("timestamp expired")
	}
	return nil
}

const telegramMiniAppHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Aphelion Status</title>
  <script src="https://telegram.org/js/telegram-web-app.js"></script>
  <style>
    :root {
      color-scheme: light dark;
      --bg: #f6f7f9;
      --panel: #ffffff;
      --text: #17202a;
      --muted: #667085;
      --line: #d9dee7;
      --accent: #1f7a5a;
      --accent-soft: #e4f3ec;
      --warn: #9a5b13;
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --bg: #101418;
        --panel: #171d22;
        --text: #e7ecef;
        --muted: #9aa6b2;
        --line: #2d3742;
        --accent: #5bc49b;
        --accent-soft: #17372b;
        --warn: #d29a4a;
      }
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--bg);
      color: var(--text);
      font: 14px/1.45 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    .app {
      width: min(980px, 100%);
      margin: 0 auto;
      padding: 16px;
    }
    header {
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 12px;
      align-items: start;
      padding: 6px 0 14px;
      border-bottom: 1px solid var(--line);
    }
    h1 {
      margin: 0;
      font-size: 20px;
      font-weight: 700;
      letter-spacing: 0;
    }
    .meta {
      margin-top: 3px;
      color: var(--muted);
      font-size: 12px;
    }
    .refresh {
      min-height: 36px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      color: var(--text);
      padding: 0 12px;
      font-weight: 600;
    }
    .tabs {
      display: flex;
      gap: 8px;
      overflow-x: auto;
      padding: 14px 0;
    }
    .tabs button {
      min-height: 34px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      color: var(--text);
      white-space: nowrap;
      padding: 0 12px;
      font-weight: 600;
    }
    .tabs button.active {
      border-color: var(--accent);
      background: var(--accent-soft);
      color: var(--accent);
    }
    .panel {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 14px;
      margin-bottom: 12px;
    }
    .panel h2 {
      margin: 0 0 10px;
      font-size: 13px;
      text-transform: uppercase;
      letter-spacing: 0;
      color: var(--muted);
    }
    .quick {
      border-left: 3px solid var(--accent);
    }
    .error {
      border-left: 3px solid var(--warn);
    }
    .lines {
      display: grid;
      gap: 6px;
    }
    .line {
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .section {
      margin-top: 8px;
      color: var(--accent);
      font-weight: 700;
    }
    .empty {
      color: var(--muted);
    }
  </style>
</head>
<body>
  <main class="app">
    <header>
      <div>
        <h1>Aphelion Status</h1>
        <div class="meta" id="meta">Loading status...</div>
      </div>
      <button class="refresh" id="refresh" type="button">Refresh</button>
    </header>
    <nav class="tabs" id="tabs" aria-label="Status views"></nav>
    <section class="panel quick" id="quickPanel" hidden>
      <h2>Quick Read</h2>
      <div id="quick"></div>
    </section>
    <section class="panel error" id="errorPanel" hidden>
      <h2>Unavailable</h2>
      <div id="error"></div>
    </section>
    <section class="panel">
      <h2>Details</h2>
      <div class="lines" id="details"></div>
    </section>
  </main>
  <script>
    const tg = window.Telegram && window.Telegram.WebApp ? window.Telegram.WebApp : null;
    if (tg) {
      tg.ready();
      tg.expand();
    }
    const state = { view: new URLSearchParams(window.location.search).get("view") || "chat" };
    const tabs = document.getElementById("tabs");
    const meta = document.getElementById("meta");
    const quickPanel = document.getElementById("quickPanel");
    const quick = document.getElementById("quick");
    const errorPanel = document.getElementById("errorPanel");
    const error = document.getElementById("error");
    const details = document.getElementById("details");
    document.getElementById("refresh").addEventListener("click", () => load(state.view));

    async function load(view) {
      state.view = view || "chat";
      errorPanel.hidden = true;
      meta.textContent = "Refreshing...";
      const params = new URLSearchParams(window.location.search);
      params.set("view", state.view);
      try {
        const apiURL = new URL(window.location.pathname.replace(/\/$/, "") + "/api/status", window.location.origin);
        apiURL.search = params.toString();
        const response = await fetch(apiURL.toString(), {
          headers: { "X-Telegram-Init-Data": tg ? tg.initData : "" },
        });
        if (!response.ok) {
          throw new Error(await response.text() || ("HTTP " + response.status));
        }
        const data = await response.json();
        renderTabs(data.views || [], data.active_view);
        renderStatus(data);
      } catch (err) {
        quickPanel.hidden = true;
        details.innerHTML = "";
        error.textContent = String(err.message || err).trim() || "Status unavailable.";
        errorPanel.hidden = false;
        meta.textContent = "Open /status from Telegram to refresh this console.";
      }
    }

    function renderTabs(views, active) {
      tabs.innerHTML = "";
      for (const item of views) {
        const button = document.createElement("button");
        button.type = "button";
        button.textContent = item.label || item.id;
        button.className = item.id === active ? "active" : "";
        button.addEventListener("click", () => load(item.id));
        tabs.appendChild(button);
      }
    }

    function renderStatus(data) {
      const text = data.status_text || "";
      const lines = text.split(/\n/).map((line) => line.trimEnd()).filter((line) => line.trim() !== "");
      const quickLineIndex = lines.findIndex((line) => /^quick read:/i.test(line) || /^quick_read\s+/i.test(line));
      if (quickLineIndex >= 0) {
        quick.textContent = lines[quickLineIndex].replace(/^quick read:\s*/i, "").replace(/^quick_read\s+/i, "");
        quickPanel.hidden = false;
        lines.splice(quickLineIndex, 1);
      } else {
        quickPanel.hidden = true;
      }
      details.innerHTML = "";
      for (const line of lines) {
        const node = document.createElement("div");
        node.className = line.endsWith(":") ? "line section" : "line";
        node.textContent = line;
        details.appendChild(node);
      }
      if (!lines.length) {
        const node = document.createElement("div");
        node.className = "empty";
        node.textContent = "No status lines returned.";
        details.appendChild(node);
      }
      const generated = data.generated_at ? new Date(data.generated_at).toLocaleString() : "unknown";
      meta.textContent = "Chat " + data.chat_id + " | " + (data.is_admin ? "admin" : "user") + " | " + generated;
    }

    load(state.view);
  </script>
</body>
</html>`
