//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/openai"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/voice"
)

const turnTimeout = 10 * time.Minute

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
	llm, err := provider.NewAnthropic(provider.AnthropicOptions{
		APIKey:     cfg.Providers.Anthropic.APIKey,
		Model:      cfg.Providers.Anthropic.Model,
		MaxTokens:  cfg.Providers.Anthropic.MaxTokens,
		HTTPClient: httpClient,
		UserAgent:  cfg.Identity.UserAgent,
	})
	if err != nil {
		return err
	}

	sandboxRoots, err := sandbox.DefaultRoots(cfg.Agent.Workspace, cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	sandboxResolver, err := sandbox.NewResolver(sandboxRoots, sandbox.DefaultProfiles())
	if err != nil {
		return err
	}
	tools := tool.NewRegistryWithSandbox(cfg.Agent.Workspace, time.Duration(cfg.Agent.ToolTimeout)*time.Second, sandboxResolver)
	principalResolver := principal.NewResolver(
		cfg.Principals.Telegram.AdminUserIDs,
		cfg.Principals.Telegram.ApprovedUserIDs,
	)
	tgClient := telegram.NewClient(
		cfg.Telegram.BotToken,
		telegram.WithHTTPClient(httpClient),
		telegram.WithPollTimeout(cfg.Telegram.PollTimeout),
	)

	rt, err := runtime.New(cfg, store, llm, tools, tgClient)
	if err != nil {
		return err
	}

	if cfg.Voice.Mode != "" && cfg.Voice.Mode != "off" {
		openaiClient, err := openai.NewClient(openai.ClientOptions{
			APIKey:     cfg.Voice.OpenAIAPIKey,
			BaseURL:    cfg.Voice.OpenAIBaseURL,
			HTTPClient: httpClient,
			UserAgent:  cfg.Identity.UserAgent,
		})
		if err != nil {
			return err
		}
		transcriber, err := openai.NewTranscriptionClient(openaiClient, openai.TranscriptionOptions{
			Model: cfg.Voice.OpenAIModel,
		})
		if err != nil {
			return err
		}
		synth, err := voice.NewElevenLabs(voice.ElevenLabsOptions{
			APIKey:     cfg.Voice.ElevenLabsAPIKey,
			BaseURL:    cfg.Voice.ElevenLabsBaseURL,
			VoiceID:    cfg.Voice.ElevenLabsVoiceID,
			ModelID:    cfg.Voice.ElevenLabsModelID,
			HTTPClient: httpClient,
		})
		if err != nil {
			return err
		}
		rt.ConfigureVoice(cfg.Voice, transcriber, synth)
	}

	router := core.NewRouter(rt.AgentFunc())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	rt.StartIdleExpiryLoop(ctx, log.Printf)
	rt.StartHeartbeatLoop(ctx, log.Printf)
	rt.StartCronLoop(ctx, log.Printf)

	poller := telegram.NewPoller(tgClient, func(parent context.Context, msg core.InboundMessage) error {
		turnCtx, cancel := context.WithTimeout(parent, turnTimeout)
		go func() {
			defer cancel()
			router.Route(turnCtx, msg)
		}()
		return nil
	},
		telegram.WithPollerTimeout(cfg.Telegram.PollTimeout),
		telegram.WithPrincipalResolver(principalResolver),
	)

	log.Printf("INFO aphelion started workspace=%s db_path=%s model=%s", cfg.Agent.Workspace, cfg.Sessions.DBPath, cfg.Providers.Anthropic.Model)
	return poller.Run(ctx)
}

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "aphelion", "config.toml")
}
