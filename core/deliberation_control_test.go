//go:build linux

package core

import "testing"

func TestEncodeDeliberationControlCallbackData(t *testing.T) {
	t.Parallel()

	if got := EncodeDeliberationControlCallbackData(0, DeliberationControlActionStop); got != "" {
		t.Fatalf("EncodeDeliberationControlCallbackData() = %q, want empty for invalid run id", got)
	}
	if got := EncodeDeliberationControlCallbackData(91, DeliberationControlAction("pause")); got != "" {
		t.Fatalf("EncodeDeliberationControlCallbackData() = %q, want empty for invalid action", got)
	}
	if got := EncodeDeliberationControlCallbackData(91, DeliberationControlActionStop); got != "deliberation:91:stop" {
		t.Fatalf("EncodeDeliberationControlCallbackData() = %q, want deliberation:91:stop", got)
	}
}

func TestDecodeDeliberationControlCallbackData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		data    string
		wantID  int64
		wantAct DeliberationControlAction
		ok      bool
	}{
		{data: "", ok: false},
		{data: "status:chat", ok: false},
		{data: "deliberation:", ok: false},
		{data: "deliberation:abc:stop", ok: false},
		{data: "deliberation:0:stop", ok: false},
		{data: "deliberation:91", ok: false},
		{data: "deliberation:91:pause", ok: false},
		{data: "deliberation:91:stop", wantID: 91, wantAct: DeliberationControlActionStop, ok: true},
		{data: "deliberation:42:detach", wantID: 42, wantAct: DeliberationControlActionDetach, ok: true},
	}

	for _, tt := range tests {
		gotID, gotAct, ok := DecodeDeliberationControlCallbackData(tt.data)
		if ok != tt.ok || gotID != tt.wantID || gotAct != tt.wantAct {
			t.Fatalf("DecodeDeliberationControlCallbackData(%q) = (%d, %q, %v), want (%d, %q, %v)", tt.data, gotID, gotAct, ok, tt.wantID, tt.wantAct, tt.ok)
		}
	}
}
