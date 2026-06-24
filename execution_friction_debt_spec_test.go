//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func TestExecutionFrictionDebtSpecOutputExposure(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 1, 2, 3, 4)

	store := newExecutionFrictionStore(t)
	key := session.SessionKey{ChatID: 57001, UserID: 1001}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "inspect read-only output exposure")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}

	canary := "example-redaction-canary-value"
	metadataPath := "/workspace/example/credential-slot"
	rawPreview := "stdout:\ntoken: " + canary + "\npath: " + metadataPath
	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"cat example"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.NoteTurnRunToolFinish(run.ID, rawPreview, ""); err != nil {
		t.Fatalf("NoteTurnRunToolFinish() err = %v", err)
	}
	gotRun, err := store.TurnRun(run.ID)
	if err != nil {
		t.Fatalf("TurnRun() err = %v", err)
	}

	redacted := session.ProjectToolResultForAudience(rawPreview, session.ExposureAudienceModelPreview)
	projections := []frictionExposureProjection{
		{
			Name:      "redacted evidence projection",
			Audience:  "model",
			Value:     redacted.Text,
			Ordinary:  true,
			PolicyRef: redacted.PolicyRef,
		},
		{
			Name:      "persisted turn-run result preview",
			Audience:  "model",
			Value:     gotRun.LastToolResultPreview,
			Ordinary:  true,
			PolicyRef: exposurePolicyRefFromPreview(gotRun.LastToolResultPreview),
		},
	}
	assertOrdinaryExposureUsesSafeProductionProjection(t, projections, []string{canary, metadataPath})
}

func TestExecutionFrictionDebtSpecUncertainEffectNeedsVerification(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 7, 8)

	store := newExecutionFrictionStore(t)
	key := session.SessionKey{ChatID: 57002, UserID: 1001}
	now := time.Now().UTC()
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "approve and continue")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	attempt := session.EffectAttemptInput{
		AttemptID:    "friction-uncertain-attempt",
		Key:          key,
		TurnRunID:    run.ID,
		OperationID:  "op-friction",
		PhaseID:      "phase-friction",
		LeaseID:      "lease-friction",
		Executor:     "exec",
		Tool:         "exec",
		Command:      "git push origin example",
		EffectKind:   string(commandeffect.KindExternal),
		EffectReason: "remote publication",
		Status:       session.EffectAttemptStatusUncertain,
		ErrorText:    "outcome unavailable",
		StartedAt:    now,
		UpdatedAt:    now,
	}
	if _, err := store.UpsertEffectAttempt(attempt); err != nil {
		t.Fatalf("UpsertEffectAttempt() err = %v", err)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	nextStates := frictionNextStatesFromEvents(events)
	assertDebtSpecNextStateExists(t, nextStates, "needs_verification", "friction-uncertain-attempt")
}

func TestExecutionFrictionDebtSpecApprovedContinuationNextState(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 5, 6)

	store := newExecutionFrictionStore(t)
	key := session.SessionKey{ChatID: 57006, UserID: 1001}
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "continuation",
		State:              session.NextActionReadyToExecute,
		SubjectKind:        "continuation_lease",
		SubjectRef:         "lease-friction",
		NextAction:         "execute the approved continuation turn",
		RequiredAuthority:  "workspace_write",
		OperatorProjection: "Approved continuation is ready for one bounded execution turn.",
	}); err != nil {
		t.Fatalf("RecordNextAction(ready) err = %v", err)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	nextStates := frictionNextStatesFromEvents(events)
	assertDebtSpecNextStateExists(t, nextStates, "ready_to_execute", "lease-friction")
}

func TestExecutionFrictionDebtSpecPhaseSupersessionNextState(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 13)

	store := newExecutionFrictionStore(t)
	key := session.SessionKey{ChatID: 57007, UserID: 1001}
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "continuation",
		State:              session.NextActionSuperseded,
		SubjectKind:        "phase",
		SubjectRef:         "phase-friction",
		NextAction:         "retire the stale phase and use the current operation state",
		RetryPolicy:        "do_not_execute_superseded_projection",
		OperatorProjection: "The earlier phase projection was superseded.",
	}); err != nil {
		t.Fatalf("RecordNextAction(superseded) err = %v", err)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	nextStates := frictionNextStatesFromEvents(events)
	assertDebtSpecNextStateExists(t, nextStates, "superseded", "phase-friction")
}

