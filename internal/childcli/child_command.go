//go:build linux

package childcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
)

type DurableAgentChildBootstrap struct {
	Config config.Config `json:"config"`
}

type DurableAgentChildDeps struct {
	RunTelegramGroupChild func(context.Context, config.Config, core.InboundMessage) (any, error)
	RunChildWake          func(context.Context, config.Config, string, time.Time) error
}

func RunDurableAgentChildCommand(args []string, deps DurableAgentChildDeps) error {
	fs := flag.NewFlagSet("durable-agent child-run", flag.ContinueOnError)
	bootstrapPath := fs.String("bootstrap", "", "path to durable child bootstrap json")
	messagePath := fs.String("message", "", "path to inbound message json")
	agentID := fs.String("agent", "", "durable agent id for non-interactive child wake")
	nowRaw := fs.String("now", "", "override wake timestamp (RFC3339 or RFC3339Nano)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bootstrapPath == "" {
		return fmt.Errorf("durable-agent child-run requires --bootstrap")
	}

	var bootstrap DurableAgentChildBootstrap
	if err := DecodeJSONFile(*bootstrapPath, &bootstrap); err != nil {
		return fmt.Errorf("load durable child bootstrap: %w", err)
	}

	if strings.TrimSpace(*messagePath) != "" {
		if deps.RunTelegramGroupChild == nil {
			return fmt.Errorf("durable-agent child-run message runner is unavailable")
		}
		var msg core.InboundMessage
		if err := DecodeJSONFile(*messagePath, &msg); err != nil {
			return fmt.Errorf("load durable child message: %w", err)
		}
		result, err := deps.RunTelegramGroupChild(context.Background(), bootstrap.Config, msg)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(result)
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent child-run requires --message or --agent")
	}
	if deps.RunChildWake == nil {
		return fmt.Errorf("durable-agent child-run wake runner is unavailable")
	}
	now, err := ParseDurableChildWakeTime(*nowRaw)
	if err != nil {
		return err
	}
	return deps.RunChildWake(context.Background(), bootstrap.Config, *agentID, now)
}

func DecodeJSONFile(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func ParseDurableChildWakeTime(raw string) (time.Time, error) {
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
