//go:build linux

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	runtimepkg "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

type durableChildNoopOutbound struct{}

func (durableChildNoopOutbound) SendMessage(context.Context, core.OutboundMessage) (int64, error) {
	return 0, fmt.Errorf("outbound delivery is unavailable in durable child mode")
}

func runDurableTelegramGroupChildBootstrap(ctx context.Context, bootstrap runtimepkg.DurableAgentChildBootstrap, msg core.InboundMessage) (*runtimepkg.DurableGroupChildResult, error) {
	cfg := &bootstrap.Config
	if err := prepareFilesystem(cfg); err != nil {
		return nil, err
	}
	if _, err := seedAgentPromptFiles(cfg); err != nil {
		return nil, err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	httpClient := &http.Client{Timeout: 90 * time.Second}
	var llm agent.Provider
	if strings.EqualFold(strings.TrimSpace(cfg.Governor.Backend), "native") {
		nativeProvider, err := buildNativeProviderChain(cfg, httpClient)
		if err != nil {
			return nil, err
		}
		if nativeProvider == nil {
			return nil, fmt.Errorf("durable child bootstrap does not define a usable native provider")
		}
		llm = nativeProvider
	}

	rt, err := runtimepkg.New(cfg, store, llm, nil, durableChildNoopOutbound{})
	if err != nil {
		return nil, err
	}

	result, err := rt.RunDurableTelegramGroupChild(context.Background(), msg)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func runDurableAgentChildCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent child-run", flag.ContinueOnError)
	bootstrapPath := fs.String("bootstrap", "", "path to durable child bootstrap json")
	messagePath := fs.String("message", "", "path to inbound message json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bootstrapPath == "" {
		return fmt.Errorf("durable-agent child-run requires --bootstrap")
	}
	if *messagePath == "" {
		return fmt.Errorf("durable-agent child-run requires --message")
	}

	var bootstrap runtimepkg.DurableAgentChildBootstrap
	if err := decodeJSONFile(*bootstrapPath, &bootstrap); err != nil {
		return fmt.Errorf("load durable child bootstrap: %w", err)
	}
	var msg core.InboundMessage
	if err := decodeJSONFile(*messagePath, &msg); err != nil {
		return fmt.Errorf("load durable child message: %w", err)
	}

	result, err := runDurableTelegramGroupChildBootstrap(context.Background(), bootstrap, msg)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(result)
}

func decodeJSONFile(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
