//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/idolum-ai/aphelion/session"
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
	PolicyRef            string `json:"policy_ref"`
	ProtectedEvidenceRef string `json:"protected_evidence_ref,omitempty"`
}

type projectedToolFailureError struct {
	safe string
	raw  error
}

func (e projectedToolFailureError) Error() string {
	if strings.TrimSpace(e.safe) == "" {
		return "tool execution failed"
	}
	return e.safe
}

func (e projectedToolFailureError) Unwrap() error {
	return e.raw
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

func classifyProjectedToolFailure(err error, output string) (string, string) {
	if errors.Is(err, context.Canceled) {
		return "canceled", "do_not_retry"
	}
	lower := strings.ToLower(strings.TrimSpace(output + "\n" + errorString(err)))
	switch {
	case strings.Contains(lower, "authority") || strings.Contains(lower, "approval") || strings.Contains(lower, "grant") || strings.Contains(lower, "permission") || strings.Contains(lower, "denied"):
		return "authority_rejected", "ask_for_grant"
	case strings.Contains(lower, "deadline") || strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		return "timeout", "retry_once"
	default:
		return "tool_error", "reformulate"
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
