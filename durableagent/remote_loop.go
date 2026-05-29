//go:build linux

package durableagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

const defaultRemoteChildPollInterval = 30 * time.Second

type RemoteChildLoopResult struct {
	Syncs                   int
	MessagesProcessed       int
	UploadedReviewArtifacts int
	LastPolicyVersion       int64
}

type remoteChildCycleRunner interface {
	RunOnce(ctx context.Context, bootstrapPath string, msg core.InboundMessage) (*RemoteChildRunResult, error)
	RunParentConversation(ctx context.Context, bootstrapPath string) (*RemoteChildRunResult, error)
}

type RemoteChildLoopRunner struct {
	runner remoteChildCycleRunner
	Sleep  func(context.Context, time.Duration) error
}

func NewRemoteChildLoopRunner(runner *RemoteChildRunner) *RemoteChildLoopRunner {
	return &RemoteChildLoopRunner{
		runner: runner,
		Sleep:  sleepWithContext,
	}
}

func (r *RemoteChildLoopRunner) Run(ctx context.Context, bootstrapPath string, inboxDir string, interval time.Duration, maxIterations int) (*RemoteChildLoopResult, error) {
	if r == nil || r.runner == nil {
		return nil, fmt.Errorf("durable agent remote child runner is nil")
	}
	inboxDir = filepath.Clean(strings.TrimSpace(inboxDir))
	if inboxDir == "" {
		return nil, fmt.Errorf("durable agent remote inbox dir is required")
	}
	if interval <= 0 {
		interval = defaultRemoteChildPollInterval
	}
	result := &RemoteChildLoopResult{}
	for iteration := 0; maxIterations <= 0 || iteration < maxIterations; iteration++ {
		paths, err := queuedRemoteMessagePaths(inboxDir)
		if err != nil {
			return result, err
		}
		if len(paths) == 0 {
			runResult, err := r.runner.RunParentConversation(ctx, bootstrapPath)
			if err != nil {
				return result, err
			}
			result.Syncs++
			result.UploadedReviewArtifacts += runResult.UploadedReviewArtifacts
			result.LastPolicyVersion = runResult.Sync.PolicyVersion
			if runResult.AcknowledgedParent {
				result.MessagesProcessed++
			}
		} else {
			for _, messagePath := range paths {
				msg, err := readRemoteInboundMessage(messagePath)
				if err != nil {
					return result, err
				}
				runResult, err := r.runner.RunOnce(ctx, bootstrapPath, msg)
				if err != nil {
					return result, err
				}
				result.Syncs++
				result.MessagesProcessed++
				result.UploadedReviewArtifacts += runResult.UploadedReviewArtifacts
				result.LastPolicyVersion = runResult.Sync.PolicyVersion
				if err := os.Remove(messagePath); err != nil {
					return result, fmt.Errorf("remove processed remote child message %s: %w", messagePath, err)
				}
			}
		}
		if maxIterations > 0 && iteration+1 >= maxIterations {
			return result, nil
		}
		if err := r.sleep(ctx, interval); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (r *RemoteChildLoopRunner) sleep(ctx context.Context, interval time.Duration) error {
	if r != nil && r.Sleep != nil {
		return r.Sleep(ctx, interval)
	}
	return sleepWithContext(ctx, interval)
}

func queuedRemoteMessagePaths(inboxDir string) ([]string, error) {
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		return nil, fmt.Errorf("read durable agent remote inbox dir: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(inboxDir, name))
	}
	sort.Strings(paths)
	return paths, nil
}

func readRemoteInboundMessage(path string) (core.InboundMessage, error) {
	raw, err := os.ReadFile(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return core.InboundMessage{}, fmt.Errorf("read durable agent remote message: %w", err)
	}
	var msg core.InboundMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return core.InboundMessage{}, fmt.Errorf("decode durable agent remote message: %w", err)
	}
	return msg, nil
}

func sleepWithContext(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
