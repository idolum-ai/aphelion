//go:build linux

package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestBuildTelegramStatusMiniAppURLSignsState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	raw := buildTelegramStatusMiniAppURL("https://status.example.test/telegram/status-app?source=status", "123:secret", 7, 99, now)
	if raw == "" {
		t.Fatal("buildTelegramStatusMiniAppURL() = empty, want signed URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	if u.Query().Get("source") != "status" {
		t.Fatalf("source query = %q, want preserved", u.Query().Get("source"))
	}
	chatID, senderID, err := validateTelegramStatusMiniAppState(u.Query(), "123:secret", time.Hour, now)
	if err != nil {
		t.Fatalf("validateTelegramStatusMiniAppState() err = %v", err)
	}
	if chatID != 7 || senderID != 99 {
		t.Fatalf("validated state = (%d,%d), want (7,99)", chatID, senderID)
	}

	tampered := u.Query()
	tampered.Set("chat_id", "8")
	if _, _, err := validateTelegramStatusMiniAppState(tampered, "123:secret", time.Hour, now); err == nil {
		t.Fatal("validateTelegramStatusMiniAppState(tampered) err = nil, want signature failure")
	}
}

func TestValidateTelegramMiniAppInitData(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	raw := signedTelegramMiniAppInitData("123:secret", 99, now)
	auth, err := validateTelegramMiniAppInitData(raw, "123:secret", time.Hour, now)
	if err != nil {
		t.Fatalf("validateTelegramMiniAppInitData() err = %v", err)
	}
	if auth.UserID != 99 {
		t.Fatalf("auth user id = %d, want 99", auth.UserID)
	}

	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse init data: %v", err)
	}
	values.Set("user", `{"id":100}`)
	if _, err := validateTelegramMiniAppInitData(values.Encode(), "123:secret", time.Hour, now); err == nil {
		t.Fatal("validateTelegramMiniAppInitData(tampered) err = nil, want hash failure")
	}
}

func TestTelegramStatusMiniAppHandlerServesSignedStatus(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	router := &stubCommandRouter{
		canRestart: true,
		statusChat: core.ChatStatusSnapshot{
			ChatID:     7,
			QueueDepth: 1,
			PendingItems: []core.PendingItem{
				{Kind: core.PendingItemKindQueue, ChatID: 7, Summary: "queued follow-up"},
			},
		},
		statusSystem:  core.SystemStatusSnapshot{ActiveTurnCount: 2},
		personaEffort: "gpt-5.5",
	}
	handler := &telegramStatusMiniAppHandler{
		router:     router,
		botToken:   "123:secret",
		authMaxAge: time.Hour,
		now: func() time.Time {
			return now
		},
	}
	signedURL := buildTelegramStatusMiniAppURL("https://status.example.test/telegram/status-app", "123:secret", 7, 99, now)
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, telegramMiniAppStatusAPIPath+"?"+parsed.RawQuery, nil)
	req.Header.Set(telegramMiniAppInitDataHeader, signedTelegramMiniAppInitData("123:secret", 99, now))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var response telegramMiniAppStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ChatID != 7 || response.SenderID != 99 || !response.IsAdmin {
		t.Fatalf("response identity = %#v, want chat 7 sender 99 admin", response)
	}
	if !strings.Contains(response.StatusText, "Status Scope: chat") {
		t.Fatalf("status text = %q, want chat status", response.StatusText)
	}
	foundSystem := false
	for _, view := range response.Views {
		if view.ID == string(statusViewSystem) {
			foundSystem = true
			break
		}
	}
	if !foundSystem {
		t.Fatalf("views = %#v, want admin system view", response.Views)
	}
}

func TestTelegramStatusMiniAppHandlerRejectsSenderMismatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	handler := &telegramStatusMiniAppHandler{
		router:     &stubCommandRouter{},
		botToken:   "123:secret",
		authMaxAge: time.Hour,
		now: func() time.Time {
			return now
		},
	}
	signedURL := buildTelegramStatusMiniAppURL("https://status.example.test/telegram/status-app", "123:secret", 7, 99, now)
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, telegramMiniAppStatusAPIPath+"?"+parsed.RawQuery, nil)
	req.Header.Set(telegramMiniAppInitDataHeader, signedTelegramMiniAppInitData("123:secret", 100, now))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status code = %d body=%q, want 403", rec.Code, rec.Body.String())
	}
}

func signedTelegramMiniAppInitData(botToken string, userID int64, authDate time.Time) string {
	values := url.Values{}
	values.Set("auth_date", strconvFormatUnix(authDate))
	values.Set("query_id", "test-query")
	values.Set("user", fmt.Sprintf(`{"id":%d,"first_name":"Test"}`, userID))
	dataCheckString := telegramMiniAppDataCheckString(values)
	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(botToken))
	dataMAC := hmac.New(sha256.New, secretMAC.Sum(nil))
	_, _ = dataMAC.Write([]byte(dataCheckString))
	values.Set("hash", hex.EncodeToString(dataMAC.Sum(nil)))
	return values.Encode()
}

func strconvFormatUnix(t time.Time) string {
	return fmt.Sprintf("%d", t.UTC().Unix())
}
