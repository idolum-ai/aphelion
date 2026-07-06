//go:build linux

package tool

import (
	"fmt"
	"time"
)

const externalToolMaxTimeoutSeconds = 30 * 60

func validateExternalToolTimeoutBudget(manifest ExternalToolManifest) error {
	if manifest.Execution.TimeoutSeconds < 0 {
		return fmt.Errorf("external tool manifest execution.timeout_seconds must be >= 0")
	}
	if manifest.Execution.TimeoutSeconds > externalToolMaxTimeoutSeconds {
		return fmt.Errorf("external tool manifest execution.timeout_seconds %d exceeds maximum %d", manifest.Execution.TimeoutSeconds, externalToolMaxTimeoutSeconds)
	}
	if manifest.Constraints.MaxRuntimeSeconds < 0 {
		return fmt.Errorf("external tool manifest constraints.max_runtime_seconds must be >= 0")
	}
	if manifest.Constraints.MaxRuntimeSeconds > externalToolMaxTimeoutSeconds {
		return fmt.Errorf("external tool manifest constraints.max_runtime_seconds %d exceeds maximum %d", manifest.Constraints.MaxRuntimeSeconds, externalToolMaxTimeoutSeconds)
	}
	return nil
}

func externalToolExecutionTimeout(manifest ExternalToolManifest, fallback time.Duration) (time.Duration, error) {
	if err := validateExternalToolTimeoutBudget(manifest); err != nil {
		return 0, err
	}
	timeout := defaultTimeout(fallback)
	if manifest.Execution.TimeoutSeconds > 0 {
		timeout = time.Duration(manifest.Execution.TimeoutSeconds) * time.Second
	}
	if manifest.Constraints.MaxRuntimeSeconds > 0 {
		constraintTimeout := time.Duration(manifest.Constraints.MaxRuntimeSeconds) * time.Second
		if timeout <= 0 || constraintTimeout < timeout {
			timeout = constraintTimeout
		}
	}
	return defaultTimeout(timeout), nil
}
