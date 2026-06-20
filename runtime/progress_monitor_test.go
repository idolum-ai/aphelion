//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func TestStartTurnMonitorRunActivityHeartbeatUpdatesLastActivity(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	previousInterval := turnRunActivityHeartbeatInterval
	turnRunActivityHeartbeatInterval = 10 * time.Millisecond
	defer func() {
		turnRunActivityHeartbeatInterval = previousInterval
	}()

	key := session.SessionKey{ChatID: 9911, UserID: 0, Scope: telegramDMScopeRef(9911)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "long provider request", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	if monitor.runID == 0 {
		t.Fatal("startTurnMonitor() did not create a turn run")
	}
	before, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(before) err = %v", err)
	}

	time.Sleep(35 * time.Millisecond)
	after, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(after) err = %v", err)
	}
	if !after.LastActivityAt.After(before.LastActivityAt) {
		t.Fatalf("last_activity_at = %s, want > %s", after.LastActivityAt.Format(time.RFC3339Nano), before.LastActivityAt.Format(time.RFC3339Nano))
	}

	monitor.Finish(context.Background(), nil)
}

func TestTurnMonitorToolAndTurnDurationsAreLedgered(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9912, UserID: 0, Scope: telegramDMScopeRef(9912)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "duration test", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	monitor.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"true"}`))
	monitor.ToolFinished(context.Background(), "exec", json.RawMessage(`{"command":"true"}`), "ok", nil)
	monitor.Finish(context.Background(), nil)

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertPayloadNonNegativeInt64(t, payloadForEventType(events, core.ExecutionEventToolSucceeded), "tool_duration_ms")
	assertPayloadNonNegativeInt64(t, payloadForEventType(events, core.ExecutionEventTurnCompleted), "turn_duration_ms")
}

func TestTurnMonitorMarksSideEffectingExecErrorsUncertain(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9915, UserID: 0, Scope: telegramDMScopeRef(9915)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "side effect uncertainty", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	input := json.RawMessage(`{"command":"sh -c 'touch did-run && false'"}`)
	monitor.ToolStarted(context.Background(), "exec", input)
	monitor.ToolFinished(context.Background(), "exec", input, "", errors.New("command failed with exit code 1"))
	monitor.Finish(context.Background(), nil)

	attempts, err := store.EffectAttemptsByTurnRun(key, monitor.runID)
	if err != nil {
		t.Fatalf("EffectAttemptsByTurnRun() err = %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %#v, want one side-effect attempt", attempts)
	}
	if attempts[0].Status != session.EffectAttemptStatusUncertain {
		t.Fatalf("attempt status = %q, want uncertain because a process error is not a rollback certificate", attempts[0].Status)
	}
}

func TestTurnMonitorMarksPreDispatchExecDenialsRejected(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9917, UserID: 0, Scope: telegramDMScopeRef(9917)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "pre-dispatch rejection", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	input := json.RawMessage(`{"command":"git push origin main"}`)
	monitor.ToolStarted(context.Background(), "exec", input)
	monitor.ToolFinished(context.Background(), "exec", input, "", toolpkg.ErrExecRejectedBeforeDispatch)
	monitor.Finish(context.Background(), nil)

	attempts, err := store.EffectAttemptsByTurnRun(key, monitor.runID)
	if err != nil {
		t.Fatalf("EffectAttemptsByTurnRun() err = %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %#v, want one side-effect attempt", attempts)
	}
	if attempts[0].Status != session.EffectAttemptStatusRejected {
		t.Fatalf("attempt status = %q, want rejected because dispatch never started", attempts[0].Status)
	}
}

func TestTurnMonitorRedactsCredentialBearingExecMetadata(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9916, UserID: 0, Scope: telegramDMScopeRef(9916)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "command metadata sensitivity", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	command := strings.Join([]string{
		`curl -H '`,
		"Author",
		`ization: `,
		"Bearer",
		` command-secret-value' 'https://example.test/hook?access_token=query-secret-value'`,
	}, "")
	inputBytes, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatalf("marshal tool input: %v", err)
	}
	input := json.RawMessage(inputBytes)
	monitor.ToolStarted(context.Background(), "exec", input)
	monitor.ToolFinished(context.Background(), "exec", input, "ok", nil)
	monitor.Finish(context.Background(), nil)

	events, err := store.ExecutionEventsByTurnRun(key, monitor.runID, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsByTurnRun() err = %v", err)
	}
	attempts, err := store.EffectAttemptsByTurnRun(key, monitor.runID)
	if err != nil {
		t.Fatalf("EffectAttemptsByTurnRun() err = %v", err)
	}
	var combined strings.Builder
	for _, event := range events {
		combined.WriteString(event.PayloadJSON)
		combined.WriteString("\n")
	}
	for _, attempt := range attempts {
		combined.WriteString(attempt.Command)
		combined.WriteString("\n")
		combined.WriteString(attempt.SubjectJSON)
		combined.WriteString("\n")
		combined.WriteString(attempt.ErrorText)
		combined.WriteString("\n")
	}
	got := combined.String()
	for _, secret := range []string{"command-secret-value", "query-secret-value"} {
		if strings.Contains(got, secret) {
			t.Fatalf("execution metadata leaked %q:\n%s", secret, got)
		}
	}
	for _, want := range []string{"<redacted:bearer:", "<redacted:url_query:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("execution metadata = %s, want redaction marker %q", got, want)
		}
	}
}

