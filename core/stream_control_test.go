//go:build linux

package core

import "testing"

func TestEncodeDecodeStreamControlCallbackData(t *testing.T) {
	t.Parallel()

	encoded := EncodeStreamControlCallbackData("abc123", StreamControlActionStop)
	if encoded != "stream:abc123:stop" {
		t.Fatalf("encoded = %q, want stream:abc123:stop", encoded)
	}
	streamID, action, ok := DecodeStreamControlCallbackData(encoded)
	if !ok || streamID != "abc123" || action != StreamControlActionStop {
		t.Fatalf("decoded = (%q,%q,%v), want abc123/stop/true", streamID, action, ok)
	}
}

func TestDecodeStreamControlCallbackDataRejectsInvalid(t *testing.T) {
	t.Parallel()

	for _, data := range []string{"", "deliberation:1:stop", "stream:", "stream:id:pause", "stream::stop"} {
		if streamID, action, ok := DecodeStreamControlCallbackData(data); ok {
			t.Fatalf("DecodeStreamControlCallbackData(%q) = (%q,%q,true), want false", data, streamID, action)
		}
	}
}
