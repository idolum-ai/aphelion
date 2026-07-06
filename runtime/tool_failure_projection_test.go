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
	"github.com/idolum-ai/aphelion/session"
)

type secretBearingToolError struct {
	text string
}

func (e *secretBearingToolError) Error() string {
	return e.text
}

type typedSafeToolFailure struct{}

func (typedSafeToolFailure) Error() string {
	return "adapter_lifecycle_failed: safe wake failure"
}

func (typedSafeToolFailure) SafeToolFailureClass() string {
	return "adapter_lifecycle_failed"
}

func (typedSafeToolFailure) SafeToolFailureSummary() string {
	return "durable_agent wake_once stopped because the child adapter lifecycle is not registered or verified"
}

func (typedSafeToolFailure) SafeToolFailureRetryPolicy() string {
	return "retry_after_adapter_lifecycle_repair"
}

func TestObservedToolRegistryProjectedFailurePreservesTypedSafeFailure(t *testing.T) {
	t.Parallel()

	_, registry, _ := newObservedFailureRegistry(t, "safe wake output", typedSafeToolFailure{})
	output, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"fail"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want projected typed failure")
	}
	if err.Error() != "durable_agent wake_once stopped because the child adapter lifecycle is not registered or verified" {
		t.Fatalf("projected err = %q, want typed safe summary", err.Error())
	}
	var failure map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &failure); jsonErr != nil {
		t.Fatalf("projected failure json: %v\n%s", jsonErr, output)
	}
	if asString(failure["failure_class"]) != "adapter_lifecycle_failed" {
		t.Fatalf("failure_class = %#v, want adapter_lifecycle_failed", failure["failure_class"])
	}
	if asString(failure["retry_policy"]) != "retry_after_adapter_lifecycle_repair" {
		t.Fatalf("retry_policy = %#v, want typed retry policy", failure["retry_policy"])
	}
	if asString(failure["safe_summary"]) != err.Error() {
		t.Fatalf("safe_summary = %#v, want %q", failure["safe_summary"], err.Error())
	}
}

func TestObservedToolRegistryProjectedFailureDoesNotExposeRawErrorObject(t *testing.T) {
	t.Parallel()

	rawToken := "github_pat_errorobject1234567890"
	rawPath := "/workspace/credential-object"
	store, registry, key := newObservedFailureRegistry(t, "stdout token="+rawToken, &secretBearingToolError{text: "provider failed token=" + rawToken + " path: " + rawPath})
	output, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"fail"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want projected failure error")
	}
	if strings.Contains(output, rawToken) || strings.Contains(output, rawPath) || strings.Contains(err.Error(), rawToken) || strings.Contains(err.Error(), rawPath) {
		t.Fatalf("projected failure leaked raw material output=%q err=%q", output, err.Error())
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		t.Fatalf("errors.Unwrap(err) = %T %q, want nil", unwrapped, unwrapped.Error())
	}
	var recovered *secretBearingToolError
	if errors.As(err, &recovered) {
		t.Fatalf("errors.As recovered raw secret-bearing error: %T %q", recovered, recovered.Error())
	}

	var failure map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &failure); jsonErr != nil {
		t.Fatalf("projected failure json: %v\n%s", jsonErr, output)
	}
	protectedRef := asString(failure["protected_evidence_ref"])
	if protectedRef == "" {
		t.Fatalf("projected failure missing protected ref: %#v", failure)
	}
	protected, ok, evidenceErr := store.EvidenceObject(protectedRef)
	if evidenceErr != nil || !ok {
		t.Fatalf("EvidenceObject(%s) ok=%t err=%v", protectedRef, ok, evidenceErr)
	}
	if protected.RedactionClass != session.EvidenceRedactionBlocked {
		t.Fatalf("protected redaction class = %q, want non_hydratable", protected.RedactionClass)
	}
	if !strings.Contains(protected.PayloadJSON, rawToken) || !strings.Contains(protected.PayloadJSON, rawPath) {
		t.Fatalf("protected payload did not retain raw failure detail: %s", protected.PayloadJSON)
	}
	hydrated, hydrateErr := store.HydrateEvidence(session.EvidenceHydrationQuery{
		Key:                 key,
		Query:               "inspect projected failure",
		RequiredEvidenceIDs: []string{protectedRef},
		Limit:               10,
	})
	if hydrateErr != nil {
		t.Fatalf("HydrateEvidence() err = %v", hydrateErr)
	}
	if len(hydrated.Required) != 1 || strings.TrimSpace(hydrated.Required[0].PayloadJSON) != "{}" {
		t.Fatalf("hydrated required = %#v, want metadata with empty payload", hydrated.Required)
	}
}

