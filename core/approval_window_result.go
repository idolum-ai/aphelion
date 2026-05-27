//go:build linux

package core

// ApprovalWindowEnableResult carries rendered text plus typed authority state.
// Text is presentation only; Active is the authority signal.
type ApprovalWindowEnableResult struct {
	Text       string
	Active     bool
	LeaseID    string
	OverrideID string
}

// ApprovalWindowCancelResult carries rendered text plus typed cancellation state.
// Text is presentation only; Canceled is the authority signal.
type ApprovalWindowCancelResult struct {
	Text     string
	Canceled bool
}