func TestTurnMonitorRecordsLargeToolOutputDigest(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9913, UserID: 0, Scope: telegramDMScopeRef(9913)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "large output test", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	output := "HEAD important\n" + strings.Repeat("middle output\n", 2000) + "TAIL important\n"
	monitor.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"large"}`))
	monitor.ToolFinished(context.Background(), "exec", json.RawMessage(`{"command":"large"}`), output, nil)
	monitor.Finish(context.Background(), nil)

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	payload := payloadForEventType(events, core.ExecutionEventToolSucceeded)
	digest, ok := payloadMap(payload, "result_digest")
	if !ok {
		t.Fatalf("tool succeeded payload = %#v, want result_digest", payload)
	}
	if payloadString(digest, "sha256") == "" || payloadString(digest, "omitted_bytes") == "" {
		t.Fatalf("result_digest = %#v, want sha256 and omitted metadata", digest)
	}
	if !strings.Contains(payloadString(digest, "head"), "HEAD important") || !strings.Contains(payloadString(digest, "tail"), "TAIL important") {
		t.Fatalf("result_digest = %#v, want head and tail evidence", digest)
	}
	evidenceRef := payloadString(digest, "evidence_ref")
	if evidenceRef == "" {
		t.Fatalf("result_digest = %#v, want evidence_ref for retained full output", digest)
	}
	obj, ok, err := store.EvidenceObject(evidenceRef)
	if err != nil || !ok {
		t.Fatalf("EvidenceObject(%q) ok=%t err=%v", evidenceRef, ok, err)
	}
	if obj.SourceKind != session.EvidenceSourceToolOutput || obj.EpistemicStatus != session.EvidenceStatusAttested {
		t.Fatalf("evidence object = %#v, want attested tool output", obj)
	}
	if obj.RedactionClass != session.EvidenceRedactionNone {
		t.Fatalf("redaction class = %q, want none for non-sensitive output", obj.RedactionClass)
	}
	evidencePayload := payloadMapFromJSON(obj.PayloadJSON)
	if !strings.Contains(payloadString(evidencePayload, "output"), "HEAD important") ||
		!strings.Contains(payloadString(evidencePayload, "output"), "middle output") ||
		!strings.Contains(payloadString(evidencePayload, "output"), "TAIL important") {
		t.Fatalf("tool output evidence payload = %#v, want full retained output", evidencePayload)
	}
}

func TestTurnMonitorRedactsSensitiveLargeToolOutputEvidence(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9914, UserID: 0, Scope: telegramDMScopeRef(9914)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "large sensitive output test", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	input := json.RawMessage(`{"command":"printf 'Authorization: Bearer input-secret-value'"}`)
	output := "HEAD OPENAI_API_KEY=sk-head-secret-value\n" +
		strings.Repeat("middle safe output\n", 2000) +
		`{"password":"pw-middle-secret-value"}` + "\n" +
		"TAIL Authorization: Bearer tail-secret-value\n"
	monitor.ToolStarted(context.Background(), "exec", input)
	monitor.ToolFinished(context.Background(), "exec", input, output, nil)
	monitor.Finish(context.Background(), nil)

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	payload := payloadForEventType(events, core.ExecutionEventToolSucceeded)
	digest, ok := payloadMap(payload, "result_digest")
	if !ok {
		t.Fatalf("tool succeeded payload = %#v, want result_digest", payload)
	}
	for _, secret := range []string{"sk-head-secret-value", "tail-secret-value"} {
		if strings.Contains(payloadString(digest, "head"), secret) || strings.Contains(payloadString(digest, "tail"), secret) {
			t.Fatalf("result_digest leaked %q: %#v", secret, digest)
		}
	}
	evidenceRef := payloadString(digest, "evidence_ref")
	if evidenceRef == "" {
		t.Fatalf("result_digest = %#v, want evidence_ref", digest)
	}
	obj, ok, err := store.EvidenceObject(evidenceRef)
	if err != nil || !ok {
		t.Fatalf("EvidenceObject(%q) ok=%t err=%v", evidenceRef, ok, err)
	}
	if obj.RedactionClass != session.EvidenceRedactionSecret {
		t.Fatalf("redaction class = %q, want credential-bearing", obj.RedactionClass)
	}
	evidencePayload := payloadMapFromJSON(obj.PayloadJSON)
	combined := payloadString(evidencePayload, "input_preview") + "\n" + payloadString(evidencePayload, "output") + "\n" + obj.Digest
	for _, secret := range []string{"input-secret-value", "sk-head-secret-value", "pw-middle-secret-value", "tail-secret-value"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("evidence leaked %q:\n%s", secret, combined)
		}
	}
	for _, want := range []string{"<redacted:bearer:", "<redacted:api_key:", "<redacted:password:"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("evidence = %s, want %q", combined, want)
		}
	}
	if got := payloadString(evidencePayload, "redaction_class"); got != session.EvidenceRedactionSecret {
		t.Fatalf("payload redaction_class = %q, want %q", got, session.EvidenceRedactionSecret)
	}
}

func TestLargeToolOutputRedactionDoesNotDependOnEvidencePersistence(t *testing.T) {
	output := "HEAD OPENAI_API_KEY=sk-head-secret-value\n" + strings.Repeat("middle\n", 20000) + "TAIL Authorization: Bearer tail-secret-value\n"
	digest, ok := agent.BuildToolOutputDigest(output, agent.DefaultToolOutputDigestInlineLimit)
	if !ok {
		t.Fatal("BuildToolOutputDigest ok=false, want large output digest")
	}
	safe := redactLargeToolOutputEvidence(`{"command":"echo token=input-secret-value"}`, output, digest)
	if safe.RedactionClass != session.EvidenceRedactionSecret {
		t.Fatalf("redaction class = %q, want credential-bearing", safe.RedactionClass)
	}
	for _, secret := range []string{"input-secret-value", "sk-head-secret-value", "tail-secret-value"} {
		if strings.Contains(safe.InputPreview.Text, secret) || strings.Contains(safe.Digest.Head, secret) || strings.Contains(safe.Digest.Tail, secret) {
			t.Fatalf("safe redaction leaked %q: input=%q head=%q tail=%q", secret, safe.InputPreview.Text, safe.Digest.Head, safe.Digest.Tail)
		}
	}
	for _, want := range []string{"<redacted:token:", "<redacted:api_key:", "<redacted:bearer:"} {
		if !strings.Contains(safe.InputPreview.Text+"\n"+safe.Digest.Head+"\n"+safe.Digest.Tail, want) {
			t.Fatalf("safe redaction missing marker %q: %#v", want, safe)
		}
	}
}

func payloadMapFromJSON(raw string) map[string]any {
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &payload)
	return payload
}

func TestTurnMonitorRecordsModelAndToolBatchEvents(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9914, UserID: 0, Scope: telegramDMScopeRef(9914)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "batch evidence", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	modelEvent := agent.ModelRequestEvent{
		Attempt:       2,
		HistoryCount:  7,
		ToolCount:     3,
		Duration:      2 * time.Millisecond,
		ToolCallCount: 2,
		OutputChars:   11,
		TokenUsage:    core.TokenUsage{InputTokens: 13, OutputTokens: 5, TotalTokens: 18},
	}
	monitor.ModelRequestStarted(context.Background(), modelEvent)
	monitor.ModelRequestFinished(context.Background(), modelEvent)
	batchEvent := agent.ToolBatchEvent{
		Mode:              "parallel",
		BatchSize:         2,
		ToolNames:         []string{"read_file", "search"},
		Duration:          3 * time.Millisecond,
		FailedCount:       1,
		ParallelEligible:  true,
		ParallelSafeCount: 2,
	}
	monitor.ToolBatchStarted(context.Background(), batchEvent)
	monitor.ToolBatchFinished(context.Background(), batchEvent)
	monitor.Finish(context.Background(), nil)

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	modelPayload := payloadForEventType(events, core.ExecutionEventModelRequestSucceeded)
	if got, ok := payloadInt64(modelPayload, "attempt"); !ok || got != 2 {
		t.Fatalf("model attempt payload = %#v, want attempt 2", modelPayload)
	}
	if got, ok := payloadInt64(modelPayload, "total_tokens"); !ok || got != 18 {
		t.Fatalf("model token payload = %#v, want total_tokens 18", modelPayload)
	}
	batchPayload := payloadForEventType(events, core.ExecutionEventToolBatchCompleted)
	if payloadString(batchPayload, "mode") != "parallel" {
		t.Fatalf("tool batch payload = %#v, want parallel mode", batchPayload)
	}
	if got, ok := payloadInt64(batchPayload, "failed_count"); !ok || got != 1 {
		t.Fatalf("tool batch failed_count payload = %#v, want 1", batchPayload)
	}
	if got := payloadStringSlice(batchPayload, "tools"); len(got) != 2 || got[0] != "read_file" || got[1] != "search" {
		t.Fatalf("tool batch tools payload = %#v, want read_file/search", batchPayload)
	}
	if got, ok := payloadBool(batchPayload, "parallel_eligible"); !ok || !got {
		t.Fatalf("tool batch parallel eligibility payload = %#v, want true", batchPayload)
	}
	if got, ok := payloadInt64(batchPayload, "parallel_safe_count"); !ok || got != 2 {
		t.Fatalf("tool batch parallel safe count payload = %#v, want 2", batchPayload)
	}
}
