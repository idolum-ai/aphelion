//go:build linux

package runtime

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestSystemStatusSnapshotClassifiesTelegramIngressFailures(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	if err := store.RecordTelegramIngressFailure(session.TelegramIngressFailureRecord{
		Surface:    "telegram:primary",
		UpdateID:   8801,
		UpdateKind: "message",
		ChatID:     42,
		MessageID:  10,
		ErrorText:  "context deadline exceeded",
		CreatedAt:  now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("RecordTelegramIngressFailure(transient) err = %v", err)
	}
	if err := store.RecordTelegramIngressFailure(session.TelegramIngressFailureRecord{
		Surface:    "telegram:primary",
		UpdateID:   8802,
		UpdateKind: "callback_query",
		ChatID:     42,
		MessageID:  11,
		ErrorText:  "unauthorized bot token",
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressFailure(config) err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if len(snapshot.TelegramIngress) != 2 {
		t.Fatalf("telegram ingress failures = %#v, want 2", snapshot.TelegramIngress)
	}
	byUpdate := map[int64]core.TelegramIngressFailureSnapshot{}
	for _, failure := range snapshot.TelegramIngress {
		byUpdate[failure.UpdateID] = failure
	}
	if byUpdate[8801].FailureClass != core.ReliabilityFailureTransportTransient ||
		byUpdate[8801].RetryPolicy != core.ReliabilityRetryBackoffOrFailover {
		t.Fatalf("transient ingress classification = %#v, want transient backoff", byUpdate[8801])
	}
	if byUpdate[8802].FailureClass != core.ReliabilityFailureTransportConfig ||
		byUpdate[8802].RetryPolicy != core.ReliabilityRetryConfigRepair {
		t.Fatalf("config ingress classification = %#v, want config repair", byUpdate[8802])
	}
}
