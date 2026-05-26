//go:build linux

package config

import (
	"fmt"
	"strings"
	"time"
)

func validateTelegramConfig(cfg *Config) error {
	if strings.TrimSpace(cfg.Telegram.BotToken) == "" {
		return fmt.Errorf("telegram.bot_token is required")
	}
	if cfg.Telegram.PollTimeout <= 0 {
		return fmt.Errorf("telegram.poll_timeout must be > 0")
	}
	if raw := strings.TrimSpace(cfg.Telegram.StreamEditInterval); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("telegram.stream_edit_interval must be a valid duration: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("telegram.stream_edit_interval must be > 0")
		}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Telegram.ToolProgress)) {
	case "", "all", "new", "off":
	default:
		return fmt.Errorf("telegram.tool_progress must be one of all|new|off")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Telegram.ToolProgressStyle)) {
	case "", "semantic", "raw":
	default:
		return fmt.Errorf("telegram.tool_progress_style must be one of semantic|raw")
	}
	if cfg.Telegram.ToolProgressWindow <= 0 {
		return fmt.Errorf("telegram.tool_progress_window must be > 0")
	}
	if _, err := ParseByteSize(strings.TrimSpace(cfg.Telegram.Media.DownloadMaxSize)); err != nil {
		return fmt.Errorf("telegram.media.download_max_size must be a valid positive size: %w", err)
	}
	if _, err := ParseByteSize(strings.TrimSpace(cfg.Telegram.Media.MaxPDFBytes)); err != nil {
		return fmt.Errorf("telegram.media.max_pdf_bytes must be a valid positive size: %w", err)
	}
	if err := validateTelegramDurableGroups(cfg); err != nil {
		return err
	}
	if err := validateTelegramChildBots(cfg); err != nil {
		return err
	}
	return nil
}
