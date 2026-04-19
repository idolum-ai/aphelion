//go:build linux

package main

import (
	"testing"

	"github.com/idolum-ai/aphelion/config"
)

func TestValidateDurableChildBootstrapConfigAllowsIsolatedConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	if err := validateDurableChildBootstrapConfig(&cfg); err != nil {
		t.Fatalf("validateDurableChildBootstrapConfig() err = %v, want nil", err)
	}
}

func TestValidateDurableChildBootstrapConfigRejectsTelegramBotToken(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Telegram.BotToken = "tg-parent"
	err := validateDurableChildBootstrapConfig(&cfg)
	if err == nil {
		t.Fatal("validateDurableChildBootstrapConfig() err = nil, want error")
	}
}

func TestValidateDurableChildBootstrapConfigRejectsTelegramPrincipals(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Principals.Telegram.AdminUserIDs = []int64{1}
	err := validateDurableChildBootstrapConfig(&cfg)
	if err == nil {
		t.Fatal("validateDurableChildBootstrapConfig() err = nil, want error")
	}
}

func TestValidateDurableChildBootstrapConfigRejectsDurableGroups(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Telegram.DurableGroups = []config.TelegramDurableGroupConfig{{ChatID: 123, AgentID: "child", Charter: "x"}}
	err := validateDurableChildBootstrapConfig(&cfg)
	if err == nil {
		t.Fatal("validateDurableChildBootstrapConfig() err = nil, want error")
	}
}

func TestValidateDurableChildBootstrapConfigRejectsNilConfig(t *testing.T) {
	t.Parallel()

	if err := validateDurableChildBootstrapConfig(nil); err == nil {
		t.Fatal("validateDurableChildBootstrapConfig(nil) err = nil, want error")
	}
}
