//go:build linux

package session

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestModelSlotOverrideRoundTripAndSupersede(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	first, err := store.SetModelSlotOverride(ModelSlotOverrideRecord{
		Slot:           core.ModelSlotGovernor,
		Config:         core.ModelSlotConfig{Slot: core.ModelSlotGovernor, Provider: core.ModelProviderOpenAI, Model: "gpt-5.5", Effort: "high", Transport: "auto"},
		PreviousConfig: core.ModelSlotConfig{Slot: core.ModelSlotGovernor, Provider: core.ModelProviderAnthropic, Model: "claude-sonnet-4-6", Effort: "medium", Transport: "auto"},
		CreatedBy:      "telegram:1001",
		Reason:         "test override",
		CreatedAt:      now,
	})
	if err != nil {
		t.Fatalf("SetModelSlotOverride(first) err = %v", err)
	}
	if first.ID == 0 {
		t.Fatal("first.ID = 0, want persisted id")
	}

	second, err := store.SetModelSlotOverride(ModelSlotOverrideRecord{
		Slot:           core.ModelSlotGovernor,
		Config:         core.ModelSlotConfig{Slot: core.ModelSlotGovernor, Provider: core.ModelProviderAnthropic, Model: "claude-opus-4.7", Effort: "xhigh", Transport: "auto"},
		PreviousConfig: first.Config,
		CreatedBy:      "telegram:1001",
		Reason:         "second override",
		CreatedAt:      now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("SetModelSlotOverride(second) err = %v", err)
	}

	active, ok, err := store.ActiveModelSlotOverride(core.ModelSlotGovernor, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ActiveModelSlotOverride() err = %v", err)
	}
	if !ok {
		t.Fatal("ActiveModelSlotOverride() ok = false")
	}
	if active.ID != second.ID || active.Config.Model != "claude-opus-4.7" {
		t.Fatalf("active = %#v, want second override", active)
	}
	history, err := store.ModelSlotOverrideHistory(core.ModelSlotGovernor, 10)
	if err != nil {
		t.Fatalf("ModelSlotOverrideHistory() err = %v", err)
	}
	if len(history) != 2 || history[1].Status != "superseded" {
		t.Fatalf("history = %#v, want superseded first record", history)
	}
}

func TestExpireModelSlotOverrides(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	if _, err := store.SetModelSlotOverride(ModelSlotOverrideRecord{
		Slot:      core.ModelSlotGovernor,
		Config:    core.ModelSlotConfig{Slot: core.ModelSlotGovernor, Provider: core.ModelProviderOpenAI, Model: "gpt-5.5", Effort: "high", Transport: "auto"},
		CreatedBy: "telegram:1001",
		ExpiresAt: now.Add(-time.Second),
		CreatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SetModelSlotOverride() err = %v", err)
	}

	expired, err := store.ExpireModelSlotOverrides(now)
	if err != nil {
		t.Fatalf("ExpireModelSlotOverrides() err = %v", err)
	}
	if len(expired) != 1 || expired[0].Slot != core.ModelSlotGovernor {
		t.Fatalf("expired = %#v, want governor override", expired)
	}
	if _, ok, err := store.ActiveModelSlotOverride(core.ModelSlotGovernor, now); err != nil {
		t.Fatalf("ActiveModelSlotOverride() err = %v", err)
	} else if ok {
		t.Fatal("ActiveModelSlotOverride() ok = true, want expired override hidden")
	}
}
