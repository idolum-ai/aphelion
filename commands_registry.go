//go:build linux

package main

import (
	"context"
	"fmt"

	"github.com/idolum-ai/aphelion/telegram"
)

var defaultTelegramCommands = []telegram.BotCommand{
	{Command: "start", Description: "Show intro and command help"},
	{Command: "help", Description: "Show available commands"},
	{Command: "status", Description: "Show live status and controls"},
	{Command: "debug", Description: "Show a detailed debug snapshot"},
	{Command: "doctor", Description: "Run an admin runtime diagnosis"},
	{Command: "tailnet", Description: "Show tailnet status and controls"},
	{Command: "agents", Description: "List durable agents and controls"},
	{Command: "memory", Description: "Review memory and set focus"},
	{Command: "mission", Description: "Show and manage the Mission Ledger"},
	{Command: "model", Description: "Show and change model slots"},
	{Command: "autonomy", Description: "Show autonomy policy"},
	{Command: "autoapprove", Description: "Temporarily auto-approve admin approval prompts"},
	{Command: "stop", Description: "Stop current work in this chat"},
	{Command: "new", Description: "Start a fresh chat session context"},
	{Command: "detach", Description: "Detach from pending work in this chat"},
	{Command: "restart", Description: "Force an immediate gateway restart"},
	{Command: "reinstall", Description: "Queue a rebuild/reinstall/restart request"},
	{Command: "set_persona_model", Description: "Choose persona model"},
	{Command: "set_governor_effort", Description: "Choose system reasoning effort"},
}

func registerTelegramCommands(ctx context.Context, client *telegram.Client) error {
	if client == nil {
		return fmt.Errorf("telegram client is required")
	}
	return client.SetMyCommands(ctx, defaultTelegramCommands)
}