func TestExecutionFrictionDebtSpecTypedOperationAlternatives(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 9, 10, 11, 12)

	plannerCases := []struct {
		name        string
		command     string
		wantBlocked bool
	}{
		{
			name:        "path-qualified executable",
			command:     "./bin/repair-child-config",
			wantBlocked: true,
		},
		{
			name:        "interpreter repair",
			command:     "python -c 'open(\"child.conf\", \"w\").write(\"x\")'",
			wantBlocked: true,
		},
		{
			name:        "multi-effect repair",
			command:     "mkdir -p out && git push origin example",
			wantBlocked: true,
		},
	}
	var failures []string
	for _, tc := range plannerCases {
		plan := commandeffect.PlanCommand(tc.command)
		blocked := plan.Dynamic || plan.MultipleAuthorities || commandeffect.RepresentativeEffect(plan).Kind == commandeffect.KindUnknown
		if tc.wantBlocked && !blocked {
			failures = append(failures, fmt.Sprintf("%s was not rejected by production planner: %#v", tc.name, plan))
		}
		if tc.wantBlocked && !productionTypedAlternativeAvailable(tc.name) {
			failures = append(failures, fmt.Sprintf("%s is safely rejected but no typed replacement operation is registered", tc.name))
		}
	}

	failFrictionDebtSpec(t, failures)
}

func TestExecutionFrictionDebtSpecCapabilityInvocationOutcome(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 14, 15, 17)

	store := newExecutionFrictionStore(t)
	key := session.SessionKey{ChatID: 57003, UserID: 1001}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "invoke child-local resource")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	invocation, err := store.RecordCapabilityInvocation(session.CapabilityInvocation{
		GrantID:              "grant-friction-file-read",
		Principal:            "telegram:1001",
		Action:               "read_file",
		Status:               "allowed",
		SessionID:            session.SessionIDForKey(key),
		TurnRunID:            run.ID,
		ContinuationLeaseID:  "lease-friction-file",
		AuthoritySource:      "continuation_lease",
		OutcomeStatus:        "",
		OutcomeErrorText:     "",
		OperationPlanLeaseID: "",
	})
	if err != nil {
		t.Fatalf("RecordCapabilityInvocation() err = %v", err)
	}
	invocation, err = store.CompleteCapabilityInvocation(invocation.InvocationID, "failed", "simulated downstream failure", time.Now().UTC())
	if err != nil {
		t.Fatalf("CompleteCapabilityInvocation() err = %v", err)
	}
	var failures []string
	if invocation.TurnRunID == 0 || invocation.ContinuationLeaseID == "" || invocation.AuthoritySource == "" {
		failures = append(failures, "capability invocation lacks causal run or lease evidence")
	}
	if invocation.OutcomeStatus == "" {
		failures = append(failures, "capability invocation has authorization evidence but no terminal execution outcome")
	}
	failFrictionDebtSpec(t, failures)
}

func TestExecutionFrictionDebtSpecResourcePreflight(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 16)

	store := newExecutionFrictionStore(t)
	key := session.SessionKey{ChatID: 57008, UserID: 1001}
	if err := store.RecordResourcePreflight(key, 0, "/host/denied", "host_mode_denied", "host-mode resource denial requires a narrower root or permission repair", time.Now().UTC()); err != nil {
		t.Fatalf("RecordResourcePreflight() err = %v", err)
	}
	var failures []string
	if !productionResourcePreflightClassifiesHostModeDenial(store, key) {
		failures = append(failures, "host-mode resource denial has no production preflight classifier")
	}
	failFrictionDebtSpec(t, failures)
}

func TestExecutionFrictionDebtSpecDurableChildProtocol(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 18, 19)

	store := newExecutionFrictionStore(t)
	key := session.SessionKey{ChatID: 57004, UserID: 1001}
	now := time.Now().UTC()
	if err := appendFrictionEvent(store, key, core.ExecutionEventDurableWakeStarted, "durable", "started", map[string]any{
		"agent_id":       "child-example",
		"turn":           1,
		"task_packet_id": "child_task:friction",
	}, now); err != nil {
		t.Fatalf("append durable wake event: %v", err)
	}
	if err := appendFrictionEvent(store, key, core.ExecutionEventDurableWakeCompleted, "durable", "completed", map[string]any{
		"agent_id":        "child-example",
		"typed_result_id": "child_result:friction",
		"next_action":     "review child result or continue the bounded task",
	}, now.Add(5*time.Second)); err != nil {
		t.Fatalf("append durable wake completed event: %v", err)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertDurableChildProductionEventsConverge(t, events)
}

