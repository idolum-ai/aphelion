//go:build linux

package core

import "testing"

func TestEncodeContinuationCallbackDataCompactsLongID(t *testing.T) {
	longID := "button-backed-materialization-live-test-v1"
	data := EncodeContinuationCallbackData(longID, "ask_next_lease")
	if data == "" {
		t.Fatal("EncodeContinuationCallbackData() empty")
	}
	if len(data) > TelegramCallbackDataMaxBytes {
		t.Fatalf("callback data length = %d, want <= %d: %q", len(data), TelegramCallbackDataMaxBytes, data)
	}
	id, action, ok := DecodeContinuationCallbackData(data)
	if !ok || id != ContinuationCallbackAlias(longID) || action != "ask_next_lease" {
		t.Fatalf("DecodeContinuationCallbackData(%q) = id=%q action=%q ok=%t, want compact alias/%q/true", data, id, action, ok, "ask_next_lease")
	}
}

func TestEncodeContinuationCallbackDataKeepsShortID(t *testing.T) {
	data := EncodeContinuationCallbackData("decision-v2", "approve_lease")
	if data != "continuation:decision-v2:approve_lease" {
		t.Fatalf("EncodeContinuationCallbackData() = %q", data)
	}
}
