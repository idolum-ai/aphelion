//go:build linux

package durableagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestRemoteChildLoopRunnerRunsParentConversationWhenInboxEmpty(t *testing.T) {
	t.Parallel()

	inboxDir := t.TempDir()
	fake := &fakeRemoteChildCycleRunner{
		parentResults: []*RemoteChildRunResult{
			{Sync: RemoteSyncResult{PolicyVersion: 2}, UploadedReviewArtifacts: 1, AcknowledgedParent: true},
			{Sync: RemoteSyncResult{PolicyVersion: 3}, UploadedReviewArtifacts: 2},
		},
	}
	loop := &RemoteChildLoopRunner{runner: fake, Sleep: func(context.Context, time.Duration) error { return nil }}

	result, err := loop.Run(context.Background(), "bootstrap.json", inboxDir, time.Millisecond, 2)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if fake.parentCalls != 2 {
		t.Fatalf("parentCalls = %d, want 2", fake.parentCalls)
	}
	if result.Syncs != 2 || result.MessagesProcessed != 1 || result.UploadedReviewArtifacts != 3 || result.LastPolicyVersion != 3 {
		t.Fatalf("result = %#v, want two syncs, one acknowledged parent message, three uploads, last policy 3", result)
	}
}

func TestRemoteChildLoopRunnerProcessesJSONFilesInDeterministicOrder(t *testing.T) {
	t.Parallel()

	inboxDir := t.TempDir()
	writeLoopMessage(t, inboxDir, "002.json", "second")
	writeLoopMessage(t, inboxDir, "001.json", "first")
	if err := os.WriteFile(filepath.Join(inboxDir, "ignore.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatalf("WriteFile(ignore) err = %v", err)
	}
	fake := &fakeRemoteChildCycleRunner{
		runOnceResult: &RemoteChildRunResult{Sync: RemoteSyncResult{PolicyVersion: 7}, UploadedReviewArtifacts: 1},
	}
	loop := &RemoteChildLoopRunner{runner: fake, Sleep: func(context.Context, time.Duration) error { return nil }}

	result, err := loop.Run(context.Background(), "bootstrap.json", inboxDir, time.Millisecond, 1)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if !reflect.DeepEqual(fake.runOnceTexts, []string{"first", "second"}) {
		t.Fatalf("runOnceTexts = %#v, want deterministic filename order", fake.runOnceTexts)
	}
	if result.Syncs != 2 || result.MessagesProcessed != 2 || result.UploadedReviewArtifacts != 2 || result.LastPolicyVersion != 7 {
		t.Fatalf("result = %#v, want two processed message files", result)
	}
	for _, name := range []string{"001.json", "002.json"} {
		if _, err := os.Stat(filepath.Join(inboxDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists, err=%v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(inboxDir, "ignore.txt")); err != nil {
		t.Fatalf("ignore.txt stat err = %v, want non-json file preserved", err)
	}
}

func TestRemoteChildLoopRunnerReturnsPartialResultOnMessageError(t *testing.T) {
	t.Parallel()

	inboxDir := t.TempDir()
	writeLoopMessage(t, inboxDir, "001.json", "first")
	writeLoopMessage(t, inboxDir, "002.json", "second")
	wantErr := errors.New("executor failed")
	fake := &fakeRemoteChildCycleRunner{
		runOnceResult: &RemoteChildRunResult{Sync: RemoteSyncResult{PolicyVersion: 4}, UploadedReviewArtifacts: 1},
		runOnceErrAt:  2,
		runOnceErr:    wantErr,
	}
	loop := &RemoteChildLoopRunner{runner: fake, Sleep: func(context.Context, time.Duration) error { return nil }}

	result, err := loop.Run(context.Background(), "bootstrap.json", inboxDir, time.Millisecond, 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() err = %v, want %v", err, wantErr)
	}
	if result == nil || result.Syncs != 1 || result.MessagesProcessed != 1 || result.UploadedReviewArtifacts != 1 || result.LastPolicyVersion != 4 {
		t.Fatalf("partial result = %#v, want first message accounted before error", result)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, "001.json")); !os.IsNotExist(err) {
		t.Fatalf("001.json still exists, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, "002.json")); err != nil {
		t.Fatalf("002.json stat err = %v, want failed message preserved", err)
	}
}

func TestRemoteChildLoopRunnerStopsOnContextCancellationFromSleep(t *testing.T) {
	t.Parallel()

	inboxDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeRemoteChildCycleRunner{
		parentResults: []*RemoteChildRunResult{{Sync: RemoteSyncResult{PolicyVersion: 9}}},
	}
	loop := &RemoteChildLoopRunner{runner: fake, Sleep: func(ctx context.Context, _ time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}}

	result, err := loop.Run(ctx, "bootstrap.json", inboxDir, time.Millisecond, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() err = %v, want context.Canceled", err)
	}
	if result == nil || result.Syncs != 1 || result.LastPolicyVersion != 9 {
		t.Fatalf("partial result = %#v, want first parent sync before cancellation", result)
	}
}

func writeLoopMessage(t *testing.T, inboxDir string, name string, text string) {
	t.Helper()
	raw, err := json.Marshal(core.InboundMessage{Text: text, Timestamp: time.Now().UTC()})
	if err != nil {
		t.Fatalf("json.Marshal(message) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, name), raw, 0o600); err != nil {
		t.Fatalf("WriteFile(%s) err = %v", name, err)
	}
}

type fakeRemoteChildCycleRunner struct {
	parentCalls   int
	parentResults []*RemoteChildRunResult
	parentErr     error

	runOnceCalls  int
	runOnceTexts  []string
	runOnceResult *RemoteChildRunResult
	runOnceErrAt  int
	runOnceErr    error
}

func (r *fakeRemoteChildCycleRunner) RunParentConversation(context.Context, string) (*RemoteChildRunResult, error) {
	r.parentCalls++
	if r.parentErr != nil {
		return nil, r.parentErr
	}
	if len(r.parentResults) >= r.parentCalls && r.parentResults[r.parentCalls-1] != nil {
		return r.parentResults[r.parentCalls-1], nil
	}
	return &RemoteChildRunResult{}, nil
}

func (r *fakeRemoteChildCycleRunner) RunOnce(_ context.Context, _ string, msg core.InboundMessage) (*RemoteChildRunResult, error) {
	r.runOnceCalls++
	r.runOnceTexts = append(r.runOnceTexts, msg.Text)
	if r.runOnceErrAt > 0 && r.runOnceCalls == r.runOnceErrAt {
		return nil, r.runOnceErr
	}
	if r.runOnceResult != nil {
		return r.runOnceResult, nil
	}
	return &RemoteChildRunResult{}, nil
}