func TestExecutionFrictionDebtSpecRestartSpanningChildToolFlow(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 24)

	store := newExecutionFrictionStore(t)
	key := session.SessionKey{ChatID: 57009, UserID: 1001}
	if err := appendFrictionEvent(store, key, "execution_authority.restart_spanning_tool_flow", "execution_authority", "verified", map[string]any{
		"valid_authority_after_reopen": true,
		"revocation_observed":          true,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("append restart-spanning proof: %v", err)
	}
	assertRestartSpanningNativeWorkProofExists(t, store, key)
}

func TestExecutionFrictionDebtSpecContextStatusAndReliability(t *testing.T) {
	requireExecutionFrictionDebtSpecs(t)
	assertExecutionFrictionDebtSpecShape(t, 20, 21, 22, 23)

	store := newExecutionFrictionStore(t)
	key := session.SessionKey{ChatID: 57005, UserID: 1001}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "long repair loop")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolFinish(run.ID, strings.Repeat("repair loop detail\n", 800), ""); err != nil {
		t.Fatalf("NoteTurnRunToolFinish() err = %v", err)
	}
	if err := appendFrictionEvent(store, key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
		"classification": "external_transient",
		"retry_policy":   "bounded_backoff",
	}, time.Now().UTC()); err != nil {
		t.Fatalf("append provider failure: %v", err)
	}
	if err := store.RecordPersistenceLatencyClassification(key, "execution_events", 500*time.Millisecond, time.Now().UTC()); err != nil {
		t.Fatalf("RecordPersistenceLatencyClassification() err = %v", err)
	}

	var failures []string
	gotRun, err := store.TurnRun(run.ID)
	if err != nil {
		t.Fatalf("TurnRun() err = %v", err)
	}
	if !productionCompactStatePacketAvailable(gotRun) {
		failures = append(failures, "long repair loop has no compact current-state packet")
	}
	statusClassification := productionSourceInstallStatusClassification()
	if statusClassification != "release_update_available" {
		failures = append(failures, fmt.Sprintf("source install status classified as %q, want release_update_available", statusClassification))
	}
	if !productionPersistenceLatencyClassified(store, key) {
		failures = append(failures, "TES latency has no production operational-amplifier classification")
	}
	if !productionProviderTransientClassified(store, key) {
		failures = append(failures, "provider transient is not classified with bounded retry semantics")
	}
	failFrictionDebtSpec(t, failures)
}

func requireExecutionFrictionDebtSpecs(t *testing.T) {
	t.Helper()
	if os.Getenv("APHELION_RUN_FRICTION_EVALS") != "1" {
		t.Skip("set APHELION_RUN_FRICTION_EVALS=1 to run execution-friction executable debt specs")
	}
}

func newExecutionFrictionStore(t *testing.T) *session.SQLiteStore {
	t.Helper()
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "aphelion-test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type frictionExposureProjection struct {
	Name      string
	Audience  string
	Value     string
	Ordinary  bool
	PolicyRef string
}

func assertOrdinaryExposureUsesSafeProductionProjection(t *testing.T, projections []frictionExposureProjection, canaries []string) {
	t.Helper()

	var failures []string
	for _, projection := range projections {
		if strings.TrimSpace(projection.Name) == "" || strings.TrimSpace(projection.Audience) == "" {
			failures = append(failures, "projection missing name or audience")
			continue
		}
		if projection.Ordinary && strings.TrimSpace(projection.PolicyRef) == "" {
			failures = append(failures, fmt.Sprintf("%s has no production exposure policy reference", projection.Name))
		}
		if !projection.Ordinary {
			continue
		}
		for _, canary := range canaries {
			if canary != "" && strings.Contains(projection.Value, canary) {
				failures = append(failures, fmt.Sprintf("%s exposes raw canary to %s", projection.Name, projection.Audience))
			}
		}
	}
	failFrictionDebtSpec(t, failures)
}

func exposurePolicyRefFromPreview(preview string) string {
	for _, line := range strings.Split(preview, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "policy_ref:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "policy_ref:"))
		}
	}
	return ""
}

type frictionNextState struct {
	State      string
	SubjectRef string
	Owner      string
	NextAction string
}

func frictionNextStatesFromEvents(events []session.ExecutionEvent) []frictionNextState {
	var out []frictionNextState
	for _, event := range events {
		if event.EventType != "workflow.next_state" {
			continue
		}
		var payload struct {
			State      string `json:"state"`
			SubjectRef string `json:"subject_ref"`
			Owner      string `json:"owner"`
			NextAction string `json:"next_action"`
		}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		out = append(out, frictionNextState{
			State:      strings.TrimSpace(payload.State),
			SubjectRef: strings.TrimSpace(payload.SubjectRef),
			Owner:      strings.TrimSpace(payload.Owner),
			NextAction: strings.TrimSpace(payload.NextAction),
		})
	}
	return out
}

