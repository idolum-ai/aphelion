//go:build linux

package childcli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type TelegramChildBotRoute struct {
	AgentID            string
	TokenFile          string
	ChatID             int64
	RespondOn          string
	ReviewTargetChatID int64
	Enabled            bool
	NoSend             bool
}

type TelegramChildBotDeps struct {
	Stat        func(string) (os.FileInfo, error)
	ReadFile    func(string) ([]byte, error)
	RunPoller   func(context.Context, *telegram.Client, core.DurableAgent, TelegramChildBotRoute, *config.Config, *session.SQLiteStore) error
	RunDryStart func(context.Context, *telegram.Client, core.DurableAgent, TelegramChildBotRoute, *config.Config, *session.SQLiteStore) error
}

func RunTelegramChildBotCommandWithDeps(args []string, deps TelegramChildBotDeps) error {
	if deps.Stat == nil {
		deps.Stat = os.Stat
	}
	if deps.ReadFile == nil {
		deps.ReadFile = os.ReadFile
	}

	fs := flag.NewFlagSet("telegram-child-bot", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentFlag := fs.String("agent", "", "durable telegram_group agent id")
	tokenFileFlag := fs.String("token-file", "", "path to the child bot token file")
	chatIDFlag := fs.Int64("chat-id", 0, "single admitted Telegram group/supergroup chat id")
	respondOnFlag := fs.String("respond-on", "", "group admission mode: mentions or all")
	reviewTargetFlag := fs.Int64("review-target-chat-id", 0, "parent review Telegram chat id")
	preflight := fs.Bool("preflight", false, "validate config/token metadata/durable agent and exit without reading token or calling Telegram")
	statusOnly := fs.Bool("status", false, "print child bot health/status without reading token or calling Telegram")
	dryStart := fs.Bool("dry-start", false, "construct the runner in no-send mode and exit without polling or calling Telegram")
	noSend := fs.Bool("no-send", false, "suppress Telegram outbound replies when processing admitted updates")
	getMeSmoke := fs.Bool("get-me-smoke", false, "read token and call Telegram getMe once, then exit without polling or sending messages")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	route, err := SelectTelegramChildBotRoute(cfg, *agentFlag, *tokenFileFlag, *chatIDFlag, *respondOnFlag, *reviewTargetFlag)
	if err != nil {
		return err
	}
	if *noSend || *dryStart {
		route.NoSend = true
	}
	if err := ValidateTelegramChildBotTokenMetadata(route.TokenFile, deps.Stat); err != nil {
		return err
	}

	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	agentRow, err := LoadTelegramChildBotAgent(store, route.AgentID)
	if err != nil {
		return err
	}
	if route.ReviewTargetChatID == 0 {
		route.ReviewTargetChatID = agentRow.ReviewTargetChatID
	}
	if route.ReviewTargetChatID == 0 {
		return fmt.Errorf("telegram child bot %q requires a review target chat id", route.AgentID)
	}
	if err := ValidateTelegramChildBotRouteAgainstAgent(route, agentRow); err != nil {
		return err
	}

	if *preflight || *statusOnly {
		action := "telegram-child-bot preflight"
		if *statusOnly {
			action = "telegram-child-bot status"
		}
		PrintTelegramChildBotHealthStatus(os.Stdout, action, configPath, route, agentRow)
		return nil
	}
	if *dryStart {
		if deps.RunDryStart == nil {
			return fmt.Errorf("telegram child bot dry-start runner is unavailable")
		}
		return deps.RunDryStart(context.Background(), nil, agentRow, route, cfg, store)
	}

	token, err := ReadTelegramChildBotToken(route.TokenFile, deps.ReadFile)
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 90 * time.Second}
	client := telegram.NewClient(token, telegram.WithHTTPClient(httpClient), telegram.WithPollTimeout(cfg.Telegram.PollTimeout))
	if *getMeSmoke {
		return RunTelegramChildBotGetMeSmoke(context.Background(), client, route, configPath)
	}
	if deps.RunPoller == nil {
		return fmt.Errorf("telegram child bot poller is unavailable")
	}
	return deps.RunPoller(context.Background(), client, agentRow, route, cfg, store)
}

