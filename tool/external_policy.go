//go:build linux

package tool

import (
	"errors"
	"fmt"
	"strings"
)

type externalPolicyViolationError struct {
	Reason string
}

func (e externalPolicyViolationError) Error() string {
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "external tool policy ceiling is not enforceable"
	}
	return "policy_violation: " + reason
}

func isExternalPolicyViolation(err error) bool {
	var policyErr externalPolicyViolationError
	return errors.As(err, &policyErr)
}

func validateExternalProcessPolicy(manifest ExternalToolManifest) error {
	manifest = NormalizeExternalToolManifest(manifest)
	if manifest.Execution.Mode != "process" && manifest.Execution.Mode != "subprocess" {
		return nil
	}
	if network := strings.TrimSpace(manifest.Constraints.Network); network != "" && network != "none" {
		return externalPolicyViolationError{Reason: fmt.Sprintf("process-mode network=%q is not enforceable by the process executor", network)}
	}
	if filesystem := strings.TrimSpace(manifest.Constraints.Filesystem); filesystem != "" && filesystem != "none" {
		return externalPolicyViolationError{Reason: fmt.Sprintf("process-mode filesystem=%q is not enforceable by the process executor", filesystem)}
	}
	return nil
}
