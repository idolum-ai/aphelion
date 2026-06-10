//go:build linux

package core

import "testing"

func TestReentryRecommendationCallbackRoundTrip(t *testing.T) {
	t.Parallel()

	selectData := EncodeReentryRecommendationCallbackData("reentry-1", "c1", ReentryRecommendationCallbackSelect)
	if selectData == "" {
		t.Fatal("select callback data is empty")
	}
	recommendationID, candidateID, action, ok := DecodeReentryRecommendationCallbackData(selectData)
	if !ok || recommendationID != "reentry-1" || candidateID != "c1" || action != ReentryRecommendationCallbackSelect {
		t.Fatalf("Decode(select) = %q/%q/%q ok=%v", recommendationID, candidateID, action, ok)
	}

	ignoreData := EncodeReentryRecommendationCallbackData("reentry-1", "", ReentryRecommendationCallbackIgnore)
	recommendationID, candidateID, action, ok = DecodeReentryRecommendationCallbackData(ignoreData)
	if !ok || recommendationID != "reentry-1" || candidateID != "" || action != ReentryRecommendationCallbackIgnore {
		t.Fatalf("Decode(ignore) = %q/%q/%q ok=%v", recommendationID, candidateID, action, ok)
	}
}

func TestReentryRecommendationCallbackRejectsMalformedData(t *testing.T) {
	t.Parallel()

	for _, data := range []string{
		"",
		"reentry:select:reentry-1",
		"reentry:ignore:reentry-1:c1",
		"reentry:unknown:reentry-1:c1",
	} {
		if recommendationID, candidateID, action, ok := DecodeReentryRecommendationCallbackData(data); ok {
			t.Fatalf("Decode(%q) = %q/%q/%q ok=true, want malformed rejection", data, recommendationID, candidateID, action)
		}
	}
}
