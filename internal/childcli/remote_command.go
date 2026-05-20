//go:build linux

package childcli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

type DurableAgentRemoteDeps struct {
	ClientFactory   durableagent.RemoteClientFactory
	ExecutorFactory func(store *session.SQLiteStore, dbPath string) durableagent.RemoteChildExecutor
}

func RunDurableAgentRemoteCommand(args []string, deps DurableAgentRemoteDeps) error {
	fs := flag.NewFlagSet("durable-agent remote", flag.ContinueOnError)
	bootstrapPath := fs.String("bootstrap", "", "path to remote child bootstrap json")
	dbPath := fs.String("db", "", "path to child state sqlite db")
	messagePath := fs.String("message", "", "path to inbound message json for run-once")
	inboxDir := fs.String("inbox-dir", "", "path to inbound message queue dir for loop")
	pollInterval := fs.String("poll-interval", "", "remote child loop poll interval")
	iterations := fs.Int("iterations", 0, "maximum loop iterations before exit; 0 runs until canceled")
	if err := fs.Parse(args); err != nil {
		return err
	}

	action := "sync"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}
	if strings.TrimSpace(*bootstrapPath) == "" {
		return fmt.Errorf("durable-agent remote requires --bootstrap")
	}
	if strings.TrimSpace(*dbPath) == "" {
		return fmt.Errorf("durable-agent remote requires --db")
	}

	store, err := session.NewSQLiteStore(strings.TrimSpace(*dbPath))
	if err != nil {
		return err
	}
	defer store.Close()

	remote := durableagent.NewRemoteRuntime(store, deps.ClientFactory)
	switch action {
	case "", "sync":
		result, err := remote.Sync(context.Background(), *bootstrapPath)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: durable-agent remote sync\n")
		fmt.Fprintf(os.Stdout, "enrolled: %t\n", result.Enrolled)
		fmt.Fprintf(os.Stdout, "policy_changed: %t\n", result.PolicyChanged)
		fmt.Fprintf(os.Stdout, "policy_version: %d\n", result.PolicyVersion)
		return nil
	case "run-once":
		if strings.TrimSpace(*messagePath) == "" {
			return fmt.Errorf("durable-agent remote run-once requires --message")
		}
		if deps.ExecutorFactory == nil {
			return fmt.Errorf("durable-agent remote child executor is unavailable")
		}
		var msg core.InboundMessage
		if err := DecodeJSONFile(*messagePath, &msg); err != nil {
			return fmt.Errorf("load remote child message: %w", err)
		}
		runner := durableagent.NewRemoteChildRunner(store, remote, deps.ExecutorFactory(store, strings.TrimSpace(*dbPath)))
		result, err := runner.RunOnce(context.Background(), *bootstrapPath, msg)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: durable-agent remote run-once\n")
		fmt.Fprintf(os.Stdout, "enrolled: %t\n", result.Sync.Enrolled)
		fmt.Fprintf(os.Stdout, "policy_changed: %t\n", result.Sync.PolicyChanged)
		fmt.Fprintf(os.Stdout, "policy_version: %d\n", result.Sync.PolicyVersion)
		fmt.Fprintf(os.Stdout, "uploaded_review_artifacts: %d\n", result.UploadedReviewArtifacts)
		fmt.Fprintf(os.Stdout, "acknowledged_parent_conversation: %t\n", result.AcknowledgedParent)
		return nil
	case "loop":
		if strings.TrimSpace(*inboxDir) == "" {
			return fmt.Errorf("durable-agent remote loop requires --inbox-dir")
		}
		if deps.ExecutorFactory == nil {
			return fmt.Errorf("durable-agent remote child executor is unavailable")
		}
		interval, err := ParseRemotePollInterval(*pollInterval)
		if err != nil {
			return err
		}
		loop := durableagent.NewRemoteChildLoopRunner(durableagent.NewRemoteChildRunner(store, remote, deps.ExecutorFactory(store, strings.TrimSpace(*dbPath))))
		result, err := loop.Run(context.Background(), *bootstrapPath, *inboxDir, interval, *iterations)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: durable-agent remote loop\n")
		fmt.Fprintf(os.Stdout, "syncs: %d\n", result.Syncs)
		fmt.Fprintf(os.Stdout, "messages_processed: %d\n", result.MessagesProcessed)
		fmt.Fprintf(os.Stdout, "uploaded_review_artifacts: %d\n", result.UploadedReviewArtifacts)
		fmt.Fprintf(os.Stdout, "policy_version: %d\n", result.LastPolicyVersion)
		return nil
	default:
		return fmt.Errorf("durable-agent remote action must be one of sync|run-once|loop")
	}
}

func ParseRemotePollInterval(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse durable-agent remote poll interval: %w", err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("durable-agent remote poll interval must be > 0")
	}
	return value, nil
}
