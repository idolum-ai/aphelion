//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

type toolOutputProjection struct {
	Output   string
	Err      error
	Recorded bool
}

type projectedToolFailure struct {
	OK                   bool   `json:"ok"`
	SafeSummary          string `json:"safe_summary"`
	FailureClass         string `json:"failure_class"`
	RetryPolicy          string `json:"retry_policy"`
	Retryable            bool   `json:"retryable"`
	ContextCancelled     bool   `json:"context_cancelled,omitempty"`
	DeadlineExceeded     bool   `json:"deadline_exceeded,omitempty"`
	PolicyRef            string `json:"policy_ref"`
	ProtectedEvidenceRef string `json:"protected_evidence_ref,omitempty"`
}

type projectedToolFailureError struct {
	safe                 string
	failureClass         string
	retryable            bool
	contextCancelled     bool
	deadlineExceeded     bool
	execRejected         bool
	policyRef            string
	protectedEvidenceRef string
}

func (e projectedToolFailureError) Error() string {
	if strings.TrimSpace(e.safe) == "" {
		return "tool execution failed"
	}
	return e.safe
}

func (e projectedToolFailureError) Is(target error) bool {
	switch target {
	case context.Canceled:
		return e.contextCancelled
	case context.DeadlineExceeded:
		return e.deadlineExceeded
	case toolpkg.ErrExecRejectedBeforeDispatch:
		return e.execRejected
	default:
		return false
	}
}

func (e projectedToolFailureError) ProjectedToolFailure() bool {
	return true
}

func renderProjectedToolFailure(failure projectedToolFailure) string {
	failure.OK = false
	if strings.TrimSpace(failure.SafeSummary) == "" {
		failure.SafeSummary = "tool execution failed"
	}
	if strings.TrimSpace(failure.FailureClass) == "" {
		failure.FailureClass = "tool_error"
	}
	if strings.TrimSpace(failure.RetryPolicy) == "" {
		failure.RetryPolicy = "reformulate"
	}
	if strings.TrimSpace(failure.PolicyRef) == "" {
		failure.PolicyRef = session.ExposureProjectionPolicyToolOutputV1
	}
	raw, err := json.Marshal(failure)
	if err != nil {
		return `{"ok":false,"safe_summary":"tool execution failed","failure_class":"tool_error","retry_policy":"reformulate","policy_ref":"session.exposure_projection.tool_output/v1"}`
	}
	return string(raw)
}

func projectedToolFailurePayload(output string) (map[string]any, bool) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, false
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, false
	}
	ok, hasOK := payload["ok"].(bool)
	if !hasOK || ok {
		return nil, false
	}
	if _, ok := payload["safe_summary"].(string); !ok {
		return nil, false
	}
	if _, ok := payload["failure_class"].(string); !ok {
		return nil, false
	}
	if _, ok := payload["retry_policy"].(string); !ok {
		return nil, false
	}
	if _, ok := payload["policy_ref"].(string); !ok {
		return nil, false
	}
	return payload, true
}

type projectedToolFailureSignals struct {
	FailureClass     string
	SafeSummary      string
	RetryPolicy      string
	Retryable        bool
	ContextCancelled bool
	DeadlineExceeded bool
	ExecRejected     bool
}

type safeProjectedToolFailure interface {
	SafeToolFailureClass() string
	SafeToolFailureSummary() string
	SafeToolFailureRetryPolicy() string
}

func classifyProjectedToolFailure(err error, output string) projectedToolFailureSignals {
	var safeFailure safeProjectedToolFailure
	if errors.As(err, &safeFailure) {
		failureClass := strings.TrimSpace(safeFailure.SafeToolFailureClass())
		if failureClass == "" {
			failureClass = "tool_error"
		}
		retryPolicy := strings.TrimSpace(safeFailure.SafeToolFailureRetryPolicy())
		if retryPolicy == "" {
			retryPolicy = "reformulate"
		}
		return projectedToolFailureSignals{
			FailureClass: failureClass,
			SafeSummary:  strings.TrimSpace(safeFailure.SafeToolFailureSummary()),
			RetryPolicy:  retryPolicy,
			Retryable:    strings.Contains(retryPolicy, "retry") || strings.Contains(retryPolicy, "backoff"),
		}
	}
	if errors.Is(err, context.Canceled) {
		return projectedToolFailureSignals{
			FailureClass:     "canceled",
			RetryPolicy:      "do_not_retry",
			ContextCancelled: true,
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return projectedToolFailureSignals{
			FailureClass:     "timeout",
			RetryPolicy:      "retry_once",
			Retryable:        true,
			DeadlineExceeded: true,
		}
	}
	if errors.Is(err, toolpkg.ErrExecRejectedBeforeDispatch) {
		return projectedToolFailureSignals{
			FailureClass: "authority_rejected",
			RetryPolicy:  "ask_for_grant",
			ExecRejected: true,
		}
	}
	lower := strings.ToLower(strings.TrimSpace(output + "\n" + errorString(err)))
	switch {
	case strings.Contains(lower, "authority") || strings.Contains(lower, "approval") || strings.Contains(lower, "grant") || strings.Contains(lower, "permission") || strings.Contains(lower, "denied"):
		return projectedToolFailureSignals{FailureClass: "authority_rejected", RetryPolicy: "ask_for_grant"}
	case strings.Contains(lower, "deadline") || strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		return projectedToolFailureSignals{FailureClass: "timeout", RetryPolicy: "retry_once", Retryable: true, DeadlineExceeded: true}
	default:
		return projectedToolFailureSignals{FailureClass: "tool_error", RetryPolicy: "reformulate"}
	}
}

func safeToolFailureSummary(failureClass string, protectedRef string) string {
	summary := "tool execution failed"
	switch strings.TrimSpace(failureClass) {
	case "authority_rejected":
		summary = "tool execution failed: authority required"
	case "timeout":
		summary = "tool execution failed: timeout"
	case "canceled":
		summary = "tool execution canceled"
	}
	if strings.TrimSpace(protectedRef) != "" {
		summary += "; details protected"
	}
	return summary
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