func SelectTelegramChildBotRoute(cfg *config.Config, agentID string, tokenFile string, chatID int64, respondOn string, reviewTarget int64) (TelegramChildBotRoute, error) {
	if cfg == nil {
		return TelegramChildBotRoute{}, fmt.Errorf("config is nil")
	}
	agentID = strings.TrimSpace(agentID)
	tokenFile = strings.TrimSpace(tokenFile)
	respondOn = strings.TrimSpace(respondOn)

	var selected *config.TelegramChildBotConfig
	if agentID != "" {
		for i := range cfg.Telegram.ChildBots {
			if cfg.Telegram.ChildBots[i].AgentID == agentID {
				selected = &cfg.Telegram.ChildBots[i]
				break
			}
		}
	} else {
		for i := range cfg.Telegram.ChildBots {
			if cfg.Telegram.ChildBots[i].Enabled {
				if selected != nil {
					return TelegramChildBotRoute{}, fmt.Errorf("telegram child bot --agent is required when multiple child bots are enabled")
				}
				selected = &cfg.Telegram.ChildBots[i]
			}
		}
	}

	route := TelegramChildBotRoute{}
	if selected != nil {
		route = TelegramChildBotRoute{
			AgentID:            selected.AgentID,
			TokenFile:          selected.TokenFile,
			ChatID:             selected.ChatID,
			RespondOn:          NormalizeChildBotRespondOn(selected.RespondOn),
			ReviewTargetChatID: selected.ReviewTargetChatID,
			Enabled:            selected.Enabled,
		}
	} else if agentID != "" && tokenFile != "" && chatID != 0 {
		route = TelegramChildBotRoute{AgentID: agentID, TokenFile: tokenFile, ChatID: chatID, RespondOn: "mentions", ReviewTargetChatID: reviewTarget, Enabled: true}
	} else {
		return TelegramChildBotRoute{}, fmt.Errorf("telegram child bot route is required; configure [[telegram.child_bots]] or pass --agent, --token-file, and --chat-id")
	}

	if agentID != "" {
		route.AgentID = agentID
	}
	if tokenFile != "" {
		route.TokenFile = tokenFile
	}
	if chatID != 0 {
		route.ChatID = chatID
	}
	if respondOn != "" {
		route.RespondOn = NormalizeChildBotRespondOn(respondOn)
	}
	if reviewTarget != 0 {
		route.ReviewTargetChatID = reviewTarget
	}
	if route.RespondOn == "" {
		route.RespondOn = "mentions"
	}
	switch route.RespondOn {
	case "all", "mentions":
	default:
		return TelegramChildBotRoute{}, fmt.Errorf("telegram child bot respond_on must be one of all|mentions")
	}
	if !route.Enabled && selected != nil {
		return TelegramChildBotRoute{}, fmt.Errorf("telegram child bot %q is not enabled", route.AgentID)
	}
	if strings.TrimSpace(route.AgentID) == "" || strings.TrimSpace(route.TokenFile) == "" || route.ChatID == 0 {
		return TelegramChildBotRoute{}, fmt.Errorf("telegram child bot route requires agent_id, token_file, and chat_id")
	}
	return route, nil
}

func NormalizeChildBotRespondOn(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "mentions":
		return "mentions"
	case "all":
		return "all"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func ValidateTelegramChildBotTokenMetadata(path string, stat func(string) (os.FileInfo, error)) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("telegram child bot token_file is required")
	}
	if stat == nil {
		stat = os.Stat
	}
	info, err := stat(path)
	if err != nil {
		return fmt.Errorf("telegram child bot token_file metadata check failed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("telegram child bot token_file must be a regular file")
	}
	if info.Size() <= 0 {
		return fmt.Errorf("telegram child bot token_file must not be empty")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("telegram child bot token_file permissions must not grant group/other access")
	}
	return nil
}

func ReadTelegramChildBotToken(path string, readFile func(string) ([]byte, error)) (string, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	raw, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("read telegram child bot token_file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("telegram child bot token_file must not be empty")
	}
	return token, nil
}

func LoadTelegramChildBotAgent(store *session.SQLiteStore, agentID string) (core.DurableAgent, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return core.DurableAgent{}, fmt.Errorf("telegram child bot agent id is required")
	}
	agentRow, err := store.DurableAgent(agentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.DurableAgent{}, fmt.Errorf("durable agent %q not found", agentID)
		}
		return core.DurableAgent{}, fmt.Errorf("load durable agent %q: %w", agentID, err)
	}
	if agentRow == nil {
		return core.DurableAgent{}, fmt.Errorf("durable agent %q not found", agentID)
	}
	return *agentRow, nil
}

func ValidateTelegramChildBotRouteAgainstAgent(route TelegramChildBotRoute, agentRow core.DurableAgent) error {
	if strings.TrimSpace(agentRow.AgentID) != strings.TrimSpace(route.AgentID) {
		return fmt.Errorf("telegram child bot route agent %q does not match durable agent %q", route.AgentID, agentRow.AgentID)
	}
	if strings.TrimSpace(agentRow.ChannelKind) != "telegram_group" {
		return fmt.Errorf("telegram child bot agent %q must have channel_kind telegram_group", route.AgentID)
	}
	if status := strings.ToLower(strings.TrimSpace(agentRow.Status)); status != "" && status != "active" {
		return fmt.Errorf("telegram child bot agent %q is not active", route.AgentID)
	}
	bootstrap := core.NormalizeNodeLLMBootstrap(agentRow.BootstrapLLM)
	if !bootstrap.Configured() {
		return fmt.Errorf("telegram child bot agent %q requires configured child-local bootstrap", route.AgentID)
	}
	if strings.TrimSpace(agentRow.LivePolicy.Charter) == "" {
		return fmt.Errorf("telegram child bot agent %q requires a live policy charter", route.AgentID)
	}
	return nil
}

