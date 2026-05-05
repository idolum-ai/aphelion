//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestTelegramChildBotPreflightDoesNotReadTokenOrPoll(t *testing.T) {
	t.Parallel()

	fixture := writeTelegramChildBotFixture(t, "telegram_group", "active", 0o600)
	readCalled := false
	pollCalled := false
	out, err := captureStdout(t, func() error {
		return runTelegramChildBotCommandWithDeps([]string{"--config", fixture.configPath, "--agent", "synth", "--preflight"}, telegramChildBotDeps{
			Stat: os.Stat,
			ReadFile: func(string) ([]byte, error) {
				readCalled = true
				return nil, errors.New("token should not be read during preflight")
			},
			RunPoller: func(context.Context, *telegram.Client, core.DurableAgent, telegramChildBotRoute, *config.Config, *session.SQLiteStore) error {
				pollCalled = true
				return errors.New("poller should not run during preflight")
			},
		})
	})
	if err != nil {
		t.Fatalf("runTelegramChildBotCommandWithDeps() err = %v", err)
	}
	if readCalled {
		t.Fatal("preflight read token file; want metadata only")
	}
	if pollCalled {
		t.Fatal("preflight ran poller; want validation only")
	}
	for _, want := range []string{
		"action: telegram-child-bot preflight",
		"agent_id: synth",
		"chat_id: -5056905988",
		"respond_on: mentions",
		"token_file_status: metadata_ok",
		"durable_agent_status: active",
		"channel_kind: telegram_group",
		"live_policy_status: configured",
		"bootstrap_status: configured",
		"next_gate: get-me-smoke_requires_separate_live_approval",
		"status: ok",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("preflight output = %q, want substring %q", out, want)
		}
	}
	if strings.Contains(out, "123:SECRET") {
		t.Fatalf("preflight output leaked token: %q", out)
	}
}

func TestTelegramChildBotStatusDoesNotReadTokenOrPoll(t *testing.T) {
	t.Parallel()

	fixture := writeTelegramChildBotFixture(t, "telegram_group", "active", 0o600)
	readCalled := false
	pollCalled := false
	out, err := captureStdout(t, func() error {
		return runTelegramChildBotCommandWithDeps([]string{"--config", fixture.configPath, "--agent", "synth", "--status"}, telegramChildBotDeps{
			Stat: os.Stat,
			ReadFile: func(string) ([]byte, error) {
				readCalled = true
				return nil, errors.New("token should not be read during status")
			},
			RunPoller: func(context.Context, *telegram.Client, core.DurableAgent, telegramChildBotRoute, *config.Config, *session.SQLiteStore) error {
				pollCalled = true
				return errors.New("poller should not run during status")
			},
		})
	})
	if err != nil {
		t.Fatalf("runTelegramChildBotCommandWithDeps() err = %v", err)
	}
	if readCalled {
		t.Fatal("status read token file; want metadata only")
	}
	if pollCalled {
		t.Fatal("status ran poller; want validation only")
	}
	for _, want := range []string{
		"action: telegram-child-bot status",
		"agent_id: synth",
		"token_file_status: metadata_ok",
		"durable_agent_status: active",
		"channel_kind: telegram_group",
		"live_policy_status: configured",
		"bootstrap_status: configured",
		"next_gate: get-me-smoke_requires_separate_live_approval",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output = %q, want substring %q", out, want)
		}
	}
	if strings.Contains(out, "123:SECRET") || strings.Contains(out, fixture.tokenPath) {
		t.Fatalf("status output leaked token or token path: %q", out)
	}
}

