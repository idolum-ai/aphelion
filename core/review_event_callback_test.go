//go:build linux

package core

import "testing"

func TestReviewEventCallbackRoundTrip(t *testing.T) {
	data := EncodeReviewEventCallbackData(42, ReviewEventActionApprove)
	if data != "review_event:42:approve" {
		t.Fatalf("EncodeReviewEventCallbackData() = %q", data)
	}
	id, action, ok := DecodeReviewEventCallbackData(data)
	if !ok || id != 42 || action != ReviewEventActionApprove {
		t.Fatalf("DecodeReviewEventCallbackData() = %d %q %t", id, action, ok)
	}
	if _, _, ok := DecodeReviewEventCallbackData("decision:42:approve"); ok {
		t.Fatal("DecodeReviewEventCallbackData() ok=true for other callback lane")
	}
}