type TelegramChildBotHealthStatus struct {
	Action             string
	ConfigPath         string
	AgentID            string
	ChatID             int64
	RespondOn          string
	ReviewTargetChatID int64
	TokenFileStatus    string
	DurableAgentStatus string
	ChannelKind        string
	LivePolicyStatus   string
	BootstrapStatus    string
	NextGate           string
}

func BuildTelegramChildBotHealthStatus(action string, configPath string, route TelegramChildBotRoute, agentRow core.DurableAgent) TelegramChildBotHealthStatus {
	policyStatus := "missing"
	if strings.TrimSpace(agentRow.LivePolicy.Charter) != "" {
		policyStatus = "configured"
	}
	bootstrapStatus := "missing"
	if core.NormalizeNodeLLMBootstrap(agentRow.BootstrapLLM).Configured() {
		bootstrapStatus = "configured"
	}
	agentStatus := strings.TrimSpace(agentRow.Status)
	if agentStatus == "" {
		agentStatus = "active"
	}
	return TelegramChildBotHealthStatus{
		Action:             strings.TrimSpace(action),
		ConfigPath:         strings.TrimSpace(configPath),
		AgentID:            strings.TrimSpace(route.AgentID),
		ChatID:             route.ChatID,
		RespondOn:          strings.TrimSpace(route.RespondOn),
		ReviewTargetChatID: route.ReviewTargetChatID,
		TokenFileStatus:    "metadata_ok",
		DurableAgentStatus: agentStatus,
		ChannelKind:        strings.TrimSpace(agentRow.ChannelKind),
		LivePolicyStatus:   policyStatus,
		BootstrapStatus:    bootstrapStatus,
		NextGate:           "get-me-smoke_requires_separate_live_approval",
	}
}

func PrintTelegramChildBotHealthStatus(w io.Writer, action string, configPath string, route TelegramChildBotRoute, agentRow core.DurableAgent) {
	health := BuildTelegramChildBotHealthStatus(action, configPath, route, agentRow)
	fmt.Fprintf(w, "action: %s\n", health.Action)
	fmt.Fprintf(w, "config: %s\n", health.ConfigPath)
	fmt.Fprintf(w, "agent_id: %s\n", health.AgentID)
	fmt.Fprintf(w, "chat_id: %d\n", health.ChatID)
	fmt.Fprintf(w, "respond_on: %s\n", health.RespondOn)
	fmt.Fprintf(w, "review_target_chat_id: %d\n", health.ReviewTargetChatID)
	fmt.Fprintf(w, "token_file_status: %s\n", health.TokenFileStatus)
	fmt.Fprintf(w, "durable_agent_status: %s\n", health.DurableAgentStatus)
	fmt.Fprintf(w, "channel_kind: %s\n", health.ChannelKind)
	fmt.Fprintf(w, "live_policy_status: %s\n", health.LivePolicyStatus)
	fmt.Fprintf(w, "bootstrap_status: %s\n", health.BootstrapStatus)
	fmt.Fprintf(w, "next_gate: %s\n", health.NextGate)
	fmt.Fprintf(w, "status: ok\n")
}

func RunTelegramChildBotGetMeSmoke(ctx context.Context, client *telegram.Client, route TelegramChildBotRoute, configPath string) error {
	if client == nil {
		return fmt.Errorf("telegram client is unavailable")
	}
	getMeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	botUser, err := client.GetMe(getMeCtx)
	if err != nil {
		return fmt.Errorf("telegram child bot getMe failed: %w", err)
	}
	username := ""
	if botUser != nil {
		username = strings.TrimSpace(botUser.Username)
	}
	fmt.Fprintf(os.Stdout, "action: telegram-child-bot get-me-smoke\n")
	fmt.Fprintf(os.Stdout, "config: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", route.AgentID)
	fmt.Fprintf(os.Stdout, "chat_id: %d\n", route.ChatID)
	fmt.Fprintf(os.Stdout, "bot_username: %s\n", username)
	fmt.Fprintf(os.Stdout, "status: ok\n")
	return nil
}

type ConfigStartupError struct {
	Path string
	Err  error
}

func (e *ConfigStartupError) Error() string {
	return fmt.Sprintf("config %s: %v (run 'aphelion --config %s --check-config' to validate)", e.Path, e.Err, e.Path)
}

func (e *ConfigStartupError) Unwrap() error { return e.Err }

func (e *ConfigStartupError) IsConfigStartupError() {}

func loadConfigForCommand(override string) (*config.Config, string, error) {
	configPath, err := config.ResolveConfigPath(override)
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, "", &ConfigStartupError{Path: configPath, Err: err}
	}
	return cfg, configPath, nil
}