func TestTelegramChildBotGetMeSmokeUsesTelegramIdentityOnly(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/botTOKEN/getMe" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return &http.Response{
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"ok":true,"result":{"id":42,"is_bot":true,"username":"synth_bot"}}`)),
		}, nil
	})
	client := telegram.NewClient("TOKEN",
		telegram.WithBaseURL("https://api.telegram.org/botTOKEN/"),
		telegram.WithHTTPClient(&http.Client{Transport: transport}),
	)
	out, err := captureStdout(t, func() error {
		return runTelegramChildBotGetMeSmoke(context.Background(), client, telegramChildBotRoute{AgentID: "synth", ChatID: -5056905988}, "/tmp/config.toml")
	})
	if err != nil {
		t.Fatalf("runTelegramChildBotGetMeSmoke() err = %v", err)
	}
	for _, want := range []string{"action: telegram-child-bot get-me-smoke", "agent_id: synth", "chat_id: -5056905988", "bot_username: synth_bot", "status: ok"} {
		if !strings.Contains(out, want) {
			t.Fatalf("get-me smoke output = %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "TOKEN") {
		t.Fatalf("get-me smoke output leaked token: %q", out)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSelectTelegramChildBotRoutePreservesConfigRespondOnAll(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Telegram.ChildBots = []config.TelegramChildBotConfig{{
		AgentID:   "synth",
		TokenFile: "/tmp/synth-token",
		ChatID:    -5056905988,
		RespondOn: "all",
		Enabled:   true,
	}}
	route, err := selectTelegramChildBotRoute(cfg, "synth", "", 0, "", 0)
	if err != nil {
		t.Fatalf("selectTelegramChildBotRoute() err = %v", err)
	}
	if route.RespondOn != "all" {
		t.Fatalf("RespondOn = %q, want all from config", route.RespondOn)
	}
}

func TestTelegramChildBotPreflightFailsClosedOnWeakTokenPermissions(t *testing.T) {
	t.Parallel()

	fixture := writeTelegramChildBotFixture(t, "telegram_group", "active", 0o644)
	_, err := captureStdout(t, func() error {
		return runTelegramChildBotCommandWithDeps([]string{"--config", fixture.configPath, "--agent", "synth", "--preflight"}, telegramChildBotDeps{Stat: os.Stat})
	})
	if err == nil || !strings.Contains(err.Error(), "permissions must not grant group/other access") {
		t.Fatalf("preflight err = %v, want fail-closed permission error", err)
	}
}

func TestTelegramChildBotPreflightFailsClosedOnWrongAgentState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		channelKind string
		status      string
		wantErr     string
	}{
		{name: "wrong channel", channelKind: "external_channel", status: "active", wantErr: "must have channel_kind telegram_group"},
		{name: "inactive", channelKind: "telegram_group", status: "inactive", wantErr: "is not active"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := writeTelegramChildBotFixture(t, tc.channelKind, tc.status, 0o600)
			_, err := captureStdout(t, func() error {
				return runTelegramChildBotCommandWithDeps([]string{"--config", fixture.configPath, "--agent", "synth", "--preflight"}, telegramChildBotDeps{Stat: os.Stat})
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("preflight err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestTelegramChildBotRunUsesFakePollerAfterReadingToken(t *testing.T) {
	t.Parallel()

	fixture := writeTelegramChildBotFixture(t, "telegram_group", "active", 0o600)
	readCalled := false
	pollCalled := false
	err := runTelegramChildBotCommandWithDeps([]string{"--config", fixture.configPath, "--agent", "synth"}, telegramChildBotDeps{
		Stat: os.Stat,
		ReadFile: func(path string) ([]byte, error) {
			readCalled = true
			if path != fixture.tokenPath {
				t.Fatalf("read token path = %q, want %q", path, fixture.tokenPath)
			}
			return []byte("123:SECRET\n"), nil
		},
		RunPoller: func(_ context.Context, _ *telegram.Client, agentRow core.DurableAgent, route telegramChildBotRoute, _ *config.Config, _ *session.SQLiteStore) error {
			pollCalled = true
			if agentRow.AgentID != "synth" || route.AgentID != "synth" || route.ChatID != -5056905988 || route.RespondOn != "mentions" {
				t.Fatalf("agent/route = %#v / %#v, want synth route", agentRow, route)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runTelegramChildBotCommandWithDeps() err = %v", err)
	}
	if !readCalled {
		t.Fatal("run did not read token before fake poller")
	}
	if !pollCalled {
		t.Fatal("run did not invoke fake poller")
	}
}

type telegramChildBotFixture struct {
	configPath string
	tokenPath  string
}

func writeTelegramChildBotFixture(t *testing.T, channelKind string, status string, tokenMode os.FileMode) telegramChildBotFixture {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "synth-token")
	if err := os.WriteFile(tokenPath, []byte("123:SECRET\n"), tokenMode); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if err := os.Chmod(tokenPath, tokenMode); err != nil {
		t.Fatalf("chmod token: %v", err)
	}
	dbPath := filepath.Join(dir, "sessions.db")
	configPath := filepath.Join(dir, "aphelion.toml")
	raw := `[telegram]
bot_token = "main-bot-token"

[[telegram.child_bots]]
agent_id = "synth"
token_file = "` + strings.ReplaceAll(tokenPath, `\`, `\\`) + `"
chat_id = -5056905988
respond_on = "mentions"
enabled = true

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"

[sessions]
db_path = "` + strings.ReplaceAll(dbPath, `\`, `\\`) + `"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "synth",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 123,
		ChannelKind:        channelKind,
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Help Mada with job-search intake only inside approved gates.",
			OutboundMode: "reply_with_policy_authorization",
			DriftPolicy:  "admin_review",
		},
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:         "codex",
			CodexAuthSource: "inherit",
			CodexHome:       filepath.Join(dir, "codex-home"),
		},
		Status: status,
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	return telegramChildBotFixture{configPath: configPath, tokenPath: tokenPath}
}
