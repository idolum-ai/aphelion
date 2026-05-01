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
	hide := EncodeReviewEventCallbackData(42, ReviewEventActionHide)
	if hide != "review_event:42:hide" {
		t.Fatalf("EncodeReviewEventCallbackData(hide) = %q", hide)
	}
	id, action, ok = DecodeReviewEventCallbackData("review_event:42:expand")
	if !ok || id != 42 || action != ReviewEventActionExpand {
		t.Fatalf("DecodeReviewEventCallbackData(expand) = %d %q %t", id, action, ok)
	}
	if _, _, ok := DecodeReviewEventCallbackData("decision:42:approve"); ok {
		t.Fatal("DecodeReviewEventCallbackData() ok=true for other callback lane")
	}
}

func TestMissionControlReviewEventCallbackActionsRoundTrip(t *testing.T) {
	for _, action := range []ReviewEventAction{
		ReviewEventActionMissionAdd,
		ReviewEventActionMissionAskEdit,
		ReviewEventActionMissionPark,
		ReviewEventActionMissionReject,
	} {
		data := EncodeReviewEventCallbackData(77, action)
		id, got, ok := DecodeReviewEventCallbackData(data)
		if !ok || id != 77 || got != action {
			t.Fatalf("DecodeReviewEventCallbackData(%q) = id=%d action=%q ok=%t, want 77/%q/true", data, id, got, ok, action)
		}
	}
}
