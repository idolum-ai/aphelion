//go:build linux

package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDurableAgentChannelConfigNormalizesExternalJSON(t *testing.T) {
	t.Parallel()

	var cfg DurableAgentChannelConfig
	if err := json.Unmarshal([]byte(`{"external":{"address":" child-endpoint ","adapter":"Child_Adapter","poll_interval":"5m"}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal channel config err = %v", err)
	}
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	external := cfg.ExternalConfig()
	if external == nil {
		t.Fatal("ExternalConfig() = nil, want config")
	}
	if external.Address != "child-endpoint" || external.Adapter != "child_adapter" || external.PollInterval != "5m" {
		t.Fatalf("ExternalConfig() = %#v, want normalized config", external)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal channel config err = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"external"`) {
		t.Fatalf("marshaled config = %s, want external key", text)
	}
	if strings.Contains(text, `"email"`) {
		t.Fatalf("marshaled config = %s, should not emit removed email key", text)
	}
}

func TestDurableAgentChannelConfigNormalizesScheduledReviewJSON(t *testing.T) {
	t.Parallel()

	var cfg DurableAgentChannelConfig
	if err := json.Unmarshal([]byte(`{"scheduled_review":{"title":" Daily Review ","schedule_kind":"Daily","time_utc":"00:10","window":"PREVIOUS_DAY","max_messages":1200,"artifact_kind":"Scheduled_Check_In","transcript_dir":"/ .aphelion/daily-review /","guidance_question":" Next? "}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal channel config err = %v", err)
	}
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	scheduled := cfg.ScheduledReviewConfig()
	if scheduled == nil {
		t.Fatal("ScheduledReviewConfig() = nil, want config")
	}
	if scheduled.Title != "Daily Review" || scheduled.ScheduleKind != "daily" || scheduled.Window != "previous_day" || scheduled.ArtifactKind != "scheduled_check_in" || scheduled.GuidanceQuestion != "Next?" {
		t.Fatalf("ScheduledReviewConfig() = %#v, want normalized scheduled review config", scheduled)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal channel config err = %v", err)
	}
	if !strings.Contains(string(raw), `"scheduled_review"`) {
		t.Fatalf("marshaled config = %s, want scheduled_review key", raw)
	}
}