func assertDebtSpecNextStateExists(t *testing.T, states []frictionNextState, wantState string, subjectContains string) {
	t.Helper()
	for _, state := range states {
		if state.State == wantState && strings.Contains(state.SubjectRef, subjectContains) && state.Owner != "" && state.NextAction != "" {
			return
		}
	}
	t.Fatalf("no production workflow.next_state event for state=%q subject containing %q with owner and next action; got %#v", wantState, subjectContains, states)
}

func productionTypedAlternativeAvailable(rejectedShape string) bool {
	_, ok := toolpkg.TypedRepairOperationForRejectedShape(rejectedShape)
	return ok
}

func productionResourcePreflightClassifiesHostModeDenial(store *session.SQLiteStore, key session.SessionKey) bool {
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		return false
	}
	for _, event := range events {
		if event.EventType == "resource.preflight" && strings.Contains(event.PayloadJSON, "host_mode_denied") {
			return true
		}
	}
	return false
}

func assertDurableChildProductionEventsConverge(t *testing.T, events []session.ExecutionEvent) {
	t.Helper()
	wakes := 0
	var failures []string
	for _, event := range events {
		if event.EventType == core.ExecutionEventDurableWakeStarted {
			wakes++
			if !strings.Contains(event.PayloadJSON, "task_packet_id") || strings.Contains(event.PayloadJSON, `"task_packet_id":""`) {
				failures = append(failures, "durable wake start lacks compact task packet id")
			}
		}
		if event.EventType == core.ExecutionEventDurableWakeCompleted {
			if strings.Contains(event.PayloadJSON, `"typed_result_id":""`) {
				failures = append(failures, "durable wake completion lacks typed result id")
			}
			if strings.Contains(event.PayloadJSON, `"next_action":""`) {
				failures = append(failures, "durable wake completion lacks bounded next action")
			}
		}
	}
	if wakes > 1 {
		failures = append(failures, fmt.Sprintf("small child task used %d wake/report cycles, want one compact wake", wakes))
	}
	failFrictionDebtSpec(t, failures)
}

func assertRestartSpanningNativeWorkProofExists(t *testing.T, store *session.SQLiteStore, key session.SessionKey) {
	t.Helper()
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(restart proof) err = %v", err)
	}
	for _, event := range events {
		if event.EventType == "execution_authority.restart_spanning_tool_flow" &&
			strings.Contains(event.PayloadJSON, "revocation_observed") {
			return
		}
	}
	t.Fatalf("no production restart-spanning child/native-work-to-tool proof with revocation observation")
}

func productionCompactStatePacketAvailable(run *session.TurnRun) bool {
	if run == nil {
		return false
	}
	preview := strings.TrimSpace(run.LastToolResultPreview)
	return strings.Contains(preview, "compact_current_state") && len(preview) < 2000
}

func productionSourceInstallStatusClassification() string {
	return core.ClassifySourceInstallReliability(core.SourceInstallStatusInput{
		RunningRevision:  "abc123",
		ExpectedRevision: "abc123",
		LatestVersion:    "v0.3.0",
		MetadataStatus:   "current",
		UpdateAvailable:  true,
	}).Condition
}

func productionPersistenceLatencyClassified(store *session.SQLiteStore, key session.SessionKey) bool {
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		return false
	}
	for _, event := range events {
		if event.EventType == "persistence.latency_classified" {
			return true
		}
	}
	return false
}

func productionProviderTransientClassified(store *session.SQLiteStore, key session.SessionKey) bool {
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		return false
	}
	for _, event := range events {
		if event.EventType == core.ExecutionEventProviderAttemptFailed &&
			strings.Contains(event.PayloadJSON, "external_transient") &&
			strings.Contains(event.PayloadJSON, "bounded_backoff") {
			return true
		}
	}
	return false
}

func appendFrictionEvent(store *session.SQLiteStore, key session.SessionKey, eventType string, stage string, status string, payload map[string]any, createdAt time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   eventType,
		Stage:       stage,
		Status:      status,
		PayloadJSON: string(raw),
		CreatedAt:   createdAt,
	}})
	return err
}

func failFrictionDebtSpec(t *testing.T, failures []string) {
	t.Helper()
	if len(failures) == 0 {
		return
	}
	t.Fatalf("execution-friction executable debt spec found unresolved gaps:\n- %s", strings.Join(failures, "\n- "))
}
