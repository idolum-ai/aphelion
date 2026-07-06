//go:build linux

package tool

import "strings"

type safeToolFailureError struct {
	class       string
	summary     string
	retryPolicy string
	cause       error
}

func (e safeToolFailureError) Error() string {
	summary := strings.TrimSpace(e.summary)
	if summary != "" {
		return summary
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return "tool execution failed"
}

func (e safeToolFailureError) Unwrap() error {
	return e.cause
}

func (e safeToolFailureError) SafeToolFailureClass() string {
	return strings.TrimSpace(e.class)
}

func (e safeToolFailureError) SafeToolFailureSummary() string {
	return strings.TrimSpace(e.summary)
}

func (e safeToolFailureError) SafeToolFailureRetryPolicy() string {
	return strings.TrimSpace(e.retryPolicy)
}
