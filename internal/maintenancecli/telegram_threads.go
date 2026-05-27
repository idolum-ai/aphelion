//go:build linux

package maintenancecli

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func runTelegramThreadsMaintenanceCommand(args []string) error {
	if commandGroupHelpRequested(args) {
		printCommandGroupHelp("telegram-threads", []string{"sanitize"})
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: telegram-threads sanitize [--config <path>] [--apply]")
	}
	switch args[0] {
	case "sanitize":
		return runTelegramThreadsSanitizeCommand(args[1:])
	default:
		return fmt.Errorf("unknown telegram-threads command %q", args[0])
	}
}

func runTelegramThreadsSanitizeCommand(args []string) error {
	fs := flag.NewFlagSet("telegram-threads sanitize", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	apply := fs.Bool("apply", false, "apply repairs; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	changed, err := store.SanitizeTelegramThreadDisplaySlots(time.Now(), *apply)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: telegram-threads sanitize\n")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "apply: %t\n", *apply)
	fmt.Fprintf(os.Stdout, "changes: %d\n", changed)
	if *apply {
		fmt.Fprintf(os.Stdout, "status: applied\n")
	} else {
		fmt.Fprintf(os.Stdout, "status: dry-run\n")
	}
	return nil
}
