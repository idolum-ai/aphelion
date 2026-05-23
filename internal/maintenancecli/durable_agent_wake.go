//go:build linux

package maintenancecli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

type DurableAgentWakeRuntime interface {
	RunDurableAgentChildWake(context.Context, string, time.Time) error
}

type DurableAgentWakeRuntimeFactory func(*config.Config) (DurableAgentWakeRuntime, func(), error)

func RunDurableAgentWakeCommand(args []string, factory DurableAgentWakeRuntimeFactory) error {
	fs := flag.NewFlagSet("durable-agent wake", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	nowRaw := fs.String("now", "", "override wake timestamp (RFC3339 or RFC3339Nano)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent wake requires --agent")
	}
	now, err := parseDurableAgentWakeTime(*nowRaw)
	if err != nil {
		return err
	}
	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	if factory == nil {
		return fmt.Errorf("durable-agent wake runtime factory is unavailable")
	}
	rt, cleanup, err := factory(cfg)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	started := time.Now().UTC()
	if err := rt.RunDurableAgentChildWake(context.Background(), strings.TrimSpace(*agentID), now); err != nil {
		return err
	}
	completed := time.Now().UTC()
	fmt.Fprintf(os.Stdout, "action: durable-agent wake\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", strings.TrimSpace(*agentID))
	fmt.Fprintf(os.Stdout, "config: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "wake_time: %s\n", now.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(os.Stdout, "started_at: %s\n", started.Format(time.RFC3339Nano))
	fmt.Fprintf(os.Stdout, "completed_at: %s\n", completed.Format(time.RFC3339Nano))
	fmt.Fprintf(os.Stdout, "status: completed\n")
	return nil
}

func parseDurableAgentWakeTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now().UTC(), nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse durable child --now: %w", err)
	}
	return parsed.UTC(), nil
}
