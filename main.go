//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentpkg "github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	providerpkg "github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

const turnTimeout = 10 * time.Minute

var bootstrapFiles = []string{
	"SOUL.md",
	"IDENTITY.md",
	"USER.md",
	"AGENTS.md",
	"TOOLS.md",
}

func main() {
	if err := run(); err != nil {
		log.Printf("ERROR aphelion exited with error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", defaultConfigPath(), "path to config.toml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Sessions.DBPath), 0o700); err != nil {
		return fmt.Errorf("create sessions directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Agent.Workspace, 0o755); err != nil {
		return fmt.Errorf("create workspace directory: %w", err)
	}

	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	httpClient := &http.Client{Timeout: 90 * time.Second}
	model, err := providerpkg.NewAnthropic(providerpkg.AnthropicOptions{
		APIKey:     cfg.Providers.Anthropic.APIKey,
		Model:      cfg.Providers.Anthropic.Model,
		MaxTokens:  cfg.Providers.Anthropic.MaxTokens,
		HTTPClient: httpClient,
		UserAgent:  cfg.Identity.UserAgent,
	})
	if err != nil {
		return err
	}

	tools := toolpkg.NewRegistry(cfg.Agent.Workspace, time.Duration(cfg.Agent.ToolTimeout)*time.Second)
	systemPrompt := loadSystemPrompt(cfg.Agent.Workspace)

	tgClient := telegram.NewClient(
		cfg.Telegram.BotToken,
		telegram.WithHTTPClient(httpClient),
		telegram.WithPollTimeout(cfg.Telegram.PollTimeout),
	)

	router := core.NewRouter(func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		return handleTurn(ctx, cfg, store, model, tools, tgClient, systemPrompt, msg)
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	poller := telegram.NewPoller(tgClient, func(parent context.Context, msg core.InboundMessage) error {
		turnCtx, cancel := context.WithTimeout(parent, turnTimeout)
		go func() {
			defer cancel()
			router.Route(turnCtx, msg)
		}()
		return nil
	}, telegram.WithPollerTimeout(cfg.Telegram.PollTimeout))

	log.Printf("INFO aphelion started workspace=%s db_path=%s model=%s", cfg.Agent.Workspace, cfg.Sessions.DBPath, cfg.Providers.Anthropic.Model)
	return poller.Run(ctx)
}

func handleTurn(
	ctx context.Context,
	cfg *config.Config,
	store *session.SQLiteStore,
	model agentpkg.Provider,
	tools agentpkg.ToolRegistry,
	tgClient *telegram.Client,
	systemPrompt string,
	msg core.InboundMessage,
) (*core.TurnResult, error) {
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	sess.ChatType = "dm"
	sess.UserName = msg.SenderName
	sess.SystemPrompt = systemPrompt

	history, err := toAgentHistory(sess.Messages)
	if err != nil {
		return nil, fmt.Errorf("assemble history: %w", err)
	}

	userText := strings.TrimSpace(msg.Text)
	if userText == "" {
		userText = "[empty message]"
	}

	input := make([]agentpkg.Message, 0, len(history)+2)
	if systemPrompt != "" {
		input = append(input, agentpkg.Message{Role: "system", Content: systemPrompt})
	}
	input = append(input, history...)
	input = append(input, agentpkg.Message{Role: "user", Content: userText})

	result, outHistory, err := agentpkg.RunTurn(ctx, model, tools, &agentpkg.Budget{
		Max:     cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, input)
	if err != nil {
		return nil, fmt.Errorf("run turn: %w", err)
	}

	sess.TurnCount++
	newMessages, err := toSessionMessages(userText, outHistory[len(input):], sess.TurnCount)
	if err != nil {
		return nil, fmt.Errorf("convert new messages: %w", err)
	}

	if err := store.Save(sess, newMessages, result.TokenUsage); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	replyText := strings.TrimSpace(result.Text)
	if replyText == "" {
		replyText = "(no response)"
	}

	_, sendErr := tgClient.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    replyText,
		ReplyTo: &msg.MessageID,
	})
	if sendErr != nil {
		return result, fmt.Errorf("send telegram reply: %w", sendErr)
	}

	return result, nil
}

func toAgentHistory(messages []session.Message) ([]agentpkg.Message, error) {
	out := make([]agentpkg.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Compacted {
			continue
		}

		entry := agentpkg.Message{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolID,
		}
		if strings.TrimSpace(msg.ToolCalls) != "" {
			if err := json.Unmarshal([]byte(msg.ToolCalls), &entry.ToolCalls); err != nil {
				return nil, fmt.Errorf("decode tool calls for message %d: %w", msg.ID, err)
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

func toSessionMessages(userText string, generated []agentpkg.Message, turnIndex int) ([]session.Message, error) {
	out := []session.Message{{
		Role:         "user",
		Content:      userText,
		ContentChars: len(userText),
		TurnIndex:    turnIndex,
	}}

	for _, msg := range generated {
		entry := session.Message{
			Role:         msg.Role,
			Content:      msg.Content,
			ContentChars: len(msg.Content),
			TurnIndex:    turnIndex,
			ToolID:       msg.ToolCallID,
		}

		if len(msg.ToolCalls) > 0 {
			raw, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return nil, fmt.Errorf("encode tool calls: %w", err)
			}
			entry.ToolCalls = string(raw)
		}

		if msg.Role == "tool" {
			entry.ToolName = toolNameFromContent(msg.Content)
		}

		out = append(out, entry)
	}

	return out, nil
}

func toolNameFromContent(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return "exec"
}

func loadSystemPrompt(workspace string) string {
	parts := []string{
		fmt.Sprintf(
			"You are a Linux personal assistant operating inside the workspace %q. Use the exec tool whenever shell interaction is useful. Inspect before changing, prefer concise answers, and only claim work you actually completed.",
			workspace,
		),
	}

	for _, name := range bootstrapFiles {
		path := filepath.Join(workspace, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			log.Printf("WARN failed to read bootstrap file path=%s err=%v", path, err)
			continue
		}
		text := strings.TrimSpace(string(raw))
		if text != "" {
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n\n")
}

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "aphelion", "config.toml")
}