func TestObservedToolRegistryProjectedFailureSurfacesSafeNoSuchFileDiagnostic(t *testing.T) {
	t.Parallel()

	relativePath := "packages/react-core/src/v2/components/renderers/__tests__/A2UIRecoveryStates.test.tsx"
	rawOutput := "stderr:\nsed: can't read " + relativePath + ": No such file or directory\n"
	_, registry, _ := newObservedFailureRegistry(t, rawOutput, errors.New("command failed with exit code 2"))
	output, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"sed -n '1,260p' packages/react-core/src/v2/components/renderers/__tests__/A2UIRecoveryStates.test.tsx"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want projected failure error")
	}
	var failure map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &failure); jsonErr != nil {
		t.Fatalf("projected failure json: %v\n%s", jsonErr, output)
	}
	summary := asString(failure["safe_summary"])
	if !strings.Contains(summary, "No such file or directory") || !strings.Contains(summary, relativePath) {
		t.Fatalf("safe_summary = %q, want actionable missing-file diagnostic", summary)
	}
	if !strings.Contains(err.Error(), "No such file or directory") {
		t.Fatalf("projected err = %q, want actionable missing-file diagnostic", err.Error())
	}
	if asString(failure["protected_evidence_ref"]) == "" {
		t.Fatalf("projected failure = %#v, want protected evidence retained for raw stderr", failure)
	}
}

func TestObservedToolRegistryProjectedFailurePreservesContextSentinelsSafely(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		target       error
		flag         string
		failureClass string
		retryable    bool
	}{
		{
			name:         "canceled",
			err:          context.Canceled,
			target:       context.Canceled,
			flag:         "context_cancelled",
			failureClass: "canceled",
		},
		{
			name:         "deadline",
			err:          context.DeadlineExceeded,
			target:       context.DeadlineExceeded,
			flag:         "deadline_exceeded",
			failureClass: "timeout",
			retryable:    true,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, registry, _ := newObservedFailureRegistry(t, "", tc.err)
			output, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"fail"}`))
			if err == nil {
				t.Fatal("Execute() err = nil, want projected failure error")
			}
			if !errors.Is(err, tc.target) {
				t.Fatalf("errors.Is(%T, %v) = false, want true", err, tc.target)
			}
			if errors.Unwrap(err) != nil {
				t.Fatalf("errors.Unwrap(err) = %#v, want nil", errors.Unwrap(err))
			}
			var failure map[string]any
			if jsonErr := json.Unmarshal([]byte(output), &failure); jsonErr != nil {
				t.Fatalf("projected failure json: %v\n%s", jsonErr, output)
			}
			if asString(failure["failure_class"]) != tc.failureClass {
				t.Fatalf("failure_class = %#v, want %s", failure["failure_class"], tc.failureClass)
			}
			if got, _ := failure["retryable"].(bool); got != tc.retryable {
				t.Fatalf("retryable = %#v, want %t", failure["retryable"], tc.retryable)
			}
			if got, _ := failure[tc.flag].(bool); !got {
				t.Fatalf("%s = %#v, want true in %#v", tc.flag, failure[tc.flag], failure)
			}
		})
	}
}

func TestClassifyProjectedToolFailureDoesNotTreatApprovalPathAsAuthority(t *testing.T) {
	t.Parallel()

	signals := classifyProjectedToolFailure(
		errors.New("command failed with exit code 2"),
		"sed: can't read runtime/continuation_approval.go: No such file or directory",
	)
	if signals.FailureClass != "tool_error" || signals.RetryPolicy != "reformulate" {
		t.Fatalf("classifyProjectedToolFailure(approval path missing) = %#v, want ordinary tool_error", signals)
	}

	signals = classifyProjectedToolFailure(
		errors.New("command failed with exit code 1"),
		"cat: grant-only-authority-bundle.md: No such file or directory",
	)
	if signals.FailureClass != "tool_error" || signals.RetryPolicy != "reformulate" {
		t.Fatalf("classifyProjectedToolFailure(grant path missing) = %#v, want ordinary tool_error", signals)
	}
}

func TestClassifyProjectedToolFailureRecognizesAuthorityPhrases(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"authority required; details protected",
		"missing capability grant; review request capg-runtime-read is required",
		"command requires an approved proposal: workspace escape",
		"read_file open /secret: permission denied",
	} {
		signals := classifyProjectedToolFailure(errors.New(text), "")
		if signals.FailureClass != "authority_rejected" || signals.RetryPolicy != "ask_for_grant" {
			t.Fatalf("classifyProjectedToolFailure(%q) = %#v, want authority rejection", text, signals)
		}
	}
}

func newObservedFailureRegistry(t *testing.T, output string, err error) (*session.SQLiteStore, *observedToolRegistry, session.SessionKey) {
	t.Helper()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	tools := &principalRecordingTools{
		defs:              []agent.ToolDef{testExecToolDef()},
		supportsPrincipal: true,
		output:            output,
		err:               err,
	}
	rt, newErr := New(cfg, store, provider, tools, sender)
	if newErr != nil {
		t.Fatalf("New() err = %v", newErr)
	}
	key := session.SessionKey{ChatID: 506, UserID: 0}
	run, runErr := store.BeginTurnRun(key, session.TurnRunKindInteractive, "failure projection direct test")
	if runErr != nil {
		t.Fatalf("BeginTurnRun() err = %v", runErr)
	}
	monitor := &turnMonitor{
		runtime:    rt,
		key:        key,
		runID:      run.ID,
		startedAt:  time.Now().UTC(),
		toolStarts: make(map[string][]time.Time),
	}
	return store, &observedToolRegistry{base: tools, observer: monitor}, key
}
