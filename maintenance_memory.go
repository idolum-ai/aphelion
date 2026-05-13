//go:build linux

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func runGCCommand(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}

	expired := 0
	store, err := openStoreIfExists(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	if store != nil {
		defer store.Close()
		d, err := time.ParseDuration(cfg.Sessions.IdleExpiry)
		if err != nil {
			return fmt.Errorf("parse sessions.idle_expiry: %w", err)
		}
		expired, err = store.ExpireIdle(d)
		if err != nil {
			return err
		}
	}

	removedTemps, err := cleanupTempTrees(cfg)
	if err != nil {
		return err
	}
	archivedNotes, err := archiveColdDailyNotes(cfg, time.Now())
	if err != nil {
		return err
	}
	archivedCurated, err := archiveOversizedCuratedMemory(cfg, time.Now())
	if err != nil {
		return err
	}
	prunedTESEvents, tesRetentionState, err := pruneExecutionEventsForRetention(cfg, time.Now().UTC())
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "expired_sessions: %d\n", expired)
	fmt.Fprintf(os.Stdout, "removed_temp_dirs: %d\n", removedTemps)
	fmt.Fprintf(os.Stdout, "archived_daily_notes: %d\n", archivedNotes)
	fmt.Fprintf(os.Stdout, "archived_curated_memory: %d\n", archivedCurated)
	fmt.Fprintf(os.Stdout, "tes_retention: %s\n", tesRetentionState)
	fmt.Fprintf(os.Stdout, "pruned_execution_events: %d\n", prunedTESEvents)
	return nil
}

func runForgetCommand(args []string) error {
	fs := flag.NewFlagSet("forget", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	chatID := fs.Int64("chat", 0, "chat id to forget")
	sessionUserID := fs.Int64("session-user", 0, "session user id (default 0)")
	principalID := fs.Int64("principal", 0, "telegram principal id to forget")
	sharedMemory := fs.Bool("shared-memory", false, "clear shared dynamic memory files")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}

	var deletedSessions int
	store, err := openStoreIfExists(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	if store != nil {
		defer store.Close()
	}

	if *chatID > 0 {
		if store == nil {
			return fmt.Errorf("sessions database %s does not exist", cfg.Sessions.DBPath)
		}
		n, err := store.DeleteSession(session.SessionKey{ChatID: *chatID, UserID: *sessionUserID})
		if err != nil {
			return err
		}
		deletedSessions += n
	}

	removedPrincipalRoots := 0
	if *principalID > 0 {
		if store != nil {
			n, err := store.DeleteSession(session.SessionKey{ChatID: *principalID, UserID: 0})
			if err != nil {
				return err
			}
			deletedSessions += n
		}
		for _, path := range []string{
			filepath.Join(cfg.Agent.UserWorkspaceRoot, fmt.Sprintf("%d", *principalID)),
			filepath.Join(cfg.Agent.UserMemoryRoot, fmt.Sprintf("%d", *principalID)),
		} {
			ok, err := removeAllIfExists(path)
			if err != nil {
				return err
			}
			if ok {
				removedPrincipalRoots++
			}
		}
		if store != nil {
			if err := store.ResetRhizome(filepath.Clean(filepath.Join(cfg.Agent.UserMemoryRoot, fmt.Sprintf("%d", *principalID)))); err != nil {
				return err
			}
		}
	}

	removedSharedFiles := 0
	if *sharedMemory {
		removedSharedFiles, err = clearSharedDynamicMemory(cfg)
		if err != nil {
			return err
		}
		if store != nil {
			if err := store.ResetRhizome(filepath.Clean(cfg.Agent.SharedMemoryRoot)); err != nil {
				return err
			}
		}
	}

	if *chatID == 0 && *principalID == 0 && !*sharedMemory {
		return fmt.Errorf("forget requires at least one target: --chat, --principal, or --shared-memory")
	}

	fmt.Fprintf(os.Stdout, "deleted_sessions: %d\n", deletedSessions)
	fmt.Fprintf(os.Stdout, "removed_principal_roots: %d\n", removedPrincipalRoots)
	fmt.Fprintf(os.Stdout, "removed_shared_memory_paths: %d\n", removedSharedFiles)
	return nil
}

func runResetCommand(args []string) error {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	scope := fs.String("scope", "runtime", "reset scope: runtime|memory|all")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}

	scopeValue := strings.ToLower(strings.TrimSpace(*scope))
	switch scopeValue {
	case "runtime", "memory", "all":
	default:
		return fmt.Errorf("reset scope must be one of runtime|memory|all")
	}

	runtimeReset := scopeValue == "runtime" || scopeValue == "all"
	memoryReset := scopeValue == "memory" || scopeValue == "all"

	if runtimeReset {
		store, err := openStoreIfExists(cfg.Sessions.DBPath)
		if err != nil {
			return err
		}
		if store != nil {
			if err := store.ResetRuntime(); err != nil {
				_ = store.Close()
				return err
			}
			if err := store.Close(); err != nil {
				return err
			}
		}
	}

	removedUserWorkspaces := 0
	if runtimeReset {
		removedUserWorkspaces, err = removeContents(cfg.Agent.UserWorkspaceRoot)
		if err != nil {
			return err
		}
	}

	removedSharedMemory := 0
	removedUserMemory := 0
	if memoryReset {
		removedSharedMemory, err = clearSharedDynamicMemory(cfg)
		if err != nil {
			return err
		}
		removedUserMemory, err = removeContents(cfg.Agent.UserMemoryRoot)
		if err != nil {
			return err
		}
		store, err := openStoreIfExists(cfg.Sessions.DBPath)
		if err != nil {
			return err
		}
		if store != nil {
			defer store.Close()
			if err := store.ResetAllRhizome(); err != nil {
				return err
			}
		}
	}

	removedTemps, err := cleanupTempTrees(cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "reset_scope: %s\n", scopeValue)
	fmt.Fprintf(os.Stdout, "removed_user_workspaces: %d\n", removedUserWorkspaces)
	fmt.Fprintf(os.Stdout, "removed_shared_memory_paths: %d\n", removedSharedMemory)
	fmt.Fprintf(os.Stdout, "removed_user_memory_entries: %d\n", removedUserMemory)
	fmt.Fprintf(os.Stdout, "removed_temp_dirs: %d\n", removedTemps)
	return nil
}
