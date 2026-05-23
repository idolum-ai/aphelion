//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/turn"
)

func TestScheduledJobsFromCronConfigPreservesCronCompatibility(t *testing.T) {
	t.Parallel()

	jobs := scheduledJobsFromCronConfig(config.CronConfig{
		Enabled: true,
		Jobs: []config.CronJobConfig{
			{ID: "nightly", Every: "1h", Prompt: "summarize", Delivery: "announce", Enabled: true},
			{ID: "disabled", Every: "1h", Prompt: "skip", Delivery: "none", Enabled: false},
		},
	})
	if len(jobs) != 1 {
		t.Fatalf("jobs len = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if job.ID.String() != "nightly" || job.Kind != scheduledJobKindCron || job.Delivery.OutboundKind != "cron" {
		t.Fatalf("job = %#v, want cron-compatible identity", job)
	}
	if !job.Delivery.Mode.announces() {
		t.Fatalf("delivery = %q, want announce", job.Delivery)
	}
	if got := scheduledJobSessionKey(job); got.ChatID != cronSessionChatID("nightly") || got.Scope != cronScopeRef("nightly") {
		t.Fatalf("scheduled key = %#v, want existing cron key", got)
	}
	if got := renderScheduledJobRequest(job); got != renderCronRequest(config.CronJobConfig{ID: "nightly", Every: "1h", Prompt: "summarize", Delivery: "announce", Enabled: true}) {
		t.Fatalf("scheduled request = %q, want cron adapter request %q", got, renderCronRequest(config.CronJobConfig{ID: "nightly", Every: "1h", Prompt: "summarize", Delivery: "announce", Enabled: true}))
	}
}

func TestRenderCronRequestPreservesEmptyDeliveryCompatibility(t *testing.T) {
	t.Parallel()

	got := renderCronRequest(config.CronJobConfig{ID: "empty-delivery", Every: "1h", Prompt: "check", Enabled: true})
	if !strings.Contains(got, "Cron job run: empty-delivery") || !strings.Contains(got, "Delivery mode: \n") {
		t.Fatalf("renderCronRequest() = %q, want empty delivery label preserved", got)
	}
}

func TestScheduledDeliveryModeDefaultsAndUnknownsDoNotAnnounce(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw      string
		want     scheduledDeliveryMode
		announce bool
	}{
		{raw: "", want: scheduledDeliveryNone, announce: false},
		{raw: "none", want: scheduledDeliveryNone, announce: false},
		{raw: "announce", want: scheduledDeliveryAnnounce, announce: true},
		{raw: "local_artifact", want: scheduledDeliveryMode("local_artifact"), announce: false},
	}
	for _, tc := range cases {
		got := normalizeScheduledDeliveryMode(tc.raw)
		if got != tc.want || got.announces() != tc.announce {
			t.Fatalf("normalizeScheduledDeliveryMode(%q) = (%q,%v), want (%q,%v)", tc.raw, got, got.announces(), tc.want, tc.announce)
		}
	}
}

func TestRunScheduledJobRecordsEvidenceWithoutAnnounce(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "scheduled canonical"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	job := scheduledJob{
		ID:     "quiet-local",
		Every:  "30m",
		Prompt: "Summarize local scheduler evidence.",
		Delivery: scheduledJobDelivery{
			Mode:         normalizeScheduledDeliveryMode("local_artifact"),
			Label:        "local_artifact",
			OutboundKind: "scheduled_job",
		},
		Enabled: true,
	}
	if err := rt.runScheduledJobOnce(context.Background(), job); err != nil {
		t.Fatalf("runScheduledJobOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent len = %d, want 0 for non-announce delivery", len(sender.sent))
	}
	sender.mu.Unlock()

	key := scheduledJobSessionKey(job)
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(scheduled job session) err = %v", err)
	}
	if sess.LastFloorText != "scheduled canonical" {
		t.Fatalf("scheduled floor = %q, want scheduled canonical", sess.LastFloorText)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventTurnStarted)
	assertHasEventType(t, events, core.ExecutionEventTurnCompleted)
	payload := payloadForEventType(events, core.ExecutionEventTurnStarted)
	requestText, _ := payload["request_text"].(string)
	if !strings.Contains(requestText, "Scheduled job run: quiet-local") || !strings.Contains(requestText, "Delivery mode: local_artifact") {
		t.Fatalf("turn.started request_text = %q, want scheduled job id and delivery mode", requestText)
	}
}

func TestScheduledJobSessionKeyUsesGenericScopeForNonCron(t *testing.T) {
	t.Parallel()

	job := normalizeScheduledJob(scheduledJob{ID: "quiet-local"})
	key := scheduledJobSessionKey(job)
	if key.Scope.Kind != scheduledJobScopeKind || key.Scope.ID != "quiet-local" {
		t.Fatalf("scheduled scope = %#v, want generic scheduled job scope", key.Scope)
	}
	if key.ChatID == cronSessionChatID("quiet-local") {
		t.Fatalf("generic scheduled chat id reused cron namespace: %d", key.ChatID)
	}
}

func TestPlanScheduledJobDeliverySeparatesDeliveryFromExecution(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	job := scheduledJobFromCronConfig(config.CronJobConfig{ID: "announce", Every: "1h", Prompt: "tell me", Delivery: "announce", Enabled: true})
	key := scheduledJobSessionKey(job)
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(job session) err = %v", err)
	}
	result := scheduledJobExecutionResult{
		Execution: scheduledJobExecution{Job: job, Key: key, Session: sess},
		Turn: &turn.Result{
			Turn:         &core.TurnResult{Text: "floor"},
			Commit:       turn.CommitResult{Persisted: true},
			FloorText:    "floor",
			VisibleReply: "visible",
		},
	}
	plan, ok, err := rt.planScheduledJobDelivery(result)
	if err != nil || !ok {
		t.Fatalf("planScheduledJobDelivery() = %#v, %v, %v; want plan", plan, ok, err)
	}
	if plan.TargetChatID != 1001 || plan.ReplyText != "visible" || plan.FloorText != "floor" || plan.OutboundKind != "cron" {
		t.Fatalf("delivery plan = %#v, want explicit admin cron delivery", plan)
	}

	job.Delivery.Mode = scheduledDeliveryNone
	result.Execution.Job = job
	if plan, ok, err := rt.planScheduledJobDelivery(result); err != nil || ok {
		t.Fatalf("planScheduledJobDelivery(non-announce) = %#v, %v, %v; want no plan", plan, ok, err)
	}
}

func TestScheduledJobCadenceRejectsInvalidDuration(t *testing.T) {
	t.Parallel()

	if _, err := (scheduledJob{Every: "not-a-duration"}).cadence(); err == nil {
		t.Fatal("cadence() err = nil, want invalid duration error")
	}
	if got, err := (scheduledJob{Every: "2h"}).cadence(); err != nil || got != 2*time.Hour {
		t.Fatalf("cadence() = %v, %v; want 2h nil", got, err)
	}
}
